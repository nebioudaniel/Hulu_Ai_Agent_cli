package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func mapToJSON(t *testing.T, body map[string]string) []byte {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return data
}

func livePost(t *testing.T, srvURL, path string, bearer, deviceToken string, body map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srvURL+path, bytes.NewReader(mapToJSON(t, body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if deviceToken != "" {
		req.Header.Set("X-Device-Token", deviceToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return resp.StatusCode, out
}

func claim(t *testing.T, srvURL, token, agentID, code string) {
	t.Helper()
	status, _ := livePost(t, srvURL, "/pair/claim", token, "", map[string]string{
		"pairing_code": code,
		"agent_id":     agentID,
	})
	if status != http.StatusOK {
		t.Fatalf("claim %s: got %d", agentID, status)
	}
}

func pairAndToken(t *testing.T, srvURL, sessionToken, agentID string) string {
	t.Helper()
	status, start := livePost(t, srvURL, "/pair/start", sessionToken, "", map[string]string{})
	if status != http.StatusCreated {
		t.Fatalf("pair/start: got %d", status)
	}
	claim(t, srvURL, sessionToken, agentID, start["pairing_code"].(string))

	status, dev := livePost(t, srvURL, "/devices/token", sessionToken, "", map[string]string{"agent_id": agentID})
	if status != http.StatusOK {
		t.Fatalf("devices/token: got %d", status)
	}
	return dev["device_token"].(string)
}

func wsTicket(t *testing.T, srvURL, bearer, deviceToken, role, agentID string) string {
	t.Helper()
	body := map[string]string{"role": role}
	if agentID != "" {
		body["agent_id"] = agentID
	}
	status, out := livePost(t, srvURL, "/ws/ticket", bearer, deviceToken, body)
	if status != http.StatusOK {
		t.Fatalf("ws/ticket (%s %s): got %d", role, agentID, status)
	}
	return out["ticket"].(string)
}

func dialWS(t *testing.T, srvURL, ticket string) *websocket.Conn {
	t.Helper()
	wsBase := "ws" + strings.TrimPrefix(srvURL, "http")
	u := wsBase + "/ws?ticket=" + url.QueryEscape(ticket)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

// readFor reads one message or fails the test if nothing arrives in time.
func readFor(t *testing.T, conn *websocket.Conn, timeout time.Duration) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected a message within %s: %v", timeout, err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	return msg
}

// ensureSilent checks that no message arrives within the timeout window.
func ensureSilent(t *testing.T, conn *websocket.Conn, timeout time.Duration) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("expected no message, but one arrived")
	}
	_ = conn.SetReadDeadline(time.Time{})
}

// TestRoutingAndPeers covers: device tokens, same-user routing, cross-user
// isolation, agent-to-app forwarding, and peer online/offline events.
func TestRoutingAndPeers(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	cleanDB(t, db)

	store := dbSessionStore{db: db}
	srv := httptest.NewServer(newMux(db, store))
	defer srv.Close()

	callRegister(t, db, "alice@example.com", "alice-password")
	callRegister(t, db, "bob@example.com", "bob-password")
	tokenA := callLogin(t, db, "alice@example.com", "alice-password")
	tokenB := callLogin(t, db, "bob@example.com", "bob-password")

	devTokenA := pairAndToken(t, srv.URL, tokenA, "agent-a")
	pairAndToken(t, srv.URL, tokenB, "agent-b")

	// Bobby cannot get a ticket targeting Alice's agent.
	status, _ := livePost(t, srv.URL, "/ws/ticket", tokenB, "",
		map[string]string{"role": "app", "agent_id": "agent-a"})
	if status != http.StatusForbidden {
		t.Fatalf("cross-user app ticket should be 403, got %d", status)
	}

	// Alice connects her phone first so she sees the agent come online.
	aliceApp := dialWS(t, srv.URL, wsTicket(t, srv.URL, tokenA, "", "app", "agent-a"))
	defer aliceApp.Close()

	// The agent logs in via its device token (no session token used).
	agentA := dialWS(t, srv.URL, wsTicket(t, srv.URL, "", devTokenA, "agent", "agent-a"))
	defer agentA.Close()

	// Alice's app sees the peer-online event.
	msg := readFor(t, aliceApp, 5*time.Second)
	if !strings.Contains(string(msg), `"status":"online"`) {
		t.Fatalf("expected peer online event, got %s", msg)
	}

	// Alice's phone sends a command; only her agent receives it.
	if err := aliceApp.WriteMessage(websocket.TextMessage,
		[]byte(`{"type":"command","command":"ls"}`)); err != nil {
		t.Fatalf("send command: %v", err)
	}
	msg = readFor(t, agentA, 5*time.Second)
	if !strings.Contains(string(msg), `"command":"ls"`) {
		t.Fatalf("agent should receive alice's command, got %s", msg)
	}

	// The agent replies; the result goes back to Alice's app.
	if err := agentA.WriteMessage(websocket.TextMessage,
		[]byte(`{"type":"command_result","command":"ls","output":"ok","ok":true}`)); err != nil {
		t.Fatalf("send result: %v", err)
	}
	msg = readFor(t, aliceApp, 5*time.Second)
	if !strings.Contains(string(msg), `"command_result"`) {
		t.Fatalf("app should receive agent result, got %s", msg)
	}

	// When the agent disconnects, the app hears the offline event.
	agentA.Close()
	msg = readFor(t, aliceApp, 5*time.Second)
	if !strings.Contains(string(msg), `"status":"offline"`) {
		t.Fatalf("expected peer offline event, got %s", msg)
	}
}

// TestDeviceTokenControlsAgentIdentity: a device token is bound to one agent.
func TestDeviceTokenControlsAgentIdentity(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	cleanDB(t, db)

	store := dbSessionStore{db: db}
	srv := httptest.NewServer(newMux(db, store))
	defer srv.Close()

	callRegister(t, db, "alice@example.com", "alice-password")
	tokenA := callLogin(t, db, "alice@example.com", "alice-password")
	devTokenA := pairAndToken(t, srv.URL, tokenA, "agent-x")

	// The device token can only open a ticket for its own agent_id.
	status, _ := livePost(t, srv.URL, "/ws/ticket", "", devTokenA,
		map[string]string{"role": "agent", "agent_id": "other-agent"})
	if status != http.StatusForbidden {
		t.Fatalf("device token must not claim another agent, got %d", status)
	}

	// Revoking the device kills its tickets at once.
	status, _ = livePost(t, srv.URL, "/devices/revoke", tokenA, "", map[string]string{"agent_id": "agent-x"})
	if status != http.StatusOK {
		t.Fatalf("revoke: got %d", status)
	}
	status, _ = livePost(t, srv.URL, "/ws/ticket", "", devTokenA,
		map[string]string{"role": "agent", "agent_id": "agent-x"})
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked device token should be rejected, got %d", status)
	}
}
