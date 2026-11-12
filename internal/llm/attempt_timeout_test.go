package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// A provider that stalls before sending response headers must not consume the
// whole timeout on the first attempt: each bounded attempt is retried until the
// total budget is spent.
func TestChatRetriesAttemptThatStallsBeforeHeaders(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		BaseURL: server.URL, APIKey: "test-key",
		Timeout: 600 * time.Millisecond, RetryCount: 2, RetryBackoff: 10 * time.Millisecond,
	})
	if client.attemptTimeout >= 600*time.Millisecond {
		t.Fatalf("per-attempt timeout must stay below the total budget, got %s", client.attemptTimeout)
	}
	resp, err := client.Chat(context.Background(), schema.ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("Chat after one stalled attempt: %v", err)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("unexpected content: %#v", resp)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected one retry after the stalled attempt, got %d requests", got)
	}
}

// When every attempt stalls, the caller still gets a bounded, distinguishable
// failure instead of waiting for the full provider timeout per attempt.
func TestChatResponsesReportsExhaustedRetriesWhenEveryAttemptStalls(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		// Outlives the per-attempt deadline but stays bounded so the test server
		// closes promptly.
		time.Sleep(400 * time.Millisecond)
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		BaseURL: server.URL, APIKey: "test-key", WireAPI: "responses",
		Timeout: 300 * time.Millisecond, RetryCount: 2, RetryBackoff: 10 * time.Millisecond,
	})
	started := time.Now()
	_, err := client.Chat(context.Background(), schema.ChatRequest{Model: "test-model"})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("expected a provider failure when every attempt stalls")
	}
	var exhausted *ProviderRetryExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected ProviderRetryExhaustedError, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("expected more than one bounded attempt, got %d", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("stalled attempts must stay bounded, took %s", elapsed)
	}
}
