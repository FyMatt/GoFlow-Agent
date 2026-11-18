package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestStreamHeartbeatsDoNotHideMissingModelProgress(t *testing.T) {
	for _, wire := range []string{"responses", ""} {
		t.Run(wire, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for {
					fmt.Fprint(w, ": keep-alive\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-r.Context().Done():
						return
					case <-time.After(20 * time.Millisecond):
					}
				}
			}))
			defer server.Close()
			client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "test-key", WireAPI: wire, Timeout: 100 * time.Millisecond, RetryCount: 1, RetryBackoff: time.Millisecond})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := client.StreamChat(ctx, schema.ChatRequest{Model: "test", Messages: []schema.Message{{Role: "user", Content: "go"}}}, nil)
			if err == nil || ctx.Err() != nil {
				t.Fatalf("heartbeats kept a stalled model alive until caller cancellation: %v", err)
			}
		})
	}
}
