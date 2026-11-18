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

func TestResponsesCompletedEventDoesNotWaitForConnectionEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"完成\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "test-key", WireAPI: "responses", Timeout: time.Second})
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	response, err := client.StreamChat(ctx, schema.ChatRequest{Model: "test", Messages: []schema.Message{{Role: "user", Content: "go"}}}, nil)
	if err != nil || response.Message.Content != "完成" || response.Usage.PromptTokens != 12 {
		t.Fatalf("terminal event must settle output and usage without waiting for EOF: response=%#v err=%v", response, err)
	}
}

func TestResponsesRetriesEOFBeforeAssistantOutput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
			return
		}
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"恢复\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "test-key", WireAPI: "responses", Timeout: time.Second, RetryCount: 1, RetryBackoff: time.Millisecond})
	result, err := client.StreamChat(t.Context(), schema.ChatRequest{Model: "test", Messages: []schema.Message{{Role: "user", Content: "go"}}}, nil)
	if err != nil || result.Message.Content != "恢复" || calls.Load() != 2 {
		t.Fatalf("safe pre-output EOF retry failed: response=%#v err=%v calls=%d", result, err, calls.Load())
	}
}
