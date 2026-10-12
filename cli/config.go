package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ProviderConfig points at an OpenAI-compatible chat API. Ollama exposes the
// same /v1/chat/completions endpoint, so one client works for both.
type ProviderConfig struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"api_key,omitempty"`
}

// AgentConfig is the local agent settings file. Secrets are stored 0600, never
// logged, and never sent back to the relay.
type AgentConfig struct {
	ProjectDir string         `json:"project_dir"`
	Provider   ProviderConfig `json:"provider"`
}

func configPath() (string, error) {
	if p := os.Getenv("AGENT_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-cli", "config.json"), nil
}

func loadConfig() (AgentConfig, error) {
	cfg := AgentConfig{}
	path, err := configPath()
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func saveConfig(cfg AgentConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
