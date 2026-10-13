package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const (
	httpTimeout  = 10 * time.Second
	pingInterval = 30 * time.Second
)

func main() {
	serverPtr := flag.String("server", "127.0.0.1:8080", "Relay server address (host:port)")
	emailPtr := flag.String("email", "", "Account email to log in with")
	agentPtr := flag.String("agent", "", "Target agent id to route messages to")
	flag.Parse()

	if *emailPtr == "" {
		fmt.Fprintln(os.Stderr, "fakephone: -email is required")
		os.Exit(2)
	}
	if *agentPtr == "" {
		fmt.Fprintln(os.Stderr, "fakephone: -agent is required")
		os.Exit(2)
	}

	// Always restore the terminal, even on ctrl+c/SIGTERM (os.Exit skips defers).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		echo.restore()
		fmt.Fprintln(os.Stderr, "\nfakephone: interrupted")
		os.Exit(130)
	}()

	password, err := readPassword("Password: ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakephone: reading password: %v\n", err)
		os.Exit(1)
	}

	token, err := login(*serverPtr, *emailPtr, password)
	password = "" // drop the secret as soon as it is used
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakephone: login failed: %v\n", err)
		os.Exit(1)
	}

	if err := run(*serverPtr, *agentPtr, token); err != nil {
		fmt.Fprintf(os.Stderr, "fakephone: %v\n", err)
		os.Exit(1)
	}
}

// login exchanges email/password for a session token. The token is never
// printed.
func login(server, email, password string) (string, error) {
	var resp struct {
		Token string `json:"token"`
	}
	status, err := postJSON(server, "/login", "", map[string]string{
		"email":    email,
		"password": password,
	}, &resp)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK || resp.Token == "" {
		return "", fmt.Errorf("relay returned HTTP %d", status)
	}
	return resp.Token, nil
}

// fetchTicket requests a one-time WebSocket ticket for role "app". A phone does
// not need a device token, so the session bearer is used.
func fetchTicket(server, token, agentID string) (string, error) {
	body := map[string]string{"role": "app"}
	if agentID != "" {
		body["agent_id"] = agentID
	}

	var resp struct {
		Ticket string `json:"ticket"`
	}
	status, err := postJSON(server, "/ws/ticket", token, body, &resp)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK || resp.Ticket == "" {
		return "", fmt.Errorf("ticket request returned HTTP %d", status)
	}
	return resp.Ticket, nil
}

func run(server, agentID, token string) error {
	ticket, err := fetchTicket(server, token, agentID)
	if err != nil {
		return err
	}

	wsURL := fmt.Sprintf("ws://%s/ws?ticket=%s", server, url.QueryEscape(ticket))
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	defer conn.Close()

	fmt.Printf("fakephone connected as app; target agent %q. Press ctrl+c to quit.\n", agentID)

	approvals := make(chan envelope)
	errc := make(chan error, 1)
	go readLoop(conn, approvals, errc)
	go pingLoop(conn)

	in := bufio.NewReader(os.Stdin)
	for {
		select {
		case err := <-errc:
			return err
		case env := <-approvals:
			approved := askApproval(in, env)
			if err := conn.WriteMessage(websocket.TextMessage, approvalReply(env.RequestID, approved)); err != nil {
				return fmt.Errorf("sending approval: %w", err)
			}
			fmt.Printf("sent approval for %s: %s\n", env.RequestID, yesNo(approved))
		}
	}
}

// readLoop prints every message and forwards approval requests to the main
// goroutine so the terminal prompt does not race with the read.
func readLoop(conn *websocket.Conn, approvals chan<- envelope, errc chan<- error) {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			errc <- err
			return
		}
		fmt.Println("<- " + formatMessage(raw))

		if env, ok := parseEnvelope(raw); ok && env.Type == "approval_request" {
			approvals <- env
		}
	}
}

// pingLoop sends WebSocket control pings to keep the connection alive. Control
// frames are not forwarded by the relay and are safe to send concurrently.
func pingLoop(conn *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for range ticker.C {
		if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
			return
		}
	}
}

func askApproval(in *bufio.Reader, env envelope) bool {
	fmt.Println()
	fmt.Println("!! approval requested")
	fmt.Printf("   command: %s\n", env.Command)
	fmt.Printf("   request: %s\n", env.RequestID)
	fmt.Print("   approve? [y/N] ")

	line, err := in.ReadString('\n')
	if err != nil {
		fmt.Println()
		return false
	}
	return isYes(line)
}

func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func yesNo(b bool) string {
	if b {
		return "approved"
	}
	return "denied"
}

// postJSON sends a JSON POST with an optional Bearer token and decodes the JSON
// reply on success.
func postJSON(server, path, bearer string, body, out any) (int, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+server+path, bytes.NewReader(buf))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
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
