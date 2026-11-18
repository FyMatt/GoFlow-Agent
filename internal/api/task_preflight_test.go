package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestTaskPreflightAcceptsRunnableAgentSelection(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:    "file_tools",
		Command: "unused-test-command",
		Enabled: true,
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/runs/preflight", strings.NewReader(`{
		"type":"agent",
		"agent":"planner",
		"skill":"execution-plan",
		"tool":"file_tools/read_file",
		"input":"inspect the project"
	}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected preflight 200, got %d body=%s", response.Code, response.Body.String())
	}
	var result TaskPreflightResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	if !result.Valid || result.Kind != "agent" || result.Target != "planner" {
		t.Fatalf("expected valid Agent preflight, got %#v", result)
	}
	if len(result.ResourcePins) == 0 {
		t.Fatalf("expected resource pins, got %#v", result)
	}
}

func TestTaskPreflightAutoRoutesFocusedCodeTaskToAgent(t *testing.T) {
	server := NewServer(newTaskPreflightAutoRuntime(t))
	result := server.taskPreflight(t.Context(), taskPreflightRequest{
		Type:  "auto",
		Input: "修复 clamp.go 中的边界条件错误并运行相关测试",
	})
	// Domain routing is owned by kit contracts; without a matching kit the
	// kernel keeps the focused task on the active agent instead of guessing.
	if !result.Valid || result.Kind != "agent" || result.Target != "chat" || result.Domain != "" || result.Complexity != "simple" {
		t.Fatalf("expected the unmatched focused task to stay on the active agent, got %#v", result)
	}
}

func TestTaskPreflightAutoRoutesFocusedUIAdjustmentToSoftwareAgent(t *testing.T) {
	server := NewServer(newTaskPreflightAutoRuntime(t))
	result := server.taskPreflight(t.Context(), taskPreflightRequest{
		Type:  "auto",
		Input: "发布留言的按钮太靠近昵称输入框了，需要调整一下",
	})
	if !result.Valid || result.Kind != "agent" || result.Target != "chat" || result.Domain != "" || result.Complexity != "simple" {
		t.Fatalf("expected the unmatched UI adjustment to stay on the active agent, got %#v", result)
	}
}

func TestTaskPreflightAutoOrchestratesOpenEndedUIEnhancement(t *testing.T) {
	server := NewServer(newTaskPreflightAutoRuntime(t))
	result := server.taskPreflight(t.Context(), taskPreflightRequest{
		Type:  "auto",
		Input: "再帮我美化美化这个项目",
	})
	if !result.Valid || result.Kind != "workflow" || result.Target != "plan-implement-audit" || result.Domain != "" || result.Strategy != "workflow" {
		t.Fatalf("expected open-ended UI work to use an orchestrated software Workflow, got %#v", result)
	}
	for _, expected := range []struct {
		stage string
		agent string
	}{
		{stage: "clarify", agent: "planner"},
		{stage: "implement", agent: "fixer"},
		{stage: "verify", agent: "quality-engineer"},
		{stage: "audit", agent: "auditor"},
		{stage: "final-report", agent: "planner"},
	} {
		found := false
		for _, step := range result.Orchestration {
			if step.Stage == expected.stage && step.Agent == expected.agent && step.Purpose != "" && step.ExpectedOutput != "" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing orchestration step %#v in %#v", expected, result.Orchestration)
		}
	}
}

func TestTaskPreflightAutoRoutesComplexEngineeringTaskToWorkflow(t *testing.T) {
	server := NewServer(newTaskPreflightAutoRuntime(t))
	result := server.taskPreflight(t.Context(), taskPreflightRequest{
		Type:  "auto",
		Input: "跨多个模块实现并发缓存和数据迁移，补充测试与静态检查，完成部署验证并提供回滚方案",
	})
	if !result.Valid || result.Kind != "workflow" || result.Target != "task-decomposition-plan" || result.Domain != "" || result.Complexity != "complex" {
		t.Fatalf("expected a complex task without a kit match to use the generic decomposition workflow, got %#v", result)
	}
}

func TestTaskPreflightAutoKeepsGeneralQuestionOnDirectAgent(t *testing.T) {
	server := NewServer(newTaskPreflightAutoRuntime(t))
	result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "解释一下今天的任务安排"})
	if !result.Valid || result.Kind != "agent" || result.Complexity != "simple" {
		t.Fatalf("expected general request to use direct Agent, got %#v", result)
	}
}

