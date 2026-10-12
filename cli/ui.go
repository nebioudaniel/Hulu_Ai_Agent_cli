package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"
	"github.com/skip2/go-qrcode"
)

// ── grayscale / black & white palette ──────────────────────
var (
	subtleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	brightStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Bold(true)
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	// logo: gray fade
	logoRow0 = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	logoRow1 = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	logoRow2 = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	// clean input box with a fixed professional width
	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("236")).
			Padding(0, 1).
			Width(65)

	tipDot = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

var logoAgentTitle = []string{
	`█   █ █   █ █    █    █   █ `,
	`█▀▀▀█ █   █ █    █    █   █`,
	`█   █ █▄▄▄█ █▄▄▄ █▄▄▄ █▄▄▄█ `,
}

var logoAgent = []string{
	`▄▀▀▀▄ ▄▀▀▀  █▀▀▀▀ █▀▄ █ ▀▀█▀▀`,
	`█▀▀▀█ █  █▄ █▀▀▀  █  ▀█   █  `,
	`█   █ █▄▄▄█ █▄▄▄▄ █   █   █  `,
}

type LogEntry struct {
	Timestamp string
	Command   string
	Output    string
	IsError   bool
}

type model struct {
	serverAddr      string
	agentID         string
	token           string
	deviceToken     string
	projectDir      string
	provider        ProviderConfig
	connected       bool
	statusMsg       string
	conn            *websocket.Conn
	width           int
	height          int
	showPairing     bool
	showLogs        bool
	pairingCode     string
	qrString        string
	pairingErr      string
	logs            []LogEntry
	reconnectDelay  time.Duration
	pendingApproval pendingApproval
}

// pendingApproval tracks a command waiting for the phone's answer.
type pendingApproval struct {
	requestID string
	cmdStr    string
}

// Custom messages
type connectedMsg struct{ conn *websocket.Conn }
type errMsg struct{ err error }
type authErrMsg struct{ err error }
type commandMsg struct{ cmdStr string }
type commandResultMsg struct {
	cmdStr string
	output string
	isErr  bool
}
type pairingFetchedMsg struct {
	code string
	qr   string
	err  string
}
type deviceTokenMsg struct {
	token string
	err   error
}
type reconnectMsg struct{}
type heartbeatTickMsg struct{}
type ignoredMsg struct{}
type approvalResponseMsg struct {
	requestID string
	approved  bool
}
type approvalTimeoutMsg struct{ requestID string }
type chatMsg struct{ prompt string }
type chatResultMsg struct {
	prompt string
	output string
	isErr  bool
}
type connectMsg struct {
	baseURL string
	model   string
	apiKey  string
}

// wsEnvelope is the JSON wrapper for messages between the app and the agent.
type wsEnvelope struct {
	Type      string `json:"type"`
	Command   string `json:"command"`
	Prompt    string `json:"prompt"`
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
}

const (
	heartbeatInterval = 20 * time.Second
	maxReconnectDelay = 30 * time.Second
	approvalTimeout   = 60 * time.Second
)

func initialModel(serverAddr, agentID, token, deviceToken, projectDir string, provider ProviderConfig) model {
	return model{
		serverAddr:     serverAddr,
		agentID:        agentID,
		token:          token,
		deviceToken:    deviceToken,
		projectDir:     projectDir,
		provider:       provider,
		connected:      false,
		statusMsg:      "Connecting to relay server...",
		reconnectDelay: time.Second,
	}
}

// Async WebSocket connection. A short-lived one-time ticket is fetched first,
// so the long-lived session/device tokens never appear in a URL.
func connectWebSocket(serverAddr, token, deviceToken, agentID string) tea.Cmd {
	return func() tea.Msg {
		ticket, status, err := fetchTicket(serverAddr, token, deviceToken, "agent", agentID)
		if err != nil {
			if status == http.StatusUnauthorized {
				return authErrMsg{err: err}
			}
			return errMsg{err: err}
		}

		wsURL := fmt.Sprintf("ws://%s/ws?ticket=%s", serverAddr, url.QueryEscape(ticket))
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			return errMsg{err: err}
		}
		return connectedMsg{conn: conn}
	}
}

// ensureDeviceToken registers this agent with the relay and saves a revocable
// device token so WebSocket tickets no longer need the session token.
func ensureDeviceToken(serverAddr, token, agentID string) tea.Cmd {
	return func() tea.Msg {
		if token == "" || agentID == "" {
			return deviceTokenMsg{}
		}
		raw, status, err := fetchDeviceToken(serverAddr, token, agentID)
		if err != nil {
			return deviceTokenMsg{err: fmt.Errorf("device token (HTTP %d): %w", status, err)}
		}
		return deviceTokenMsg{token: raw}
	}
}

