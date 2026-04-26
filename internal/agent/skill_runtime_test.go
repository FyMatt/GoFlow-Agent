package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type stubSkillManager struct {
	skill *schema.Skill
}

func (s stubSkillManager) List() []schema.Skill {
	if s.skill == nil {
		return nil
	}
	return []schema.Skill{*s.skill}
}

func (s stubSkillManager) Match(string) (*schema.Skill, bool) {
	if s.skill == nil {
		return nil, false
	}
	return s.skill, true
}

func (s stubSkillManager) MatchWithDiagnostics(string) (*schema.Skill, schema.SkillMatchDiagnostic, bool) {
	if s.skill == nil {
		return nil, schema.SkillMatchDiagnostic{}, false
	}
	return s.skill, schema.SkillMatchDiagnostic{SkillName: s.skill.Name, Score: 1, Reason: "test stub"}, true
}

type stubLLMClient struct {
	responses []schema.ChatResponse
	calls     int
}

type scriptedLLMCall struct {
	response schema.ChatResponse
	err      error
	events   []schema.StreamEvent
}

type scriptedLLMClient struct {
	calls    []scriptedLLMCall
	requests []schema.ChatRequest
}

func (s *scriptedLLMClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, fmt.Errorf("unused")
}

func (s *scriptedLLMClient) StreamChat(_ context.Context, req schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	s.requests = append(s.requests, req)
	if len(s.calls) == 0 {
		return schema.ChatResponse{Message: schema.Message{Content: "done"}}, nil
	}
	call := s.calls[0]
	s.calls = s.calls[1:]
	if handler != nil {
		for _, event := range call.events {
			if err := handler(event); err != nil {
				return schema.ChatResponse{}, err
			}
		}
	}
	if call.err != nil {
		return schema.ChatResponse{}, call.err
	}
	if handler != nil && call.response.Message.Content != "" {
		if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: call.response.Message.Content}); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	return call.response, nil
}

func (s *scriptedLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

func (s *stubLLMClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, fmt.Errorf("unused")
}

func (s *stubLLMClient) StreamChat(_ context.Context, _ schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	if s.calls >= len(s.responses) {
		return schema.ChatResponse{Message: schema.Message{Content: "done"}}, nil
	}
	resp := s.responses[s.calls]
	s.calls++
	if handler != nil && resp.Message.Content != "" {
		if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: resp.Message.Content}); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	return resp, nil
}

func (s *stubLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

type delayedUsageLLMClient struct {
	response schema.ChatResponse
	delay    time.Duration
	calls    int
}

func (s *delayedUsageLLMClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, fmt.Errorf("unused")
}

func (s *delayedUsageLLMClient) StreamChat(ctx context.Context, _ schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	s.calls++
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return schema.ChatResponse{}, ctx.Err()
		case <-time.After(s.delay):
		}
	}
	if handler != nil && s.response.Message.Content != "" {
		if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: s.response.Message.Content}); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	return s.response, nil
}

func (s *delayedUsageLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

type fallbackAwareLLMClient struct {
	delayedUsageLLMClient
	fallbackUsed bool
}

func (s *fallbackAwareLLMClient) FallbackUsed() bool {
	return s.fallbackUsed
}

type verifierPassTestLLMClient struct {
	streamResponse schema.ChatResponse
	chatResponse   schema.ChatResponse
	chatRequests   []schema.ChatRequest
}

func (s *verifierPassTestLLMClient) Chat(_ context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	s.chatRequests = append(s.chatRequests, req)
	return s.chatResponse, nil
}

func (s *verifierPassTestLLMClient) StreamChat(_ context.Context, _ schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	if handler != nil && s.streamResponse.Message.Content != "" {
		if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: s.streamResponse.Message.Content}); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	return s.streamResponse, nil
}

func (s *verifierPassTestLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

func TestRunStreamDetectsFallbackUsageFromLLMClient(t *testing.T) {
	llm := &fallbackAwareLLMClient{
		delayedUsageLLMClient: delayedUsageLLMClient{response: schema.ChatResponse{Message: schema.Message{Content: "fallback answer from wrapped client"}}},
		fallbackUsed:          true,
	}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Provider:         "primary",
		Mode:             "audit",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(&stubRuntimeMCP{})}
	audit := runtime.NewAuditLogger(true, false)

	result, err := runner.RunStream(context.Background(), "inspect this", stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), audit, nil, nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if !result.AuditTrail[len(result.AuditTrail)-1].FallbackUsed {
		t.Fatalf("expected automatic fallback flag in audit trail, got %#v", result.AuditTrail)
	}
	if !hasFallbackStructuredSection(result.Structured) {
		t.Fatalf("expected fallback structured section, got %#v", result.Structured)
	}
}

func TestRunStreamPersistsSkillMatchDiagnostic(t *testing.T) {
	skill := &schema.Skill{Name: "code-audit", Mode: "audit"}
	llm := &stubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "done"}}}}
	mcp := &stubRuntimeMCP{}
	state := session.New(4)
	runner := &AgentRunner{id: "auditor", profile: config.AgentProfile{
		Name:             "Auditor",
		Mode:             "audit",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	if _, err := runner.RunStream(context.Background(), "audit this", stubSkillManager{skill: skill}, mcp, state, runtime.NewAuditLogger(false, false), nil, nil); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	snapshot := state.Snapshot()
	if snapshot.LastSkill != "code-audit" || snapshot.LastSkillMatch == nil {
		t.Fatalf("expected last skill match in session snapshot, got %#v", snapshot)
	}
	if snapshot.LastSkillMatch.Score != 1 || snapshot.LastSkillMatch.Reason != "test stub" {
		t.Fatalf("unexpected match diagnostic: %#v", snapshot.LastSkillMatch)
	}
}

type stubRuntimeMCP struct {
	tools  []schema.Tool
	calls  int
	result schema.ToolResult
	err    error
}

func (s *stubRuntimeMCP) ListTools(context.Context) ([]schema.Tool, error) {
	return s.tools, nil
}

func (s *stubRuntimeMCP) RefreshTools(context.Context) ([]schema.Tool, error) {
	return s.tools, nil
}

func (s *stubRuntimeMCP) CallTool(context.Context, string, []byte) (schema.ToolResult, error) {
	s.calls++
	if s.err != nil {
		return schema.ToolResult{}, s.err
	}
	if s.result.Content != "" || s.result.ToolName != "" || s.result.IsError || s.result.Denied || s.result.Suspended {
		return s.result, nil
	}
	return schema.ToolResult{Content: "ok"}, nil
}

func (s *stubRuntimeMCP) HealthStatus(context.Context) map[string]string {
	return map[string]string{"stub": "ready"}
}

func (s *stubRuntimeMCP) ToolNames() []string {
	return nil
}

func TestSkillAllowedToolKindsRestrictExecution(t *testing.T) {
	skill := &schema.Skill{Name: "safe-plan", AllowedToolKinds: []string{"read"}}
	llm := &stubLLMClient{responses: []schema.ChatResponse{{
		ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: []byte(`{"path":"a.txt","content":"x"}`)}},
	}}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite},
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "plan this", stubSkillManager{skill: skill}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, nil)
	if err != nil {
		t.Fatalf("expected skill-level restriction as tool result, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected blocked tool call, got %d MCP calls", mcp.calls)
	}
	if len(result.ToolResults) != 1 || !result.ToolResults[0].Denied || !result.ToolResults[0].IsError {
		t.Fatalf("expected denied tool result, got %#v", result.ToolResults)
	}
	if !strings.Contains(result.ToolResults[0].Content, "tool kind write is not allowed; allowed kinds: read") {
		t.Fatalf("unexpected denied tool result: %#v", result.ToolResults[0])
	}
}

