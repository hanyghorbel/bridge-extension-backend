package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"bridge-extension-backend/internal/auth"
	"bridge-extension-backend/internal/models"
	"bridge-extension-backend/internal/repository"
)

type CompanyHandler struct {
	companyRepo *repository.CompanyRepository
}

func NewCompanyHandler(repo *repository.CompanyRepository) *CompanyHandler {
	return &CompanyHandler{companyRepo: repo}
}

// SyncRequest defines the incoming payload format extracted from the LinkedIn DOM
type SyncRequest struct {
	CompanyName string `json:"company_name"`
	LinkedinURL string `json:"linkedin_url"`
	Domain      string `json:"domain"` // e.g. "google.com" extracted from their contact info
}

type syncConflictResponse struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	RecordURL string `json:"record_url"`
}

func writeSyncConflict(w http.ResponseWriter, message, recordURL string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(syncConflictResponse{
		Status:    "conflict",
		Message:   message,
		RecordURL: recordURL,
	})
}

func (h *CompanyHandler) HandleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authorize user request using the JWT Utility
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized payload signature", http.StatusUnauthorized)
		return
	}
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	userID, err := auth.ValidateJWT(tokenString)
	if err != nil {
		http.Error(w, "Invalid or expired session token", http.StatusUnauthorized)
		return
	}

	// Parse inbound JSON extraction from extension
	var reqBody SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Bad request payload", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(reqBody.CompanyName) == "" || strings.TrimSpace(reqBody.LinkedinURL) == "" {
		http.Error(w, "company_name and linkedin_url are required", http.StatusBadRequest)
		return
	}

	normalizedURL, err := normalizeLinkedInCompanyURL(reqBody.LinkedinURL)
	if err != nil {
		http.Error(w, "Invalid linkedin_url", http.StatusBadRequest)
		return
	}
	reqBody.LinkedinURL = normalizedURL

	// Check if this company has already been synced by this user
	existingSync, err := h.companyRepo.GetSyncedCompanyByLinkedInURL(userID, reqBody.LinkedinURL)
	if err == nil && existingSync != nil {
		writeSyncConflict(w, "Company record already exists in Attio", existingSync.AttioRecordURL)
		return
	}

	// Resolve the user's personal Attio token from the DB
	attioToken, err := h.companyRepo.GetUserAttioToken(userID)
	if err != nil {
		http.Error(w, "Failed retrieving integration credentials", http.StatusInternalServerError)
		return
	}

	// Construct Attio v2 JSON object write requirements
	// Ref: Attio Docs expects structured fields inside {"data": {"values": { ... }}}
	attioPayload := map[string]interface{}{
		"data": map[string]interface{}{
			"values": map[string]interface{}{
				"name":     reqBody.CompanyName,
				"linkedin": reqBody.LinkedinURL,
				"domains":  []string{reqBody.Domain},
			},
		},
	}
	jsonBytes, err := json.Marshal(attioPayload)
	if err != nil {
		http.Error(w, "Failed preparing payload", http.StatusInternalServerError)
		return
	}

	// Attio v2 standard route for default standard company structures
	attioURL := "https://api.attio.com/v2/objects/companies/records"

	req, err := http.NewRequest("POST", attioURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		http.Error(w, "Failed creating request to Attio", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+attioToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed connecting to Attio gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Printf("Attio API Error - Status: %d, Body: %s", resp.StatusCode, string(bodyBytes))
		http.Error(w, "Attio integration engine rejected data parameters", http.StatusBadGateway)
		return
	}

	// Decode Attio's mapping response target values
	var attioResp struct {
		Data struct {
			ID struct {
				RecordID string `json:"record_id"`
			} `json:"id"`
			WebURL string `json:"web_url"` // Instant link back to workspace page view
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&attioResp); err != nil {
		http.Error(w, "Failed decoding record metadata", http.StatusInternalServerError)
		return
	}

	// Record successfully synchronized logs inside local Postgres layer
	syncLog := &models.SyncedCompany{
		UserID:         userID,
		LinkedinURL:    reqBody.LinkedinURL,
		AttioRecordID:  attioResp.Data.ID.RecordID,
		AttioRecordURL: attioResp.Data.WebURL,
		CompanyName:    reqBody.CompanyName,
	}
	if err := h.companyRepo.SaveSyncedCompany(syncLog); err != nil {
		log.Printf("Non-blocking telemetry logging error: %v", err)
	}

	// Hand response link payloads cleanly back to the client interface execution loop
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":     "success",
		"record_url": attioResp.Data.WebURL,
	})
}

type companyLookupResponse struct {
	Status      string    `json:"status"`
	CompanyName string    `json:"company_name,omitempty"`
	RecordURL   string    `json:"record_url,omitempty"`
	SyncedAt    time.Time `json:"synced_at,omitempty"`
}

func (h *CompanyHandler) HandleLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized payload signature", http.StatusUnauthorized)
		return
	}
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	userID, err := auth.ValidateJWT(tokenString)
	if err != nil {
		http.Error(w, "Invalid or expired session token", http.StatusUnauthorized)
		return
	}

	linkedinURL := strings.TrimSpace(r.URL.Query().Get("linkedin_url"))
	if linkedinURL == "" {
		http.Error(w, "linkedin_url query parameter is required", http.StatusBadRequest)
		return
	}

	normalizedURL, err := normalizeLinkedInCompanyURL(linkedinURL)
	if err != nil {
		http.Error(w, "Invalid LinkedIn company URL", http.StatusBadRequest)
		return
	}

	existingSync, err := h.companyRepo.GetSyncedCompanyByLinkedInURL(userID, normalizedURL)
	if err != nil || existingSync == nil {
		rawURL := strings.Split(linkedinURL, "?")[0]
		existingSync, err = h.companyRepo.GetSyncedCompanyByLinkedInURL(userID, rawURL)
	}

	w.Header().Set("Content-Type", "application/json")
	if err != nil || existingSync == nil {
		json.NewEncoder(w).Encode(companyLookupResponse{Status: "not_synced"})
		return
	}

	json.NewEncoder(w).Encode(companyLookupResponse{
		Status:      "synced",
		CompanyName: existingSync.CompanyName,
		RecordURL:   existingSync.AttioRecordURL,
		SyncedAt:    existingSync.SyncedAt,
	})
}
