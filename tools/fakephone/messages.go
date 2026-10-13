package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// envelope mirrors the JSON messages the relay forwards between the phone (role
// "app") and the agent. Only the fields fakephone needs are declared.
type envelope struct {
	Type      string `json:"type"`
	Command   string `json:"command"`
	Prompt    string `json:"prompt"`
	Output    string `json:"output"`
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
	OK        bool   `json:"ok"`
	AgentID   string `json:"agent_id"`
	Status    string `json:"status"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
}

func parseEnvelope(raw []byte) (envelope, bool) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return envelope{}, false
	}
	return env, true
}

// approvalReply builds the reply the CLI agent expects for a pending approval.
func approvalReply(requestID string, approved bool) []byte {
	msg, err := json.Marshal(map[string]any{
		"type":       "approval_response",
		"request_id": requestID,
		"approved":   approved,
	})
	if err != nil {
		return []byte(`{"type":"approval_response","approved":false}`)
	}
	return msg
}

// formatMessage renders a received message in a short, human-readable form. It
// never prints secret fields (notably the provider api_key in "connect").
func formatMessage(raw []byte) string {
	env, ok := parseEnvelope(raw)
	if !ok {
		return fmt.Sprintf("unparseable message (%d bytes): %s", len(raw), truncate(string(raw), 200))
	}

	switch env.Type {
	case "approval_request":
		return fmt.Sprintf("approval_request [%s] command: %s", env.RequestID, env.Command)
	case "command_result":
		return fmt.Sprintf("command_result (%s) %s\n%s", okLabel(env.OK), env.Command, indent(env.Output))
	case "chat_result":
		return fmt.Sprintf("chat_result (%s) prompt: %s\n%s", okLabel(env.OK), env.Prompt, indent(env.Output))
	case "peer":
		return fmt.Sprintf("peer: agent %s is %s", env.AgentID, env.Status)
	case "ping":
		return "ping"
	case "connect":
		// The api_key is deliberately omitted.
		return fmt.Sprintf("connect: base_url=%s model=%s (api key hidden)", env.BaseURL, env.Model)
	case "":
		return fmt.Sprintf("message (%d bytes): %s", len(raw), truncate(string(raw), 200))
	default:
		return fmt.Sprintf("%s message (%d bytes)", env.Type, len(raw))
	}
}

func okLabel(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func truncate(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return "   (no output)"
	}
	return "   " + strings.ReplaceAll(s, "\n", "\n   ")
}
