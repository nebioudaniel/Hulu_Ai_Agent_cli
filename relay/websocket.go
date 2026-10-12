package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// originAllowed blocks cross-site WebSocket hijacking. Browser clients send an
// Origin header and must be listed in ALLOWED_ORIGINS. Non-browser clients
// (the CLI, tests) send no Origin and are allowed.
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range allowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

var upgrader = websocket.Upgrader{
	CheckOrigin: originAllowed,
}

// wsClient is one authenticated WebSocket connection.
type wsClient struct {
	conn    *websocket.Conn
	userID  string
	role    string
	agentID string
}

// Manager keeps track of connected agents. Identity always comes from a ticket.
type Manager struct {
	mu     sync.Mutex
	agents map[string]*wsClient
}

var manager = Manager{
	agents: make(map[string]*wsClient),
}

func (m *Manager) addAgent(agentID string, c *wsClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.agents[agentID]; ok && old.conn != nil {
		_ = old.conn.Close()
	}
	m.agents[agentID] = c
}

func (m *Manager) removeAgent(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.agents, agentID)
}

func (m *Manager) agent(agentID string) (*wsClient, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.agents[agentID]
	return c, ok
}

// DisconnectAgent closes and removes a live agent connection (used on revoke).
func (m *Manager) DisconnectAgent(agentID string) {
	m.mu.Lock()
	c, ok := m.agents[agentID]
	if ok {
		delete(m.agents, agentID)
	}
	m.mu.Unlock()

	if ok && c.conn != nil {
		_ = c.conn.Close()
	}
}

// ticketIdentity is the server-side truth for a WebSocket connection.
type ticketIdentity struct {
	userID  string
	role    string
	agentID string
}

// consumeTicket marks a ticket as used and returns its identity. Using a single
// UPDATE ... RETURNING makes the one-time check atomic.
func consumeTicket(ctx context.Context, db *sql.DB, raw string) (ticketIdentity, error) {
	var id ticketIdentity
	var agentID sql.NullString

	err := db.QueryRowContext(ctx,
		`UPDATE ws_tickets SET used = true
		 WHERE token_hash = $1 AND used = false AND expires_at > now()
		 RETURNING user_id, role, agent_id`,
		hashToken(raw),
	).Scan(&id.userID, &id.role, &agentID)
	if err != nil {
		return ticketIdentity{}, err
	}

	id.agentID = agentID.String
	return id, nil
}

// deviceRevoked reports whether an agent id has been revoked.
func deviceRevoked(ctx context.Context, db *sql.DB, agentID string) bool {
	var revoked sql.NullTime
	err := db.QueryRowContext(ctx,
		"SELECT revoked_at FROM devices WHERE agent_id = $1", agentID,
	).Scan(&revoked)
	if err != nil {
		return false
	}
	return revoked.Valid
}

// handleWebSocket authenticates via a one-time ticket in the query string. It
// ignores role and client_id query parameters for identity.
func handleWebSocket(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticket := r.URL.Query().Get("ticket")
		if ticket == "" {
			http.Error(w, "Missing ticket", http.StatusBadRequest)
			return
		}

		id, err := consumeTicket(r.Context(), db, ticket)
		if err != nil {
			http.Error(w, "Invalid or expired ticket", http.StatusUnauthorized)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket upgrade error: %v", err)
			return
		}
		defer conn.Close()

		if id.role == "agent" {
			manager.addAgent(id.agentID, &wsClient{conn: conn, userID: id.userID, role: id.role, agentID: id.agentID})
			defer manager.removeAgent(id.agentID)
			log.Printf("Agent connected: %s", id.agentID)
		}

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}

			// A revoked device must not keep working.
			if id.role == "agent" && deviceRevoked(r.Context(), db, id.agentID) {
				log.Printf("Agent %s is revoked; closing", id.agentID)
				return
			}

			log.Printf("Received from [%s:%s]: %s", id.role, id.agentID, string(msg))

			// An app may forward to an agent only when both belong to the same
			// user. The target comes from the ticket, never from the URL.
			if id.role == "app" && id.agentID != "" {
				target, exists := manager.agent(id.agentID)
				if exists && target.userID == id.userID {
					if err := target.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
						log.Printf("Failed to forward message to agent: %v", err)
					}
				}
			}
		}
	}
}
