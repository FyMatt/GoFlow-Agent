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

func TestResponsesProtocolFailureUsesNextRetryForHTTP1(t *testing.T) {
	testResponsesHTTP1Fallback(t, false)
}

func TestResponsesStalledHTTP2UsesNextRetryForHTTP1(t *testing.T) {
	testResponsesHTTP1Fallback(t, true)
}

func testResponsesHTTP1Fallback(t *testing.T, stall bool) {
	t.Helper()
	var h2, h1 atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 {
			h2.Add(1)
			if stall {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, ": keep-alive\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			panic(http.ErrAbortHandler)
		}
		h1.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"恢复\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "test-key", WireAPI: "responses", Timeout: time.Second, RetryCount: 1, RetryBackoff: time.Millisecond})
	if stall {
		client.httpClient.Timeout = 100 * time.Millisecond
	}
	client.httpClient.Transport = server.Client().Transport
	response, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "test", Messages: []schema.Message{{Role: "user", Content: "go"}}}, nil)
	if err != nil || response.Message.Content != "恢复" || h2.Load() != 1 || h1.Load() != 1 {
		t.Fatalf("protocol fallback must use one existing retry, response=%#v err=%v h2=%d h1=%d", response, err, h2.Load(), h1.Load())
	}
}
