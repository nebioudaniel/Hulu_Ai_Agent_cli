package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

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

// Rate limiters. Kept in memory; fine for one relay process.
var (
	loginLimiter = newRateLimiter(10, 15*time.Minute)
	pairLimiter  = newRateLimiter(5, 15*time.Minute)
)

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

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
		if !validPassword(req.Password) {
			http.Error(w, "Password must be between 10 and 128 characters", http.StatusBadRequest)
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
		if _, err := w.Write([]byte("User registered successfully")); err != nil {
			log.Printf("register: write response: %v", err)
		}
	}
}

// loginHandler checks the password and returns a fresh session token. Only the
// sha256 of the token is stored.
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

		if req.Email == "" || req.Password == "" {
			http.Error(w, "Email and password are required", http.StatusBadRequest)
			return
		}
		if !validPassword(req.Password) {
			http.Error(w, "Password must be between 10 and 128 characters", http.StatusBadRequest)
			return
		}

		// Limit by client IP and by email address.
		if !loginLimiter.allow("ip:" + clientIP(r)) {
			http.Error(w, "Too many login attempts, try again later", http.StatusTooManyRequests)
			return
		}
		if !loginLimiter.allow("email:" + strings.ToLower(req.Email)) {
			http.Error(w, "Too many login attempts, try again later", http.StatusTooManyRequests)
			return
		}

		var userID, storedHash string
		err := db.QueryRow(
			"SELECT id, password_hash FROM users WHERE email = $1",
			req.Email,
		).Scan(&userID, &storedHash)
		if err != nil {
			http.Error(w, "Invalid email or password", http.StatusUnauthorized)
			return
		}

		match, err := CheckPassword(req.Password, storedHash)
		if err != nil || !match {
			http.Error(w, "Invalid email or password", http.StatusUnauthorized)
			return
		}

		raw, err := newToken()
		if err != nil {
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}

		expiresAt := time.Now().UTC().Add(sessionTTL)
		_, err = db.Exec(
			"INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)",
			userID, hashToken(raw), expiresAt,
		)
		if err != nil {
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"token":      raw,
			"expires_at": expiresAt.Format(time.RFC3339),
		}); err != nil {
			log.Printf("login: encode response: %v", err)
		}
	}
}

// sendCommandHandler forwards a command to the authenticated user's paired
// agent. The user comes from the session token, not from the request body.
func sendCommandHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := userIDFromContext(r.Context())
		if userID == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var req CommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// The agent must be paired to this user and not revoked.
		var deviceID int
		err := db.QueryRow(
			"SELECT id FROM devices WHERE agent_id = $1 AND user_id = $2 AND revoked_at IS NULL",
			req.AgentID, userID,
		).Scan(&deviceID)
		if err != nil {
			http.Error(w, "Agent is not paired to this account", http.StatusForbidden)
			return
		}

		agent, exists := manager.agent(req.AgentID)
		if !exists {
			http.Error(w, "Agent offline or not found", http.StatusNotFound)
			return
		}

		if err := agent.conn.WriteMessage(websocket.TextMessage, []byte(req.Command)); err != nil {
			http.Error(w, "Failed to send command", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Command dispatched to agent successfully!")); err != nil {
			log.Printf("command: write response: %v", err)
		}
	}
}

