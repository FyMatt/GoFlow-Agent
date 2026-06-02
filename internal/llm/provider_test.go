package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type stubProviderClient struct {
	chatResp   schema.ChatResponse
	streamResp schema.ChatResponse
	chatErr    error
	streamErr  error
	calls      []string
}

func (s *stubProviderClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	s.calls = append(s.calls, "chat")
	if s.chatErr != nil {
		return schema.ChatResponse{}, s.chatErr
	}
	return s.chatResp, nil
}

func (s *stubProviderClient) StreamChat(context.Context, schema.ChatRequest, interfaces.StreamHandler) (schema.ChatResponse, error) {
	s.calls = append(s.calls, "stream")
	if s.streamErr != nil {
		return schema.ChatResponse{}, s.streamErr
	}
	return s.streamResp, nil
}

func (s *stubProviderClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

func TestNewRegistryBuildsAnthropicProvider(t *testing.T) {
	registry, err := NewRegistry(config.LLMConfig{}, map[string]config.LLMConfig{
		"anthropic": {
			Provider: "anthropic",
			BaseURL:  "https://example.com/v1",
			APIKey:   "test-key",
			Model:    "claude-test",
			Timeout:  5 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	client, err := registry.Client("anthropic")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	wrapper, ok := client.(*fallbackClient)
	if !ok {
		t.Fatalf("expected fallback wrapper, got %T", client)
	}
	if _, ok := wrapper.primary.(*AnthropicClient); !ok {
		t.Fatalf("expected AnthropicClient primary, got %T", wrapper.primary)
	}
}

func TestNewRegistryAllowsIncompleteProviderButBlocksModelCall(t *testing.T) {
	registry, err := NewRegistry(config.LLMConfig{}, map[string]config.LLMConfig{
		"primary": {Provider: "openai-compatible"},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	client, err := registry.Client("primary")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	_, err = client.Chat(context.Background(), schema.ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "model provider setup required") || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("expected setup-required error, got %v", err)
	}
}

func TestRegistryClientUsesFallbackWhenPrimaryStreamFails(t *testing.T) {
	primary := &stubProviderClient{streamErr: errors.New("primary down")}
	backup := &stubProviderClient{streamResp: schema.ChatResponse{Message: schema.Message{Content: "backup ok"}}}
	registry := &Registry{
		providers: map[string]interfaces.LLMClient{"primary": primary, "backup": backup},
		configs: map[string]config.LLMConfig{
			"primary": {FallbackProvider: "backup"},
			"backup":  {},
		},
	}

	client, err := registry.Client("primary")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	resp, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "x"}, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Message.Content != "backup ok" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if !reflect.DeepEqual(primary.calls, []string{"stream"}) {
		t.Fatalf("expected primary stream call, got %#v", primary.calls)
	}
	if !reflect.DeepEqual(backup.calls, []string{"stream"}) {
		t.Fatalf("expected backup stream call, got %#v", backup.calls)
	}
}

type fallbackAwareProviderClient struct {
	stubProviderClient
	fallbackUsed bool
}

func (s *fallbackAwareProviderClient) FallbackUsed() bool {
	return s.fallbackUsed
}

func TestRegistryClientMarksFallbackUsageAfterFallbackStream(t *testing.T) {
	primary := &fallbackAwareProviderClient{stubProviderClient: stubProviderClient{streamErr: errors.New("primary down")}}
	backup := &fallbackAwareProviderClient{stubProviderClient: stubProviderClient{streamResp: schema.ChatResponse{Message: schema.Message{Content: "backup ok"}}}, fallbackUsed: true}
	registry := &Registry{
		providers: map[string]interfaces.LLMClient{"primary": primary, "backup": backup},
		configs: map[string]config.LLMConfig{
			"primary": {FallbackProvider: "backup"},
			"backup":  {},
		},
	}

	client, err := registry.Client("primary")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	_, err = client.StreamChat(context.Background(), schema.ChatRequest{Model: "x"}, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	aware, ok := client.(interface{ FallbackUsed() bool })
	if !ok || !aware.FallbackUsed() {
		t.Fatalf("expected fallback-aware client to report fallback usage")
	}
}

func TestRegistryClientMarksFallbackUsageAfterFallbackChat(t *testing.T) {
	primary := &fallbackAwareProviderClient{stubProviderClient: stubProviderClient{chatErr: errors.New("primary down")}}
	backup := &fallbackAwareProviderClient{stubProviderClient: stubProviderClient{chatResp: schema.ChatResponse{Message: schema.Message{Content: "backup ok"}}}, fallbackUsed: true}
	registry := &Registry{
		providers: map[string]interfaces.LLMClient{"primary": primary, "backup": backup},
		configs: map[string]config.LLMConfig{
			"primary": {FallbackProvider: "backup"},
			"backup":  {},
		},
	}

	client, err := registry.Client("primary")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	resp, err := client.Chat(context.Background(), schema.ChatRequest{Model: "x"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Message.Content != "backup ok" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	aware, ok := client.(interface{ FallbackUsed() bool })
	if !ok || !aware.FallbackUsed() {
		t.Fatalf("expected fallback-aware client to report fallback usage after chat fallback")
	}
}

func TestClientStreamChatFallsBackToNonStreamWhenToolArgumentsAreIncomplete(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Header.Get("Accept"))
		if r.Header.Get("Accept") == "text/event-stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\\\"path\\\":\\\"a.txt\\\",\\\"content\\\":\\\"\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{{
						"id":   "call-1",
						"type": "function",
						"function": map[string]any{
							"name":      "write_file",
							"arguments": `{"path":"a.txt","content":"hello"}`,
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "test-key", Timeout: 5 * time.Second})
	resp, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "test-model"}, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", resp.ToolCalls)
	}
	if string(resp.ToolCalls[0].Arguments) != `{"path":"a.txt","content":"hello"}` {
		t.Fatalf("expected repaired tool arguments from non-stream fallback, got %s", string(resp.ToolCalls[0].Arguments))
	}
	if !reflect.DeepEqual(methods, []string{"text/event-stream", ""}) {
		t.Fatalf("expected stream then non-stream fallback, got %#v", methods)
	}
}

func TestNormalizeOpenAIStopReasonMapsOutputLimit(t *testing.T) {
	tests := map[string]string{
		"stop":          schema.StopReasonStop,
		"tool_calls":    schema.StopReasonToolCalls,
		"function_call": schema.StopReasonToolCalls,
		"length":        schema.StopReasonMaxTokens,
		"max_tokens":    schema.StopReasonMaxTokens,
		"cancelled":     schema.StopReasonCancelled,
	}
	for input, want := range tests {
		if got := normalizeOpenAIStopReason(input); got != want {
			t.Fatalf("normalizeOpenAIStopReason(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeAnthropicStopReasonMapsOutputLimit(t *testing.T) {
	tests := map[string]string{
		"end_turn":      schema.StopReasonStop,
		"stop_sequence": schema.StopReasonStop,
		"tool_use":      schema.StopReasonToolCalls,
		"max_tokens":    schema.StopReasonMaxTokens,
		"cancelled":     schema.StopReasonCancelled,
	}
	for input, want := range tests {
		if got := normalizeAnthropicStopReason(input); got != want {
			t.Fatalf("normalizeAnthropicStopReason(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStreamAssemblerEmitsToolCallAnnouncementOnlyOnce(t *testing.T) {
	assembler := newStreamAssembler(nil)
	chunks := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"write_file","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"a.txt\","}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"content\":\"hello\"}"}}]},"finish_reason":"tool_calls"}]}`,
	}

	var toolCallEvents int
	for _, raw := range chunks {
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			t.Fatalf("decode chunk: %v", err)
		}
		events, _, err := assembler.ingest(chunk)
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		for _, event := range events {
			if event.Type == schema.StreamEventToolCall {
				toolCallEvents++
			}
		}
	}

	if toolCallEvents != 1 {
		t.Fatalf("expected one tool-call announcement while arguments stream in, got %d", toolCallEvents)
	}
	resp := assembler.response()
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Arguments) != `{"path":"a.txt","content":"hello"}` {
		t.Fatalf("expected complete assembled tool call, got %#v", resp.ToolCalls)
	}
}

func TestNormalizeResponseCapturesOpenAIUsage(t *testing.T) {
	raw := openAIResponse{
		Choices: []struct {
			FinishReason string        `json:"finish_reason"`
			Message      openAIMessage `json:"message"`
		}{{
			FinishReason: "stop",
			Message:      openAIMessage{Role: "assistant", Content: "done"},
		}},
		Usage: openAIUsage{PromptTokens: 123, CompletionTokens: 45},
	}
	raw.Usage.PromptTokensDetails.CachedTokens = 20

	resp, err := normalizeResponse(raw, nil)
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}
	if resp.Usage.PromptTokens != 123 || resp.Usage.OutputTokens != 45 || resp.Usage.CachedTokens != 20 {
		t.Fatalf("expected usage to be normalized, got %#v", resp.Usage)
	}
}

func TestNormalizeResponseCapturesProviderMessageFields(t *testing.T) {
	raw := openAIResponse{
		Choices: []struct {
			FinishReason string        `json:"finish_reason"`
			Message      openAIMessage `json:"message"`
		}{{
			FinishReason: "tool_calls",
			Message: openAIMessage{
				Role:    "assistant",
				Content: "need a tool",
				ProviderFields: providerFields{
					"reasoning_content": json.RawMessage(`"private reasoning"`),
				},
				ToolCalls: []openAIToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: openAIToolFunction{
						Name:      "read_file",
						Arguments: `{"path":"a.txt"}`,
					},
				}},
			},
		}},
	}

	resp, err := normalizeResponse(raw, map[string]struct{}{"reasoning_content": {}})
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}
	if got := string(resp.Message.ProviderFields["reasoning_content"]); got != `"private reasoning"` {
		t.Fatalf("expected reasoning_content provider field, got %q", got)
	}
}

func TestNormalizeResponseOmitsProviderMessageFieldsWhenNotAllowed(t *testing.T) {
	raw := openAIResponse{
		Choices: []struct {
			FinishReason string        `json:"finish_reason"`
			Message      openAIMessage `json:"message"`
		}{{
			FinishReason: "stop",
			Message: openAIMessage{
				Role: "assistant",
				ProviderFields: providerFields{
					"reasoning_content": json.RawMessage(`"private reasoning"`),
				},
			},
		}},
	}

	resp, err := normalizeResponse(raw, nil)
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}
	if len(resp.Message.ProviderFields) != 0 {
		t.Fatalf("expected provider fields to be gated by allow-list, got %#v", resp.Message.ProviderFields)
	}
}

func TestNewOpenAIRequestReplaysAllowedProviderMessageFields(t *testing.T) {
	req := schema.ChatRequest{Messages: []schema.Message{{
		Role:      "assistant",
		Content:   "need a tool",
		ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}},
		ProviderFields: map[string]json.RawMessage{
			"reasoning_content": json.RawMessage(`"private reasoning"`),
			"ignored":           json.RawMessage(`"nope"`),
		},
	}}}
	payload := newOpenAIRequest(req, map[string]struct{}{"reasoning_content": {}})
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if !strings.Contains(string(data), `"reasoning_content":"private reasoning"`) {
		t.Fatalf("expected request to include reasoning_content, got %s", string(data))
	}
	if strings.Contains(string(data), "ignored") || strings.Contains(string(data), "provider_fields") {
		t.Fatalf("expected only provider wire fields, got %s", string(data))
	}
}

func TestNewOpenAIRequestReplaysConfiguredProviderMessageFields(t *testing.T) {
	req := schema.ChatRequest{Messages: []schema.Message{{
		Role: "assistant",
		ProviderFields: map[string]json.RawMessage{
			"vendor_trace": json.RawMessage(`"opaque value"`),
		},
	}}}
	payload := newOpenAIRequest(req, providerMessageFieldSet([]string{"vendor_trace"}))
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if !strings.Contains(string(data), `"vendor_trace":"opaque value"`) {
		t.Fatalf("expected configured provider field to be replayed, got %s", string(data))
	}
}

func TestProviderMessageFieldSetRejectsReservedAndUnsafeFields(t *testing.T) {
	fields := providerMessageFieldSet([]string{"reasoning_content", "content", "bad.field", "x-provider"})
	if _, ok := fields["reasoning_content"]; !ok {
		t.Fatalf("expected configured reasoning_content field, got %#v", fields)
	}
	if _, ok := fields["x-provider"]; !ok {
		t.Fatalf("expected configured hyphenated provider field, got %#v", fields)
	}
	if _, ok := fields["content"]; ok {
		t.Fatalf("expected reserved content field to be rejected, got %#v", fields)
	}
	if _, ok := fields["bad.field"]; ok {
		t.Fatalf("expected unsafe field to be rejected, got %#v", fields)
	}
}

func TestNewOpenAIRequestOmitsProviderMessageFieldsWhenNotAllowed(t *testing.T) {
	req := schema.ChatRequest{Messages: []schema.Message{{
		Role: "assistant",
		ProviderFields: map[string]json.RawMessage{
			"reasoning_content": json.RawMessage(`"private reasoning"`),
		},
	}}}
	payload := newOpenAIRequest(req, nil)
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if strings.Contains(string(data), "reasoning_content") {
		t.Fatalf("expected provider field to be gated by allow-list, got %s", string(data))
	}
}

func TestStreamAssemblerCapturesOpenAIUsageChunk(t *testing.T) {
	assembler := newStreamAssembler(nil)
	contentChunk := `{"choices":[{"delta":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
	var first openAIStreamChunk
	if err := json.Unmarshal([]byte(contentChunk), &first); err != nil {
		t.Fatalf("decode first chunk: %v", err)
	}
	if _, _, err := assembler.ingest(first); err != nil {
		t.Fatalf("ingest first chunk: %v", err)
	}
	usageChunk := `{"choices":[],"usage":{"prompt_tokens":1234,"completion_tokens":56,"prompt_tokens_details":{"cached_tokens":100}}}`
	var second openAIStreamChunk
	if err := json.Unmarshal([]byte(usageChunk), &second); err != nil {
		t.Fatalf("decode usage chunk: %v", err)
	}
	if _, _, err := assembler.ingest(second); err != nil {
		t.Fatalf("ingest usage chunk: %v", err)
	}

	resp := assembler.response()
	if resp.Usage.PromptTokens != 1234 || resp.Usage.OutputTokens != 56 || resp.Usage.CachedTokens != 100 {
		t.Fatalf("expected stream usage to be normalized, got %#v", resp.Usage)
	}
}

func TestStreamAssemblerCapturesProviderMessageFields(t *testing.T) {
	assembler := newStreamAssembler(map[string]struct{}{"reasoning_content": {}})
	chunks := []string{
		`{"choices":[{"delta":{"role":"assistant","reasoning_content":"private "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"reasoning","tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.txt\"}"}}]},"finish_reason":"tool_calls"}]}`,
	}
	for _, raw := range chunks {
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			t.Fatalf("decode chunk: %v", err)
		}
		if _, _, err := assembler.ingest(chunk); err != nil {
			t.Fatalf("ingest chunk: %v", err)
		}
	}
	resp := assembler.response()
	if got := string(resp.Message.ProviderFields["reasoning_content"]); got != `"private reasoning"` {
		t.Fatalf("expected assembled reasoning_content, got %q", got)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected tool call to remain assembled, got %#v", resp.ToolCalls)
	}
}

func TestClientStreamChatReadsUsageChunkAfterFinish(t *testing.T) {
	var requestPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1234,\"completion_tokens\":56,\"prompt_tokens_details\":{\"cached_tokens\":100}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "test-key", Timeout: 5 * time.Second})
	resp, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "test-model"}, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Message.Content != "done" {
		t.Fatalf("unexpected content: %#v", resp)
	}
	if resp.Usage.PromptTokens != 1234 || resp.Usage.OutputTokens != 56 || resp.Usage.CachedTokens != 100 {
		t.Fatalf("expected stream usage to be normalized, got %#v", resp.Usage)
	}
	streamOptions, ok := requestPayload["stream_options"].(map[string]any)
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("expected stream_options.include_usage=true, got %#v", requestPayload)
	}
}

func TestClientChatRejectsIncompleteNonStreamToolArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{{
						"id":   "call-1",
						"type": "function",
						"function": map[string]any{
							"name":      "write_file",
							"arguments": `{"path":"a.txt","content":"`,
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "test-key", Timeout: 5 * time.Second})
	_, err := client.Chat(context.Background(), schema.ChatRequest{Model: "test-model"})
	if err == nil {
		t.Fatal("expected invalid non-stream tool arguments error")
	}
	if !strings.Contains(err.Error(), "invalid tool arguments") {
		t.Fatalf("expected invalid tool arguments error, got %v", err)
	}
	if strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("expected EOF to be blocked before MCP decode, got %v", err)
	}
}
