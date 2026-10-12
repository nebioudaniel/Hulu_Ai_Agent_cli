package main

import (
	"encoding/json"
	"fmt"
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

// resolveProjectDir validates and canonicalizes the folder where remote
// commands may run. There is no default: the folder must be set, absolute,
// exist, and not be "/" or the home directory. Symlinks are resolved so a
// configured path cannot silently point somewhere else.
func resolveProjectDir(projectDir string) (string, error) {
	if projectDir == "" {
		return "", fmt.Errorf("project folder is required: pass -project, set AGENT_PROJECT_DIR, or save it in the config")
	}
	if !filepath.IsAbs(projectDir) {
		return "", fmt.Errorf("project folder must be an absolute path: %s", projectDir)
	}

	resolved, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return "", fmt.Errorf("could not resolve project folder %s: %v", projectDir, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("could not stat project folder %s: %v", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project folder is not a directory: %s", resolved)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	if resolved == "/" || (home != "" && resolved == home) {
		return "", fmt.Errorf("project folder must not be / or the home directory: %s", resolved)
	}
	return resolved, nil
}
