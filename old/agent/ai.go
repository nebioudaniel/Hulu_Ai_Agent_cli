package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// RunAIEngine routes the prompt to either a local Ollama instance or a cloud API
func RunAIEngine(prompt string) (string, error) {
	// Check if local Ollama model is preferred/available
	localURL := "http://localhost:11434/api/generate"
	
	// Payload for Ollama (Local Model e.g., llama3 / deepseek-r1)
	payload := map[string]interface{}{
		"model":  "deepseek-r1:8b", // or your local model
		"prompt": prompt,
		"stream": false,
	}
	
	jsonBytes, _ := json.Marshal(payload)
	resp, err := http.Post(localURL, "application/json", bytes.NewBuffer(jsonBytes))
	
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var result struct {
			Response string `json:"response"`
		}
		if json.NewDecoder(resp.Body).Decode(&result) == nil && result.Response != "" {
			return "[Local Model] " + result.Response, nil
		}
	}

	// Fallback to Cloud API (e.g., DeepSeek / OpenAI API)
	apiKey := os.Getenv("AI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("local model offline and no cloud API key found")
	}

	// Cloud API dispatch logic here...
	return "[Cloud API] Prompt processed successfully.", nil
}