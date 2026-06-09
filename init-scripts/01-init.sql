-- Enable UUID extension for secure, non-sequential IDs
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE users (
                       id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
                       email VARCHAR(255) UNIQUE NOT NULL,
                        attio_access_token TEXT NOT NULL,
                        attio_refresh_token TEXT,
                        attio_workspace_member_id VARCHAR(255),
                       created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                       updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE synced_companies (
                                  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
                                  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
                                  linkedin_url VARCHAR(500) NOT NULL,
                                  attio_record_id VARCHAR(255) NOT NULL,   -- The ID returned by Attio
                                  attio_record_url TEXT NOT NULL,         -- The link to show the user
                                  company_name VARCHAR(255),
                                  synced_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    -- Prevent the same user from duplicating the same LinkedIn company
                                  UNIQUE(user_id, linkedin_url)
);