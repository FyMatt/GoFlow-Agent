package llm

import (
	"context"
	"encoding/json"
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

func TestResponsesClientSendsMediumReasoningEffort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		reasoning, _ := request["reasoning"].(map[string]any)
		if reasoning["effort"] != "medium" {
			t.Fatalf("expected reasoning.effort=medium, got %#v", request["reasoning"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{Provider: "openai-compatible", WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key", ReasoningEffort: "mid"})
	if _, err := client.Chat(context.Background(), schema.ChatRequest{Model: "gpt-5.4-mini", Messages: []schema.Message{{Role: "user", Content: "hello"}}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesClientRequestReasoningOverridesProviderDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		reasoning, _ := request["reasoning"].(map[string]any)
		if reasoning["effort"] != "high" {
			t.Fatalf("expected request override reasoning.effort=high, got %#v", request["reasoning"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{Provider: "openai-compatible", WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key", ReasoningEffort: "low"})
	if _, err := client.Chat(context.Background(), schema.ChatRequest{Model: "gpt-5.5", ReasoningEffort: "high", Messages: []schema.Message{{Role: "user", Content: "hello"}}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesClientUsesResponsesWireFormatAndNormalizesTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %s, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request["model"] != "gpt-5.5" || request["instructions"] != "be precise" {
			t.Fatalf("unexpected request metadata: %#v", request)
		}
		input, ok := request["input"].([]any)
		if !ok || len(input) != 1 {
			t.Fatalf("unexpected input: %#v", request["input"])
		}
		tools, ok := request["tools"].([]any)
		if !ok || len(tools) != 1 || tools[0].(map[string]any)["type"] != "function" {
			t.Fatalf("unexpected tools: %#v", request["tools"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}],"usage":{"input_tokens":12,"output_tokens":4,"input_tokens_details":{"cached_tokens":2}}}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{Provider: "openai-compatible", WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key"})
	response, err := client.Chat(context.Background(), schema.ChatRequest{
		Model:    "gpt-5.5",
		System:   "be precise",
		Messages: []schema.Message{{Role: "user", Content: "inspect the repository"}},
		Tools:    []schema.Tool{{Name: "read_file", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.StopReason != schema.StopReasonToolCalls || len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "read_file" || string(response.ToolCalls[0].Arguments) != `{"path":"README.md"}` {
		t.Fatalf("unexpected normalized response: %#v", response)
	}
	if response.Usage.PromptTokens != 12 || response.Usage.OutputTokens != 4 || response.Usage.CachedTokens != 2 {
		t.Fatalf("unexpected usage: %#v", response.Usage)
	}
}

func TestResponsesClientPromotesToCodeTextIntoToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"to=file_tools.apply_patch code\n{\"path\":\"internal/filesafe/path.go\",\"edits\":[]}"}]}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key"})
	response, err := client.Chat(context.Background(), schema.ChatRequest{Model: "test-model", Messages: []schema.Message{{Role: "user", Content: "fix"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "file_tools/apply_patch" || response.Message.Content != "" {
		t.Fatalf("unexpected promoted response: %#v", response)
	}
}

func TestResponsesClientStreamsTextAndToolEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %s, want /responses", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		for _, event := range []string{
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n",
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"item_1\",\"call_id\":\"call_1\",\"name\":\"read_file\",\"arguments\":\"\"}}\n\n",
			"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"delta\":\"{\\\"path\\\":\\\"a.txt\\\"}\"}\n\n",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n",
		} {
			_, _ = w.Write([]byte(event))
			flusher.Flush()
		}
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{Provider: "openai-compatible", WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key"})
	var events []schema.StreamEvent
	response, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "gpt-5.5", Messages: []schema.Message{{Role: "user", Content: "go"}}}, func(event schema.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if response.Message.Content != "hello" || len(response.ToolCalls) != 1 || string(response.ToolCalls[0].Arguments) != `{"path":"a.txt"}` {
		t.Fatalf("unexpected stream response: %#v", response)
	}
	if len(events) != 4 || events[0].Reason != "provider_request_started" || events[1].Reason != "provider_response_connected" || events[2].Type != schema.StreamEventText || events[3].Type != schema.StreamEventToolCall {
		t.Fatalf("unexpected stream events: %#v", events)
	}
}

func TestResponsesClientPromotesStreamedToCodeTextIntoToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"to=file_tools.apply_patch code\\n{\\\"path\\\":\\\"internal/filesafe/path.go\\\",\\\"edits\\\":[]}\"}\n\n")
		_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key"})
	response, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "test-model", Messages: []schema.Message{{Role: "user", Content: "fix"}}}, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "file_tools/apply_patch" || response.Message.Content != "" {
		t.Fatalf("unexpected promoted stream response: %#v", response)
	}
}

func TestResponsesClientFallsBackWhenUpstreamRequiresStreaming(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request["stream"] != true {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"This account only accepts streaming conversation requests; set \\"stream\\": true."}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"fallback\"}\n\n"))
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key"})
	response, err := client.Chat(context.Background(), schema.ChatRequest{Model: "test-model", Messages: []schema.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.Message.Content != "fallback" || requests.Load() != 2 {
		t.Fatalf("unexpected fallback response=%#v requests=%d", response, requests.Load())
	}
}

func TestResponsesClientDoesNotStreamFallbackForOtherBadRequests(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid request"}}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key"})
	_, err := client.Chat(context.Background(), schema.ChatRequest{Model: "test-model", Messages: []schema.Message{{Role: "user", Content: "hello"}}})
	if err == nil || requests.Load() != 1 {
		t.Fatalf("expected one failed non-stream request, err=%v requests=%d", err, requests.Load())
	}
}

func TestResponsesClientTreatsFailedEmptyStatusAsRetryableError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"status":"failed","error":{"message":"upstream overloaded","code":"server_error"},"output":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		WireAPI:      "responses",
		BaseURL:      server.URL,
		APIKey:       "test-key",
		RetryCount:   2,
		RetryBackoff: time.Millisecond,
	})
	response, err := client.Chat(context.Background(), schema.ChatRequest{Model: "gpt-5.4-mini", Messages: []schema.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.Message.Content != "recovered" || requests.Load() != 2 {
		t.Fatalf("unexpected retry result: response=%#v requests=%d", response, requests.Load())
	}
}

func TestResponsesClientRetriesCompletedEmptyResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"status":"completed","output":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key", RetryCount: 2, RetryBackoff: time.Millisecond})
	response, err := client.Chat(context.Background(), schema.ChatRequest{Model: "test-model", Messages: []schema.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.Message.Content != "recovered" || requests.Load() != 2 {
		t.Fatalf("unexpected response=%#v requests=%d", response, requests.Load())
	}
}

func TestResponsesClientTreatsFailedEmptyStreamAsRetryableError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		if requests.Add(1) == 1 {
			_, _ = fmt.Fprint(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"empty generation\"}}}\n\n")
			flusher.Flush()
			return
		}
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\"}\n\n")
		_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		WireAPI:      "responses",
		BaseURL:      server.URL,
		APIKey:       "test-key",
		RetryCount:   2,
		RetryBackoff: time.Millisecond,
	})
	response, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "gpt-5.4-mini", Messages: []schema.Message{{Role: "user", Content: "hello"}}}, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if response.Message.Content != "recovered" || requests.Load() != 2 {
		t.Fatalf("unexpected stream retry result: response=%#v requests=%d", response, requests.Load())
	}
}

func TestResponsesClientRetriesCompletedEmptyStream(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\"}\n\n")
		_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{WireAPI: "responses", BaseURL: server.URL, APIKey: "test-key", RetryCount: 2, RetryBackoff: time.Millisecond})
	response, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "test-model", Messages: []schema.Message{{Role: "user", Content: "hello"}}}, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if response.Message.Content != "recovered" || requests.Load() != 2 {
		t.Fatalf("unexpected response=%#v requests=%d", response, requests.Load())
	}
}

func TestResponsesClientRetriesTransientFailureBeforeFirstStreamEvent(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "temporary gateway failure", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\"}\n\n")
		_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		WireAPI:      "responses",
		BaseURL:      server.URL,
		APIKey:       "test-key",
		Timeout:      time.Second,
		RetryCount:   2,
		RetryBackoff: time.Millisecond,
	})
	var events []schema.StreamEvent
	response, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "gpt-5.5", Messages: []schema.Message{{Role: "user", Content: "go"}}}, func(event schema.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if response.Message.Content != "recovered" || requests.Load() != 2 {
		t.Fatalf("unexpected retry result: response=%#v requests=%d", response, requests.Load())
	}
	reasons := make([]string, 0, len(events))
	for _, event := range events {
		if event.Reason != "" {
			reasons = append(reasons, event.Reason)
		}
	}
	want := []string{"provider_request_started", "provider_request_retry", "provider_request_started", "provider_response_connected"}
	if strings.Join(reasons, ",") != strings.Join(want, ",") {
		t.Fatalf("provider retry status events mismatch: got=%v want=%v events=%#v", reasons, want, events)
	}
}