func TestRunStreamRecoversFromToolArgumentValidationError(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "path argument was missing"}}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "read_file",
		Kind:        "read",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "read a file", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, nil)
	if err != nil {
		t.Fatalf("expected argument validation error to recover as observation, got %v", err)
	}
	if result.Output != "path argument was missing" {
		t.Fatalf("expected final response after validation observation, got %#v", result)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected invalid tool call not to reach MCP, got %d calls", mcp.calls)
	}
	if len(result.ToolResults) != 1 || !result.ToolResults[0].IsError || result.ToolResults[0].Denied {
		t.Fatalf("expected non-denied validation error result, got %#v", result.ToolResults)
	}
	if !strings.Contains(result.ToolResults[0].Content, "missing required field") {
		t.Fatalf("expected missing required field detail, got %#v", result.ToolResults[0])
	}
	if len(llm.requests) < 2 {
		t.Fatalf("expected recovery request after validation result, got %d", len(llm.requests))
	}
	lastMessage := llm.requests[1].Messages[len(llm.requests[1].Messages)-1]
	if lastMessage.Role != "tool" || !strings.Contains(lastMessage.Content, "missing required field") {
		t.Fatalf("expected validation error observation in second request, got %#v", lastMessage)
	}
}

func TestRunStreamRecoversFromUnknownToolCall(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "missing_tool", Arguments: []byte(`{}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "tool was unavailable"}}},
	}}
	mcp := &stubRuntimeMCP{}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "use missing tool", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, nil)
	if err != nil {
		t.Fatalf("expected unknown tool call to recover as observation, got %v", err)
	}
	if result.Output != "tool was unavailable" {
		t.Fatalf("expected final response after unknown-tool observation, got %#v", result)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected unknown tool not to reach MCP, got %d calls", mcp.calls)
	}
	if len(result.ToolResults) != 1 || !result.ToolResults[0].IsError || result.ToolResults[0].Denied {
		t.Fatalf("expected non-denied unknown-tool error result, got %#v", result.ToolResults)
	}
	if !strings.Contains(result.ToolResults[0].Content, "tool not found in catalog") {
		t.Fatalf("expected tool not found detail, got %#v", result.ToolResults[0])
	}
}

func TestRunStreamRecoversFromMCPCallError(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "reported read failure"}}},
	}}
	mcp := &stubRuntimeMCP{
		tools: []schema.Tool{{
			Name:        "read_file",
			Kind:        "read",
			InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		}},
		err: fmt.Errorf("read failed"),
	}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "read a.txt", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, nil)
	if err != nil {
		t.Fatalf("expected MCP call error to recover as observation, got %v", err)
	}
	if result.Output != "reported read failure" {
		t.Fatalf("expected final response after MCP error observation, got %#v", result)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected one MCP call attempt, got %d", mcp.calls)
	}
	if len(result.ToolResults) != 1 || !result.ToolResults[0].IsError || result.ToolResults[0].Denied {
		t.Fatalf("expected non-denied MCP error result, got %#v", result.ToolResults)
	}
	if !strings.Contains(result.ToolResults[0].Content, "read failed") {
		t.Fatalf("expected MCP error detail, got %#v", result.ToolResults[0])
	}
}

func TestSkillMaxIterationsOverridesAgentProfile(t *testing.T) {
	skill := &schema.Skill{Name: "one-shot", MaxIterations: 1}
	llm := &stubLLMClient{responses: []schema.ChatResponse{{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}}}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "read_file",
		Kind:        "read",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    3,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "plan this", stubSkillManager{skill: skill}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, nil)
	if err != nil {
		t.Fatalf("expected skill max_iterations to allow one tool iteration plus final response, got %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("expected final response after single allowed tool iteration, got %#v", result)
	}
	if llm.calls != 1 {
		t.Fatalf("expected skill max_iterations to allow one tool iteration before final response fallback, got %d recorded tool-turn calls", llm.calls)
	}
}

func TestRunStreamEmitsVisibleModelWaitStatus(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{Message: schema.Message{Content: "done"}}},
	}}
	mcp := &stubRuntimeMCP{}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    1,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	var statuses []schema.StreamEvent
	result, err := runner.RunStream(context.Background(), "hello", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, func(event schema.StreamEvent) error {
		if event.Type == schema.StreamEventStatus {
			statuses = append(statuses, event)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("expected final output, got %#v", result)
	}
	foundWaitStatus := false
	for _, status := range statuses {
		if status.Content == "waiting for model response..." && status.NeedsAction {
			foundWaitStatus = true
			break
		}
	}
	if !foundWaitStatus {
		t.Fatalf("expected visible model wait status, got %#v", statuses)
	}
}

func TestRunStreamRecoversFromInvalidToolArguments(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{err: fmt.Errorf("llm chat: invalid tool arguments: tool call has invalid arguments: unexpected end of JSON input")},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "recovered after retry"}}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "read_file",
		Kind:        "read",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    3,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	var statuses []string
	result, err := runner.RunStream(context.Background(), "read a.txt", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, func(event schema.StreamEvent) error {
		if event.Type == schema.StreamEventStatus {
			statuses = append(statuses, event.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected recovery from invalid tool arguments, got %v", err)
	}
	if result.Output != "recovered after retry" {
		t.Fatalf("expected recovered final output, got %#v", result)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected recovered tool execution, got %d calls", mcp.calls)
	}
	if len(llm.requests) < 2 {
		t.Fatalf("expected retry request history, got %d requests", len(llm.requests))
	}
	if len(llm.requests[1].Messages) < 2 {
		t.Fatalf("expected retry to continue conversation with recovery guidance, got %#v", llm.requests[1].Messages)
	}
	if llm.requests[1].Messages[0].Content != "read a.txt" {
		t.Fatalf("expected original user request to remain first message, got %#v", llm.requests[1].Messages)
	}
	if !strings.Contains(strings.ToLower(llm.requests[1].Messages[len(llm.requests[1].Messages)-1].Content), "complete") {
		t.Fatalf("expected retry guidance requesting complete tool arguments, got %#v", llm.requests[1].Messages)
	}
	recoveryPrompt := llm.requests[1].Messages[len(llm.requests[1].Messages)-1].Content
	for _, expected := range []string{
		"Recovery rules:",
		"exactly one complete tool call",
		"Include every required field",
		"stop calling tools",
	} {
		if !strings.Contains(recoveryPrompt, expected) {
			t.Fatalf("expected recovery prompt to contain %q, got %q", expected, recoveryPrompt)
		}
	}
	foundDetectedStatus := false
	foundRecoveringStatus := false
	for _, status := range statuses {
		lower := strings.ToLower(status)
		if strings.Contains(lower, "detected incomplete tool arguments") {
			foundDetectedStatus = true
		}
		if strings.Contains(lower, "recovering") && strings.Contains(lower, "complete valid tool-call json") {
			foundRecoveringStatus = true
		}
	}
	if !foundDetectedStatus || !foundRecoveringStatus {
		t.Fatalf("expected staged operator-facing recovery statuses, got %#v", statuses)
	}
}

func TestRunStreamSuppressesFailedInvalidToolArgumentPrelude(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{
			events: []schema.StreamEvent{
				{Type: schema.StreamEventText, Content: "I will rewrite the file now."},
				{Type: schema.StreamEventToolCall, ToolName: "write_file", ToolCallID: "bad-call", Content: "write"},
			},
			err: fmt.Errorf("llm chat: invalid tool arguments: tool call has invalid arguments: unexpected end of JSON input"),
		},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "recovered final answer"}}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "read_file",
		Kind:        "read",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    3,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	var texts []string
	var toolCalls []string
	result, err := runner.RunStream(context.Background(), "read a.txt", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, func(event schema.StreamEvent) error {
		switch event.Type {
		case schema.StreamEventText:
			texts = append(texts, event.Content)
		case schema.StreamEventToolCall:
			toolCalls = append(toolCalls, event.ToolName)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected recovery from invalid tool arguments, got %v", err)
	}
	if result.Output != "recovered final answer" {
		t.Fatalf("expected recovered final output, got %#v", result)
	}
	joinedText := strings.Join(texts, "\n")
	if strings.Contains(joinedText, "I will rewrite the file now.") {
		t.Fatalf("expected failed prelude to be suppressed, got text events %#v", texts)
	}
	if joinedText != "recovered final answer" {
		t.Fatalf("expected only final recovered text, got %#v", texts)
	}
	if strings.Contains(strings.Join(toolCalls, ","), "write_file") {
		t.Fatalf("expected failed partial tool call to be suppressed, got %#v", toolCalls)
	}
	if len(toolCalls) != 1 || toolCalls[0] != "read_file" {
		t.Fatalf("expected only recovered read_file tool call, got %#v", toolCalls)
	}
}

func TestRunStreamSuppressesSuccessfulToolCallPrelude(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{
			events: []schema.StreamEvent{
				{Type: schema.StreamEventText, Content: "I will read the file first."},
			},
			response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}},
		},
		{response: schema.ChatResponse{Message: schema.Message{Content: "final answer after reading"}}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "read_file",
		Kind:        "read",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	var texts []string
	result, err := runner.RunStream(context.Background(), "read a.txt", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, func(event schema.StreamEvent) error {
		if event.Type == schema.StreamEventText {
			texts = append(texts, event.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected successful run, got %v", err)
	}
	if result.Output != "final answer after reading" {
		t.Fatalf("expected final output, got %#v", result)
	}
	joinedText := strings.Join(texts, "\n")
	if strings.Contains(joinedText, "I will read the file first.") {
		t.Fatalf("expected tool-call prelude to be suppressed, got text events %#v", texts)
	}
	if joinedText != "final answer after reading" {
		t.Fatalf("expected only final text, got %#v", texts)
	}
}

func TestRunStreamRetriesRepeatedInvalidToolArgumentsBeforeFailing(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{err: fmt.Errorf("llm chat: invalid tool arguments: tool call has invalid arguments: unexpected end of JSON input")},
		{err: fmt.Errorf("llm chat: invalid tool arguments: tool call has invalid arguments: unexpected end of JSON input")},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "recovered after second retry"}}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "read_file",
		Kind:        "read",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    3,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "read a.txt", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, nil)
	if err != nil {
		t.Fatalf("expected recovery after repeated invalid tool arguments, got %v", err)
	}
	if result.Output != "recovered after second retry" {
		t.Fatalf("expected recovered final output, got %#v", result)
	}
	if len(llm.requests) < 3 {
		t.Fatalf("expected original request plus two recovery attempts, got %d", len(llm.requests))
	}
	finalRecoveryPrompt := llm.requests[2].Messages[len(llm.requests[2].Messages)-1].Content
	if !strings.Contains(finalRecoveryPrompt, "This is the final recovery attempt.") {
		t.Fatalf("expected final recovery attempt warning, got %q", finalRecoveryPrompt)
	}
}

func TestRunStreamAllowsFinalResponseAfterToolIterationBudget(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}}},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-2", Name: "read_file", Arguments: []byte(`{"path":"b.txt"}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "all done"}}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "read_file",
		Kind:        "read",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "read both files then summarize", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, nil)
	if err != nil {
		t.Fatalf("expected final response after tool budget, got %v", err)
	}
	if result.Output != "all done" {
		t.Fatalf("expected final output, got %#v", result)
	}
	if len(result.ToolResults) != 2 {
		t.Fatalf("expected both tool results, got %#v", result.ToolResults)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("expected extra completion turn after tool iterations, got %d requests", len(llm.requests))
	}
}

