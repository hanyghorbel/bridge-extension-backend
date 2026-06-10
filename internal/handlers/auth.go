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

	attioAuthURL := fmt.Sprintf(
		"https://app.attio.com/authorize?response_type=code&client_id=%s&redirect_uri=%s&state=%s",
		clientID, redirectURI, state,
	)

	http.Redirect(w, r, attioAuthURL, http.StatusTemporaryRedirect)
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
}

type IntrospectResponse struct {
	Active bool `json:"active"`

	WorkspaceID string `json:"workspace_id"`

	AuthorizedByWorkspaceMemberID string `json:"authorized_by_workspace_member_id"`
}

type WorkspaceMemberResponse struct {
	Data struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Email     string `json:"email_address"`
	} `json:"data"`
}

// HandleCallback receives the code from Attio, exchanges it, saves the user, and issues a JWT
func (h *AuthHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}
	// IMPORTANT: validate state against CSRF attacks
	if state == "" {
		log.Println("WARNING: State parameter missing on callback")
	}

	// Prepare payload to exchange code for token
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

	// Exchange JSON Payload for token
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

	// Decode response into tokenResponse
	var tokenResponse TokenResponse

	if err := json.NewDecoder(resp.Body).Decode(&tokenResponse); err != nil {
		log.Printf("ERROR: Failed to parse token response: %v", err)
		http.Error(w, "Failed parsing token response", http.StatusInternalServerError)
		return
	}
	if tokenResponse.AccessToken == "" {
		log.Println("ERROR: Empty access token received")
		http.Error(w, "Invalid token response", http.StatusBadGateway)
		return
	}

	// Determine the authenticated user's email and workspace member id and workspace id
	var userEmail string
	var workspaceMemberID string
	var workspaceID string

	// Query Attio's introspect endpoint to get workspace member id
	memberReq, err := http.NewRequest("POST", "https://app.attio.com/oauth/introspect", nil)
	if err != nil {
		log.Printf("ERROR: Failed to create request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	memberReq.Header.Set("Authorization", "Bearer "+tokenResponse.AccessToken)

	memberResp, err := client.Do(memberReq)
	if err != nil {
		log.Printf("ERROR: Failed to introspect member from Attio: %v", err)
		http.Error(w, "Failed to introspect member information from Attio", http.StatusBadGateway)
		return
	}
	defer memberResp.Body.Close()
	if memberResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(memberResp.Body)
		log.Printf("ERROR: Introspect failed (%d): %s", memberResp.StatusCode, body)
		http.Error(w, "Failed to validate Attio token", http.StatusBadGateway)
		return
	}

	var IntrospectResponse IntrospectResponse
	if err := json.NewDecoder(memberResp.Body).Decode(&IntrospectResponse); err != nil {
		log.Printf("ERROR: Failed to parse introspect response: %v", err)
		http.Error(w, "Failed parsing introspect response", http.StatusInternalServerError)
		return
	}

	if !IntrospectResponse.Active {
		log.Printf("ERROR: Token invalid! login again")
		http.Error(w, "Token is invalid, please login", http.StatusBadGateway)
		return
	}

	workspaceMemberID = IntrospectResponse.AuthorizedByWorkspaceMemberID

	if workspaceMemberID == "" {
		log.Println("ERROR: Missing authorized workspace member ID")
		http.Error(w, "Unable to identify Attio user", http.StatusInternalServerError)
		return
	}

	workspaceID = IntrospectResponse.WorkspaceID

	if workspaceID == "" {
		log.Println("ERROR: Missing workspace ID")
		http.Error(w, "Unable to identify workspace", http.StatusInternalServerError)
		return
	}

	// Get user's data: email, name, lastname
	memberDetailsReq, err := http.NewRequest("GET", "https://api.attio.com/v2/workspace_members/"+workspaceMemberID, nil)
	if err != nil {
		log.Printf("ERROR: Failed to create request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	memberDetailsReq.Header.Set("Authorization", "Bearer "+tokenResponse.AccessToken)

	memberDetailsResp, err := client.Do(memberDetailsReq)
	if err != nil {
		log.Printf("ERROR: Failed to fetch workspace members from Attio: %v", err)
		http.Error(w, "Failed to fetch workspace information from Attio", http.StatusBadGateway)
		return
	}
	if memberDetailsResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(memberDetailsResp.Body)
		log.Printf("ERROR: Workspace member lookup failed (%d): %s", memberDetailsResp.StatusCode, body)
		http.Error(w, "Failed retrieving Attio user information", http.StatusBadGateway)
		return
	}
	defer memberDetailsResp.Body.Close()

	// Define the typed response structure matching Attio's actual format
	var workspaceMemberResponse WorkspaceMemberResponse
	if err := json.NewDecoder(memberDetailsResp.Body).Decode(&workspaceMemberResponse); err != nil {
		log.Printf("ERROR: Failed to parse introspect response: %v", err)
		log.Printf("ERROR: Failed to parse introspect response: %v", err)
		http.Error(w, "Failed parsing introspect response", http.StatusInternalServerError)
		return
	}
	firstName := workspaceMemberResponse.Data.FirstName
	lastName := workspaceMemberResponse.Data.LastName
	userEmail = workspaceMemberResponse.Data.Email
	//*************************** modifications end here ************************//

	if userEmail == "" {
		log.Println("ERROR: Unable to determine authenticated user's email from token response or workspace members")
		http.Error(w, "Unable to determine authenticated user from Attio response; please check Attio OAuth configuration", http.StatusInternalServerError)
		return
	}

	// 3. Persist the user data and access token into PostgreSQL
	user, err := h.userRepo.UpsertUser(userEmail, firstName, lastName, tokenResponse.AccessToken, workspaceMemberID, workspaceID)
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
					window.opener.postMessage({ type: "ATTIO_AUTH_SUCCESS", token: token }, "*");
				}
				// Close this temporary OAuth callback tab automatically
				setTimeout(() => {
					window.close();
				}, 2000);
			</script>
		</body>
		</html>
	`, appToken)
}

// syncExistingCompaniesFromAttio fetches all existing company records from Attio and stores them locally
// to prevent duplicate sync attempts. Uses POST query endpoint with pagination.
// todo: verify if this is useful and if the pagination works as intended
func (h *AuthHandler) syncExistingCompaniesFromAttio(userID, attioToken string) error {
	client := &http.Client{Timeout: 10 * time.Second}

	// Define the typed response structure matching Attio's actual format
	type AttioRecord struct {
		ID struct {
			RecordID string `json:"record_id"`
		} `json:"id"`
		WebURL string                              `json:"web_url"`
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

			if linkedinURL != "" {
				// normalizing url to only keep linkedin.com/{companyName}
				if normalizedURL, normErr := normalizeLinkedInCompanyURL(linkedinURL); normErr == nil {
					linkedinURL = normalizedURL
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
