package repository

import (
	"bridge-extension-backend/internal/models"
	"github.com/jmoiron/sqlx"
)

type UserRepository struct {
	db *sqlx.DB
}

func NewUserRepository(db *sqlx.DB) *UserRepository {
	return &UserRepository{db: db}
}

// UpsertUser inserts a new user or updates their Attio token if they already exist
func (r *UserRepository) UpsertUser(email, firstname, lastname, token, workspaceMemberID, workspaceID string) (*models.User, error) {
	// Insert or update user record. If workspace member id is provided, persist it as well.
	query := `
		INSERT INTO users (email,first_name,last_name, attio_access_token, attio_workspace_member_id, attio_workspace_id, updated_at)
		VALUES ($1, $2, $3,$4,$5,$6, NOW())
		ON CONFLICT (email)
		DO UPDATE SET
			attio_access_token = EXCLUDED.attio_access_token,
			attio_workspace_member_id = COALESCE(EXCLUDED.attio_workspace_member_id, users.attio_workspace_member_id),
		    attio_workspace_id = COALESCE(EXCLUDED.attio_workspace_id, users.attio_workspace_id),
			updated_at = NOW()
		RETURNING id, email,first_name,last_name, attio_workspace_member_id,attio_workspace_id, created_at;
	`
	var user models.User
	err := r.db.Get(&user, query, email, firstname, lastname, token, workspaceMemberID, workspaceID)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// SavePreexistingCompany stores pre-existing Attio company records in our database
// to prevent duplicate sync attempts. This is called after user authentication.
func (r *UserRepository) SavePreexistingCompany(company *models.SyncedCompany) error {
	query := `
		INSERT INTO synced_companies (user_id, linkedin_url, attio_record_id, attio_record_url, company_name, synced_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (user_id, linkedin_url)
		DO NOTHING
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
