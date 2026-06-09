package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"bridge-extension-backend/internal/auth"
	"bridge-extension-backend/internal/models"
	"bridge-extension-backend/internal/repository"
)

type AuthHandler struct {
	userRepo *repository.UserRepository
}

func NewAuthHandler(userRepo *repository.UserRepository) *AuthHandler {
	return &AuthHandler{userRepo: userRepo}
}

// generateRandomState creates a cryptographically secure random state for CSRF protection
func generateRandomState() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// HandleLogin redirects the browser window to Attio's OAuth Consent Screen
func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("ATTIO_CLIENT_ID")
	redirectURI := os.Getenv("ATTIO_REDIRECT_URI")

	if clientID == "" || redirectURI == "" {
		log.Println("ERROR: ATTIO_CLIENT_ID or ATTIO_REDIRECT_URI not set")
		http.Error(w, "Server configuration error", http.StatusInternalServerError)
		return
	}

	// Generate cryptographically secure random state to prevent CSRF
	state, err := generateRandomState()
	if err != nil {
		log.Printf("ERROR: Failed to generate state: %v", err)
		http.Error(w, "Failed to initiate login", http.StatusInternalServerError)
		return
	}

	// TODO: Store state in session/cookie to validate on callback
	// For now, at minimum we're using random state instead of hardcoded

	attioAuthURL := fmt.Sprintf(
		"https://app.attio.com/authorize?response_type=code&client_id=%s&redirect_uri=%s&state=%s",
		clientID, redirectURI, state,
	)

	http.Redirect(w, r, attioAuthURL, http.StatusTemporaryRedirect)
}

