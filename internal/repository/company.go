package repository

import (
	"bridge-extension-backend/internal/models"
	"github.com/jmoiron/sqlx"
)

type CompanyRepository struct {
	db *sqlx.DB
}

func NewCompanyRepository(db *sqlx.DB) *CompanyRepository {
	return &CompanyRepository{db: db}
}

// GetUserAttioToken pulls the unique encrypted access token for an authenticated app user
func (r *CompanyRepository) GetUserAttioToken(userID string) (string, error) {
	var token string
	query := `SELECT attio_access_token FROM users WHERE id = $1`
	err := r.db.Get(&token, query, userID)
	return token, err
}

// GetSyncedCompanyByLinkedInSlug matches any stored LinkedIn URL with the same company slug.
func (r *CompanyRepository) GetSyncedCompanyByLinkedInSlug(userID, slug string) (*models.SyncedCompany, error) {
	var company models.SyncedCompany
	query := `
		SELECT id, user_id, linkedin_url, attio_record_id, attio_record_url, company_name, synced_at
		FROM synced_companies
		WHERE user_id = $1 AND linkedin_url ILIKE $2
		LIMIT 1
	`
	pattern := "%/company/" + slug + "%"
	err := r.db.Get(&company, query, userID, pattern)
	if err != nil {
		return nil, err
	}
	return &company, nil
}

// GetSyncedCompanyByDomain checks if a company has already been synced by a user by LinkedIn URL.
// Returns the company if found, nil if not found.
func (r *CompanyRepository) GetSyncedCompanyByDomain(userID, linkedinURL string) (*models.SyncedCompany, error) {
	var company models.SyncedCompany
	// Check if this exact LinkedIn URL has already been synced for this user
	query := `
		SELECT id, user_id, linkedin_url, attio_record_id, attio_record_url, company_name, synced_at
		FROM synced_companies
		WHERE user_id = $1 AND linkedin_url = $2
		LIMIT 1
	`
	err := r.db.Get(&company, query, userID, linkedinURL)
	if err != nil {
		return nil, err
	}
	return &company, nil
}

// SaveSyncedCompany stores tracking logs after a record is written onto Attio
func (r *CompanyRepository) SaveSyncedCompany(company *models.SyncedCompany) error {
	query := `
		INSERT INTO synced_companies (user_id, linkedin_url, attio_record_id, attio_record_url, company_name, synced_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (user_id, linkedin_url) 
		DO UPDATE SET attio_record_id = EXCLUDED.attio_record_id, attio_record_url = EXCLUDED.attio_record_url, synced_at = NOW()
	`
	_, err := r.db.Exec(query,
		company.UserID,
		company.LinkedinURL,
		company.AttioRecordID,
		company.AttioRecordURL,
		company.CompanyName,
	)
	return err
}
