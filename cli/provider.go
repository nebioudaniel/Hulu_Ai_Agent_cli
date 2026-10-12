package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const providerTimeout = 60 * time.Second

func (p ProviderConfig) Validate() error {
	if strings.TrimSpace(p.BaseURL) == "" {
		return fmt.Errorf("no provider configured; send /connect with base_url, model and optional api_key")
	}
	if strings.TrimSpace(p.Model) == "" {
		return fmt.Errorf("provider model is empty")
	}
	return nil
}

// Chat sends a prompt to an OpenAI-compatible chat endpoint and returns the
// assistant reply. Timeouts and HTTP errors become clear user-facing errors.
func (p ProviderConfig) Chat(ctx context.Context, prompt string) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}

	base := strings.TrimRight(p.BaseURL, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}

	payload := map[string]any{
		"model":    p.Model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("provider unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("provider reply is invalid JSON: %w", err)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("provider returned an empty reply")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
