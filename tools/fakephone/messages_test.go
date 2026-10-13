package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestApprovalReply(t *testing.T) {
	raw := approvalReply("req-123", true)

	var got envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if got.Type != "approval_response" {
		t.Fatalf("type = %q, want approval_response", got.Type)
	}
	if got.RequestID != "req-123" {
		t.Fatalf("request_id = %q, want req-123", got.RequestID)
	}
	if !got.Approved {
		t.Fatal("approved = false, want true")
	}

	raw = approvalReply("req-123", false)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if got.Approved {
		t.Fatal("approved = true, want false")
	}
}

func TestFormatMessage(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		notWant []string
	}{
		{
			name:  "approval request shows command",
			input: `{"type":"approval_request","request_id":"r1","command":"rm -rf build"}`,
			want:  []string{"approval_request", "r1", "rm -rf build"},
		},
		{
			name:  "command result shows output",
			input: `{"type":"command_result","command":"ls","output":"a\nb","ok":true}`,
			want:  []string{"command_result", "ok", "ls", "a", "b"},
		},
		{
			name:  "failed command result",
			input: `{"type":"command_result","command":"bad","output":"boom","ok":false}`,
			want:  []string{"command_result", "failed", "boom"},
		},
		{
			name:  "peer status",
			input: `{"type":"peer","agent_id":"mac","status":"online"}`,
			want:  []string{"peer", "mac", "online"},
		},
		{
			name:    "connect never leaks api key",
			input:   `{"type":"connect","base_url":"https://api.example","model":"m1","api_key":"SUPER-SECRET"}`,
			want:    []string{"connect", "https://api.example", "m1", "hidden"},
			notWant: []string{"SUPER-SECRET"},
		},
		{
			name:  "unknown type stays short",
			input: `{"type":"mystery","secret":"nope"}`,
			want:  []string{"mystery"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMessage([]byte(tt.input))
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("formatMessage = %q, missing %q", got, w)
				}
			}
			for _, n := range tt.notWant {
				if strings.Contains(got, n) {
					t.Errorf("formatMessage leaked %q: %q", n, got)
				}
			}
		})
	}
}

func TestIsYes(t *testing.T) {
	yes := []string{"y", "Y", "yes", "YES", " yes \n"}
	no := []string{"", "n", "no", "nope", "\n", "true"}

	for _, s := range yes {
		if !isYes(s) {
			t.Errorf("isYes(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isYes(s) {
			t.Errorf("isYes(%q) = true, want false", s)
		}
	}
}

// TestAppFlow exercises the real login, ticket, and WebSocket app path against a
// stub relay, then verifies an approval_request is answered with the reply the
// CLI agent expects.
func TestAppFlow(t *testing.T) {
	const (
		wantEmail    = "alice@example.com"
		wantPassword = "alice-password"
		sessionToken = "session-token-abc"
		ticket       = "one-time-ticket"
		agentID      = "agent-a"
	)

	replyCh := make(chan envelope, 1)
	mux := http.NewServeMux()

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Email, Password string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Email != wantEmail || req.Password != wantPassword {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": sessionToken})
	})

	mux.HandleFunc("/ws/ticket", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+sessionToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			Role    string `json:"role"`
			AgentID string `json:"agent_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Role != "app" || req.AgentID != agentID {
			http.Error(w, "bad ticket request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ticket": ticket})
	})

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticket") != ticket {
			http.Error(w, "bad ticket", http.StatusUnauthorized)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		send, _ := json.Marshal(map[string]any{
			"type":       "approval_request",
			"request_id": "req-xyz",
			"command":    "rm -rf build",
		})
		if err := conn.WriteMessage(websocket.TextMessage, send); err != nil {
			return
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err == nil {
			replyCh <- env
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")

	token, err := login(addr, wantEmail, wantPassword)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	gotTicket, err := fetchTicket(addr, token, agentID)
	if err != nil {
		t.Fatalf("fetchTicket: %v", err)
	}

	wsURL := "ws://" + addr + "/ws?ticket=" + gotTicket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read approval_request: %v", err)
	}
	if !strings.Contains(formatMessage(raw), "rm -rf build") {
		t.Fatalf("unexpected approval_request rendering: %s", formatMessage(raw))
	}
	env, _ := parseEnvelope(raw)

	if err := conn.WriteMessage(websocket.TextMessage, approvalReply(env.RequestID, true)); err != nil {
		t.Fatalf("write approval reply: %v", err)
	}

	select {
	case got := <-replyCh:
		if got.Type != "approval_response" || got.RequestID != "req-xyz" || !got.Approved {
			t.Fatalf("unexpected reply: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for approval reply")
	}
}