func listenForCommands(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return errMsg{err: err}
		}
		var env wsEnvelope
		if err := json.Unmarshal(message, &env); err != nil {
			// Tolerate legacy plain-text commands.
			return commandMsg{cmdStr: string(message)}
		}
		switch env.Type {
		case "command":
			if env.Command == "" {
				return ignoredMsg{}
			}
			return commandMsg{cmdStr: env.Command}
		case "approval_response":
			return approvalResponseMsg{requestID: env.RequestID, approved: env.Approved}
		case "chat":
			if env.Prompt == "" {
				return ignoredMsg{}
			}
			return chatMsg{prompt: env.Prompt}
		case "connect":
			return connectMsg{baseURL: env.BaseURL, model: env.Model, apiKey: env.APIKey}
		default:
			// Pings and unknown control messages keep the link alive but need
			// no local action.
			return ignoredMsg{}
		}
	}
}

// heartbeat keeps the relay online: it updates last-seen on the server so the
// relay does not declare this agent offline.
func heartbeat(conn *websocket.Conn) tea.Cmd {
	return tea.Tick(heartbeatInterval, func(t time.Time) tea.Msg {
		if conn != nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`))
		}
		return heartbeatTickMsg{}
	})
}

// sendResult pushes a command result back to the phone over the WebSocket.
func sendResult(conn *websocket.Conn, cmdStr, output string, isErr bool) {
	resp, err := json.Marshal(map[string]any{
		"type":    "command_result",
		"command": cmdStr,
		"output":  output,
		"ok":      !isErr,
	})
	if err != nil || conn == nil {
		return
	}
	_ = conn.WriteMessage(websocket.TextMessage, resp)
}

// sendApprovalRequest asks the phone to approve a risky command.
func sendApprovalRequest(conn *websocket.Conn, requestID, cmdStr string) {
	if conn == nil {
		return
	}
	msg, err := json.Marshal(map[string]any{
		"type":       "approval_request",
		"request_id": requestID,
		"command":    cmdStr,
	})
	if err != nil {
		return
	}
	_ = conn.WriteMessage(websocket.TextMessage, msg)
}

// sendChatResult pushes a chat (AI) reply back to the phone.
func sendChatResult(conn *websocket.Conn, prompt, output string, isErr bool) {
	if conn == nil {
		return
	}
	msg, err := json.Marshal(map[string]any{
		"type":   "chat_result",
		"prompt": prompt,
		"output": output,
		"ok":     !isErr,
	})
	if err != nil {
		return
	}
	_ = conn.WriteMessage(websocket.TextMessage, msg)
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// chatCmd runs the configured provider in the background and replies to the
// phone. It never blocks the UI.
func chatCmd(conn *websocket.Conn, prompt string, provider ProviderConfig) tea.Cmd {
	return func() tea.Msg {
		output, err := provider.Chat(context.Background(), prompt)
		isErr := err != nil
		if isErr {
			output = err.Error()
		}
		sendChatResult(conn, prompt, output, isErr)
		return chatResultMsg{prompt: prompt, output: output, isErr: isErr}
	}
}

// executeAndRespond runs a command inside the project folder and sends the
// output back through the WebSocket. Allowlisted commands run with explicit
// arguments and no shell; anything approved on the phone runs through a shell.
func executeAndRespond(conn *websocket.Conn, projectDir, cmdStr string) tea.Cmd {
	return func() tea.Msg {
		var response string
		var isErr bool
		if argv, ok := allowlistEntry(cmdStr); ok && !containsShellChars(cmdStr) {
			response, isErr = runDirect(projectDir, argv)
		} else {
			response, isErr = runCommand(projectDir, cmdStr)
		}
		sendResult(conn, cmdStr, response, isErr)
		_ = logEvent("command_executed", map[string]any{
			"command": cmdStr,
			"ok":      !isErr,
			"output":  shortOutput(response),
		})
		return commandResultMsg{
			cmdStr: cmdStr,
			output: response,
			isErr:  isErr,
		}
	}
}

// Fetch pairing code from server asynchronously
func fetchPairingCode(serverAddr, token string) tea.Cmd {
	return func() tea.Msg {
		apiURL := fmt.Sprintf("http://%s/pair/start", serverAddr)
		req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer([]byte("{}")))
		if err != nil {
			return pairingFetchedMsg{err: "Pairing request failed"}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return pairingFetchedMsg{err: "Pairing request failed"}
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			return pairingFetchedMsg{err: "Session expired. Restart to log in."}
		}
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			return pairingFetchedMsg{err: "Server auth/DB error"}
		}

		var result struct {
			PairingCode string `json:"pairing_code"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return pairingFetchedMsg{err: "Failed to decode response"}
		}

		q, err := qrcode.New(result.PairingCode, qrcode.Low)
		if err != nil {
			return pairingFetchedMsg{err: "Failed to build QR"}
		}
		q.DisableBorder = true

		qrAscii := renderMiniQR(q, 3)

		return pairingFetchedMsg{code: result.PairingCode, qr: qrAscii}
	}
}