// handlePairStart creates a one-time pairing code for the authenticated user.
func handlePairStart(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := userIDFromContext(r.Context())
		if userID == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if !pairLimiter.allow("user:" + userID) {
			http.Error(w, "Too many pairing attempts, try again later", http.StatusTooManyRequests)
			return
		}

		plainCode, err := generateRandomCode()
		if err != nil {
			http.Error(w, "Failed to generate code", http.StatusInternalServerError)
			return
		}

		codeHash := hashToken(plainCode)
		expiresAt := time.Now().UTC().Add(2 * time.Minute)

		_, err = db.Exec(
			`INSERT INTO pairing_codes (user_id, code_hash, expires_at, used)
			 VALUES ($1, $2, $3, FALSE)`,
			userID, codeHash, expiresAt,
		)
		if err != nil {
			http.Error(w, "Failed to save pairing code", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(map[string]string{
			"pairing_code": plainCode,
			"expires_at":   expiresAt.Format(time.RFC3339),
		}); err != nil {
			log.Printf("pair/start: encode response: %v", err)
		}
	}
}

// handlePairClaim pairs an agent to the authenticated user's account. The
// pairing code must belong to the same user.
func handlePairClaim(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := userIDFromContext(r.Context())
		if userID == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
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
		if req.PairingCode == "" || req.AgentID == "" {
			http.Error(w, "pairing_code and agent_id are required", http.StatusBadRequest)
			return
		}

		codeHash := hashToken(req.PairingCode)

		var codeID int
		var codeUserID string
		err := db.QueryRow(
			`SELECT id, user_id FROM pairing_codes
			 WHERE code_hash = $1 AND used = false AND expires_at > NOW()`,
			codeHash,
		).Scan(&codeID, &codeUserID)
		if err != nil {
			http.Error(w, "Invalid, expired, or already used pairing code", http.StatusBadRequest)
			return
		}

		// The code must belong to the same account that is claiming it.
		if codeUserID != userID {
			http.Error(w, "Pairing code does not belong to this account", http.StatusForbidden)
			return
		}

		// The claimed agent_id must not already belong to another account.
		// Claiming never changes an owner: that would be a takeover.
		var owner string
		ownerErr := db.QueryRow("SELECT user_id FROM devices WHERE agent_id = $1", req.AgentID).Scan(&owner)
		switch ownerErr {
		case nil:
			if owner != userID {
				http.Error(w, "Agent already belongs to another account", http.StatusConflict)
				return
			}
		case sql.ErrNoRows:
			// Fresh agent; inserted below.
		default:
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		if _, err := db.Exec("UPDATE pairing_codes SET used = true WHERE id = $1", codeID); err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		if ownerErr == nil {
			// Re-pairing the user's own agent refreshes it, owner unchanged.
			if _, err := db.Exec(
				`UPDATE devices SET revoked_at = NULL, paired_at = NOW()
				 WHERE agent_id = $1 AND user_id = $2`,
				req.AgentID, userID,
			); err != nil {
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}
		} else {
			if _, err := db.Exec(
				"INSERT INTO devices (user_id, agent_id) VALUES ($1, $2)",
				userID, req.AgentID,
			); err != nil {
				http.Error(w, "Failed to register paired device", http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"message": "Device successfully paired!",
		}); err != nil {
			log.Printf("pair/claim: encode response: %v", err)
		}
	}
}

// revokeDeviceHandler marks one of the user's devices as revoked and drops any
// live WebSocket connection for it.
func revokeDeviceHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := userIDFromContext(r.Context())
		if userID == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var req struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.AgentID == "" {
			http.Error(w, "agent_id is required", http.StatusBadRequest)
			return
		}

		res, err := db.Exec(
			"UPDATE devices SET revoked_at = NOW() WHERE agent_id = $1 AND user_id = $2",
			req.AgentID, userID,
		)
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		affected, err := res.RowsAffected()
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		if affected == 0 {
			http.Error(w, "Device not found", http.StatusNotFound)
			return
		}

		manager.DisconnectAgent(req.AgentID)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"message": "Device revoked",
		}); err != nil {
			log.Printf("devices/revoke: encode response: %v", err)
		}
	}
}

// deviceTokenUser resolves a user + agent from the X-Device-Token header.
// Device tokens are issued per agent, stored as hashes, and are revocable.
func deviceTokenUser(db *sql.DB, r *http.Request) (userID, agentID string, valid bool) {
	raw := r.Header.Get("X-Device-Token")
	if raw == "" {
		return "", "", false
	}
	err := db.QueryRow(
		"SELECT user_id, agent_id FROM devices WHERE token_hash = $1 AND revoked_at IS NULL",
		hashToken(raw),
	).Scan(&userID, &agentID)
	if err != nil {
		return "", "", false
	}
	return userID, agentID, true
}

func sessionTokenUser(db *sql.DB, r *http.Request) (string, bool) {
	raw := bearerToken(r)
	if raw == "" {
		return "", false
	}
	store := dbSessionStore{db: db}
	userID, err := store.sessionUserID(r.Context(), hashToken(raw), time.Now().UTC())
	if err != nil {
		return "", false
	}
	return userID, true
}

