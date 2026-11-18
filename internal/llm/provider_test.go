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
	chatReq    schema.ChatRequest
	streamReq  schema.ChatRequest
}

func (s *stubProviderClient) Chat(_ context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	s.calls = append(s.calls, "chat")
	s.chatReq = req
	if s.chatErr != nil {
		return schema.ChatResponse{}, s.chatErr
	}
	return s.chatResp, nil
}

func (s *stubProviderClient) StreamChat(_ context.Context, req schema.ChatRequest, _ interfaces.StreamHandler) (schema.ChatResponse, error) {
	s.calls = append(s.calls, "stream")
	s.streamReq = req
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

func TestIsLocalBaseURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "localhost", raw: "http://localhost:1234/v1", want: true},
		{name: "ipv4 loopback", raw: "http://127.0.0.1:1234/v1", want: true},
		{name: "ipv6 loopback", raw: "http://[::1]:1234/v1", want: true},
		{name: "unix socket", raw: "unix:///tmp/goflow.sock", want: true},
		{name: "private network is not loopback", raw: "http://192.168.1.10:1234/v1", want: false},
		{name: "public endpoint", raw: "https://api.example.com/v1", want: false},
		{name: "empty", raw: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLocalBaseURL(tt.raw); got != tt.want {
				t.Fatalf("IsLocalBaseURL(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestLocalOnlyRegistryBlocksRemotePrimaryBeforeCall(t *testing.T) {
	primary := &stubProviderClient{chatResp: schema.ChatResponse{Message: schema.Message{Content: "should not run"}}}
	registry := &Registry{
		providers: map[string]interfaces.LLMClient{"primary": primary},
		configs: map[string]config.LLMConfig{
			"primary": {BaseURL: "https://api.example.com/v1"},
		},
		localOnly: true,
	}
	client, err := registry.Client("primary")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	_, err = client.Chat(context.Background(), schema.ChatRequest{Model: "test-model"})
	if err == nil || !strings.Contains(err.Error(), "local-only workspace blocks non-local") {
		t.Fatalf("expected local-only rejection, got %v", err)
	}
	if len(primary.calls) != 0 {
		t.Fatalf("expected remote primary not to be called, got %#v", primary.calls)
	}
}

func TestLocalOnlyRegistryBlocksRemoteFallback(t *testing.T) {
	primary := &stubProviderClient{chatErr: errors.New("primary down")}
	backup := &stubProviderClient{chatResp: schema.ChatResponse{Message: schema.Message{Content: "should not run"}}}
	registry := &Registry{
		providers: map[string]interfaces.LLMClient{"primary": primary, "backup": backup},
		configs: map[string]config.LLMConfig{
			"primary": {BaseURL: "http://localhost:1234/v1", FallbackProvider: "backup"},
			"backup":  {BaseURL: "https://api.example.com/v1"},
		},
		localOnly: true,
	}
	client, err := registry.Client("primary")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	_, err = client.Chat(context.Background(), schema.ChatRequest{Model: "test-model"})
	if err == nil || !strings.Contains(err.Error(), "local-only workspace blocks non-local") {
		t.Fatalf("expected remote fallback rejection, got %v", err)
	}
	if !reflect.DeepEqual(primary.calls, []string{"chat"}) {
		t.Fatalf("expected local primary to be attempted once, got %#v", primary.calls)
	}
	if len(backup.calls) != 0 {
		t.Fatalf("expected remote fallback not to be called, got %#v", backup.calls)
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

func TestRegistryClientSanitizesPromptSecretsBeforePrimaryAndFallback(t *testing.T) {
	fakeAPIKey := "sk-" + "1234567890abcdefghijklmnop"
	primary := &stubProviderClient{chatErr: errors.New("primary down")}
	backup := &stubProviderClient{chatResp: schema.ChatResponse{Message: schema.Message{Content: "backup ok"}}}
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
	_, err = client.Chat(context.Background(), schema.ChatRequest{
		System:   "password=system-secret-password",
		Messages: []schema.Message{{Role: "user", Content: "api_key: " + fakeAPIKey}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for name, request := range map[string]schema.ChatRequest{"primary": primary.chatReq, "backup": backup.chatReq} {
		wireText := request.System + "\n" + request.Messages[0].Content
		if strings.Contains(wireText, "system-secret-password") || strings.Contains(wireText, fakeAPIKey) || !strings.Contains(wireText, "[REDACTED]") {
			t.Fatalf("expected %s request to be sanitized, got %q", name, wireText)
		}
	}
}

type fallbackAwareProviderClient struct {
	stubProviderClient
	fallbackUsed bool
}

type concurrentFallbackPrimary struct {
	fallbackReached chan struct{}
	releasePrimary  chan struct{}
}

func (c *concurrentFallbackPrimary) Chat(_ context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	if req.Model == "fallback" {
		close(c.fallbackReached)
		return schema.ChatResponse{}, errors.New("primary down")
	}
	<-c.releasePrimary
	return schema.ChatResponse{Message: schema.Message{Content: "primary ok"}}, nil
}

func (c *concurrentFallbackPrimary) StreamChat(context.Context, schema.ChatRequest, interfaces.StreamHandler) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, errors.New("not implemented")
}

func (c *concurrentFallbackPrimary) Capabilities() []string { return []string{"chat"} }

type blockingFallbackProvider struct {
	started chan struct{}
	release chan struct{}
}

func (c *blockingFallbackProvider) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	close(c.started)
	<-c.release
	return schema.ChatResponse{Message: schema.Message{Content: "fallback ok"}}, nil
}

func (c *blockingFallbackProvider) StreamChat(context.Context, schema.ChatRequest, interfaces.StreamHandler) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, errors.New("not implemented")
}

func (c *blockingFallbackProvider) Capabilities() []string { return []string{"chat"} }

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

func TestRegistryClientFallbackUsageIsScopedToConcurrentResponse(t *testing.T) {
	primary := &concurrentFallbackPrimary{
		fallbackReached: make(chan struct{}),
		releasePrimary:  make(chan struct{}),
	}
	backup := &blockingFallbackProvider{started: make(chan struct{}), release: make(chan struct{})}
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

	type outcome struct {
		resp schema.ChatResponse
		err  error
	}
	fallbackResult := make(chan outcome, 1)
	go func() {
		resp, err := client.Chat(context.Background(), schema.ChatRequest{Model: "fallback"})
		fallbackResult <- outcome{resp: resp, err: err}
	}()
	<-primary.fallbackReached
	<-backup.started

	primaryResult := make(chan outcome, 1)
	go func() {
		resp, err := client.Chat(context.Background(), schema.ChatRequest{Model: "primary"})
		primaryResult <- outcome{resp: resp, err: err}
	}()
	close(primary.releasePrimary)
	primaryOutcome := <-primaryResult
	close(backup.release)
	fallbackOutcome := <-fallbackResult

	if primaryOutcome.err != nil || primaryOutcome.resp.FallbackUsed {
		t.Fatalf("primary response inherited concurrent fallback state: resp=%#v err=%v", primaryOutcome.resp, primaryOutcome.err)
	}
	if fallbackOutcome.err != nil || !fallbackOutcome.resp.FallbackUsed {
		t.Fatalf("fallback response did not report request-scoped usage: resp=%#v err=%v", fallbackOutcome.resp, fallbackOutcome.err)
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

func TestClientStreamChatKeepsProviderFieldOnlyNativeStream(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("provider-field stream must not fall back to non-stream request")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"private reasoning\"},\"finish_reason\":\"length\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		BaseURL: server.URL, APIKey: "test-key", Timeout: 5 * time.Second,
		ProviderMessageFields: []string{"reasoning_content"},
	})
	resp, err := client.StreamChat(context.Background(), schema.ChatRequest{Model: "test-model"}, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected one native stream request, got %d", requests)
	}
	if got := string(resp.Message.ProviderFields["reasoning_content"]); got != `"private reasoning"` {
		t.Fatalf("unexpected provider field %q", got)
	}
	if resp.StopReason != schema.StopReasonMaxTokens {
		t.Fatalf("unexpected stop reason %q", resp.StopReason)
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

func TestNormalizeResponseAssignsMissingToolCallID(t *testing.T) {
	raw := openAIResponse{
		Choices: []struct {
			FinishReason string        `json:"finish_reason"`
			Message      openAIMessage `json:"message"`
		}{{
			FinishReason: "tool_calls",
			Message: openAIMessage{Role: "assistant", ToolCalls: []openAIToolCall{{
				Type:     "function",
				Function: openAIToolFunction{Name: "read_file", Arguments: `{"path":"a.txt"}`},
			}}},
		}},
	}

	resp, err := normalizeResponse(raw, nil)
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}
	if len(resp.ToolCalls) != 1 || !strings.HasPrefix(resp.ToolCalls[0].ID, "goflow-call-") {
		t.Fatalf("expected a generated tool call ID, got %#v", resp.ToolCalls)
	}
	if resp.Message.ToolCalls[0].ID != resp.ToolCalls[0].ID {
		t.Fatalf("expected response and message tool call IDs to match, got %#v", resp)
	}
}

func TestStreamAssemblerAssignsMissingToolCallID(t *testing.T) {
	assembler := newStreamAssembler(nil)
	assembler.toolCalls = []partialToolCall{{Name: "read_file"}}
	assembler.toolCalls[0].Arguments.WriteString(`{"path":"a.txt"}`)

	resp := assembler.response()
	if len(resp.ToolCalls) != 1 || !strings.HasPrefix(resp.ToolCalls[0].ID, "goflow-call-") {
		t.Fatalf("expected a generated streamed tool call ID, got %#v", resp.ToolCalls)
	}
	if resp.Message.ToolCalls[0].ID != resp.ToolCalls[0].ID {
		t.Fatalf("expected streamed response and message tool call IDs to match, got %#v", resp)
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

func TestNewOpenAIRequestIncludesCapabilityProbeControls(t *testing.T) {
	payload := newOpenAIRequest(schema.ChatRequest{
		ToolChoice:     "required",
		ResponseFormat: &schema.ResponseFormat{Type: "json_object"},
	}, nil)
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"tool_choice":"required"`) || !strings.Contains(got, `"response_format":{"type":"json_object"}`) {
		t.Fatalf("expected tool and structured-output controls, got %s", got)
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

func TestStreamAssemblerEmitsReasoningDeltas(t *testing.T) {
	assembler := newStreamAssembler(nil)
	chunks := []string{
		`{"choices":[{"delta":{"role":"assistant","reasoning_content":"first "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"thought","content":"answer"},"finish_reason":"stop"}]}`,
	}
	var events []schema.StreamEvent
	for _, raw := range chunks {
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			t.Fatalf("decode chunk: %v", err)
		}
		got, _, err := assembler.ingest(chunk)
		if err != nil {
			t.Fatalf("ingest chunk: %v", err)
		}
		events = append(events, got...)
	}
	var reasoning strings.Builder
	var text strings.Builder
	for _, event := range events {
		switch event.Type {
		case schema.StreamEventReasoning:
			reasoning.WriteString(event.Content)
		case schema.StreamEventText:
			text.WriteString(event.Content)
		}
	}
	if reasoning.String() != "first thought" {
		t.Fatalf("expected streamed reasoning, got %q events=%#v", reasoning.String(), events)
	}
	if text.String() != "answer" {
		t.Fatalf("expected streamed text, got %q", text.String())
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
