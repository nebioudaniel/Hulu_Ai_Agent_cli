package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// ClientManager keeps track of connected agents and apps
type Manager struct {
	agents map[string]*websocket.Conn
	mu     sync.Mutex
}

var manager = Manager{
	agents: make(map[string]*websocket.Conn),
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	clientID := r.URL.Query().Get("client_id")
	if role == "" || clientID == "" {
		http.Error(w, "Missing role or client_id parameter", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Register connection if it's an agent
	if role == "agent" {
		manager.mu.Lock()
		manager.agents[clientID] = conn
		manager.mu.Unlock()

		defer func() {
			manager.mu.Lock()
			delete(manager.agents, clientID)
			manager.mu.Unlock()
			log.Printf("Agent disconnected: %s", clientID)
		}()

		log.Printf("Agent registered successfully: %s", clientID)
	}

	// Listen for messages
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Read error for %s (%s): %v", role, clientID, err)
			break
		}

		log.Printf("Received from [%s:%s]: %s", role, clientID, string(msg))

		// If an app sends a message, forward it to the target agent!
		if role == "app" {
			targetAgent := r.URL.Query().Get("target_agent")
			manager.mu.Lock()
			agentConn, exists := manager.agents[targetAgent]
			manager.mu.Unlock()

			if exists {
				err := agentConn.WriteMessage(websocket.TextMessage, msg)
				if err != nil {
					log.Printf("Failed to forward message to agent: %v", err)
				}
			}
		}
	}
}