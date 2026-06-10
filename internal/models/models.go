package models

import "time"

type User struct {
	ID                     string    `db:"id" json:"id"`
	Email                  string    `db:"email" json:"email"`
	FirstName              string    `db:"first_name" json:"first_name"`
	LastName               string    `db:"last_name" json:"last_name"`
	AttioAccessToken       string    `db:"attio_access_token" json:"-"`
	AttioWorkspaceMemberID string    `db:"attio_workspace_member_id" json:"attio_workspace_member_id"`
	AttioWorkspaceID       string    `db:"attio_workspace_id" json:"attio_workspace_id"`
	CreatedAt              time.Time `db:"created_at" json:"created_at"`
}

type SyncedCompany struct {
	ID             string    `db:"id" json:"id"`
	UserID         string    `db:"user_id" json:"user_id"`
	LinkedinURL    string    `db:"linkedin_url" json:"linkedin_url"`
	AttioRecordID  string    `db:"attio_record_id" json:"attio_record_id"`
	AttioRecordURL string    `db:"attio_record_url" json:"attio_record_url"`
	CompanyName    string    `db:"company_name" json:"company_name"`
	SyncedAt       time.Time `db:"synced_at" json:"synced_at"`
}
