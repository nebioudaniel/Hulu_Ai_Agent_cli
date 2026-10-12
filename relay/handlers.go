package main 
import (
	"net/http"
	"database/sql"
	"encoding/json"
	"github.com/gorilla/websocket"
	"crypto/rand"
	"encoding/hex"
	"time"
	"fmt"
	"crypto/sha256"
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
// Helper to generate a random pairing code string
// Generate a cryptographically secure pairing code.
func generateRandomCode() (string, error) {
	randomBytes := make([]byte, 16)

	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(randomBytes), nil
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
// Handler for POST /pair/start
func handlePairStart(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// TODO: Replace this with the authenticated user's ID.
		// Your users table uses UUID, so userID must be a UUID string.
   userID := "7b91f6be-9bf1-4151-a20f-b3920fbc1f9c"
		if userID == "" {
			http.Error(w, "Authentication required", http.StatusUnauthorized)
			return
		}

		// 1. Generate a random pairing code.
		plainCode, err := generateRandomCode()
		if err != nil {
			http.Error(w, "Failed to generate code",
				http.StatusInternalServerError)
			return
		}

		// 2. Hash the code before storing it.
		hashBytes := sha256.Sum256([]byte(plainCode))
		codeHash := hex.EncodeToString(hashBytes[:])

		// 3. Expire the code after two minutes.
		expiresAt := time.Now().UTC().Add(2 * time.Minute)

		// 4. Save the pairing code hash.
		query := `
			INSERT INTO pairing_codes (user_id, code_hash, expires_at, used)
			VALUES ($1, $2, $3, FALSE)
		`

		_, err = db.Exec(query, userID, codeHash, expiresAt)
		if err != nil {
			http.Error(w, "Failed to save pairing code",
				http.StatusInternalServerError)
			return
		}

		// 5. Return the plaintext code to the client.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		if err := json.NewEncoder(w).Encode(map[string]string{
			"pairing_code": plainCode,
			"expires_at":   expiresAt.Format(time.RFC3339),
		}); err != nil {
			fmt.Println("Failed to write pairing response:", err)
		}
	}
}
// Handler for POST /pair/claim
func handlePairClaim(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			PairingCode string `json:"pairing_code"`
			AgentID     string `json:"agent_id"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// 1. Hash the incoming code to match what's in the DB
		hashBytes := sha256.Sum256([]byte(req.PairingCode))
		codeHash := hex.EncodeToString(hashBytes[:])

		// 2. Query the database for a valid, unused, unexpired code
		var codeID int
		var userID string
		query := `
			SELECT id, user_id FROM pairing_codes 
			WHERE code_hash = $1 AND used = false AND expires_at > NOW()
		`
		err := db.QueryRow(query, codeHash).Scan(&codeID, &userID)
		if err != nil {
			http.Error(w, "Invalid, expired, or already used pairing code", http.StatusBadRequest)
			return
		}

		// 3. Mark the pairing code as used (one-time use protection)
		_, err = db.Exec("UPDATE pairing_codes SET used = true WHERE id = $1", codeID)
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		// Success response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Device successfully paired!",
		})
	}
}