func TestTaskRecommendationRoutesFocusedOperationsToAgent(t *testing.T) {
	result := recommendLocalTaskTarget("检查远程主机的磁盘空间、端口占用和服务状态", "chat")
	if result.Kind != "agent" || result.Target != "chat" || result.Domain != "" || result.Complexity != "simple" {
		t.Fatalf("expected focused operations task to use operations Agent, got %#v", result)
	}
}

func TestTaskRecommendationRoutesComplexOperationsToWorkflow(t *testing.T) {
	result := recommendLocalTaskTarget("在维护窗口内批量检查多台服务器，审批后修改配置并重启服务，验证结果，失败时执行回滚", "chat")
	if result.Kind != "workflow" || result.Target != "task-decomposition-plan" || result.Domain != "" || result.Complexity != "complex" {
		t.Fatalf("expected complex operations task to use controlled workflow, got %#v", result)
	}
}

// Domain routing belongs to kit contracts. Without a matching kit the kernel
// must not guess a domain: complex work goes to the generic decomposition
// workflow and focused work stays with the active agent.
func TestTaskRecommendationFallsBackWithoutDomainGuessing(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		kind       string
		target     string
		complexity string
	}{
		{name: "security", input: "在已授权仓库中完成跨模块安全审计并修复认证绕过，补充公开和隐藏回归测试，输出审计报告与回滚方案", kind: "workflow", target: "task-decomposition-plan", complexity: "complex"},
		{name: "active-security", input: "在明确授权范围和时间窗口内执行渗透测试与主动探测，记录证据、停止条件和复测结论", kind: "workflow", target: "task-decomposition-plan", complexity: "complex"},
		{name: "research", input: "核验多个官方来源和原始论文，保留冲突结论与访问时间，独立复核后交付 Markdown、JSON 和 CSV", kind: "workflow", target: "task-decomposition-plan", complexity: "complex"},
		{name: "documentation", input: "读取 DOCX 模板并保留页眉页脚和表格格式，合并 Excel 数据生成 PPTX，同时导出 PDF 并完成逐页视觉复核", kind: "workflow", target: "task-decomposition-plan", complexity: "complex"},
		{name: "focused-document", input: "读取并修改需求文档中的负责人字段", kind: "agent", target: "chat", complexity: "simple"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := recommendLocalTaskTarget(test.input, "chat")
			if result.Kind != test.kind || result.Target != test.target || result.Domain != "" || result.Complexity != test.complexity {
				t.Fatalf("unexpected recommendation: %#v", result)
			}
			if result.Profile.EstimatedStageCount < 1 {
				t.Fatalf("expected explainable complexity profile, got %#v", result.Profile)
			}
		})
	}
}

func TestTaskComplexityProfileRequiresStructuralSignals(t *testing.T) {
	simple := analyzeTaskComplexity("这是一个复杂任务，请解释当前代码")
	if simple.Level != "simple" {
		t.Fatalf("the word complex alone must not force Workflow routing: %#v", simple)
	}
	complex := analyzeTaskComplexity("跨模块迁移缓存状态机；修改 API 与持久层；补充单元测试和集成测试；运行静态检查；生成发布包；失败时回滚")
	if complex.Level != "complex" || complex.Score < 6 || len(complex.VerificationGates) < 2 || len(complex.RiskControls) == 0 {
		t.Fatalf("expected structural complex-task evidence, got %#v", complex)
	}
}

func newTaskPreflightAutoRuntime(t *testing.T) *agent.Runtime {
	t.Helper()
	return newReferenceWorkflowFixtureRuntime(t, true)
}