func TestRunStreamReturnsBudgetSummaryWhenFinalTurnStillCallsTool(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}}},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-2", Name: "read_file", Arguments: []byte(`{"path":"b.txt"}`)}}}},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-3", Name: "read_file", Arguments: []byte(`{"path":"c.txt"}`)}}}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "read_file",
		Kind:        "read",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    1,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "read files then summarize", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, nil)
	if err != nil {
		t.Fatalf("expected graceful local budget summary, got %v", err)
	}
	if !strings.Contains(result.Output, "Tool iteration budget reached") {
		t.Fatalf("expected local budget summary, got %#v", result)
	}
	if len(result.ToolResults) != 1 {
		t.Fatalf("expected completed tool result to remain available, got %#v", result.ToolResults)
	}
}

func TestRunStreamAllowsFinalTurnMutationToReachApproval(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"calculator.py"}`)}}}},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-2", Name: "write_file", Arguments: []byte(`{"path":"calculator.py","content":"print('improved')\n"}`)}}}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{
		{
			Name:        "read_file",
			Kind:        "read",
			InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		},
		{
			Name:        "write_file",
			Kind:        "write",
			InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
		},
	}}
	state := session.New(4)
	runtimeRef := &Runtime{
		cfg:       &config.Config{WorkspaceRoot: "C:/repo"},
		session:   state,
		approvals: newApprovalStore(),
	}
	runner := &AgentRunner{id: "fixer", profile: config.AgentProfile{
		Name:             "Fixer",
		Mode:             "fix",
		Model:            "test-model",
		MaxIterations:    1,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite},
		ToolPolicy:       config.ToolPolicyConfirm,
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "optimize the calculator", stubSkillManager{}, mcp, state, runtime.NewAuditLogger(false, false), runtimeRef, nil)
	if err != nil {
		t.Fatalf("expected final-turn write to suspend for approval, got %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected only read_file to execute before write approval, got %d MCP calls", mcp.calls)
	}
	if len(result.ToolResults) != 2 || !result.ToolResults[1].Suspended || result.ToolResults[1].ToolName != "write_file" {
		t.Fatalf("expected read result plus suspended write_file, got %#v", result.ToolResults)
	}
	if strings.Contains(result.Output, "Tool iteration budget reached") {
		t.Fatalf("expected approval instead of budget summary, got %#v", result)
	}
	if pending := runtimeRef.PendingApprovals(); len(pending) != 1 || pending[0].ID != "call-2" {
		t.Fatalf("expected pending approval for final-turn write, got %#v", pending)
	}
}

func TestEnsureMinimumToolIterationsOnlyBumpsMutationCapableProfiles(t *testing.T) {
	readOnly := ensureMinimumToolIterations(config.AgentProfile{
		MaxIterations:    1,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
		Mode:             "plan",
	})
	if readOnly.MaxIterations != 1 {
		t.Fatalf("expected read-only profile to keep one iteration, got %d", readOnly.MaxIterations)
	}

	mutating := ensureMinimumToolIterations(config.AgentProfile{
		MaxIterations:    1,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite},
		Mode:             "fix",
	})
	if mutating.MaxIterations != 2 {
		t.Fatalf("expected mutation-capable profile to get read/write/final room, got %d", mutating.MaxIterations)
	}
}

func TestRunStreamSummarizesWhenToolBudgetExhaustedByAnotherToolCall(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}}},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-2", Name: "read_file", Arguments: []byte(`{"path":"b.txt"}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "stopped after budget and summarized"}}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "read_file",
		Kind:        "read",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "chat",
		Model:            "test-model",
		MaxIterations:    1,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "read files then summarize", stubSkillManager{}, mcp, session.New(4), runtime.NewAuditLogger(false, false), nil, nil)
	if err != nil {
		t.Fatalf("expected graceful budget summary, got %v", err)
	}
	if result.Output != "stopped after budget and summarized" {
		t.Fatalf("expected budget summary output, got %#v", result)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("expected final no-tool summary request, got %d requests", len(llm.requests))
	}
	if len(llm.requests[2].Tools) != 0 {
		t.Fatalf("expected final budget request to disable tools, got %#v", llm.requests[2].Tools)
	}
	if !strings.Contains(llm.requests[2].Messages[len(llm.requests[2].Messages)-1].Content, "Do not call more tools") {
		t.Fatalf("expected final budget guidance, got %#v", llm.requests[2].Messages)
	}
}