func TestResponsesClientRetriesReadTimeoutAfterLifecycleOnlyEvents(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		if requests.Add(1) == 1 {
			_, _ = fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n")
			flusher.Flush()
			<-r.Context().Done()
			return
		}
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\"}\n\n")
		_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		WireAPI:      "responses",
		BaseURL:      server.URL,
		APIKey:       "test-key",
		Timeout:      50 * time.Millisecond,
		RetryCount:   1,
		RetryBackoff: time.Millisecond,
	})
	response, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "gpt-5.6-sol", Messages: []schema.Message{{Role: "user", Content: "go"}}}, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if response.Message.Content != "recovered" || requests.Load() != 2 {
		t.Fatalf("lifecycle-only timeout was not retried safely: response=%#v requests=%d", response, requests.Load())
	}
}

func TestResponsesClientDoesNotRetryReadTimeoutAfterTextOutput(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		WireAPI:      "responses",
		BaseURL:      server.URL,
		APIKey:       "test-key",
		Timeout:      50 * time.Millisecond,
		RetryCount:   1,
		RetryBackoff: time.Millisecond,
	})
	_, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "gpt-5.6-sol", Messages: []schema.Message{{Role: "user", Content: "go"}}}, nil)
	if err == nil || requests.Load() != 1 {
		t.Fatalf("partial stream timeout must not be replayed: err=%v requests=%d", err, requests.Load())
	}
}

