package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestResponsesStreamRejectsEOFBeforeTerminalEvent(t *testing.T) {
	for _, output := range []string{
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"人工\"}\n\n",
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"name\":\"write_file\"}}\n\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item-1\",\"delta\":\"{}\"}\n\n",
	} {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, output)
		}))
		client := NewClient(config.LLMConfig{WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key", RetryCount: 2, RetryBackoff: time.Millisecond})
		response, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "test-model", Messages: []schema.Message{{Role: "user", Content: "Complete the task"}}}, nil)
		server.Close()
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unexpected eof") {
			t.Fatalf("partial stream became a completed response: response=%#v err=%v", response, err)
		}
		if requests.Load() != 1 || response.Message.Content != "" || len(response.ToolCalls) != 0 {
			t.Fatalf("partial output must not be replayed or executed: requests=%d response=%#v", requests.Load(), response)
		}
	}
}
