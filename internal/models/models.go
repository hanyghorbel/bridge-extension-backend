package models

import "time"

type User struct {
	ID                string    `db:"id" json:"id"`
	Email             string    `db:"email" json:"email"`
	AttioAccessToken         string    `db:"attio_access_token" json:"-"`
	AttioRefreshToken        string    `db:"attio_refresh_token" json:"-"`
	AttioWorkspaceMemberID   string    `db:"attio_workspace_member_id" json:"attio_workspace_member_id"`
	CreatedAt         time.Time `db:"created_at" json:"created_at"`
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