func TestResponsesClientDoesNotReplayPartiallyConsumedStream(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {broken-json}\n\n")
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		WireAPI:      "responses",
		BaseURL:      server.URL,
		APIKey:       "test-key",
		Timeout:      time.Second,
		RetryCount:   2,
		RetryBackoff: time.Millisecond,
	})
	_, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "gpt-5.5", Messages: []schema.Message{{Role: "user", Content: "go"}}}, nil)
	if err == nil || requests.Load() != 1 {
		t.Fatalf("partial stream must not be replayed: err=%v requests=%d", err, requests.Load())
	}
}

func TestResponsesRequestConvertsImageAndToolResult(t *testing.T) {
	payload, err := newResponsesRequest(schema.ChatRequest{
		Model: "gpt-5.5",
		Messages: []schema.Message{
			{Role: "user", Content: "look", ContentParts: []schema.MessageContentPart{{Type: "image", DataURL: "data:image/png;base64,abc"}}},
			{Role: "assistant", ToolCalls: []schema.ToolCall{{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a"}`)}}},
			{Role: "tool", ToolCallID: "call_1", Content: "contents"},
		},
	})
	if err != nil {
		t.Fatalf("newResponsesRequest: %v", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	text := string(data)
	for _, expected := range []string{"input_image", "data:image/png;base64,abc", "function_call", "function_call_output", "call_1"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("payload missing %q: %s", expected, text)
		}
	}
}

func TestResponsesStreamAssemblerEmitsReasoningDeltas(t *testing.T) {
	assembler := newResponsesStreamAssembler()
	var events []schema.StreamEvent
	handler := func(event schema.StreamEvent) error {
		events = append(events, event)
		return nil
	}
	if err := assembler.ingest("response.reasoning_summary_text.delta", []byte(`{"type":"response.reasoning_summary_text.delta","delta":"think "}`), handler); err != nil {
		t.Fatalf("ingest reasoning: %v", err)
	}
	if err := assembler.ingest("response.output_text.delta", []byte(`{"type":"response.output_text.delta","delta":"answer"}`), handler); err != nil {
		t.Fatalf("ingest text: %v", err)
	}
	if len(events) != 2 || events[0].Type != schema.StreamEventReasoning || events[0].Content != "think " || events[1].Type != schema.StreamEventText {
		t.Fatalf("unexpected streamed events: %#v", events)
	}
}

func TestResponsesProviderFailureDoesNotRetryPermanentAccessErrors(t *testing.T) {
	err := responsesProviderFailure("failed", "Upstream access forbidden, please contact administrator", "")
	if err == nil {
		t.Fatal("expected provider failure")
	}
	if isRetryableError(err) {
		t.Fatalf("permanent access failure must not be retried: %v", err)
	}
	if !strings.Contains(err.Error(), "provider request failed") {
		t.Fatalf("expected permanent provider error label, got %v", err)
	}
}
