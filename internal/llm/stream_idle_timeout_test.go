package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestActiveStreamsOutliveTimeoutButRespectCallerDeadline(t *testing.T) {
	for _, wire := range []string{"responses", ""} {
		for _, callerDeadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deadline=%t", wire, callerDeadline), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					flusher := w.(http.Flusher)
					for i := 0; i < 9; i++ {
						if i > 0 {
							select {
							case <-r.Context().Done():
								return
							case <-time.After(50 * time.Millisecond):
							}
						}
						if wire == "responses" {
							fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n")
						} else {
							fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n")
						}
						flusher.Flush()
					}
					if wire == "responses" {
						fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
					} else {
						fmt.Fprint(w, "data: [DONE]\n\n")
					}
				}))
				defer server.Close()
				client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "test-key", WireAPI: wire, Timeout: 300 * time.Millisecond})
				ctx := t.Context()
				if callerDeadline {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
					defer cancel()
				}
				response, err := client.StreamChat(ctx, schema.ChatRequest{Model: "test", Messages: []schema.Message{{Role: "user", Content: "continue"}}}, nil)
				if callerDeadline {
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("caller deadline lost: %v", err)
					}
				} else if err != nil || response.Message.Content != strings.Repeat("x", 9) {
					t.Fatalf("active stream was cut short: content=%q err=%v", response.Message.Content, err)
				}
			})
		}
	}
}