// wsTicketHandler issues a short-lived, one-time ticket used to open a
// WebSocket. Authentication comes from a session Bearer token or from a
// revocable X-Device-Token for the agent itself. The long-lived secret never
// appears in a URL.
func wsTicketHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := userIDFromContext(r.Context())
		deviceAgentID := ""
		if userID == "" {
			var ok bool
			userID, deviceAgentID, ok = deviceTokenUser(db, r)
			if !ok {
				userID, ok = sessionTokenUser(db, r)
				if !ok {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
		}

		var req struct {
			Role    string `json:"role"`
			AgentID string `json:"agent_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.Role != "agent" && req.Role != "app" {
			http.Error(w, "role must be \"agent\" or \"app\"", http.StatusBadRequest)
			return
		}
		if req.Role == "agent" && req.AgentID == "" {
			http.Error(w, "agent_id is required for role agent", http.StatusBadRequest)
			return
		}

		// A device token is bound to one agent and cannot jump to another.
		if deviceAgentID != "" && req.Role == "agent" && req.AgentID != deviceAgentID {
			http.Error(w, "Device token does not match agent", http.StatusForbidden)
			return
		}

		// An agent id belongs to at most one user. For role app, agent_id is
		// the optional target agent and must belong to the same user.
		if req.AgentID != "" {
			var owner string
			err := db.QueryRow("SELECT user_id FROM devices WHERE agent_id = $1", req.AgentID).Scan(&owner)
			if err == nil && owner != userID {
				http.Error(w, "Agent belongs to another account", http.StatusForbidden)
				return
			}
			if err != nil && err != sql.ErrNoRows {
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}
		}

		ticket, err := newToken()
		if err != nil {
			http.Error(w, "Failed to create ticket", http.StatusInternalServerError)
			return
		}

		expiresAt := time.Now().UTC().Add(ticketTTL)
		_, err = db.Exec(
			`INSERT INTO ws_tickets (user_id, role, agent_id, token_hash, expires_at)
			 VALUES ($1, $2, $3, $4, $5)`,
			userID, req.Role, sql.NullString{String: req.AgentID, Valid: req.AgentID != ""}, hashToken(ticket), expiresAt,
		)
		if err != nil {
			http.Error(w, "Failed to create ticket", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"ticket":     ticket,
			"expires_at": expiresAt.Format(time.RFC3339),
		}); err != nil {
			log.Printf("ws/ticket: encode response: %v", err)
		}
	}
}

// deviceTokenHandler issues a revocable device token for the authenticated
// user's agent. The agent uses it for WebSocket tickets, so it survives session
// expiry and can be revoked without touching the password.
func deviceTokenHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := userIDFromContext(r.Context())
		if userID == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var req struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.AgentID == "" {
			http.Error(w, "agent_id is required", http.StatusBadRequest)
			return
		}

		// An agent id belongs to at most one user.
		var owner string
		err := db.QueryRow("SELECT user_id FROM devices WHERE agent_id = $1", req.AgentID).Scan(&owner)
		if err == nil && owner != userID {
			http.Error(w, "Agent belongs to another account", http.StatusForbidden)
			return
		}
		if err != nil && err != sql.ErrNoRows {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		raw, err := newToken()
		if err != nil {
			http.Error(w, "Failed to create device token", http.StatusInternalServerError)
			return
		}

		_, err = db.Exec(
			`INSERT INTO devices (user_id, agent_id, token_hash, revoked_at)
			 VALUES ($1, $2, $3, NULL)
			 ON CONFLICT (agent_id) DO UPDATE
			 SET token_hash = $3, revoked_at = NULL, paired_at = NOW()`,
			userID, req.AgentID, hashToken(raw),
		)
		if err != nil {
			http.Error(w, "Failed to save device token", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"device_token": raw,
		}); err != nil {
			log.Printf("devices/token: encode response: %v", err)
		}
	}
}

// healthHandler reports whether the relay can reach its database.
func healthHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := db.Ping(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "unhealthy"}); encErr != nil {
				log.Printf("health: encode response: %v", encErr)
			}
			return
		}

		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			log.Printf("health: encode response: %v", err)
		}
	}
}
