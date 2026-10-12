package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

const httpTimeout = 10 * time.Second

// session is the locally saved login state. The token is stored with 0600
// permissions in the user config directory.
type session struct {
	Server    string `json:"server"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

func sessionPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-cli", "session.json"), nil
}

func loadSession() (session, error) {
	path, err := sessionPath()
	if err != nil {
		return session{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return session{}, err
	}

	var s session
	if err := json.Unmarshal(data, &s); err != nil {
		return session{}, err
	}
	return s, nil
}

func saveSession(s session) error {
	path, err := sessionPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ensureSession returns a usable session. It prefers an AGENT_TOKEN env
// override (used for tests), then a saved session, then an interactive login.
func ensureSession(server string) (session, error) {
	if tok := strings.TrimSpace(os.Getenv("AGENT_TOKEN")); tok != "" {
		return session{Server: server, Token: tok}, nil
	}

	if s, err := loadSession(); err == nil && s.Server == server && s.Token != "" {
		return s, nil
	}

	return login(server)
}

// login asks for email and password (password with no echo) and stores the
// returned session token.
func login(server string) (session, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Email: ")
	email, err := reader.ReadString('\n')
	if err != nil {
		return session{}, fmt.Errorf("reading email: %w", err)
	}
	email = strings.TrimSpace(email)

	fmt.Print("Password: ")
	pass, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Println()
	if err != nil {
		return session{}, fmt.Errorf("reading password: %w", err)
	}

	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	status, err := postJSON(server, "/login", "", map[string]string{
		"email":    email,
		"password": string(pass),
	}, &resp)
	if err != nil {
		return session{}, err
	}
	if status != http.StatusOK || resp.Token == "" {
		return session{}, fmt.Errorf("login failed (HTTP %d)", status)
	}

	s := session{Server: server, Token: resp.Token, ExpiresAt: resp.ExpiresAt}
	if err := saveSession(s); err != nil {
		return session{}, err
	}
	return s, nil
}

// fetchTicket asks the relay for a one-time WebSocket ticket. The HTTP status
// is returned so the caller can tell an expired session (401) from a network
// problem.
func fetchTicket(server, token, role, agentID string) (string, int, error) {
	body := map[string]string{"role": role}
	if agentID != "" {
		body["agent_id"] = agentID
	}

	var resp struct {
		Ticket string `json:"ticket"`
	}
	status, err := postJSON(server, "/ws/ticket", token, body, &resp)
	if err != nil {
		return "", 0, err
	}
	if status != http.StatusOK || resp.Ticket == "" {
		return "", status, fmt.Errorf("ticket request failed (HTTP %d)", status)
	}
	return resp.Ticket, status, nil
}

// postJSON sends a JSON POST to the relay and decodes the JSON reply on success.
func postJSON(server, path, token string, body, out any) (int, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+server+path, bytes.NewReader(buf))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if out == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}
