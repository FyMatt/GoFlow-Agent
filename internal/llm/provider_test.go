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

func TestStreamAssemblerEmitsToolCallAnnouncementOnlyOnce(t *testing.T) {
	assembler := newStreamAssembler()
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

	resp, err := normalizeResponse(raw)
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}
	if resp.Usage.PromptTokens != 123 || resp.Usage.OutputTokens != 45 || resp.Usage.CachedTokens != 20 {
		t.Fatalf("expected usage to be normalized, got %#v", resp.Usage)
	}
}

func TestStreamAssemblerCapturesOpenAIUsageChunk(t *testing.T) {
	assembler := newStreamAssembler()
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
