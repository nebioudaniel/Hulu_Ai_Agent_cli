package main 
import (
	"net/http"
	"database/sql"
	"encoding/json"
)
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func registerHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if req.Email == "" || req.Password == "" {
			http.Error(w, "Email and password are required", http.StatusBadRequest)
			return
		}

		hash, err := HashPassword(req.Password)
		if err != nil {
			http.Error(w, "Failed to process password", http.StatusInternalServerError)
			return
		}

		_, err = db.Exec(
			"INSERT INTO users (email, password_hash) VALUES ($1, $2)",
			req.Email,
			hash,
		)
		if err != nil {
			http.Error(w, "Could not register user", http.StatusConflict)
			return
		}

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("User registered successfully"))
	}
}
func loginHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Find user in database by email
		var storedHash string
		err := db.QueryRow("SELECT password_hash FROM users WHERE email = $1", req.Email).Scan(&storedHash)
		if err != nil {
			http.Error(w, "Invalid email or password", http.StatusUnauthorized)
			return
		}

		// Verify password using Argon2id check
		match, err := CheckPassword(req.Password, storedHash)
		if err != nil || !match {
			http.Error(w, "Invalid email or password", http.StatusUnauthorized)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Login successful!"))
	}
}