package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// eventLogPath returns the local JSONL audit log location. AGENT_EVENT_LOG
// overrides it (used by tests and for custom deployments).
func eventLogPath() (string, error) {
	if p := os.Getenv("AGENT_EVENT_LOG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-cli", "events.jsonl"), nil
}

// logEvent appends one JSON line to the local event log. Callers must never
// pass secrets (tokens, api keys) in data; this file is plain text.
func logEvent(kind string, data map[string]any) error {
	path, err := eventLogPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	entry := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"event":     kind,
	}
	for k, v := range data {
		entry[k] = v
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// shortOutput trims output for the audit log so a huge command result cannot
// bloat the file.
func shortOutput(s string) string {
	const limit = 500
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "...[truncated]"
}
