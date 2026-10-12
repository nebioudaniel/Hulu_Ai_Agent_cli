package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
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
	serverAddr  string
	agentID     string
	connected   bool
	statusMsg   string
	conn        *websocket.Conn
	width       int
	height      int
	showPairing bool
	showLogs    bool
	pairingCode string
	qrString    string
	pairingErr  string
	logs        []LogEntry
}

// Custom messages
type connectedMsg struct{ conn *websocket.Conn }
type errMsg struct{ err error }
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

func initialModel(serverAddr, agentID string) model {
	return model{
		serverAddr: serverAddr,
		agentID:    agentID,
		connected:  false,
		statusMsg:  "Connecting to relay server...",
	}
}

// Async WebSocket connection
func connectWebSocket(serverAddr, agentID string) tea.Cmd {
	return func() tea.Msg {
		wsURL := fmt.Sprintf("ws://%s/ws?role=agent&client_id=%s", serverAddr, agentID)
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			return errMsg{err: err}
		}
		return connectedMsg{conn: conn}
	}
}

func listenForCommands(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return errMsg{err: err}
		}
		return commandMsg{cmdStr: string(message)}
	}
}

// Executes a shell command locally and sends the output back through the WebSocket
func executeAndRespond(conn *websocket.Conn, cmdStr string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/C", cmdStr)
		} else {
			cmd = exec.Command("sh", "-c", cmdStr)
		}

		output, err := cmd.CombinedOutput()
		var response string
		isErr := false
		if err != nil {
			response = fmt.Sprintf("Error: %v\nOutput: %s", err, string(output))
			isErr = true
		} else {
			response = string(output)
		}

		if conn != nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(response))
		}

		return commandResultMsg{
			cmdStr: cmdStr,
			output: response,
			isErr:  isErr,
		}
	}
}

// Fetch pairing code from server asynchronously
func fetchPairingCode(serverAddr string) tea.Cmd {
	return func() tea.Msg {
		apiURL := fmt.Sprintf("http://%s/pair/start", serverAddr)
		resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer([]byte("{}")))
		if err != nil {
			return pairingFetchedMsg{err: "Pairing request failed"}
		}
		defer resp.Body.Close()

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
	return connectWebSocket(m.serverAddr, m.agentID)
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
				return m, fetchPairingCode(m.serverAddr)
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
		m.statusMsg = "Active & listening"
		return m, listenForCommands(m.conn)

	case commandMsg:
		m.statusMsg = fmt.Sprintf("Executing: %s", msg.cmdStr)
		return m, tea.Batch(
			executeAndRespond(m.conn, msg.cmdStr),
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

	case pairingFetchedMsg:
		if msg.err != "" {
			m.pairingErr = msg.err
		} else {
			m.pairingCode = msg.code
			m.qrString = msg.qr
			m.statusMsg = "Pairing code generated!"
		}

	case errMsg:
		m.connected = false
		m.statusMsg = "Connection failed (Retrying...)"
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return connectWebSocket(m.serverAddr, m.agentID)()
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
		subtleStyle.Render("Tip Press 'p' to pair mobile, 'ctrl+p' to view command logs")

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