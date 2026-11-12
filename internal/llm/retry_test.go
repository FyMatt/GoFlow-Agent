package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestOpenAICompatibleClientRetriesTransientGatewayFailuresByDefault(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "temporary gateway failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recovered"}}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		BaseURL:      server.URL,
		APIKey:       "test-key",
		Timeout:      time.Second,
		RetryBackoff: time.Millisecond,
	})
	response, err := client.Chat(context.Background(), schema.ChatRequest{Model: "test-model", Messages: []schema.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.Message.Content != "recovered" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("expected one retry, got %d requests", got)
	}
}

func TestIsRetryableErrorIncludesDNSLookupFailures(t *testing.T) {
	err := fmt.Errorf(`send Responses stream request: Post "https://lucen.plus/responses": dial tcp: lookup lucen.plus: no such host`)
	if !isRetryableError(err) {
		t.Fatal("DNS lookup failures must be retried")
	}
}

func TestIsRetryableErrorIncludesWindowsReceiveTimeouts(t *testing.T) {
	err := fmt.Errorf("read Responses stream: read tcp: wsarecv: A connection attempt failed because the connected party did not properly respond")
	if !isRetryableError(err) {
		t.Fatal("Windows receive timeouts must be retried")
	}
}

func TestIsRetryableErrorIncludesHTTP2ProtocolFailures(t *testing.T) {
	err := fmt.Errorf(`send Responses stream request: Post "https://tokenmarket.cheap/v1/responses": stream error: stream ID 7; PROTOCOL_ERROR; received from peer`)
	if !isRetryableError(err) {
		t.Fatal("HTTP/2 protocol stream failures must be retried before assistant output")
	}
}

func TestIsRetryableErrorRejectsModelNotFoundInsideGateway503(t *testing.T) {
	err := fmt.Errorf(`provider returned 503 Service Unavailable: {"error":{"code":"model_not_found","message":"No available channel for model gpt-5.5"}}`)
	if isRetryableError(err) {
		t.Fatal("model_not_found must remain permanent even when the gateway wraps it in HTTP 503")
	}
}