func TestRunStreamMarksFallbackUsageInResultAndAudit(t *testing.T) {
	llm := &delayedUsageLLMClient{response: schema.ChatResponse{
		Message: schema.Message{Content: "fallback answer"},
		Usage:   schema.TokenUsage{PromptTokens: 5, OutputTokens: 4, CachedTokens: 0},
	}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Provider:         "backup",
		Mode:             "audit",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(&stubRuntimeMCP{}), fallbackUsed: true}
	audit := runtime.NewAuditLogger(true, false)

	result, err := runner.RunStream(context.Background(), "inspect this", stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), audit, nil, nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if !result.AuditTrail[len(result.AuditTrail)-1].FallbackUsed {
		t.Fatalf("expected fallback flag in audit trail, got %#v", result.AuditTrail)
	}
	if !hasFallbackStructuredSection(result.Structured) {
		t.Fatalf("expected fallback structured section, got %#v", result.Structured)
	}
}

func TestRunStreamEmitsTokenUsageEvent(t *testing.T) {
	llm := &delayedUsageLLMClient{response: schema.ChatResponse{
		Message: schema.Message{Content: "answer"},
		Usage:   schema.TokenUsage{PromptTokens: 7, OutputTokens: 3, CachedTokens: 2},
	}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Provider:         "primary",
		Mode:             "plan",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(&stubRuntimeMCP{})}

	var events []schema.StreamEvent
	_, err := runner.RunStream(context.Background(), "plan this", stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), runtime.NewAuditLogger(false, false), nil, func(event schema.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	for _, event := range events {
		if event.Type != schema.StreamEventTokenUsage {
			continue
		}
		if event.PromptTokens != 7 || event.OutputTokens != 3 || event.CachedTokens != 2 || event.AgentID != "planner" || event.Mode != "plan" {
			t.Fatalf("unexpected token usage event: %#v", event)
		}
		return
	}
	t.Fatalf("expected token usage event, got %#v", events)
}

func TestRuntimeRunStreamAddsVerifierPassForFixMode(t *testing.T) {
	fixerLLM := &verifierPassTestLLMClient{streamResponse: schema.ChatResponse{
		Message: schema.Message{Content: "changed files and ran tests"},
	}}
	auditorLLM := &verifierPassTestLLMClient{chatResponse: schema.ChatResponse{
		Message: schema.Message{Content: "Status: pass\nChecked: implementation summary and test claim\nRisks: none\nNext: none"},
		Usage:   schema.TokenUsage{PromptTokens: 11, OutputTokens: 7, CachedTokens: 0},
	}}
	cfg := &config.Config{
		DefaultAgent: "fixer",
		Agents: map[string]config.AgentProfile{
			"fixer": {
				Name:             "Fixer",
				Provider:         "fixer",
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			"auditor": {
				Name:             "Auditor",
				Provider:         "auditor",
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
		Verifier: config.VerifierConfig{Enabled: true, Agent: "auditor", Modes: []string{"fix"}, MaxTokens: 128},
	}
	runtimeRef, err := NewRuntime(cfg, map[string]interfaces.LLMClient{
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	}, stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), runtime.NewAuditLogger(true, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	var events []schema.StreamEvent
	result, err := runtimeRef.RunStream(context.Background(), "fix the bug", func(event schema.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if !strings.Contains(result.Output, "Verification pass:") || len(result.Verification) == 0 || result.Verification[len(result.Verification)-1].Kind != "verifier" {
		t.Fatalf("expected verifier output and artifact, got %#v", result)
	}
	if len(auditorLLM.chatRequests) != 1 {
		t.Fatalf("expected one verifier chat request, got %d", len(auditorLLM.chatRequests))
	}
	if len(auditorLLM.chatRequests[0].Tools) != 0 {
		t.Fatalf("expected verifier pass to disable tools, got %#v", auditorLLM.chatRequests[0].Tools)
	}
	seenTokenUsage := false
	seenVerifyStage := false
	for _, event := range events {
		if event.Type == schema.StreamEventTokenUsage && event.AgentID == "auditor" && event.PromptTokens == 11 && event.OutputTokens == 7 {
			seenTokenUsage = true
		}
		if event.Type == schema.StreamEventTaskStage && event.AgentID == "auditor" && event.TaskStage == "verify" {
			seenVerifyStage = true
		}
	}
	if !seenTokenUsage {
		t.Fatalf("expected verifier token usage event, got %#v", events)
	}
	if !seenVerifyStage {
		t.Fatalf("expected verifier task stage event, got %#v", events)
	}
}

func hasFallbackStructuredSection(sections []schema.StructuredSection) bool {
	for _, section := range sections {
		if section.Kind == "provider" && len(section.Items) == 1 && section.Items[0] == "fallback" {
			return true
		}
	}
	return false
}

func TestRunStreamBuildsStructuredArtifactsForAuditMode(t *testing.T) {
	llm := &delayedUsageLLMClient{response: schema.ChatResponse{
		Message: schema.Message{Content: "Found SQL injection risk in internal/api/user.go and added parameterized query verification."},
	}}
	runner := &AgentRunner{id: "auditor", profile: config.AgentProfile{
		Name:             "Auditor",
		Mode:             "audit",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(&stubRuntimeMCP{})}
	audit := runtime.NewAuditLogger(true, false)

	result, err := runner.RunStream(context.Background(), "audit this", stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), audit, nil, nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(result.Findings) == 0 {
		t.Fatalf("expected findings, got %#v", result)
	}
	if result.Findings[0].Summary == "" {
		t.Fatalf("expected finding summary, got %#v", result.Findings)
	}
	if len(result.Verification) == 0 {
		t.Fatalf("expected verification items, got %#v", result.Verification)
	}
	if result.Verification[0].Status == "" {
		t.Fatalf("expected verification status, got %#v", result.Verification)
	}
}

func TestRunStreamBuildsStructuredArtifactsForFixMode(t *testing.T) {
	llm := &delayedUsageLLMClient{response: schema.ChatResponse{
		Message: schema.Message{Content: "Updated internal/api/user.go to use parameterized queries and adjusted internal/api/user_test.go."},
	}}
	runner := &AgentRunner{id: "fixer", profile: config.AgentProfile{
		Name:             "Fixer",
		Mode:             "fix",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(&stubRuntimeMCP{})}
	audit := runtime.NewAuditLogger(true, false)

	result, err := runner.RunStream(context.Background(), "fix this", stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), audit, nil, nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(result.Changes) == 0 {
		t.Fatalf("expected changes, got %#v", result)
	}
	if len(result.Changes[0].Files) == 0 {
		t.Fatalf("expected changed files, got %#v", result.Changes)
	}
}

func TestRunStreamAddsReflectionSectionForHighRiskAuditRequests(t *testing.T) {
	llm := &delayedUsageLLMClient{response: schema.ChatResponse{
		Message: schema.Message{Content: "Found SQL injection risk in internal/api/user.go and added parameterized query verification."},
	}}
	runner := &AgentRunner{id: "auditor", profile: config.AgentProfile{
		Name:             "Auditor",
		Mode:             "audit",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(&stubRuntimeMCP{})}
	audit := runtime.NewAuditLogger(true, false)

	result, err := runner.RunStream(context.Background(), "audit this authentication and SQL access path for high-risk security regressions", stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), audit, nil, nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	for _, section := range result.Structured {
		if section.Kind == "reflection" {
			if section.Summary == "" {
				t.Fatalf("expected reflection summary, got %#v", section)
			}
			return
		}
	}
	t.Fatalf("expected reflection structured section, got %#v", result.Structured)
}

func TestRunStreamAddsReflectionSectionForHighRiskFixRequests(t *testing.T) {
	llm := &delayedUsageLLMClient{response: schema.ChatResponse{
		Message: schema.Message{Content: "Updated internal/api/user.go authentication flow to remove a high-risk SQL injection path and tightened internal/api/user_test.go coverage."},
	}}
	runner := &AgentRunner{id: "fixer", profile: config.AgentProfile{
		Name:             "Fixer",
		Mode:             "fix",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(&stubRuntimeMCP{})}
	audit := runtime.NewAuditLogger(true, false)

	result, err := runner.RunStream(context.Background(), "fix this authentication and SQL access path for high-risk security issues", stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), audit, nil, nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	for _, section := range result.Structured {
		if section.Kind == "reflection" {
			if section.Summary == "" {
				t.Fatalf("expected reflection summary, got %#v", section)
			}
			if !strings.Contains(strings.ToLower(section.Summary), "remediation") {
				t.Fatalf("expected fix reflection summary to mention remediation, got %#v", section)
			}
			return
		}
	}
	t.Fatalf("expected reflection structured section, got %#v", result.Structured)
}

func TestRunStreamAddsPlanVerificationForPlanModeOutputs(t *testing.T) {
	llm := &delayedUsageLLMClient{response: schema.ChatResponse{
		Message: schema.Message{Content: "Plan the auth middleware refactor by updating internal/api/user.go, adding regression coverage in internal/api/user_test.go, and validating session handling."},
	}}
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:             "Planner",
		Mode:             "plan",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, llm: llm, executor: NewExecutor(&stubRuntimeMCP{})}
	audit := runtime.NewAuditLogger(true, false)

	result, err := runner.RunStream(context.Background(), "plan the auth middleware refactor", stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), audit, nil, nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	for _, item := range result.Verification {
		if item.Kind == "plan" {
			if item.Status != "proposed" {
				t.Fatalf("expected plan verification status proposed, got %#v", item)
			}
			if !strings.Contains(strings.ToLower(item.Detail), "plan") {
				t.Fatalf("expected plan verification detail to mention plan, got %#v", item)
			}
			return
		}
	}
	t.Fatalf("expected plan verification item, got %#v", result.Verification)
}

func TestRunStreamAddsReflectionVerificationForHighRiskAuditAndFixRequests(t *testing.T) {
	tests := []struct {
		name           string
		mode           string
		input          string
		output         string
		expectedDetail string
	}{
		{
			name:           "audit request adds reflection verification",
			mode:           "audit",
			input:          "audit this authentication and SQL access path for high-risk security regressions",
			output:         "Found SQL injection risk in internal/api/user.go and added parameterized query verification.",
			expectedDetail: "self-check",
		},
		{
			name:           "fix request adds reflection verification",
			mode:           "fix",
			input:          "fix this authentication and SQL access path for high-risk security issues",
			output:         "Updated internal/api/user.go authentication flow to remove a high-risk SQL injection path and tightened internal/api/user_test.go coverage.",
			expectedDetail: "remediation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			llm := &delayedUsageLLMClient{response: schema.ChatResponse{
				Message: schema.Message{Content: tc.output},
			}}
			runner := &AgentRunner{id: tc.mode, profile: config.AgentProfile{
				Name:             tc.mode,
				Mode:             tc.mode,
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
			}, llm: llm, executor: NewExecutor(&stubRuntimeMCP{})}
			audit := runtime.NewAuditLogger(true, false)

			result, err := runner.RunStream(context.Background(), tc.input, stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), audit, nil, nil)
			if err != nil {
				t.Fatalf("RunStream: %v", err)
			}
			for _, item := range result.Verification {
				if item.Kind == "reflection" {
					if item.Status != "performed" {
						t.Fatalf("expected reflection verification status performed, got %#v", item)
					}
					if !strings.Contains(strings.ToLower(item.Detail), tc.expectedDetail) {
						t.Fatalf("expected reflection verification detail to mention %q, got %#v", tc.expectedDetail, item)
					}
					return
				}
			}
			t.Fatalf("expected reflection verification item, got %#v", result.Verification)
		})
	}
}

func TestRunStreamRoutesOrdinaryChatByIntent(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedAgent string
		expectedMode  string
		defaultAgent  string
	}{
		{
			name:          "english architecture requests route to planner",
			input:         "design the rollout architecture and proposal for this migration",
			expectedAgent: "planner",
			expectedMode:  "plan",
			defaultAgent:  "chat",
		},
		{
			name:          "english how-to planning requests route to planner",
			input:         "how should we break this migration into steps and approach the rollout",
			expectedAgent: "planner",
			expectedMode:  "plan",
			defaultAgent:  "chat",
		},
		{
			name:          "english security review requests route to auditor",
			input:         "inspect this patch for risk and do a security review",
			expectedAgent: "auditor",
			expectedMode:  "audit",
			defaultAgent:  "chat",
		},
		{
			name:          "english impact evaluation requests route to auditor",
			input:         "evaluate the compatibility impact and trade-offs of this API change",
			expectedAgent: "auditor",
			expectedMode:  "audit",
			defaultAgent:  "chat",
		},
		{
			name:          "english worth-changing requests route to auditor",
			input:         "take a look and judge whether this refactor is worth doing before we change it",
			expectedAgent: "auditor",
			expectedMode:  "audit",
			defaultAgent:  "chat",
		},
		{
			name:          "english proposal-safety requests route to auditor",
			input:         "do you think this rollout plan is safe and reasonable",
			expectedAgent: "auditor",
			expectedMode:  "audit",
			defaultAgent:  "chat",
		},
		{
			name:          "english lightweight advisory requests stay on chat",
			input:         "give me a rough approach and some advice on where to start with this migration",
			expectedAgent: "chat",
			expectedMode:  "chat",
			defaultAgent:  "chat",
		},
		{
			name:          "english rewrite requests route to fixer",
			input:         "rewrite the handler and apply the change directly",
			expectedAgent: "fixer",
			expectedMode:  "fix",
			defaultAgent:  "chat",
		},
		{
			name:          "chinese optimization requests route to fixer",
			input:         "再优化下当前项目",
			expectedAgent: "fixer",
			expectedMode:  "fix",
			defaultAgent:  "chat",
		},
		{
			name:          "chinese how-to planning requests route to planner",
			input:         "plan this requirement first and split the implementation steps",
			expectedAgent: "planner",
			expectedMode:  "plan",
			defaultAgent:  "chat",
		},
		{
			name:          "chinese feature expansion requests route to fixer",
			input:         "extend this project and add new features",
			expectedAgent: "fixer",
			expectedMode:  "fix",
			defaultAgent:  "chat",
		},
		{
			name:          "chinese compatibility evaluation requests route to auditor",
			input:         "先评估一下这个改动的兼容性影响和权衡",
			expectedAgent: "auditor",
			expectedMode:  "audit",
			defaultAgent:  "chat",
		},
		{
			name:          "chinese worth-changing requests route to auditor",
			input:         "audit whether this refactor is worth changing and assess the risk",
			expectedAgent: "auditor",
			expectedMode:  "audit",
			defaultAgent:  "chat",
		},
		{
			name:          "chinese proposal-safety requests route to auditor",
			input:         "你觉得这个方案靠谱吗，风险大不大",
			expectedAgent: "auditor",
			expectedMode:  "audit",
			defaultAgent:  "chat",
		},
		{
			name:          "chinese lightweight advisory requests stay on chat",
			input:         "先给我一个大概思路和建议，告诉我从哪里开始比较好",
			expectedAgent: "chat",
			expectedMode:  "chat",
			defaultAgent:  "chat",
		},
		{
			name:          "chinese implementation requests route to fixer after completed workflow",
			input:         "请直接修改当前项目并完善这个功能",
			expectedAgent: "fixer",
			expectedMode:  "fix",
			defaultAgent:  "planner",
		},
		{
			name:          "generic chat stays on default agent",
			input:         "hello there",
			expectedAgent: "chat",
			expectedMode:  "chat",
			defaultAgent:  "chat",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runtimeRef := newRoutingTestRuntime(tc.defaultAgent)

			result, err := runtimeRef.RunStream(context.Background(), tc.input, nil)
			if err != nil {
				t.Fatalf("RunStream: %v", err)
			}
			if result.AgentID != tc.expectedAgent {
				t.Fatalf("expected agent %q, got %q", tc.expectedAgent, result.AgentID)
			}
			if result.Mode != tc.expectedMode {
				t.Fatalf("expected mode %q, got %q", tc.expectedMode, result.Mode)
			}
			if runtimeRef.ActiveAgent() != tc.defaultAgent {
				t.Fatalf("expected active agent to return to default %q, got %q", tc.defaultAgent, runtimeRef.ActiveAgent())
			}
			snapshot := runtimeRef.SessionSnapshot()
			if snapshot.ActiveAgent != tc.defaultAgent {
				t.Fatalf("expected session active agent to return to default %q, got %#v", tc.defaultAgent, snapshot)
			}
			defaultProfile, ok := runtimeRef.Profile(tc.defaultAgent)
			if !ok {
				t.Fatalf("missing default profile %q", tc.defaultAgent)
			}
			if snapshot.Mode != defaultProfile.Mode {
				t.Fatalf("expected session mode to return to default %q, got %#v", defaultProfile.Mode, snapshot)
			}
			if snapshot.LastRouting.TargetAgent != tc.expectedAgent {
				t.Fatalf("expected last routing target %q, got %#v", tc.expectedAgent, snapshot.LastRouting)
			}
			if snapshot.LastRouting.Outcome == "" {
				t.Fatalf("expected last routing outcome to be recorded, got %#v", snapshot.LastRouting)
			}
		})
	}
}

func TestNewRuntimeRestoresDefaultChatAgentWithoutResumableSessionState(t *testing.T) {
	cfg, clients, skills, mcpClient := newRuntimeConstructionFixture("chat")
	state := session.New(8)
	state.SetActiveAgent("planner")
	state.SetMode("plan")
	state.SetWorkflow(session.WorkflowSnapshot{Name: workflowNamePlanFixAudit, Status: "completed"})
	runtimeRef, err := NewRuntime(cfg, clients, skills, mcpClient, state, runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if runtimeRef.ActiveAgent() != "chat" {
		t.Fatalf("expected default chat agent after startup normalization, got %q", runtimeRef.ActiveAgent())
	}
	snapshot := runtimeRef.SessionSnapshot()
	if snapshot.ActiveAgent != "chat" || snapshot.Mode != "chat" {
		t.Fatalf("expected normalized chat session snapshot, got %#v", snapshot)
	}
}

func TestNewRuntimePreservesPendingApprovalSessionState(t *testing.T) {
	cfg, clients, skills, mcpClient := newRuntimeConstructionFixture("chat")
	state := session.New(8)
	state.SetActiveAgent("fixer")
	state.SetMode("fix")
	state.SetPendingApprovals([]session.PendingApprovalSnapshot{{CallID: "call-1", ToolName: "write_file", AgentID: "fixer"}})
	runtimeRef, err := NewRuntime(cfg, clients, skills, mcpClient, state, runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if runtimeRef.ActiveAgent() != "fixer" {
		t.Fatalf("expected resumable fixer agent, got %q", runtimeRef.ActiveAgent())
	}
	snapshot := runtimeRef.SessionSnapshot()
	if snapshot.ActiveAgent != "fixer" || snapshot.Mode != "fix" {
		t.Fatalf("expected preserved resumable snapshot, got %#v", snapshot)
	}
}

func TestNewRuntimePreservesPendingHandoffSessionState(t *testing.T) {
	cfg, clients, skills, mcpClient := newRuntimeConstructionFixture("chat")
	state := session.New(8)
	state.SetActiveAgent("planner")
	state.SetMode("plan")
	state.SetPendingHandoff(session.PendingHandoffSnapshot{
		Request:        "add export support",
		SourceAgent:    "planner",
		TargetAgent:    "fixer",
		TargetMode:     "fix",
		PlanSummary:    "Implement export support after approval",
		ExpectedAction: "confirm_execution",
	})
	runtimeRef, err := NewRuntime(cfg, clients, skills, mcpClient, state, runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if runtimeRef.ActiveAgent() != "planner" {
		t.Fatalf("expected planner agent to remain active for resumable handoff, got %q", runtimeRef.ActiveAgent())
	}
	snapshot := runtimeRef.SessionSnapshot()
	if snapshot.PendingHandoff.TargetAgent != "fixer" || snapshot.Mode != "plan" {
		t.Fatalf("expected preserved handoff snapshot, got %#v", snapshot)
	}
}

func TestRuntimeRunStreamPendingHandoffConfirmExecutesFixer(t *testing.T) {
	runtimeRef := newRoutingTestRuntime("chat")
	runtimeRef.session.SetPendingHandoff(session.PendingHandoffSnapshot{
		Request:        "add export support",
		SourceAgent:    "planner",
		TargetAgent:    "fixer",
		TargetMode:     "fix",
		PlanSummary:    "Implement export support after approval",
		ExpectedAction: "confirm_execution",
	})
	if err := runtimeRef.SetActiveAgent("planner"); err != nil {
		t.Fatalf("SetActiveAgent: %v", err)
	}

	result, err := runtimeRef.RunStream(context.Background(), "可以", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if result.AgentID != "fixer" {
		t.Fatalf("expected fixer agent to handle confirmation, got %#v", result)
	}
	if result.Mode != "fix" {
		t.Fatalf("expected fixer mode after confirmation, got %#v", result)
	}
	if result.Output != "fix response" {
		t.Fatalf("expected fixer response after confirmation, got %#v", result)
	}
	if runtimeRef.HasPendingHandoff() {
		t.Fatalf("expected pending handoff to be cleared")
	}
	if runtimeRef.ActiveAgent() != "chat" {
		t.Fatalf("expected active agent to return to chat after completed handoff execution, got %q", runtimeRef.ActiveAgent())
	}
	if got := runtimeRef.SessionSnapshot().LastRouting.Outcome; got != "handoff_confirmed" {
		t.Fatalf("expected handoff_confirmed routing outcome, got %#v", runtimeRef.SessionSnapshot().LastRouting)
	}
}

func TestRuntimeResumeApprovedOrdinaryToolCallContinuesConversation(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{
			Message: schema.Message{Content: "I will update the file."},
			ToolCalls: []schema.ToolCall{{
				ID:        "call-1",
				Name:      "write_file",
				Arguments: []byte(`{"path":"README.md","content":"updated"}`),
			}},
		}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "continued after approval"}}},
	}}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}}
	cfg := &config.Config{
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:             "Chat",
				Provider:         "chat",
				Mode:             "chat",
				Model:            "test-model",
				MaxIterations:    3,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyConfirm,
			},
		},
	}
	runtimeRef, err := NewRuntime(cfg, map[string]interfaces.LLMClient{"chat": llm}, stubSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	first, err := runtimeRef.RunStream(context.Background(), "update the README", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(first.ToolResults) != 1 || !first.ToolResults[0].Suspended {
		t.Fatalf("expected suspended write_file result, got %#v", first.ToolResults)
	}
	if len(runtimeRef.PendingApprovals()) != 1 {
		t.Fatalf("expected one pending approval, got %#v", runtimeRef.PendingApprovals())
	}
	approved, err := runtimeRef.ApproveToolCall(context.Background(), "call-1")
	if err != nil {
		t.Fatalf("ApproveToolCall: %v", err)
	}
	final, resumed, err := runtimeRef.ResumeApprovedOrdinaryToolCall(context.Background(), approved.CallID, nil)
	if err != nil {
		t.Fatalf("ResumeApprovedOrdinaryToolCall: %v", err)
	}
	if !resumed {
		t.Fatal("expected ordinary chat to resume after approval")
	}
	if final.Output != "continued after approval" {
		t.Fatalf("expected resumed final output, got %#v", final)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("expected original and resumed LLM requests, got %d", len(llm.requests))
	}
	resumedMessages := llm.requests[1].Messages
	if len(resumedMessages) < 3 {
		t.Fatalf("expected user, assistant tool call, and approved tool result messages, got %#v", resumedMessages)
	}
	if resumedMessages[len(resumedMessages)-1].Role != "tool" || resumedMessages[len(resumedMessages)-1].ToolCallID != "call-1" {
		t.Fatalf("expected approved tool result to be replayed into resumed conversation, got %#v", resumedMessages)
	}
}

func TestRuntimeRunStreamPendingHandoffRejectReturnsCancellation(t *testing.T) {
	runtimeRef := newRoutingTestRuntime("chat")
	runtimeRef.session.SetPendingHandoff(session.PendingHandoffSnapshot{
		Request:        "add export support",
		SourceAgent:    "planner",
		TargetAgent:    "fixer",
		TargetMode:     "fix",
		PlanSummary:    "Implement export support after approval",
		ExpectedAction: "confirm_execution",
	})
	if err := runtimeRef.SetActiveAgent("planner"); err != nil {
		t.Fatalf("SetActiveAgent: %v", err)
	}

	result, err := runtimeRef.RunStream(context.Background(), "\u53d6\u6d88", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if !strings.Contains(result.Output, "Cancelled pending implementation handoff") {
		t.Fatalf("expected cancellation response, got %#v", result)
	}
	if runtimeRef.HasPendingHandoff() {
		t.Fatalf("expected pending handoff to be cleared")
	}
	if runtimeRef.ActiveAgent() != "planner" {
		t.Fatalf("expected active agent to remain planner after rejection, got %q", runtimeRef.ActiveAgent())
	}
}

func TestRuntimeRunStreamPendingHandoffAmbiguousReturnsGuidanceWithoutExecuting(t *testing.T) {
	runtimeRef := newRoutingTestRuntime("chat")
	runtimeRef.session.SetPendingHandoff(session.PendingHandoffSnapshot{
		Request:        "add export support",
		SourceAgent:    "planner",
		TargetAgent:    "fixer",
		TargetMode:     "fix",
		PlanSummary:    "Implement export support after approval",
		ExpectedAction: "confirm_execution",
	})
	if err := runtimeRef.SetActiveAgent("planner"); err != nil {
		t.Fatalf("SetActiveAgent: %v", err)
	}

	result, err := runtimeRef.RunStream(context.Background(), "maybe later", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if !strings.Contains(result.Output, "A plan is waiting for confirmation before execution") {
		t.Fatalf("expected confirmation guidance, got %#v", result)
	}
	if !runtimeRef.HasPendingHandoff() {
		t.Fatalf("expected pending handoff to remain for ambiguous reply")
	}
	if runtimeRef.ActiveAgent() != "planner" {
		t.Fatalf("expected planner to remain active for ambiguous reply, got %q", runtimeRef.ActiveAgent())
	}
}

func TestRuntimeRunStreamPendingHandoffReviseKeepsPlannerActive(t *testing.T) {
	runtimeRef := newRoutingTestRuntime("chat")
	runtimeRef.session.SetPendingHandoff(session.PendingHandoffSnapshot{
		Request:        "add export support",
		SourceAgent:    "planner",
		TargetAgent:    "fixer",
		TargetMode:     "fix",
		PlanSummary:    "Implement export support after approval",
		ExpectedAction: "confirm_execution",
	})
	if err := runtimeRef.SetActiveAgent("chat"); err != nil {
		t.Fatalf("SetActiveAgent: %v", err)
	}

	result, err := runtimeRef.RunStream(context.Background(), "revise the plan and narrow the scope first", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if result.Output != "plan response" {
		t.Fatalf("expected planner to handle revision request, got %#v", result)
	}
	if runtimeRef.ActiveAgent() != "planner" {
		t.Fatalf("expected runtime to switch back to planner, got %q", runtimeRef.ActiveAgent())
	}
	if !runtimeRef.HasPendingHandoff() {
		t.Fatalf("expected pending handoff to remain open for revised plan")
	}
}

func TestRuntimeRunStreamRecordsRoutingAuditWhenAgentChanges(t *testing.T) {
	runtimeRef := newRoutingTestRuntime("chat")

	_, err := runtimeRef.RunStream(context.Background(), "plan the migration steps for this database change", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	entries := runtimeRef.AuditTrail()
	for _, entry := range entries {
		if entry.Type == "intent_route" {
			if entry.AgentID != "planner" {
				t.Fatalf("expected routing audit for planner, got %#v", entry)
			}
			if entry.Outcome != "rerouted" {
				t.Fatalf("expected rerouted outcome, got %#v", entry)
			}
			if !strings.Contains(entry.Detail, "plan:") {
				t.Fatalf("expected routing detail to include scored plan reason, got %#v", entry)
			}
			return
		}
	}
	t.Fatalf("expected intent_route audit entry, got %#v", entries)
}

func TestClassifyOrdinaryChatIntentDetailScoresExpandedVocabulary(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedMode string
		expectedHits []string
	}{
		{
			name:         "english architecture requests map to planner",
			input:        "please design the rollout architecture and roadmap",
			expectedMode: "plan",
			expectedHits: []string{"design", "architecture", "roadmap"},
		},
		{
			name:         "english how-to planning requests map to planner",
			input:        "how should we approach this migration step by step",
			expectedMode: "plan",
			expectedHits: []string{"how should", "step by step", "approach"},
		},
		{
			name:         "english security review requests map to auditor",
			input:        "do a security review and risk assessment of this patch",
			expectedMode: "audit",
			expectedHits: []string{"security review", "risk", "assessment"},
		},
		{
			name:         "english web vulnerability requests map to auditor",
			input:        "please run web vulnerability research and inspect attack surface for this page",
			expectedMode: "audit",
			expectedHits: []string{"vulnerability", "vulnerability research", "attack surface", "web vulnerability"},
		},
		{
			name:         "english binary vulnerability requests map to auditor",
			input:        "audit this binary for memory corruption and exploitability",
			expectedMode: "audit",
			expectedHits: []string{"audit", "memory corruption", "exploitability"},
		},
		{
			name:         "english impact evaluation requests map to auditor",
			input:        "evaluate the compatibility impact and trade-off of this API change",
			expectedMode: "audit",
			expectedHits: []string{"evaluate", "compatibility", "impact", "trade-off"},
		},
		{
			name:         "english worth-changing requests map to auditor",
			input:        "take a look and judge whether this refactor is worth doing before we change it",
			expectedMode: "audit",
			expectedHits: []string{"take a look", "judge whether", "worth doing"},
		},
		{
			name:         "english proposal-safety requests map to auditor",
			input:        "do you think this rollout plan is safe and reasonable",
			expectedMode: "audit",
			expectedHits: []string{"do you think", "safe", "reasonable"},
		},
		{
			name:         "english lightweight advisory requests stay on chat",
			input:        "give me a rough approach and some advice on where to start with this migration",
			expectedMode: "",
			expectedHits: nil,
		},
		{
			name:         "english implementation requests map to fixer",
			input:        "rewrite the handler and apply the change directly",
			expectedMode: "fix",
			expectedHits: []string{"rewrite", "apply the change"},
		},
		{
			name:         "chinese design requests map to planner",
			input:        "\u5148\u5e2e\u6211\u62c6\u89e3\u4e00\u4e0b\u8fd9\u4e2a\u529f\u80fd\u7684\u8bbe\u8ba1\u601d\u8def\u548c\u8def\u7ebf\u56fe",
			expectedMode: "plan",
			expectedHits: []string{"\u62c6\u89e3", "\u8bbe\u8ba1", "\u601d\u8def", "\u8def\u7ebf"},
		},
		{
			name:         "chinese how-to planning requests map to planner",
			input:        "\u8fd9\u4e2a\u9700\u6c42\u5e94\u8be5\u600e\u4e48\u89c4\u5212\u6b65\u9aa4\uff0c\u5148\u5e2e\u6211\u62c6\u4e00\u4e0b\u5b9e\u73b0\u8def\u7ebf",
			expectedMode: "plan",
			expectedHits: []string{"\u5e94\u8be5\u600e\u4e48", "\u89c4\u5212", "\u6b65\u9aa4"},
		},
		{
			name:         "chinese risk review requests map to auditor",
			input:        "\u8bf7\u5206\u6790\u4e00\u4e0b\u8fd9\u4e2a\u6539\u52a8\u7684\u98ce\u9669\u5e76\u505a\u4ee3\u7801\u5ba1\u67e5",
			expectedMode: "audit",
			expectedHits: []string{"\u5206\u6790", "\u98ce\u9669", "\u4ee3\u7801\u5ba1\u67e5"},
		},
		{
			name:         "chinese web vulnerability requests map to auditor",
			input:        "\u5e2e\u6211\u5bf9\u8fd9\u4e2a\u7f51\u7ad9\u505aweb\u6f0f\u6d1e\u6316\u6398\u5e76\u5206\u6790\u524d\u7aef\u6f0f\u6d1e",
			expectedMode: "audit",
			expectedHits: []string{"\u6f0f\u6d1e", "\u6f0f\u6d1e\u6316\u6398", "web\u6f0f\u6d1e", "\u524d\u7aef\u6f0f\u6d1e"},
		},
		{
			name:         "chinese binary reverse requests map to auditor",
			input:        "\u5e2e\u6211\u505a\u4e8c\u8fdb\u5236\u9006\u5411\uff0c\u770b\u770b\u7cfb\u7edf\u8f6f\u4ef6\u6f0f\u6d1e\u98ce\u9669",
			expectedMode: "audit",
			expectedHits: []string{"\u98ce\u9669", "\u4e8c\u8fdb\u5236\u9006\u5411", "\u7cfb\u7edf\u8f6f\u4ef6\u6f0f\u6d1e"},
		},
		{
			name:         "chinese compatibility evaluation requests map to auditor",
			input:        "\u5148\u8bc4\u4f30\u4e00\u4e0b\u8fd9\u4e2a\u6539\u52a8\u7684\u517c\u5bb9\u6027\u5f71\u54cd\u548c\u6743\u8861",
			expectedMode: "audit",
			expectedHits: []string{"\u8bc4\u4f30", "\u517c\u5bb9\u6027", "\u5f71\u54cd", "\u6743\u8861"},
		},
		{
			name:         "chinese worth-changing requests map to auditor",
			input:        "\u5148\u5e2e\u6211\u770b\u4e00\u4e0b\u8fd9\u4e2a\u91cd\u6784\u503c\u4e0d\u503c\u5f97\u6539\uff0c\u518d\u5224\u65ad\u98ce\u9669",
			expectedMode: "audit",
			expectedHits: []string{"\u5148\u5e2e\u6211\u770b", "\u503c\u4e0d\u503c\u5f97", "\u5224\u65ad", "\u98ce\u9669"},
		},
		{
			name:         "chinese proposal-safety requests map to auditor",
			input:        "\u4f60\u89c9\u5f97\u8fd9\u4e2a\u65b9\u6848\u9760\u8c31\u5417\uff0c\u98ce\u9669\u5927\u4e0d\u5927",
			expectedMode: "audit",
			expectedHits: []string{"\u4f60\u89c9\u5f97", "\u9760\u8c31", "\u98ce\u9669"},
		},
		{
			name:         "chinese lightweight advisory requests stay on chat",
			input:        "\u5148\u7ed9\u6211\u4e00\u4e2a\u5927\u6982\u601d\u8def\u548c\u5efa\u8bae\uff0c\u544a\u8bc9\u6211\u4ece\u54ea\u91cc\u5f00\u59cb\u6bd4\u8f83\u597d",
			expectedMode: "",
			expectedHits: nil,
		},
		{
			name:         "chinese direct code change requests map to fixer",
			input:        "\u76f4\u63a5\u4fee\u6539\u8fd9\u6bb5\u4ee3\u7801\u5e76\u521b\u5efa\u65b0\u529f\u80fd",
			expectedMode: "fix",
			expectedHits: []string{"\u76f4\u63a5\u4fee\u6539", "\u521b\u5efa", "\u65b0\u529f\u80fd"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := classifyOrdinaryChatIntentDetail(tt.input)
			if intent.Mode != tt.expectedMode {
				t.Fatalf("expected mode %q, got %#v", tt.expectedMode, intent)
			}
			if tt.expectedMode == "" {
				if intent.Score != 0 {
					t.Fatalf("expected zero score for default-chat advisory request, got %#v", intent)
				}
				if strings.TrimSpace(intent.Reason) != "" {
					t.Fatalf("expected empty reason for default-chat advisory request, got %#v", intent)
				}
				if len(intent.Hits) != 0 {
					t.Fatalf("expected no keyword hits for default-chat advisory request, got %#v", intent)
				}
				return
			}
			if intent.Score == 0 {
				t.Fatalf("expected positive score, got %#v", intent)
			}
			if strings.TrimSpace(intent.Reason) == "" {
				t.Fatalf("expected non-empty reason, got %#v", intent)
			}
			for _, hit := range tt.expectedHits {
				if !containsString(intent.Hits, hit) {
					t.Fatalf("expected hit %q in %#v", hit, intent)
				}
			}
		})
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

func newRuntimeConstructionFixture(defaultAgent string) (*config.Config, map[string]interfaces.LLMClient, interfaces.SkillManager, interfaces.MCPClient) {
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: defaultAgent,
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:             "Chat",
				Provider:         "chat",
				Mode:             "chat",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
			},
			"planner": {
				Name:             "Planner",
				Provider:         "planner",
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
			},
			"fixer": {
				Name:             "Fixer",
				Provider:         "fixer",
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyConfirm,
			},
			"auditor": {
				Name:             "Auditor",
				Provider:         "auditor",
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
			},
		},
	}

	chatLLM := &stubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat response"}}}}
	plannerLLM := &stubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan response"}}}}
	fixerLLM := &stubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix response"}}}}
	auditorLLM := &stubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit response"}}}}
	mcpClient := &stubRuntimeMCP{}
	clients := map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	}
	return cfg, clients, stubSkillManager{}, mcpClient
}

func newRoutingTestRuntime(defaultAgent string) *Runtime {
	cfg, clients, skills, mcpClient := newRuntimeConstructionFixture(defaultAgent)
	state := session.New(8)
	state.SetActiveAgent(defaultAgent)
	state.SetMode(cfg.Agents[defaultAgent].Mode)

	return &Runtime{
		cfg:    cfg,
		skills: skills,
		runners: map[string]*AgentRunner{
			"chat":    {id: "chat", profile: cfg.Agents["chat"], llm: clients["chat"], executor: NewExecutor(mcpClient)},
			"planner": {id: "planner", profile: cfg.Agents["planner"], llm: clients["planner"], executor: NewExecutor(mcpClient)},
			"fixer":   {id: "fixer", profile: cfg.Agents["fixer"], llm: clients["fixer"], executor: NewExecutor(mcpClient)},
			"auditor": {id: "auditor", profile: cfg.Agents["auditor"], llm: clients["auditor"], executor: NewExecutor(mcpClient)},
		},
		mcp:       mcpClient,
		session:   state,
		audit:     runtime.NewAuditLogger(true, false),
		active:    defaultAgent,
		approvals: newApprovalStore(),
	}
}
