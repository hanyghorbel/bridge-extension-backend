package main

import (
	"log"
	"net/http"
	"os"

	"bridge-extension-backend/internal/database"
	"bridge-extension-backend/internal/handlers"
	"bridge-extension-backend/internal/repository"
)

func main() {
	// Validate required environment variables
	requiredEnvVars := []string{
		"DB_HOST",
		"DB_PORT",
		"DB_USER",
		"DB_PASSWORD",
		"DB_NAME",
		"JWT_SECRET",
		"ATTIO_CLIENT_ID",
		"ATTIO_CLIENT_SECRET",
		"ATTIO_REDIRECT_URI",
	}

	for _, envVar := range requiredEnvVars {
		if os.Getenv(envVar) == "" {
			log.Fatalf("FATAL: Required environment variable %s is not set", envVar)
		}
	}

	db, err := database.NewPostgresConnection()
	if err != nil {
		log.Fatalf("FATAL: Could not connect to database: %v", err)
	}
	defer db.Close()

	log.Println("Successfully connected to PostgreSQL database.")
	// Initialize User Module layers
	userRepo := repository.NewUserRepository(db)
	authHandler := handlers.NewAuthHandler(userRepo)

	// Initialize Company Sync Module layers
	companyRepo := repository.NewCompanyRepository(db)
	companyHandler := handlers.NewCompanyHandler(companyRepo)

	// Register OAuth Endpoints
	http.HandleFunc("/auth/attio", authHandler.HandleLogin)
	http.HandleFunc("/auth/attio/callback", authHandler.HandleCallback)
	// Attach API action endpoints
	http.HandleFunc("/api/sync", companyHandler.HandleSync)
	http.HandleFunc("/api/companies/lookup", companyHandler.HandleLookup)

	port := ":8080"
	log.Printf("Server running on %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("FATAL: Server error: %v", err)
	}
}
