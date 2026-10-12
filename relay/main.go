package main

import (
	"database/sql"
	"fmt"
	_ "github.com/lib/pq"
	"log"
	"net/http"
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

	connStr := "host=127.0.0.1 port=5433 user=agentuser password=agentpassword dbname=agentdb sslmode=disable"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close() // close the connection when main ends
	err = db.Ping()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Connected to Postgres successfully!")
	//calling initdb
	if err := initDB(db); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Connected to Postgres and initialized the database!")
	password := "mysecretpassword"
		hash, err := HashPassword(password)
		if err != nil {
			log.Fatalf("Failed to hash password: %v", err)
		}
		fmt.Printf("Generated Hash: %s\n", hash)
	
		match, err := CheckPassword(password, hash)
		if err != nil || !match {
			log.Fatalf("Password verification failed for correct password!")
		}
		fmt.Println("Correct password test passed!")
	
		wrongMatch, _ := CheckPassword("wrongpassword", hash)
		if wrongMatch {
			log.Fatalf("Password verification succeeded for wrong password!")
		}
		http.HandleFunc("/ws", handleWebSocket)
		http.HandleFunc("/command", sendCommandHandler(db))
		fmt.Println("Wrong password test passed!")
		http.HandleFunc("/register", registerHandler(db))
		http.HandleFunc("/login", loginHandler(db))
		http.HandleFunc("/pair/start", handlePairStart(db))
		http.HandleFunc("/pair/claim", handlePairClaim(db))
		log.Println("Server running on http://localhost:8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
}
