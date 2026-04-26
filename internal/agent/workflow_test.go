package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	skillpkg "github.com/FyMatt/GoFlow-Agent/internal/skill"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type workflowStubLLMClient struct {
	responses []schema.ChatResponse
	calls     int
}

func (s *workflowStubLLMClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	panic("unused")
}

func (s *workflowStubLLMClient) StreamChat(_ context.Context, _ schema.ChatRequest, _ interfaces.StreamHandler) (schema.ChatResponse, error) {
	if s.calls >= len(s.responses) {
		return schema.ChatResponse{Message: schema.Message{Content: "done"}}, nil
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

func (s *workflowStubLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

type workflowRecordingLLMClient struct {
	responses []schema.ChatResponse
	requests  []schema.ChatRequest
	calls     int
}

func (s *workflowRecordingLLMClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	panic("unused")
}

func (s *workflowRecordingLLMClient) StreamChat(_ context.Context, req schema.ChatRequest, _ interfaces.StreamHandler) (schema.ChatResponse, error) {
	s.requests = append(s.requests, req)
	if s.calls >= len(s.responses) {
		return schema.ChatResponse{Message: schema.Message{Content: "done"}}, nil
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

func (s *workflowRecordingLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

type workflowSkillManager struct {
	skills []schema.Skill
}

func (m workflowSkillManager) List() []schema.Skill {
	return append([]schema.Skill(nil), m.skills...)
}

func (m workflowSkillManager) Match(query string) (*schema.Skill, bool) {
	return skillpkg.Match(query, m.skills)
}

func (m workflowSkillManager) MatchWithDiagnostics(query string) (*schema.Skill, schema.SkillMatchDiagnostic, bool) {
	return skillpkg.MatchWithDiagnostics(query, m.skills)
}

func TestWorkflowRunnerSkillChainUsesNextSkillsAndPreferredAgents(t *testing.T) {
	skills := []schema.Skill{
		{
			Name:           "execution-plan",
			Description:    "Create a plan",
			Mode:           "plan",
			PreferredAgent: "planner",
			Activation:     schema.Activation{Keywords: []string{"plan"}},
			NextSkills:     []string{"code-writing"},
			Instructions:   "Plan the work.",
		},
		{
			Name:           "code-writing",
			Description:    "Write code",
			Mode:           "fix",
			PreferredAgent: "fixer",
			Activation:     schema.Activation{Keywords: []string{"implement"}},
			NextSkills:     []string{"code-audit"},
			Instructions:   "Implement the plan.",
		},
		{
			Name:           "code-audit",
			Description:    "Audit code",
			Mode:           "audit",
			PreferredAgent: "auditor",
			Activation:     schema.Activation{Keywords: []string{"audit"}},
			Instructions:   "Audit the implementation.",
		},
	}
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:             "Chat",
				Provider:         "chat",
				Mode:             "chat",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			"planner": {
				Name:             "Planner",
				Provider:         "planner",
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
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
	}
	chatLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}}
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan output"}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix output"}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit output"}}}}
	mcpClient := &stubRuntimeMCP{}
	runtimeRef, err := NewRuntime(cfg, map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	}, workflowSkillManager{skills: skills}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), workflowNameSkillChain, "please plan this feature", true, nil)
	if err != nil {
		t.Fatalf("Run skill-chain: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 3 {
		t.Fatalf("expected completed three-stage skill chain, got %#v", result)
	}
	expectedAgents := []string{"planner", "fixer", "auditor"}
	expectedSkills := []string{"execution-plan", "code-writing", "code-audit"}
	for i, stage := range result.CompletedStages {
		if stage.Agent != expectedAgents[i] || string(stage.Stage) != expectedSkills[i] {
			t.Fatalf("unexpected stage %d: %#v", i, stage)
		}
	}
	if !strings.Contains(plannerLLM.requests[0].System, "Matched skill: execution-plan") {
		t.Fatalf("expected planner system prompt to include execution-plan skill, got %q", plannerLLM.requests[0].System)
	}
	if !strings.Contains(fixerLLM.requests[0].System, "Matched skill: code-writing") {
		t.Fatalf("expected fixer system prompt to include code-writing skill, got %q", fixerLLM.requests[0].System)
	}
	if !strings.Contains(auditorLLM.requests[0].System, "Matched skill: code-audit") {
		t.Fatalf("expected auditor system prompt to include code-audit skill, got %q", auditorLLM.requests[0].System)
	}
	if runtimeRef.ActiveAgent() != "chat" {
		t.Fatalf("expected default agent restored after skill-chain, got %s", runtimeRef.ActiveAgent())
	}
}

func TestWorkflowRunnerSkillChainResumesSuspendedStage(t *testing.T) {
	skills := []schema.Skill{
		{Name: "execution-plan", Description: "Create a plan", Mode: "plan", PreferredAgent: "planner", Activation: schema.Activation{Keywords: []string{"plan"}}, NextSkills: []string{"code-writing"}, Instructions: "Plan."},
		{Name: "code-writing", Description: "Write code", Mode: "fix", PreferredAgent: "fixer", Activation: schema.Activation{Keywords: []string{"implement"}}, NextSkills: []string{"code-audit"}, Instructions: "Implement."},
		{Name: "code-audit", Description: "Audit code", Mode: "audit", PreferredAgent: "auditor", Activation: schema.Activation{Keywords: []string{"audit"}}, Instructions: "Audit."},
	}
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat":    {Name: "Chat", Provider: "chat", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"planner": {Name: "Planner", Provider: "planner", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":   {Name: "Fixer", Provider: "fixer", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"auditor": {Name: "Auditor", Provider: "auditor", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
	}
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}
	runtimeRef, err := NewRuntime(cfg, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan output"}}}},
		"fixer": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{
			Message: schema.Message{Content: "need write"},
			ToolCalls: []schema.ToolCall{{
				ID:        "call-1",
				Name:      "write_file",
				Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
			}},
		}, {Message: schema.Message{Content: "fix output"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit output"}}}},
	}, workflowSkillManager{skills: skills}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), workflowNameSkillChain, "please plan this feature", true, nil)
	if err != nil {
		t.Fatalf("Run skill-chain: %v", err)
	}
	if first.Status != "awaiting_tool_approval" || first.NextStage != "code-writing" {
		t.Fatalf("expected suspended code-writing stage, got %#v", first)
	}
	resumed, err := runtimeRef.WorkflowRunner().Resume(context.Background(), workflowNameSkillChain, "call-1", true, nil)
	if err != nil {
		t.Fatalf("Resume skill-chain: %v", err)
	}
	if resumed.Status != "completed" || len(resumed.CompletedStages) != 3 {
		t.Fatalf("expected resumed workflow to complete, got %#v", resumed)
	}
	if resumed.CompletedStages[1].Result.Output != "fix output" || resumed.CompletedStages[2].Result.Output != "audit output" {
		t.Fatalf("unexpected resumed stage outputs: %#v", resumed.CompletedStages)
	}
}

func TestWorkflowRunnerSkillChainSelectsConditionalNextSkill(t *testing.T) {
	skills := []schema.Skill{
		{
			Name:           "triage",
			Description:    "Choose the next specialist",
			Mode:           "plan",
			PreferredAgent: "planner",
			Activation:     schema.Activation{Keywords: []string{"triage"}},
			NextSkills:     []string{"code-writing", "code-audit"},
			Metadata:       map[string]string{"next_strategy": "select"},
			Instructions:   "Triage the request.",
		},
		{
			Name:           "code-writing",
			Description:    "Implement code changes",
			Mode:           "fix",
			PreferredAgent: "fixer",
			Activation:     schema.Activation{Keywords: []string{"implement", "fix"}},
			Instructions:   "Implement.",
		},
		{
			Name:           "code-audit",
			Description:    "Audit code for security issues",
			Mode:           "audit",
			PreferredAgent: "auditor",
			Activation:     schema.Activation{Keywords: []string{"audit", "security"}},
			Instructions:   "Audit.",
		},
	}
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat":    {Name: "Chat", Provider: "chat", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"planner": {Name: "Planner", Provider: "planner", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":   {Name: "Fixer", Provider: "fixer", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
			"auditor": {Name: "Auditor", Provider: "auditor", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
	}
	mcpClient := &stubRuntimeMCP{}
	runtimeRef, err := NewRuntime(cfg, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "security audit needed"}}}},
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix output"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit output"}}}},
	}, workflowSkillManager{skills: skills}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), workflowNameSkillChain, "triage this module", true, nil)
	if err != nil {
		t.Fatalf("Run skill-chain: %v", err)
	}
	if len(result.CompletedStages) != 2 {
		t.Fatalf("expected triage plus selected audit stage, got %#v", result.CompletedStages)
	}
	if result.CompletedStages[1].Stage != "code-audit" || result.CompletedStages[1].Agent != "auditor" {
		t.Fatalf("expected conditional branch to choose code-audit, got %#v", result.CompletedStages[1])
	}
}

