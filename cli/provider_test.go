package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderChatSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth header = %q", got)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "test-model" {
			t.Errorf("model = %q", body.Model)
		}
		if len(body.Messages) != 1 || body.Messages[0].Content != "hello" {
			t.Errorf("messages = %+v", body.Messages)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "world"}},
			},
		})
	}))
	defer srv.Close()

	p := ProviderConfig{BaseURL: srv.URL, Model: "test-model", APIKey: "test-key"}
	got, err := p.Chat(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got != "world" {
		t.Fatalf("got %q, want world", got)
	}
}

func TestProviderChatHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := ProviderConfig{BaseURL: srv.URL, Model: "m"}
	if _, err := p.Chat(context.Background(), "hi"); err == nil {
		t.Fatal("expected an error for HTTP 500")
	}
}

func TestProviderChatUnreachable(t *testing.T) {
	p := ProviderConfig{BaseURL: "http://127.0.0.1:1", Model: "m"}
	if _, err := p.Chat(context.Background(), "hi"); err == nil {
		t.Fatal("expected an unreachable error")
	}
}

func TestProviderValidate(t *testing.T) {
	if err := (ProviderConfig{}).Validate(); err == nil {
		t.Fatal("empty provider should not validate")
	}
	if err := (ProviderConfig{BaseURL: "http://x"}).Validate(); err == nil {
		t.Fatal("provider without a model should not validate")
	}
	if err := (ProviderConfig{BaseURL: "http://x", Model: "m"}).Validate(); err != nil {
		t.Fatalf("valid provider rejected: %v", err)
	}
}