// HandleCallback receives the code from Attio, exchanges it, saves the user, and issues a JWT
func (h *AuthHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	// TODO: Validate state against stored session/cookie to prevent CSRF attacks
	if state == "" {
		log.Println("WARNING: State parameter missing on callback")
	}

	// 1. Prepare payload to exchange code for token
	clientID := os.Getenv("ATTIO_CLIENT_ID")
	clientSecret := os.Getenv("ATTIO_CLIENT_SECRET")
	redirectURI := os.Getenv("ATTIO_REDIRECT_URI")

	if clientID == "" || clientSecret == "" || redirectURI == "" {
		log.Println("ERROR: Missing ATTIO OAuth configuration")
		http.Error(w, "Server configuration error", http.StatusInternalServerError)
		return
	}

	payload := map[string]string{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		log.Printf("ERROR: Failed to marshal payload: %v", err)
		http.Error(w, "Failed to exchange token", http.StatusInternalServerError)
		return
	}

	// Create HTTP client with timeout to prevent hanging
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Post("https://app.attio.com/oauth/token", "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		log.Printf("ERROR: Failed to exchange token with Attio: %v", err)
		http.Error(w, "Failed to exchange token", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("ERROR: Attio token exchange returned %d: %s", resp.StatusCode, string(bodyBytes))
		http.Error(w, "Attio token exchange rejected request", http.StatusBadGateway)
		return
	}

	var tokenResponse struct {
		AccessToken       string `json:"access_token"`
		// Some providers include identifying fields in the token response; parse them if present.
		WorkspaceMemberID string `json:"workspace_member_id,omitempty"`
		Email             string `json:"email,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResponse); err != nil {
		log.Printf("ERROR: Failed to parse token response: %v", err)
		http.Error(w, "Failed parsing token response", http.StatusInternalServerError)
		return
	}

	// Try to determine the authenticated user's email and workspace member id. There is no
	// guaranteed /v2/me endpoint, so be defensive: use fields from the token response when available,
	// otherwise query workspace_members and try to find a match. If ambiguous, fail with a helpful error.
	var userEmail string
	var workspaceMemberID string

	if tokenResponse.Email != "" {
		userEmail = tokenResponse.Email
	}
	if tokenResponse.WorkspaceMemberID != "" {
		workspaceMemberID = tokenResponse.WorkspaceMemberID
	}

	// If we don't yet have an email (or workspace member id) attempt to call workspace_members
	if userEmail == "" || workspaceMemberID == "" {
		// 2. Query Attio's workspace_members endpoint to gather candidate members
		reqMembers, err := http.NewRequest("GET", "https://api.attio.com/v2/workspace_members", nil)
		if err != nil {
			log.Printf("ERROR: Failed to create request: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		reqMembers.Header.Set("Authorization", "Bearer "+tokenResponse.AccessToken)

		membersResp, err := client.Do(reqMembers)
		if err != nil {
			log.Printf("ERROR: Failed to fetch workspace members from Attio: %v", err)
			http.Error(w, "Failed to fetch workspace information from Attio", http.StatusBadGateway)
			return
		}
		defer membersResp.Body.Close()

		if membersResp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(membersResp.Body)
			log.Printf("ERROR: Attio workspace_members returned %d: %s", membersResp.StatusCode, string(bodyBytes))
			http.Error(w, "Failed to fetch workspace information from Attio", http.StatusBadGateway)
			return
		}

		var membersResponse struct {
			Data []struct {
				ID struct {
					WorkspaceMemberID string `json:"workspace_member_id"`
				} `json:"id"`
				EmailAddress string `json:"email_address"`
				AvatarURL    string `json:"avatar_url"`
			} `json:"data"`
		}

		if err := json.NewDecoder(membersResp.Body).Decode(&membersResponse); err != nil {
			log.Printf("ERROR: Failed parsing Attio workspace_members response: %v", err)
			http.Error(w, "Failed parsing Attio workspace response", http.StatusInternalServerError)
			return
		}

		// If workspaceMemberID was provided in token response, try to find the matching member and extract email
		if workspaceMemberID != "" {
			for _, m := range membersResponse.Data {
				if m.ID.WorkspaceMemberID == workspaceMemberID {
					if userEmail == "" {
						userEmail = m.EmailAddress
					}
					break
				}
			}
		}

		// If still no email, and the workspace has only one member, use that as the authenticated identity
		if userEmail == "" {
			if len(membersResponse.Data) == 1 {
				userEmail = membersResponse.Data[0].EmailAddress
				if workspaceMemberID == "" {
					workspaceMemberID = membersResponse.Data[0].ID.WorkspaceMemberID
				}
			}
		}
	}

	if userEmail == "" {
		log.Println("ERROR: Unable to determine authenticated user's email from token response or workspace members")
		http.Error(w, "Unable to determine authenticated user from Attio response; please check Attio OAuth configuration", http.StatusInternalServerError)
		return
	}

	// 3. Persist the user and access token into PostgreSQL
	user, err := h.userRepo.UpsertUser(userEmail, tokenResponse.AccessToken, workspaceMemberID)
	if err != nil {
		log.Printf("ERROR: Failed to save user session: %v", err)
		http.Error(w, "Failed to save user session", http.StatusInternalServerError)
		return
	}

	// 3.5. Sync existing companies from Attio workspace to prevent duplicate syncs
	if err := h.syncExistingCompaniesFromAttio(user.ID, tokenResponse.AccessToken); err != nil {
		log.Printf("WARNING: Failed to sync existing companies from Attio: %v", err)
		// Don't fail authentication if this errors - it's not a critical operation
	}

	// 4. Generate our backend's app JWT session token
	appToken, err := auth.GenerateJWT(user.ID)
	if err != nil {
		log.Printf("ERROR: Failed to generate JWT: %v", err)
		http.Error(w, "Failed generating session", http.StatusInternalServerError)
		return
	}

	// 5. Send token back to the extension client window via postMessage with proper origin
	// NOTE: In production, set a specific origin instead of "*" for security
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fmt.Fprintf(w, `
		<!DOCTYPE html>
		<html>
		<head><title>Authentication Successful</title></head>
		<body>
			<h3 style="font-family: sans-serif; text-align: center; margin-top: 50px;">
				Authentication successful! Syncing with your extension...
			</h3>
			<script>
				const token = "%s";
				// Post the JWT token back to the extension context that opened this tab
				if (window.opener) {
					// TODO: Use specific origin from environment variable instead of "*"
					window.opener.postMessage({ type: "ATTIO_AUTH_SUCCESS", token: token }, "*");
				}
				// Close this temporary OAuth callback tab automatically
				window.close();
			</script>
		</body>
		</html>
	`, appToken)
}

// syncExistingCompaniesFromAttio fetches all existing company records from Attio and stores them locally
// to prevent duplicate sync attempts. Uses POST query endpoint with pagination.
func (h *AuthHandler) syncExistingCompaniesFromAttio(userID, attioToken string) error {
	client := &http.Client{Timeout: 10 * time.Second}

	// Define the typed response structure matching Attio's actual format
	type AttioRecord struct {
		ID struct {
			RecordID string `json:"record_id"`
		} `json:"id"`
		WebURL string `json:"web_url"`
		Values map[string][]map[string]interface{} `json:"values"`
	}

	type AttioQueryResponse struct {
		Data []AttioRecord `json:"data"`
	}

	cursor := ""

	for {
		// Build payload: empty object for first request, add cursor if we have one
		payload := map[string]interface{}{}
		if cursor != "" {
			payload["cursor"] = cursor
		}

		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal query payload: %w", err)
		}

		req, err := http.NewRequest("POST", "https://api.attio.com/v2/objects/companies/records/query", bytes.NewBuffer(bodyBytes))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+attioToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to fetch company records from Attio: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == http.StatusNotFound {
				log.Printf("Attio query endpoint not found (status 404). Skipping pre-sync of existing companies.")
				return nil
			}
			return fmt.Errorf("Attio returned status %d: %s", resp.StatusCode, string(b))
		}

		// Read response body for both decoding and cursor extraction
		respBodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}

		var queryResp AttioQueryResponse
		if err := json.Unmarshal(respBodyBytes, &queryResp); err != nil {
			return fmt.Errorf("failed to decode Attio response: %w", err)
		}

		if len(queryResp.Data) == 0 {
			// No more records
			return nil
		}

		// Process each record
		for _, rec := range queryResp.Data {
			// Extract name from values.name[0].value
			companyName := ""
			if nameArray, ok := rec.Values["name"]; ok && len(nameArray) > 0 {
				if val, ok := nameArray[0]["value"].(string); ok {
					companyName = val
				}
			}

			// Extract LinkedIn URL from values.linkedin[0].value
			linkedinURL := ""
			if linkedinArray, ok := rec.Values["linkedin"]; ok && len(linkedinArray) > 0 {
				if val, ok := linkedinArray[0]["value"].(string); ok {
					linkedinURL = val
				}
			}

			// If no LinkedIn URL, create a fallback identifier using domain or record ID
			if linkedinURL == "" {
				// Try to use domain if available
				if domainsArray, ok := rec.Values["domains"]; ok && len(domainsArray) > 0 {
					if rootDomain, ok := domainsArray[0]["root_domain"].(string); ok && rootDomain != "" {
						linkedinURL = "attio:domain:" + rootDomain
					} else if domain, ok := domainsArray[0]["domain"].(string); ok && domain != "" {
						linkedinURL = "attio:domain:" + domain
					}
				}
				// If still no identifier, use record ID
				if linkedinURL == "" {
					linkedinURL = "attio:record:" + rec.ID.RecordID
				}
			}

			syncedCompany := &models.SyncedCompany{
				UserID:         userID,
				LinkedinURL:    linkedinURL,
				AttioRecordID:  rec.ID.RecordID,
				AttioRecordURL: rec.WebURL,
				CompanyName:    companyName,
			}

			if err := h.userRepo.SavePreexistingCompany(syncedCompany); err != nil {
				log.Printf("WARNING: Failed to save pre-existing company record %s: %v", rec.ID.RecordID, err)
			}
		}

		// Check for cursor in the response for pagination
		var raw map[string]interface{}
		if err := json.Unmarshal(respBodyBytes, &raw); err == nil {
			if nextCursor, ok := raw["cursor"].(string); ok && nextCursor != "" {
				cursor = nextCursor
				continue
			}
		}

		// No cursor found, we're done paginating
		return nil
	}
}