func TestSelectNextSkillCandidatesHonorsExplicitDirective(t *testing.T) {
	current := schema.Skill{Name: "triage", Metadata: map[string]string{"next_strategy": "planner_select"}}
	candidates := []schema.Skill{
		{Name: "code-writing", Activation: schema.Activation{Keywords: []string{"implement"}}},
		{Name: "code-audit", Activation: schema.Activation{Keywords: []string{"audit", "security"}}},
	}

	selected := selectNextSkillCandidates(current, candidates, "audit this security path", "Security review is useful, but operator asked to implement first.\nNext skill: code-writing")
	if len(selected) != 1 || selected[0].Name != "code-writing" {
		t.Fatalf("expected explicit directive to select code-writing, got %#v", selected)
	}
}

func TestParseExplicitNextSkillNamesSupportsLists(t *testing.T) {
	names := parseExplicitNextSkillNames("Goflow next skills: [code-writing, code-audit]\nNext skill: code-audit")
	if len(names) != 2 || names[0] != "code-writing" || names[1] != "code-audit" {
		t.Fatalf("unexpected explicit next skill names: %#v", names)
	}
}

func newWorkflowTestRuntimeWithSuspendedFixTool() *Runtime {
	return newWorkflowTestRuntimeWithSuspendedFixToolAndDefaultAgent(workflowAgentPlanner)
}

