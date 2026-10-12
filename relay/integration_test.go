package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// testDB opens the test database. Tests that need Postgres skip unless
// TEST_DATABASE_URL is set.
func testDB(t *testing.T) *sql.DB {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database test")
	}

	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping test db: %v", err)
	}
	if err := initDB(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return db
}

func cleanDB(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		"DELETE FROM ws_tickets",
		"DELETE FROM sessions",
		"DELETE FROM pairing_codes",
		"DELETE FROM devices",
		"DELETE FROM users",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("clean %q: %v", q, err)
		}
	}
}

func callRegister(t *testing.T, db *sql.DB, email, password string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/register",
		strings.NewReader(`{"email":"`+email+`","password":"`+password+`"}`))
	registerHandler(db)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s: got %d %s", email, rec.Code, rec.Body.String())
	}
}

func callLogin(t *testing.T, db *sql.DB, email, password string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader(`{"email":"`+email+`","password":"`+password+`"}`))
	loginHandler(db)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: got %d %s", email, rec.Code, rec.Body.String())
	}

	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("login returned empty token")
	}
	return resp.Token
}

func callAuthed(store sessionStore, inner http.HandlerFunc, token, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	requireAuth(store, inner)(rec, req)
	return rec
}

func TestAuthAndPairingFlow(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	cleanDB(t, db)

	store := dbSessionStore{db: db}

	callRegister(t, db, "alice@example.com", "alice-password")
	callRegister(t, db, "bob@example.com", "bob-password")

	tokenA := callLogin(t, db, "alice@example.com", "alice-password")
	tokenB := callLogin(t, db, "bob@example.com", "bob-password")

	// /command without a token must be rejected.
	rec := callAuthed(store, func(w http.ResponseWriter, r *http.Request) {}, "", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated command: got %d", rec.Code)
	}

	// Alice starts pairing.
	rec = callAuthed(store, handlePairStart(db), tokenA, `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pair/start: got %d %s", rec.Code, rec.Body.String())
	}
	var start struct {
		PairingCode string `json:"pairing_code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &start); err != nil {
		t.Fatalf("decode pair/start: %v", err)
	}
	if start.PairingCode == "" {
		t.Fatal("empty pairing code")
	}

	// A wrong user cannot claim Alice's code.
	rec = callAuthed(store, handlePairClaim(db), tokenB,
		`{"pairing_code":"`+start.PairingCode+`","agent_id":"wrong-agent"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong user claim should be forbidden, got %d %s", rec.Code, rec.Body.String())
	}

	// Alice claims her own code.
	rec = callAuthed(store, handlePairClaim(db), tokenA,
		`{"pairing_code":"`+start.PairingCode+`","agent_id":"agent-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("first claim should succeed, got %d %s", rec.Code, rec.Body.String())
	}

	// The same code cannot be reused.
	rec = callAuthed(store, handlePairClaim(db), tokenA,
		`{"pairing_code":"`+start.PairingCode+`","agent_id":"agent-2"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reused code should fail, got %d", rec.Code)
	}

	// An expired code cannot be claimed.
	if _, err := db.Exec(
		`INSERT INTO pairing_codes (user_id, code_hash, expires_at, used)
		 SELECT id, $1, $2, false FROM users WHERE email = 'alice@example.com'`,
		hashToken("expired-code"), time.Now().UTC().Add(-time.Minute),
	); err != nil {
		t.Fatalf("insert expired code: %v", err)
	}
	rec = callAuthed(store, handlePairClaim(db), tokenA,
		`{"pairing_code":"expired-code","agent_id":"agent-3"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expired code should fail, got %d", rec.Code)
	}

	// A ticket requires auth and can be used exactly once.
	rec = callAuthed(store, wsTicketHandler(db), tokenA, `{"role":"agent","agent_id":"mac-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ws/ticket: got %d %s", rec.Code, rec.Body.String())
	}
	var ticket struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}

	id, err := consumeTicket(context.Background(), db, ticket.Ticket)
	if err != nil {
		t.Fatalf("consume ticket: %v", err)
	}
	if id.role != "agent" || id.agentID != "mac-1" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if _, err := consumeTicket(context.Background(), db, ticket.Ticket); err == nil {
		t.Fatal("ticket should not be usable twice")
	}

	// No ticket means no WebSocket.
	wsRec := httptest.NewRecorder()
	wsReq := httptest.NewRequest(http.MethodGet, "/ws", nil)
	handleWebSocket(db)(wsRec, wsReq)
	if wsRec.Code != http.StatusBadRequest {
		t.Fatalf("ws without ticket should be 400, got %d", wsRec.Code)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	cleanDB(t, db)

	callRegister(t, db, "carol@example.com", "carol-password")

	var userID string
	if err := db.QueryRow("SELECT id FROM users WHERE email='carol@example.com'").Scan(&userID); err != nil {
		t.Fatalf("load user: %v", err)
	}

	raw := "expired-session-token"
	if _, err := db.Exec(
		`INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, hashToken(raw), time.Now().UTC().Add(-time.Hour),
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	store := dbSessionStore{db: db}
	rec := callAuthed(store, func(w http.ResponseWriter, r *http.Request) {}, raw, `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired session should be 401, got %d", rec.Code)
	}
}
