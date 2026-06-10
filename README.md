# Bridge Extension Backend

This is the backend service for the application, built using Go. It handles the core business logic, API routing, and data persistence.

## Prerequisites

Before running the backend, ensure you have the following installed on your machine:

* **Docker** and **Docker Compose**
* **Go** (optional, only needed for local development without Docker)

## Getting Started

### 1. Environment Configuration

The application requires specific environment variables to run. Create a `.env` file in the root of the backend directory and define the necessary variables.

```bash
# define these environment variables ( in a .env for example)

# Postgres
POSTGRES_USER=
POSTGRES_PASSWORD=
POSTGRES_DB=

# PGAdmin
PGADMIN_DEFAULT_EMAIL=
PGADMIN_DEFAULT_PASSWORD=

# Backend Database
DB_HOST=
DB_PORT=
DB_USER=
DB_PASSWORD=
DB_NAME=

#Backend
ATTIO_CLIENT_ID=
ATTIO_CLIENT_SECRET=
ATTIO_REDIRECT_URI=
JWT_SECRET=
```

### 2. Launching the Application

The easiest way to start the backend along with its database dependency is using Docker Compose. This ensures a consistent environment.

Run the following command to build and start the services:

```bash
docker-compose up --build
```

The API should now be accessible at `http://localhost:8080`

### Architecture & Tech Stack

Language: Go (Golang)

Database: PostgreSQL with PGadmin

Containerization: Docker & Docker Compose

### API endpoints

| Frontend Call | Backend Route |
| :--- | :--- |
| `getAuthUrl()` | `GET /auth/attio` |
| Attio redirect | `/auth/attio/callback` |
| `syncCompany()` | `POST /api/sync` |
| `lookupCompany()` | `GET /api/companies/lookup` |