// renderMiniQR downsamples the QR matrix by `factor` and half-block compresses
func renderMiniQR(q *qrcode.QRCode, factor int) string {
	m := q.Bitmap()
	size := len(m)
	n := (size + factor - 1) / factor

	down := make([][]bool, n)
	for by := 0; by < n; by++ {
		down[by] = make([]bool, n)
		for bx := 0; bx < n; bx++ {
			black, total := 0, 0
			for y := by * factor; y < min((by+1)*factor, size); y++ {
				for x := bx * factor; x < min((bx+1)*factor, size); x++ {
					total++
					if m[y][x] {
						black++
					}
				}
			}
			down[by][bx] = black*2 >= total
		}
	}

	var b strings.Builder
	for y := 0; y < n; y += 2 {
		for x := 0; x < n; x++ {
			top := down[y][x]
			bottom := y+1 < n && down[y+1][x]
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		connectWebSocket(m.serverAddr, m.token, m.deviceToken, m.agentID),
		ensureDeviceToken(m.serverAddr, m.token, m.agentID),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" || key == "q" {
			if m.conn != nil {
				m.conn.Close()
			}
			return m, tea.Quit
		}
		// Toggle pairing view on 'p'
		if key == "p" {
			m.showPairing = !m.showPairing
			m.showLogs = false
			m.pairingErr = ""
			if m.showPairing && m.pairingCode == "" {
				m.statusMsg = "Generating pairing code..."
				return m, fetchPairingCode(m.serverAddr, m.token)
			}
		}
		// Toggle logs view on 'ctrl+p'
		if key == "ctrl+p" {
			m.showLogs = !m.showLogs
			m.showPairing = false
		}

	case connectedMsg:
		m.connected = true
		m.conn = msg.conn
		m.reconnectDelay = time.Second
		m.statusMsg = "Active & listening"
		_ = logEvent("connected", map[string]any{"agent_id": m.agentID, "server": m.serverAddr})
		return m, tea.Batch(
			listenForCommands(m.conn),
			heartbeat(m.conn),
		)

	case deviceTokenMsg:
		if msg.token != "" {
			m.deviceToken = msg.token
			sess := session{Server: m.serverAddr, Token: m.token, AgentID: m.agentID, DeviceToken: m.deviceToken}
			_ = saveSession(sess)
		}

	case commandMsg:
		_ = logEvent("command_received", map[string]any{"command": msg.cmdStr, "agent_id": m.agentID})

		if m.pendingApproval.requestID != "" {
			sendResult(m.conn, msg.cmdStr, "Busy: another command is waiting for approval", true)
			return m, listenForCommands(m.conn)
		}

		// Allowlisted commands run immediately, without a shell. Everything
		// else needs the phone to approve within 60 seconds (denied by
		// default).
		if isAutoRun(msg.cmdStr) {
			m.statusMsg = fmt.Sprintf("Executing: %s", msg.cmdStr)
			return m, tea.Batch(
				executeAndRespond(m.conn, m.projectDir, msg.cmdStr),
				listenForCommands(m.conn),
			)
		}

		id := newRequestID()
		m.pendingApproval = pendingApproval{requestID: id, cmdStr: msg.cmdStr}
		sendApprovalRequest(m.conn, id, msg.cmdStr)
		m.statusMsg = "Command needs approval — waiting for phone (60s)"
		_ = logEvent("approval_requested", map[string]any{"command": msg.cmdStr, "request_id": id})
		return m, tea.Batch(
			listenForCommands(m.conn),
			tea.Tick(approvalTimeout, func(t time.Time) tea.Msg {
				return approvalTimeoutMsg{requestID: id}
			}),
		)

	case approvalResponseMsg:
		if m.pendingApproval.requestID != "" && m.pendingApproval.requestID == msg.requestID {
			cmdStr := m.pendingApproval.cmdStr
			m.pendingApproval = pendingApproval{}
			if !msg.approved {
				m.statusMsg = "Command denied on phone"
				sendResult(m.conn, cmdStr, "Denied on phone", true)
				_ = logEvent("approval_denied", map[string]any{"command": cmdStr})
				return m, listenForCommands(m.conn)
			}
			m.statusMsg = fmt.Sprintf("Approved — executing: %s", cmdStr)
			_ = logEvent("approval_approved", map[string]any{"command": cmdStr})
			return m, tea.Batch(
				executeAndRespond(m.conn, m.projectDir, cmdStr),
				listenForCommands(m.conn),
			)
		}
		return m, listenForCommands(m.conn)

	case approvalTimeoutMsg:
		// Deny by default. No new listener: one is already reading.
		if m.pendingApproval.requestID == msg.requestID {
			cmdStr := m.pendingApproval.cmdStr
			m.pendingApproval = pendingApproval{}
			m.statusMsg = "Approval timed out — denied by default"
			sendResult(m.conn, cmdStr, "Denied: no approval within 60 seconds", true)
			_ = logEvent("approval_timeout", map[string]any{"command": cmdStr})
		}
		return m, nil

	case chatMsg:
		m.statusMsg = "Thinking…"
		_ = logEvent("chat_requested", map[string]any{"prompt": shortOutput(msg.prompt)})
		return m, tea.Batch(
			chatCmd(m.conn, msg.prompt, m.provider),
			listenForCommands(m.conn),
		)

	case chatResultMsg:
		if msg.isErr {
			m.statusMsg = "Chat failed: " + shortOutput(msg.output)
		} else {
			m.statusMsg = "Chat reply sent"
		}
		_ = logEvent("chat_result", map[string]any{"ok": !msg.isErr, "output": shortOutput(msg.output)})
		return m, nil

	case connectMsg:
		// Never log or echo api_key.
		m.provider = ProviderConfig{BaseURL: msg.baseURL, Model: msg.model, APIKey: msg.apiKey}
		m.statusMsg = "Provider configured"
		_ = logEvent("provider_configured", map[string]any{"base_url": msg.baseURL, "model": msg.model})

		cfg, _ := loadConfig()
		cfg.ProjectDir = m.projectDir
		cfg.Provider = m.provider
		if err := saveConfig(cfg); err != nil {
			m.statusMsg = "Provider configured, but saving failed: " + err.Error()
		}

		if err := m.provider.Validate(); err != nil {
			m.statusMsg = "Provider: " + err.Error()
			return m, listenForCommands(m.conn)
		}
		return m, tea.Batch(
			chatCmd(m.conn, "Reply with the single word: OK", m.provider),
			listenForCommands(m.conn),
		)

	case commandResultMsg:
		m.statusMsg = fmt.Sprintf("Executed: %s", msg.cmdStr)
		m.logs = append(m.logs, LogEntry{
			Timestamp: time.Now().Format("15:04:05"),
			Command:   msg.cmdStr,
			Output:    strings.TrimSpace(msg.output),
			IsError:   msg.isErr,
		})
		return m, nil

	case heartbeatTickMsg:
		return m, heartbeat(m.conn)

	case ignoredMsg:
		return m, listenForCommands(m.conn)

	case pairingFetchedMsg:
		if msg.err != "" {
			m.pairingErr = msg.err
		} else {
			m.pairingCode = msg.code
			m.qrString = msg.qr
			m.statusMsg = "Pairing code generated!"
		}

	case authErrMsg:
		m.connected = false
		m.pendingApproval = pendingApproval{}
		m.statusMsg = "Session expired. Restart to log in."
		_ = logEvent("session_expired", nil)
		return m, nil

	case reconnectMsg:
		return m, connectWebSocket(m.serverAddr, m.token, m.deviceToken, m.agentID)

	case errMsg:
		m.connected = false
		m.conn = nil
		m.pendingApproval = pendingApproval{}
		_ = logEvent("disconnected", map[string]any{"error": msg.err.Error()})
		delay := m.reconnectDelay
		m.reconnectDelay *= 2
		if m.reconnectDelay > maxReconnectDelay {
			m.reconnectDelay = maxReconnectDelay
		}
		m.statusMsg = fmt.Sprintf("Connection lost, retrying in %s", delay)
		return m, tea.Tick(delay, func(t time.Time) tea.Msg {
			return reconnectMsg{}
		})
	}

	return m, nil
}

