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

	// 1. Authorize user request using the JWT Utility
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

	// 2. Parse inbound JSON extraction from extension
	var reqBody SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Bad request payload", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(reqBody.CompanyName) == "" || strings.TrimSpace(reqBody.LinkedinURL) == "" {
		http.Error(w, "company_name and linkedin_url are required", http.StatusBadRequest)
		return
	}

	// 3. Check if this company has already been synced by this user
	existingSync, err := h.companyRepo.GetSyncedCompanyByDomain(userID, reqBody.LinkedinURL)
	if err == nil && existingSync != nil {
		writeSyncConflict(w, "Company record already exists in Attio", existingSync.AttioRecordURL)
		return
	}

	// 4. Resolve the user's personal Attio token from the DB
	attioToken, err := h.companyRepo.GetUserAttioToken(userID)
	if err != nil {
		http.Error(w, "Failed retrieving integration credentials", http.StatusInternalServerError)
		return
	}

	// 5. Construct Attio v2 JSON object write requirements
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

		// Attempt to detect uniqueness conflict and handle gracefully by mapping the
		// conflicting remote record into our local DB and returning a 409 to the client.
		var errBody map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &errBody); err == nil {
			if code, _ := errBody["code"].(string); code == "uniqueness_conflict" {
				// Extract any UUIDs mentioned in the message text
				msg, _ := errBody["message"].(string)
				re := regexp.MustCompile(`[0-9a-fA-F-]{36}`)
				ids := re.FindAllString(msg, -1)
				if len(ids) > 0 {
					// Try to fetch the first conflicting record from Attio and persist locally
					conflictingID := ids[0]
					recURL := "https://api.attio.com/v2/objects/companies/records/" + conflictingID
					reqGet, _ := http.NewRequest("GET", recURL, nil)
					reqGet.Header.Set("Authorization", "Bearer "+attioToken)
					getResp, err := client.Do(reqGet)
					if err == nil && getResp != nil {
						defer getResp.Body.Close()
						if getResp.StatusCode == http.StatusOK {
							var single struct {
								Data struct {
									ID struct {
										RecordID string `json:"record_id"`
									} `json:"id"`
									Values struct {
										Name     []map[string]interface{} `json:"name"`
										LinkedIn []map[string]interface{} `json:"linkedin"`
										Domains  []map[string]interface{} `json:"domains"`
									} `json:"values"`
									WebURL string `json:"web_url"`
								} `json:"data"`
							}
							if err := json.NewDecoder(getResp.Body).Decode(&single); err == nil {
								companyName := ""
								if len(single.Data.Values.Name) > 0 {
									if val, ok := single.Data.Values.Name[0]["value"].(string); ok {
										companyName = val
									}
								}

								linkedinURL := ""
								if len(single.Data.Values.LinkedIn) > 0 {
									if val, ok := single.Data.Values.LinkedIn[0]["value"].(string); ok {
										linkedinURL = val
									}
								}

								if linkedinURL == "" {
									linkedinURL = "attio:record:" + single.Data.ID.RecordID
								}

								syncLog := &models.SyncedCompany{
									UserID:         userID,
									LinkedinURL:    linkedinURL,
									AttioRecordID:  single.Data.ID.RecordID,
									AttioRecordURL: single.Data.WebURL,
									CompanyName:    companyName,
								}
								if err := h.companyRepo.SaveSyncedCompany(syncLog); err != nil {
									log.Printf("WARNING: failed saving conflicting Attio record locally: %v", err)
								}
								writeSyncConflict(w, "Company record already exists in Attio", single.Data.WebURL)
								return
							}
						}
					}
				}
			}
		}

		http.Error(w, "Attio integration engine rejected data parameters", http.StatusBadGateway)
		return
	}
	// Reset the body for the decoder below
	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 6. Decode Attio's mapping response target values
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

	// 7. Record successfully synchronized logs inside local Postgres layer
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

	// 8. Hand response link payloads cleanly back to the client interface execution loop
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":     "success",
		"record_url": attioResp.Data.WebURL,
	})
}