func TestTaskPreflightExposesLocalTargetOnlyForHostScopedResource(t *testing.T) {
	hostAgent := config.AgentProfile{
		Name:             "Host Inspector",
		TargetScope:      "host",
		Provider:         "primary",
		Mode:             "audit",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
		ToolPolicy:       config.ToolPolicyAllow,
	}
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesMCPCostRiskPolicyAndExtraAgents(t, session.New(8), "", nil, nil, config.CostControlConfig{}, config.ToolRiskPolicyConfig{}, map[string]config.AgentProfile{"host-inspector": hostAgent})
	server := NewServer(runtimeRef)
	result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "agent", Agent: "host-inspector", Input: "inspect local host"})
	if !result.Valid || !result.RequiresExecutionTarget {
		t.Fatalf("expected valid target-scoped preflight, got %#v", result)
	}
	if result.ExecutionTarget.ID != session.LocalTargetID || result.ExecutionTarget.Kind != "local" {
		t.Fatalf("expected local execution Target, got %#v", result.ExecutionTarget)
	}

	ordinary := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "agent", Agent: "planner", Input: "inspect project"})
	if !ordinary.Valid || ordinary.RequiresExecutionTarget {
		t.Fatalf("ordinary workspace task must not surface Target, got %#v", ordinary)
	}

	toolRuntime := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:        "host_tools",
		TargetScope: "host",
		Command:     "unused-test-command",
		Enabled:     true,
	}})
	if !NewServer(toolRuntime).toolRequiresExecutionTarget("host_tools/host_facts") {
		t.Fatal("host-scoped MCP tool must require the local execution Target")
	}
}

func TestTaskStartRejectsUnknownExecutionTargetBeforeCreation(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{
		"input":"inspect remote host",
		"agent":"planner",
		"target_id":"server-a",
		"background":true
	}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected unsupported Target rejection, got %d body=%s", response.Code, response.Body.String())
	}
	var result TaskPreflightResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode Target rejection: %v", err)
	}
	if result.Valid || !preflightIssueCodeExists(result.Issues, "execution_target_not_found") {
		t.Fatalf("expected execution_target_not_found, got %#v", result)
	}
	if runs := runtimeRef.SessionSnapshot().AgentRuns; len(runs) != 0 {
		t.Fatalf("unsupported Target must not create a task, got %#v", runs)
	}
}

func TestAgentRunPreflightRejectsDeniedToolBeforeTaskCreation(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:    "stub",
		Command: "unused-test-command",
		Enabled: true,
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{
		"input":"write a file",
		"agent":"planner",
		"tool":"stub/write_file",
		"background":true
	}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected preflight 422, got %d body=%s", response.Code, response.Body.String())
	}
	var result TaskPreflightResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode rejection: %v", err)
	}
	if result.Valid || !preflightIssueCodeExists(result.Issues, "tool_not_allowed") {
		t.Fatalf("expected denied-tool issue, got %#v", result)
	}
	if runs := runtimeRef.SessionSnapshot().AgentRuns; len(runs) != 0 {
		t.Fatalf("preflight rejection must not create a task, got %#v", runs)
	}
}

func TestWorkflowRunPreflightRejectsMissingLiveExecutionMetadataBeforeTaskCreation(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	runner := runtimeRef.WorkflowRunner()
	doc := agent.WorkflowGraphDocument{
		Name: "live-change",
		Stages: []agent.WorkflowGraphStageDocument{{
			Name:  "change",
			Agent: "fixer",
			Skill: "code-writing",
			Execution: agent.WorkflowGraphExecutionContractDocument{
				Mode:             "live",
				RiskLevel:        "high",
				RequiresRollback: true,
			},
		}},
	}
	if err := runner.SaveWorkflowGraphDocument(doc.Name, doc); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/live-change", strings.NewReader(`{
		"input":"apply the approved change",
		"background":true
	}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected workflow preflight 422, got %d body=%s", response.Code, response.Body.String())
	}
	var result TaskPreflightResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode rejection: %v", err)
	}
	if result.Valid || !preflightIssueCodeExists(result.Issues, "workflow_startup_contract") {
		t.Fatalf("expected startup contract issue, got %#v", result)
	}
	if runs := runtimeRef.SessionSnapshot().WorkflowRuns; len(runs) != 0 {
		t.Fatalf("preflight rejection must not create a workflow task, got %#v", runs)
	}
}

func TestOptionalSkillToolMCPFailureRemainsWarning(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	issues := server.mcpServerPreflightIssues(config.MCPServerRef{
		Name:      "optional_notes",
		Enabled:   true,
		Optional:  true,
		Isolation: "process_group",
	}, false, "notes", map[string]string{"optional_notes": "degraded: dependency missing"}, "warning")
	if len(issues) == 0 || taskPreflightHasErrors(issues) {
		t.Fatalf("optional tool failure should warn without blocking, got %#v", issues)
	}
}

func preflightIssueCodeExists(issues []TaskPreflightIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
