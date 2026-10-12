package main

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	_ "github.com/lib/pq"
)

const defaultAddr = "127.0.0.1:8080"

// Runtime settings, filled from the environment in main.
var (
	allowedOrigins []string
	sessionTTL     = 12 * time.Hour
	ticketTTL      = 30 * time.Second
)

func initDB(db *sql.DB) error {
	query := `
        CREATE TABLE IF NOT EXISTS users (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            email TEXT UNIQUE NOT NULL,
            password_hash TEXT NOT NULL,
            created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
        );
        CREATE TABLE IF NOT EXISTS pairing_codes (
            id SERIAL PRIMARY KEY,
            user_id UUID REFERENCES users(id) ON DELETE CASCADE,
            code_hash TEXT NOT NULL,
            expires_at TIMESTAMP NOT NULL,
            used BOOLEAN DEFAULT FALSE
        );
        CREATE TABLE IF NOT EXISTS devices (
            id SERIAL PRIMARY KEY,
            user_id UUID REFERENCES users(id) ON DELETE CASCADE,
            agent_id TEXT UNIQUE NOT NULL,
            paired_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
        );
        CREATE TABLE IF NOT EXISTS sessions (
            id SERIAL PRIMARY KEY,
            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            token_hash TEXT UNIQUE NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
            expires_at TIMESTAMPTZ NOT NULL,
            revoked_at TIMESTAMPTZ
        );
        CREATE TABLE IF NOT EXISTS ws_tickets (
            id SERIAL PRIMARY KEY,
            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            role TEXT NOT NULL,
            agent_id TEXT,
            token_hash TEXT UNIQUE NOT NULL,
            expires_at TIMESTAMPTZ NOT NULL,
            used BOOLEAN NOT NULL DEFAULT FALSE
        );
        ALTER TABLE devices ADD COLUMN IF NOT EXISTS token_hash TEXT;
        ALTER TABLE devices ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;
    `

	_, err := db.Exec(query)
	return err
}

func main() {
	// Load a local .env file if there is one. Real environment variables win.
	if err := loadDotEnv(".env"); err != nil {
		log.Printf("could not read .env: %v", err)
	}

	sessionTTL = envDuration("SESSION_TTL", sessionTTL)
	ticketTTL = envDuration("TICKET_TTL", ticketTTL)
	allowedOrigins = envList("ALLOWED_ORIGINS")

	databaseURL := envOr("DATABASE_URL", "")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set (copy .env.example to .env)")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("cannot reach database: %v", err)
	}
	log.Println("Connected to Postgres")

	if err := initDB(db); err != nil {
		log.Fatalf("could not initialize database: %v", err)
	}
	log.Println("Database initialized")

	store := dbSessionStore{db: db}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(db))
	mux.HandleFunc("/register", registerHandler(db))
	mux.HandleFunc("/login", loginHandler(db))
	mux.HandleFunc("/ws", handleWebSocket(db))
	mux.HandleFunc("/ws/ticket", requireAuth(store, wsTicketHandler(db)))
	mux.HandleFunc("/command", requireAuth(store, sendCommandHandler(db)))
	mux.HandleFunc("/pair/start", requireAuth(store, handlePairStart(db)))
	mux.HandleFunc("/pair/claim", requireAuth(store, handlePairClaim(db)))
	mux.HandleFunc("/devices/revoke", requireAuth(store, revokeDeviceHandler(db)))

	addr := envOr("RELAY_ADDR", defaultAddr)
	log.Printf("Server running on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
