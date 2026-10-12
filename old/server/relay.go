package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// AgentHub manages active WebSocket connections for connected agents
type AgentHub struct {
	sync.Mutex
	clients map[string]*websocket.Conn
}

var Hub = AgentHub{
	clients: make(map[string]*websocket.Conn),
}

// CommandRequest represents the payload sent by your mobile app to the relay
type CommandRequest struct {
	AgentID string `json:"agent_id"`
	Command string `json:"command"`
}

// HandleWebSocket manages the incoming connection from your Mac agent CLI
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[!] WebSocket upgrade failed: %v\n", err)
		return
	}

	role := r.URL.Query().Get("role")
	clientID := r.URL.Query().Get("client_id")

	if role == "agent" && clientID != "" {
		Hub.Lock()
		Hub.clients[clientID] = conn
		Hub.Unlock()
		fmt.Printf("[+] Agent connected: %s\n", clientID)

		// Clean up on disconnect
		defer func() {
			Hub.Lock()
			delete(Hub.clients, clientID)
			Hub.Unlock()
			conn.Close()
			fmt.Printf("[-] Agent disconnected: %s\n", clientID)
		}()

		// Keep connection alive and read responses/streams from agent
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			// Relay output back or log it
			fmt.Printf("[%s Output]: %s\n", clientID, string(msg))
		}
	}
}

// HandleDispatchCommand receives API calls from mobile and forwards them to the Mac agent
func HandleDispatchCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	Hub.Lock()
	conn, exists := Hub.clients[req.AgentID]
	Hub.Unlock()

	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Agent not connected"})
		return
	}

	// Dispatch command down the WebSocket to your Mac
	err := conn.WriteMessage(websocket.TextMessage, []byte(req.Command))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to send command to agent"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "dispatched",
		"agent":   req.AgentID,
		"command": req.Command,
	})
}