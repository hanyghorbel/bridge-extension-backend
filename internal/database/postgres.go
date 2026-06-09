package database

import (
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql
	"github.com/jmoiron/sqlx"
)

// NewPostgresConnection creates a connection pool based on environment variables
func NewPostgresConnection() (*sqlx.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
	)

	var db *sqlx.DB
	var err error

	// Retry mechanism because Postgres might take a few seconds to start in Docker
	d := 1000 * time.Millisecond
	for i := 0; i < 5; i++ {
		db, err = sqlx.Connect("pgx", dsn)
		if err == nil {
			break
		}
		log.Printf("Waiting for database connection... (attempt %d/5) error: %v", i+1, err)
		time.Sleep(d)
		d = 2 * d
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to database after 5 attempts: %w", err)
	}

	// Test the connection before returning
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return db, nil
}