func newWorkflowTestRuntimeWithSuspendedFixToolAndDefaultAgent(defaultAgent string) *Runtime {
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
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyConfirm,
			},
			workflowAgentPlanner: {
				Name:             "Planner",
				Provider:         workflowAgentPlanner,
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentFixer: {
				Name:             "Fixer",
				Provider:         workflowAgentFixer,
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyConfirm,
			},
			workflowAgentAuditor: {
				Name:             "Auditor",
				Provider:         workflowAgentAuditor,
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}

	plannerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "plan ready"},
	}}}
	fixerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "need to write files"},
		ToolCalls: []schema.ToolCall{{
			ID:        "call-1",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
		}},
	}, {
		Message: schema.Message{Content: "fix completed"},
	}}}
	auditorLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "audit done"},
	}}}
	chatLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "chat ready"},
	}}}
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}

	return &Runtime{
		cfg:    cfg,
		skills: stubSkillManager{},
		runners: map[string]*AgentRunner{
			"chat":               {id: "chat", profile: cfg.Agents["chat"], llm: chatLLM, executor: NewExecutor(mcpClient)},
			workflowAgentPlanner: {id: workflowAgentPlanner, profile: cfg.Agents[workflowAgentPlanner], llm: plannerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentFixer:   {id: workflowAgentFixer, profile: cfg.Agents[workflowAgentFixer], llm: fixerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentAuditor: {id: workflowAgentAuditor, profile: cfg.Agents[workflowAgentAuditor], llm: auditorLLM, executor: NewExecutor(mcpClient)},
		},
		mcp:       mcpClient,
		session:   session.New(8),
		audit:     runtime.NewAuditLogger(false, false),
		active:    defaultAgent,
		approvals: newApprovalStore(),
	}
}

type repeatedFixSuspensionCapture struct {
	runtime *Runtime
	fixer   *workflowRecordingLLMClient
	auditor *workflowRecordingLLMClient
}

func newWorkflowTestRuntimeWithRepeatedSuspendedFixTools() repeatedFixSuspensionCapture {
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: workflowAgentPlanner,
		Agents: map[string]config.AgentProfile{
			workflowAgentPlanner: {
				Name:             "Planner",
				Provider:         workflowAgentPlanner,
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentFixer: {
				Name:             "Fixer",
				Provider:         workflowAgentFixer,
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyConfirm,
			},
			workflowAgentAuditor: {
				Name:             "Auditor",
				Provider:         workflowAgentAuditor,
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}

	plannerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "plan ready"},
	}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "need to write first file"},
		ToolCalls: []schema.ToolCall{{
			ID:        "call-1",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"calculator.py","content":"print(1)"}`),
		}},
	}, {
		Message: schema.Message{Content: "need to write second file"},
		ToolCalls: []schema.ToolCall{{
			ID:        "call-2",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"test_calculator.py","content":"assert True"}`),
		}},
	}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "audit should not run yet"},
	}}}
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}

	runtimeRef := &Runtime{
		cfg:    cfg,
		skills: stubSkillManager{},
		runners: map[string]*AgentRunner{
			workflowAgentPlanner: {id: workflowAgentPlanner, profile: cfg.Agents[workflowAgentPlanner], llm: plannerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentFixer:   {id: workflowAgentFixer, profile: cfg.Agents[workflowAgentFixer], llm: fixerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentAuditor: {id: workflowAgentAuditor, profile: cfg.Agents[workflowAgentAuditor], llm: auditorLLM, executor: NewExecutor(mcpClient)},
		},
		mcp:       mcpClient,
		session:   session.New(8),
		audit:     runtime.NewAuditLogger(false, false),
		active:    workflowAgentPlanner,
		approvals: newApprovalStore(),
	}
	return repeatedFixSuspensionCapture{runtime: runtimeRef, fixer: fixerLLM, auditor: auditorLLM}
}

type auditPromptCapture struct {
	runtime *Runtime
	auditor *workflowRecordingLLMClient
}

func newWorkflowTestRuntimeWithAuditPromptCapture() auditPromptCapture {
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: workflowAgentPlanner,
		Agents: map[string]config.AgentProfile{
			workflowAgentPlanner: {
				Name:             "Planner",
				Provider:         workflowAgentPlanner,
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentFixer: {
				Name:             "Fixer",
				Provider:         workflowAgentFixer,
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyConfirm,
			},
			workflowAgentAuditor: {
				Name:             "Auditor",
				Provider:         workflowAgentAuditor,
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}

	plannerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "plan ready"},
	}}}
	fixerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "need to write files"},
		ToolCalls: []schema.ToolCall{{
			ID:        "call-1",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
		}},
	}, {
		Message: schema.Message{Content: "fix completed"},
	}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "audit done"},
	}}}
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}

	runtimeRef := &Runtime{
		cfg:    cfg,
		skills: stubSkillManager{},
		runners: map[string]*AgentRunner{
			workflowAgentPlanner: {id: workflowAgentPlanner, profile: cfg.Agents[workflowAgentPlanner], llm: plannerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentFixer:   {id: workflowAgentFixer, profile: cfg.Agents[workflowAgentFixer], llm: fixerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentAuditor: {id: workflowAgentAuditor, profile: cfg.Agents[workflowAgentAuditor], llm: auditorLLM, executor: NewExecutor(mcpClient)},
		},
		mcp:       mcpClient,
		session:   session.New(8),
		audit:     runtime.NewAuditLogger(false, false),
		active:    workflowAgentPlanner,
		approvals: newApprovalStore(),
	}
	return auditPromptCapture{runtime: runtimeRef, auditor: auditorLLM}
}

func TestWorkflowRunnerRunsRegisteredPlanFixAuditWorkflow(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newWorkflowTestRuntimeWithCompletedStages()

	result, err := runtimeRef.WorkflowRunner().Run(ctx, workflowNamePlanFixAudit, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Name != workflowNamePlanFixAudit {
		t.Fatalf("expected workflow %q, got %q", workflowNamePlanFixAudit, result.Name)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %q", result.Status)
	}
	if len(result.CompletedStages) != 3 {
		t.Fatalf("expected 3 completed stages, got %d", len(result.CompletedStages))
	}
	if result.CompletedStages[0].Stage != WorkflowStagePlan {
		t.Fatalf("expected first stage to be plan, got %#v", result.CompletedStages)
	}
	if result.CompletedStages[1].Stage != WorkflowStageFix {
		t.Fatalf("expected second stage to be fix, got %#v", result.CompletedStages)
	}
	if result.CompletedStages[2].Stage != WorkflowStageAudit {
		t.Fatalf("expected third stage to be audit, got %#v", result.CompletedStages)
	}
	if runtimeRef.ActiveAgent() != workflowAgentPlanner {
		t.Fatalf("expected runtime to restore default planner agent, got %q", runtimeRef.ActiveAgent())
	}
	if snapshot := runtimeRef.SessionSnapshot(); snapshot.ActiveAgent != workflowAgentPlanner || snapshot.Mode != "plan" {
		t.Fatalf("expected session to restore planner/plan mode, got %#v", snapshot)
	}
}

func TestWorkflowRunnerRecoversFromInvalidToolArguments(t *testing.T) {
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: workflowAgentPlanner,
		Agents: map[string]config.AgentProfile{
			workflowAgentPlanner: {
				Name:             "Planner",
				Provider:         workflowAgentPlanner,
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentFixer: {
				Name:             "Fixer",
				Provider:         workflowAgentFixer,
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    3,
				AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentAuditor: {
				Name:             "Auditor",
				Provider:         workflowAgentAuditor,
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}

	plannerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan ready"}}}}
	fixerLLM := &scriptedLLMClient{calls: []scriptedLLMCall{
		{err: fmt.Errorf("llm chat: invalid tool arguments: tool call has invalid arguments: unexpected end of JSON input")},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: []byte(`{"path":"a.txt","content":"hello"}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "fix completed after retry"}}},
	}}
	auditorLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit done"}}}}
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}
	runtimeRef := &Runtime{
		cfg:    cfg,
		skills: stubSkillManager{},
		runners: map[string]*AgentRunner{
			workflowAgentPlanner: {id: workflowAgentPlanner, profile: cfg.Agents[workflowAgentPlanner], llm: plannerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentFixer:   {id: workflowAgentFixer, profile: cfg.Agents[workflowAgentFixer], llm: fixerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentAuditor: {id: workflowAgentAuditor, profile: cfg.Agents[workflowAgentAuditor], llm: auditorLLM, executor: NewExecutor(mcpClient)},
		},
		mcp:       mcpClient,
		session:   session.New(8),
		audit:     runtime.NewAuditLogger(false, false),
		active:    workflowAgentPlanner,
		approvals: newApprovalStore(),
	}

	var statuses []string
	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), workflowNamePlanFixAudit, "implement the change", true, func(event schema.StreamEvent) error {
		if event.Type == schema.StreamEventStatus {
			statuses = append(statuses, event.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected workflow recovery from invalid tool arguments, got %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow after recovery, got %#v", result)
	}
	if len(result.CompletedStages) != 3 {
		t.Fatalf("expected completed workflow stages, got %#v", result.CompletedStages)
	}
	if result.CompletedStages[1].Result.Output != "fix completed after retry" {
		t.Fatalf("expected recovered fixer output, got %#v", result.CompletedStages[1].Result)
	}
	if mcpClient.calls != 1 {
		t.Fatalf("expected recovered write tool execution, got %d", mcpClient.calls)
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

func TestWorkflowRunnerAllowsFinalResponseAfterFixerIterationBudget(t *testing.T) {
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: workflowAgentPlanner,
		Agents: map[string]config.AgentProfile{
			workflowAgentPlanner: {
				Name:             "Planner",
				Provider:         workflowAgentPlanner,
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentFixer: {
				Name:             "Fixer",
				Provider:         workflowAgentFixer,
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentAuditor: {
				Name:             "Auditor",
				Provider:         workflowAgentAuditor,
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}

	plannerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan ready"}}}}
	fixerLLM := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: []byte(`{"path":"a.txt","content":"hello"}`)}}}},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-2", Name: "write_file", Arguments: []byte(`{"path":"b.txt","content":"world"}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "fix completed after long run"}}},
	}}
	auditorLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit done"}}}}
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}
	runtimeRef := &Runtime{
		cfg:    cfg,
		skills: stubSkillManager{},
		runners: map[string]*AgentRunner{
			workflowAgentPlanner: {id: workflowAgentPlanner, profile: cfg.Agents[workflowAgentPlanner], llm: plannerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentFixer:   {id: workflowAgentFixer, profile: cfg.Agents[workflowAgentFixer], llm: fixerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentAuditor: {id: workflowAgentAuditor, profile: cfg.Agents[workflowAgentAuditor], llm: auditorLLM, executor: NewExecutor(mcpClient)},
		},
		mcp:       mcpClient,
		session:   session.New(8),
		audit:     runtime.NewAuditLogger(false, false),
		active:    workflowAgentPlanner,
		approvals: newApprovalStore(),
	}

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), workflowNamePlanFixAudit, "expand this project", true, nil)
	if err != nil {
		t.Fatalf("expected workflow to allow final fixer response after iteration budget, got %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow, got %#v", result)
	}
	if result.CompletedStages[1].Result.Output != "fix completed after long run" {
		t.Fatalf("expected final fixer output, got %#v", result.CompletedStages[1].Result)
	}
	if len(result.CompletedStages[1].Result.ToolResults) != 2 {
		t.Fatalf("expected both fixer tool results, got %#v", result.CompletedStages[1].Result.ToolResults)
	}
	if len(fixerLLM.requests) != 3 {
		t.Fatalf("expected extra fixer completion turn, got %d requests", len(fixerLLM.requests))
	}
}

func TestContinueAgentRunSummarizesWorkflowStageWhenFinalTurnStillCallsReadTool(t *testing.T) {
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}}}},
		{response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{ID: "call-2", Name: "read_file", Arguments: []byte(`{"path":"b.txt"}`)}}}},
		{response: schema.ChatResponse{Message: schema.Message{Content: "workflow stage summarized after budget"}}},
	}}
	readTool := schema.Tool{Name: "read_file", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{readTool}}
	cfg := &config.Config{
		WorkspaceRoot: "C:/repo",
		Session:       config.SessionConfig{MaxHistory: 8},
		Agents: map[string]config.AgentProfile{
			workflowAgentPlanner: {
				Name:             "Planner",
				Provider:         workflowAgentPlanner,
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    1,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}
	runtimeRef := &Runtime{
		cfg: cfg,
		runners: map[string]*AgentRunner{
			workflowAgentPlanner: {id: workflowAgentPlanner, profile: cfg.Agents[workflowAgentPlanner], llm: llm, executor: NewExecutor(mcpClient)},
		},
		mcp:       mcpClient,
		session:   session.New(8),
		audit:     runtime.NewAuditLogger(false, false),
		active:    workflowAgentPlanner,
		approvals: newApprovalStore(),
	}

	result, err := continueAgentRunWithSkill(context.Background(), runtimeRef, workflowAgentPlanner, "read files then summarize", nil, schema.ToolCall{}, "", schema.ToolResult{}, nil)
	if err != nil {
		t.Fatalf("expected workflow stage budget summary, got %v", err)
	}
	if result.Output != "workflow stage summarized after budget" {
		t.Fatalf("expected final budget summary output, got %#v", result)
	}
	if len(result.ToolResults) != 1 {
		t.Fatalf("expected only completed first tool result, got %#v", result.ToolResults)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("expected final no-tool summary request, got %d", len(llm.requests))
	}
	if len(llm.requests[2].Tools) != 0 {
		t.Fatalf("expected workflow final budget request to disable tools, got %#v", llm.requests[2].Tools)
	}
	if !strings.Contains(llm.requests[2].Messages[len(llm.requests[2].Messages)-1].Content, "Do not call more tools") {
		t.Fatalf("expected final budget guidance, got %#v", llm.requests[2].Messages)
	}
}

func TestWorkflowRunnerRunRejectsUnknownWorkflow(t *testing.T) {
	runtimeRef := newWorkflowTestRuntimeWithCompletedStages()

	_, err := runtimeRef.WorkflowRunner().Run(context.Background(), "missing", "request", true, nil)
	if err == nil {
		t.Fatal("expected error for unknown workflow")
	}
}

func newWorkflowTestRuntimeWithCompletedStages() *Runtime {
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: workflowAgentPlanner,
		Agents: map[string]config.AgentProfile{
			workflowAgentPlanner: {
				Name:             "Planner",
				Provider:         workflowAgentPlanner,
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentFixer: {
				Name:             "Fixer",
				Provider:         workflowAgentFixer,
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentAuditor: {
				Name:             "Auditor",
				Provider:         workflowAgentAuditor,
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}

	plannerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan ready"}}}}
	fixerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix completed"}}}}
	auditorLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit done"}}}}
	mcpClient := &stubRuntimeMCP{}

	return &Runtime{
		cfg:    cfg,
		skills: stubSkillManager{},
		runners: map[string]*AgentRunner{
			workflowAgentPlanner: {id: workflowAgentPlanner, profile: cfg.Agents[workflowAgentPlanner], llm: plannerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentFixer:   {id: workflowAgentFixer, profile: cfg.Agents[workflowAgentFixer], llm: fixerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentAuditor: {id: workflowAgentAuditor, profile: cfg.Agents[workflowAgentAuditor], llm: auditorLLM, executor: NewExecutor(mcpClient)},
		},
		mcp:       mcpClient,
		session:   session.New(8),
		audit:     runtime.NewAuditLogger(false, false),
		active:    workflowAgentPlanner,
		approvals: newApprovalStore(),
	}
}

func newWorkflowTestRuntimeWithAuditWriteAttempt() *Runtime {
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: workflowAgentPlanner,
		Agents: map[string]config.AgentProfile{
			workflowAgentPlanner: {
				Name:             "Planner",
				Provider:         workflowAgentPlanner,
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentFixer: {
				Name:             "Fixer",
				Provider:         workflowAgentFixer,
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			workflowAgentAuditor: {
				Name:             "Auditor",
				Provider:         workflowAgentAuditor,
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}

	plannerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan ready"}}}}
	fixerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix completed"}}}}
	auditorLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "audit should not write"},
		ToolCalls: []schema.ToolCall{{
			ID:        "call-audit-write",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"audit.txt","content":"should fail"}`),
		}},
	}}}
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}

	return &Runtime{
		cfg:    cfg,
		skills: stubSkillManager{},
		runners: map[string]*AgentRunner{
			workflowAgentPlanner: {id: workflowAgentPlanner, profile: cfg.Agents[workflowAgentPlanner], llm: plannerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentFixer:   {id: workflowAgentFixer, profile: cfg.Agents[workflowAgentFixer], llm: fixerLLM, executor: NewExecutor(mcpClient)},
			workflowAgentAuditor: {id: workflowAgentAuditor, profile: cfg.Agents[workflowAgentAuditor], llm: auditorLLM, executor: NewExecutor(mcpClient)},
		},
		mcp:       mcpClient,
		session:   session.New(8),
		audit:     runtime.NewAuditLogger(false, false),
		active:    workflowAgentPlanner,
		approvals: newApprovalStore(),
	}
}
