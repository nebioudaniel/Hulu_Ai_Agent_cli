package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Heartbeat defaults. A peer is considered offline if it sends nothing for
// heartbeatTimeout (3 missed 20-second beats).
const (
	defaultHeartbeatInterval = 20 * time.Second
	heartbeatMaxMisses       = 3
	heartbeatSweepInterval   = 10 * time.Second
)

var (
	heartbeatInterval = defaultHeartbeatInterval
	heartbeatTimeout  = defaultHeartbeatInterval * heartbeatMaxMisses
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

// wsClient is one authenticated WebSocket connection. lastSeen updates on every
// message so the heartbeat monitor can spot dead peers.
type wsClient struct {
	conn     *websocket.Conn
	userID   string
	role     string
	agentID  string
	lastSeen atomic.Int64
}

func newWSClient(conn *websocket.Conn, id ticketIdentity) *wsClient {
	c := &wsClient{conn: conn, userID: id.userID, role: id.role, agentID: id.agentID}
	c.touch()
	return c
}

func (c *wsClient) touch() {
	c.lastSeen.Store(time.Now().UTC().UnixNano())
}

func (c *wsClient) lastSeenTime() time.Time {
	return time.Unix(0, c.lastSeen.Load()).UTC()
}

// Manager keeps track of connected agents and apps. Identity always comes from
// a consumed ticket (which records the owning user).
type Manager struct {
	mu     sync.Mutex
	agents map[string]*wsClient
	apps   map[int64]*wsClient
	seq    int64
}

var manager = Manager{
	agents: make(map[string]*wsClient),
	apps:   make(map[int64]*wsClient),
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

// addApp registers an app connection and returns its unique id.
func (m *Manager) addApp(c *wsClient) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	m.apps[m.seq] = c
	return m.seq
}

func (m *Manager) removeApp(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.apps, id)
}

// notifyApps sends a message to every connected app of the given user.
func (m *Manager) notifyApps(userID string, msg []byte) {
	m.mu.Lock()
	var targets []*wsClient
	for _, c := range m.apps {
		if c.userID == userID {
			targets = append(targets, c)
		}
	}
	m.mu.Unlock()

	for _, c := range targets {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Printf("notify app failed: %v", err)
		}
	}
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

// expireIdleAgents drops agents that stopped sending heartbeats. The reader
// goroutine notices the closed connection, tears down the session, and emits
// the peer-offline event.
func (m *Manager) expireIdleAgents() {
	now := time.Now().UTC()
	m.mu.Lock()
	var toClose []*websocket.Conn
	for agentID, c := range m.agents {
		if now.Sub(c.lastSeenTime()) > heartbeatTimeout {
			log.Printf("Agent %s idle for %s; considering offline", agentID, heartbeatTimeout)
			toClose = append(toClose, c.conn)
			delete(m.agents, agentID)
		}
	}
	m.mu.Unlock()

	for _, conn := range toClose {
		_ = conn.Close()
	}
}

func startHeartbeatMonitor() {
	ticker := time.NewTicker(heartbeatSweepInterval)
	defer ticker.Stop()
	for range ticker.C {
		manager.expireIdleAgents()
	}
}

// notifyPeerStatus tells every connected app of the user that an agent went
// online or offline.
func notifyPeerStatus(userID, agentID string, online bool) {
	status := "offline"
	if online {
		status = "online"
	}
	event, err := json.Marshal(map[string]any{
		"type":     "peer",
		"agent_id": agentID,
		"status":   status,
	})
	if err != nil {
		return
	}
	manager.notifyApps(userID, event)
}

// isPingMessage reports whether a message is an internal heartbeat ping. Pings
// keep the connection alive but are not forwarded to other peers.
func isPingMessage(msg []byte) bool {
	var m struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		return false
	}
	return m.Type == "ping"
}

// messageType extracts the "type" field of a message for logging. Never log
// the message body itself.
func messageType(msg []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		return "unknown"
	}
	return m.Type
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

		client := newWSClient(conn, id)

		var appID int64
		switch id.role {
		case "agent":
			manager.addAgent(id.agentID, client)
			defer manager.removeAgent(id.agentID)
			notifyPeerStatus(id.userID, id.agentID, true)
			defer notifyPeerStatus(id.userID, id.agentID, false)
			log.Printf("Agent connected: %s", id.agentID)
		case "app":
			appID = manager.addApp(client)
			defer manager.removeApp(appID)
			log.Printf("App connected: user=%s target=%q", id.userID, id.agentID)
		}

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			client.touch()

			// A revoked device must not keep working.
			if id.role == "agent" && deviceRevoked(r.Context(), db, id.agentID) {
				log.Printf("Agent %s is revoked; closing", id.agentID)
				return
			}

			log.Printf("Received role=%s type=%s bytes=%d", id.role, messageType(msg), len(msg))

			switch id.role {
			case "app":
				// Forward only to a paired agent of the SAME user. The target
				// comes from the ticket, never from the URL.
				if id.agentID != "" {
					target, exists := manager.agent(id.agentID)
					if exists && target.userID == id.userID {
						if err := target.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
							log.Printf("Failed to forward message to agent: %v", err)
						}
					}
				}
			case "agent":
				// Agent messages (results, approvals, chat) go back to every
				// connected app of the same user, so phones can listen again.
				if !isPingMessage(msg) {
					manager.notifyApps(id.userID, msg)
				}
			}
		}
	}
}
