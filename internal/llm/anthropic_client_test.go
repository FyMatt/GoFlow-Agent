package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type anthropicRequestPayload struct {
	Model     string `json:"model"`
	System    string `json:"system,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func TestAnthropicClientChatUsesAnthropicMessagesAPI(t *testing.T) {
	var capturedPath string
	var capturedAPIKey string
	var capturedVersion string
	var capturedContentType string
	var payload anthropicRequestPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAPIKey = r.Header.Get("x-api-key")
		capturedVersion = r.Header.Get("anthropic-version")
		capturedContentType = r.Header.Get("Content-Type")
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [{"type": "text", "text": "hello back"}],
			"stop_reason": "end_turn",
			"usage": {
				"input_tokens": 100,
				"output_tokens": 20,
				"cache_creation_input_tokens": 5,
				"cache_read_input_tokens": 15
			}
		}`))
	}))
	defer server.Close()

	client := NewAnthropicClient(config.LLMConfig{
		Provider: "anthropic",
		BaseURL:  server.URL,
		APIKey:   "test-key",
		Timeout:  5 * time.Second,
	})

	resp, err := client.Chat(context.Background(), schema.ChatRequest{
		Model:     "claude-opus-4-6",
		System:    "You are helpful.",
		MaxTokens: 128,
		Messages: []schema.Message{{
			Role:    "user",
			Content: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if capturedPath != "/messages" {
		t.Fatalf("expected /messages path, got %q", capturedPath)
	}
	if capturedAPIKey != "test-key" {
		t.Fatalf("expected x-api-key header, got %q", capturedAPIKey)
	}
	if capturedVersion == "" {
		t.Fatal("expected anthropic-version header")
	}
	if capturedContentType != "application/json" {
		t.Fatalf("expected json content type, got %q", capturedContentType)
	}
	if payload.Model != "claude-opus-4-6" {
		t.Fatalf("unexpected model: %#v", payload)
	}
	if payload.System != "You are helpful." {
		t.Fatalf("unexpected system prompt: %#v", payload)
	}
	if payload.MaxTokens != 128 {
		t.Fatalf("unexpected max tokens: %#v", payload)
	}
	if len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || payload.Messages[0].Content != "hello" {
		t.Fatalf("unexpected messages: %#v", payload.Messages)
	}
	if resp.Message.Content != "hello back" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("unexpected stop reason: %#v", resp)
	}
	if resp.Usage.PromptTokens != 120 || resp.Usage.OutputTokens != 20 || resp.Usage.CachedTokens != 15 {
		t.Fatalf("unexpected token usage: %#v", resp.Usage)
	}
}