func (m model) View() string {
	if m.width == 0 {
		m.width, m.height = 80, 24
	}

	// ── logo ──
	var logo strings.Builder
	for i, line := range logoAgentTitle {
		logo.WriteString(renderLogoRow(i, line))
	}
	logo.WriteString("\n")
	for i, line := range logoAgent {
		logo.WriteString(renderLogoRow(i, line))
	}

	// ── status dot ──
	status := errorStyle.Render("● Disconnected")
	if m.connected {
		status = successStyle.Render("● Connected")
	}

	// ── box content: swap text area ↔ QR pairing ↔ execution logs ──
	var boxContent string
	if m.showPairing {
		boxContent = m.pairingBoxContent(status)
	} else if m.showLogs {
		boxContent = m.logsBoxContent()
	} else {
		placeholder := subtleStyle.Render("Ask anything… ") +
			lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(`"Fix broken tests"`)
		agentLine := subtleStyle.Render("Agent · ") + brightStyle.Render(m.agentID) +
			"    " + status + "    " + subtleStyle.Render(m.statusMsg)
		boxContent = lipgloss.JoinVertical(lipgloss.Left,
			placeholder,
			agentLine,
		)
	}

	inputBox := inputBoxStyle.Render(boxContent)
	boxWidth := lipgloss.Width(inputBox)

	// ── hint row under the box ──
	rightHintText := "press 'p' pair    ctrl+p logs"
	if m.showPairing {
		rightHintText = "press 'p' return    ctrl+p logs"
	} else if m.showLogs {
		rightHintText = "ctrl+p return dashboard"
	}
	leftHint := subtleStyle.Render("agent.go")
	rightHint := subtleStyle.Render(rightHintText)
	gap := boxWidth - lipgloss.Width(leftHint) - lipgloss.Width(rightHint)
	if gap < 1 {
		gap = 1
	}
	hints := leftHint + strings.Repeat(" ", gap) + rightHint

	// ── tip line ──
	tip := " " + tipDot.Render("●") + " " +
		subtleStyle.Render("Tip  'p' pair · 'ctrl+p' logs · phone sends /connect · /chat · /command")

	content := lipgloss.JoinVertical(lipgloss.Center,
		strings.TrimRight(logo.String(), "\n"),
		"",
		inputBox,
		"",
		hints,
		"",
		tip,
	)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

// pairingBoxContent renders the QR + code inside the main box's text area
func (m model) pairingBoxContent(status string) string {
	if m.pairingErr != "" {
		return lipgloss.JoinVertical(lipgloss.Left,
			errorStyle.Render("⚠ Pairing error: ")+subtleStyle.Render(m.pairingErr),
			subtleStyle.Render("Agent · ")+brightStyle.Render(m.agentID)+"    "+status,
		)
	}
	if m.pairingCode == "" {
		return lipgloss.JoinVertical(lipgloss.Left,
			subtleStyle.Render("Generating secure pairing code…"),
			subtleStyle.Render("Agent · ")+brightStyle.Render(m.agentID)+"    "+status,
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		brightStyle.Render("Scan to pair a mobile device"),
		"",
		m.qrString,
		"",
		subtleStyle.Render("Code:  ")+brightStyle.Render(m.pairingCode),
		subtleStyle.Render("Agent · ")+brightStyle.Render(m.agentID)+"    "+status,
	)
}

// logsBoxContent renders execution audit history
func (m model) logsBoxContent() string {
	if len(m.logs) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			brightStyle.Render("Execution Audit Logs"),
			"",
			subtleStyle.Render("No commands executed yet. Ready for relay tasks."),
		)
	}

	var sb strings.Builder
	sb.WriteString(brightStyle.Render("Execution Audit Logs") + "\n\n")

	// Show last 3 logs for compact view
	start := 0
	if len(m.logs) > 3 {
		start = len(m.logs) - 3
	}

	for i := start; i < len(m.logs); i++ {
		log := m.logs[i]
		prefix := successStyle.Render("[$]")
		if log.IsError {
			prefix = errorStyle.Render("[!]")
		}
		sb.WriteString(fmt.Sprintf("%s %s: %s\n", subtleStyle.Render(log.Timestamp), prefix, brightStyle.Render(log.Command)))
		if log.Output != "" {
			// Truncate output lines for compact TUI view
			lines := strings.Split(log.Output, "\n")
			if len(lines) > 2 {
				sb.WriteString(subtleStyle.Render(fmt.Sprintf("    %s\n    ... (%d more lines)\n", lines[0], len(lines)-1)))
			} else {
				sb.WriteString(subtleStyle.Render("    "+log.Output) + "\n")
			}
		}
	}

	return sb.String()
}

func renderLogoRow(i int, line string) string {
	switch i {
	case 0:
		return logoRow0.Render(line) + "\n"
	case 1:
		return logoRow1.Render(line) + "\n"
	default:
		return logoRow2.Render(line) + "\n"
	}
}
