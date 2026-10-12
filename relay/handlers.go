package main 
import (
	"net/http"
	"database/sql"
	"encoding/json"
	"github.com/gorilla/websocket"
)
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type CommandRequest struct {
	AgentID string `json:"agent_id"`
	Command string `json:"command"`
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
func sendCommandHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req CommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Find connected agent in our WebSocket manager
		manager.mu.Lock()
		agentConn, exists := manager.agents[req.AgentID]
		manager.mu.Unlock()

		if !exists {
			http.Error(w, "Agent offline or not found", http.StatusNotFound)
			return
		}

		// Send command down the WebSocket to the agent CLI
		err := agentConn.WriteMessage(websocket.TextMessage, []byte(req.Command))
		if err != nil {
			http.Error(w, "Failed to send command", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Command dispatched to agent successfully!"))
	}
}