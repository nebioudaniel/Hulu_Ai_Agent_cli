package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"
)

const defaultAddr = "127.0.0.1:8080"

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
            user_id UUID REFERENCES users(id),
            code_hash TEXT NOT NULL,
            expires_at TIMESTAMP NOT NULL,
            used BOOLEAN DEFAULT FALSE
        );
        CREATE TABLE IF NOT EXISTS devices (
            id SERIAL PRIMARY KEY,
            user_id UUID REFERENCES users(id),
            agent_id TEXT UNIQUE NOT NULL,
            paired_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
        );
    `

	_, err := db.Exec(query)
	return err
}

func main() {
	// Load a local .env file if there is one. Real environment variables win.
	if err := loadDotEnv(".env"); err != nil {
		log.Printf("could not read .env: %v", err)
	}

	databaseURL := os.Getenv("DATABASE_URL")
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

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(db))
	mux.HandleFunc("/ws", handleWebSocket)
	mux.HandleFunc("/command", sendCommandHandler(db))
	mux.HandleFunc("/register", registerHandler(db))
	mux.HandleFunc("/login", loginHandler(db))
	mux.HandleFunc("/pair/start", handlePairStart(db))
	mux.HandleFunc("/pair/claim", handlePairClaim(db))

	addr := os.Getenv("RELAY_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	log.Printf("Server running on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
