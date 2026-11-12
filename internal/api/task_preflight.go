package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/executiontarget"
	"github.com/FyMatt/GoFlow-Agent/internal/llm"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type taskPreflightRequest struct {
	Type     string `json:"type"`
	Agent    string `json:"agent,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Skill    string `json:"skill,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Input    string `json:"input,omitempty"`
	Approve  bool   `json:"approve,omitempty"`
	TargetID string `json:"target_id,omitempty"`
}

type TaskPreflightResult struct {
	Valid                   bool                    `json:"valid"`
	Kind                    string                  `json:"kind"`
	Target                  string                  `json:"target"`
	Domain                  string                  `json:"domain,omitempty"`
	Complexity              string                  `json:"complexity,omitempty"`
	ComplexityProfile       TaskComplexityProfile   `json:"complexity_profile,omitempty"`
	Reason                  string                  `json:"reason,omitempty"`
	ExecutionTarget         executionTargetSummary  `json:"execution_target"`
	RequiresExecutionTarget bool                    `json:"requires_execution_target"`
	ResourcePins            []session.ResourcePin   `json:"resource_pins,omitempty"`
	Strategy                string                  `json:"strategy,omitempty"`
	Orchestration           []TaskOrchestrationStep `json:"orchestration,omitempty"`
	Issues                  []TaskPreflightIssue    `json:"issues,omitempty"`
}

type TaskOrchestrationStep struct {
	Stage          string   `json:"stage"`
	NodeType       string   `json:"node_type,omitempty"`
	Agent          string   `json:"agent,omitempty"`
	Skill          string   `json:"skill,omitempty"`
	Purpose        string   `json:"purpose,omitempty"`
	ExpectedOutput string   `json:"expected_output,omitempty"`
	Tools          []string `json:"tools,omitempty"`
}

type TaskPreflightIssue struct {
	Severity       string `json:"severity"`
	Code           string `json:"code"`
	ResourceKind   string `json:"resource_kind,omitempty"`
	ResourceName   string `json:"resource_name,omitempty"`
	Stage          string `json:"stage,omitempty"`
	Field          string `json:"field,omitempty"`
	Message        string `json:"message"`
	Recommendation string `json:"recommendation,omitempty"`
}

type taskPreflightToolCatalog struct {
	tools  []schema.Tool
	names  []string
	health map[string]string
}

func (s *Server) handleTaskPreflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req taskPreflightRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	writeJSON(w, s.taskPreflight(r.Context(), req))
}

func decodeJSONBody(r *http.Request, value any) error {
	if r == nil || r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	return json.NewDecoder(r.Body).Decode(value)
}

func (s *Server) taskPreflight(ctx context.Context, req taskPreflightRequest) TaskPreflightResult {
	kind := strings.ToLower(strings.TrimSpace(req.Type))
	if kind == "" {
		if strings.TrimSpace(req.Workflow) != "" {
			kind = "workflow"
		} else {
			kind = "agent"
		}
	}
	result := TaskPreflightResult{Kind: kind, ExecutionTarget: localExecutionTarget()}
	if s == nil || s.runtime == nil {
		result.Issues = []TaskPreflightIssue{preflightIssue("error", "runtime_unavailable", "runtime is not configured", "start GoFlow with a valid runtime configuration")}
		return result
	}
	if strings.TrimSpace(req.Input) == "" {
		result.Issues = append(result.Issues, preflightIssue("error", "input_required", "task input is required", "describe the task before starting it"))
	}
	switch kind {
	case "auto":
		return s.autoKitTaskPreflight(ctx, req)
	case "agent":
		result.Target = strings.TrimSpace(req.Agent)
		if result.Target == "" {
			result.Target = strings.TrimSpace(s.runtime.ActiveAgent())
		}
		result.ResourcePins = s.agentTaskResourcePins(result.Target, req.Skill, req.Tool)
		result.RequiresExecutionTarget = s.agentTaskRequiresExecutionTarget(result.Target, req.Skill, req.Tool)
		result.Issues = append(result.Issues, s.agentTaskPreflightIssues(ctx, result.Target, req.Skill, req.Tool)...)
	case "workflow":
		result.Target = strings.TrimSpace(req.Workflow)
		result.ResourcePins = s.workflowTaskResourcePins(result.Target)
		result.RequiresExecutionTarget = s.workflowTaskRequiresExecutionTarget(result.Target)
		result.Issues = append(result.Issues, s.workflowTaskPreflightIssues(ctx, result.Target, req.Approve)...)
	default:
		result.Target = strings.TrimSpace(req.Agent)
		result.Issues = append(result.Issues, preflightIssue("error", "task_type_invalid", fmt.Sprintf("unsupported task type %q", req.Type), "use agent or workflow"))
	}
	s.resolveTaskExecutionTarget(req.TargetID, &result)
	result.Strategy = "direct"
	if result.Kind == "workflow" {
		result.Strategy = "workflow"
	}
	result.Orchestration = s.taskPreflightOrchestration(result.Kind, result.Target, result.Domain)
	result.Issues = normalizeTaskPreflightIssues(result.Issues)
	result.Valid = !taskPreflightHasErrors(result.Issues)
	return result
}

type localTaskRecommendation struct {
	Kind       string
	Target     string
	Domain     string
	Complexity string
	Reason     string
	Profile    TaskComplexityProfile
}

func recommendLocalTaskTarget(input, activeAgent string) localTaskRecommendation {
	profile := analyzeTaskComplexity(input)
	domain := ""
	if profile.Level == "complex" {
		target := complexWorkflowForTask(profile, input)
		return localTaskRecommendation{
			Kind:       "workflow",
			Target:     target,
			Domain:     domain,
			Complexity: profile.Level,
			Reason:     complexTaskRoutingReason(profile, target),
			Profile:    profile,
		}
	}
	if plannedWorkflowForTask(profile, input) != "" {
		target := plannedWorkflowForTask(profile, input)
		return localTaskRecommendation{
			Kind:       "workflow",
			Target:     target,
			Domain:     domain,
			Complexity: profile.Level,
			Reason:     "需求属于开放式界面改进；先由规划智能体明确视觉目标和验收标准，再由实现智能体修改，质量智能体验证浏览器效果，最后由审计智能体复核交付。",
			Profile:    profile,
		}
	}
	target := directAgentForDomain(domain, activeAgent)
	return localTaskRecommendation{
		Kind:       "agent",
		Target:     target,
		Domain:     domain,
		Complexity: profile.Level,
		Reason:     "任务范围集中，交付物、风险控制和验证步骤可在一次直接执行中闭环。",
		Profile:    profile,
	}
}

func plannedWorkflowForTask(profile TaskComplexityProfile, input string) string {
	_ = profile
	normalized := strings.ToLower(strings.TrimSpace(input))
	if containsAnyTerm(normalized, []string{"美化", "改版", "重新设计", "视觉优化", "界面优化", "样式优化", "整体优化", "优化一下这个项目", "polish the ui", "redesign", "visual refresh"}) {
		return "plan-implement-audit"
	}
	return ""
}

func (s *Server) taskPreflightOrchestration(kind, target, domain string) []TaskOrchestrationStep {
	target = strings.TrimSpace(target)
	if target == "" || s == nil || s.runtime == nil {
		return nil
	}
	if kind != "workflow" {
		skill := ""
		if domain == "software" {
			skill = "code-writing"
		}
		return []TaskOrchestrationStep{{
			Stage:          "execute",
			NodeType:       "agent",
			Agent:          target,
			Skill:          skill,
			Purpose:        "由领域智能体直接完成聚焦任务。",
			ExpectedOutput: "真实执行结果、验证证据和剩余风险。",
		}}
	}
	doc, err := s.runtime.WorkflowRunner().LoadRunnableWorkflowGraphDocument(target)
	if err != nil {
		return nil
	}
	requirements := make(map[string]agent.WorkflowGraphStageRequirement)
	for _, requirement := range s.runtime.WorkflowRunner().WorkflowGraphStageRequirements(doc) {
		requirements[requirement.Stage] = requirement
	}
	steps := make([]TaskOrchestrationStep, 0, len(doc.Stages))
	for _, stage := range doc.Stages {
		nodeType := strings.ToLower(strings.TrimSpace(stage.NodeType))
		if nodeType == "start" || nodeType == "end" {
			continue
		}
		requirement := requirements[stage.Name]
		steps = append(steps, TaskOrchestrationStep{
			Stage:          stage.Name,
			NodeType:       nodeType,
			Agent:          firstWorkflowRunQueryValue(requirement.Agent, stage.Agent),
			Skill:          firstWorkflowRunQueryValue(requirement.Skill, stage.Skill),
			Purpose:        taskOrchestrationPurpose(stage.Name, nodeType),
			ExpectedOutput: taskOrchestrationExpectedOutput(stage.Name, nodeType),
			Tools:          append([]string(nil), requirement.Tools...),
		})
	}
	return steps
}

func taskOrchestrationPurpose(stage, nodeType string) string {
	name := strings.ToLower(strings.TrimSpace(stage))
	switch {
	case strings.Contains(name, "clarify") || strings.Contains(name, "intake"):
		return "澄清原始需求、范围、约束和验收条件。"
	case strings.Contains(name, "plan"):
		return "把需求转成可执行步骤、目标文件和验证方案。"
	case strings.Contains(name, "implement") || strings.Contains(name, "fix") || strings.Contains(name, "repair"):
		return "依据计划修改工作区，并记录真实文件差异。"
	case strings.Contains(name, "verify") || strings.Contains(name, "test"):
		return "运行项目检查；界面任务同时进行浏览器视觉验证。"
	case strings.Contains(name, "audit"):
		return "独立复核需求覆盖、回归风险和证据完整性。"
	case strings.Contains(name, "quality") || nodeType == "quality_gate":
		return "根据实现、验证和审计证据决定通过、警告或返工。"
	case strings.Contains(name, "review") || nodeType == "checkpoint":
		return "存在警告时请求人工复核。"
	case strings.Contains(name, "report") || strings.Contains(name, "handoff") || strings.Contains(name, "summary"):
		return "汇总所有阶段证据，生成最终交付说明。"
	default:
		return "执行工作流阶段并保留可追溯产出。"
	}
}

func taskOrchestrationExpectedOutput(stage, nodeType string) string {
	name := strings.ToLower(strings.TrimSpace(stage))
	switch {
	case strings.Contains(name, "clarify") || strings.Contains(name, "intake"):
		return "任务契约与验收标准"
	case strings.Contains(name, "plan"):
		return "实施计划、目标文件与验证清单"
	case strings.Contains(name, "implement") || strings.Contains(name, "fix") || strings.Contains(name, "repair"):
		return "文件变更、Diff 与实现报告"
	case strings.Contains(name, "verify") || strings.Contains(name, "test"):
		return "测试、运行或浏览器验证证据"
	case strings.Contains(name, "audit"):
		return "审计结论、缺口与风险"
	case strings.Contains(name, "quality") || nodeType == "quality_gate":
		return "质量门禁结论"
	case strings.Contains(name, "report") || strings.Contains(name, "handoff") || strings.Contains(name, "summary"):
		return "最终状态、交付内容与后续事项"
	default:
		return "阶段产物"
	}
}

// directAgentForDomain deliberately has no per-domain table: domains are
// declared by kits, and an unmatched task stays with the active agent instead
// of being guessed from keywords in the kernel.
func directAgentForDomain(domain, activeAgent string) string {
	_ = domain
	return firstWorkflowRunQueryValue(strings.TrimSpace(activeAgent), "chat")
}

// complexWorkflowForTask returns the generic decomposition workflow. Domain
// routing belongs to kit contracts, so the kernel no longer maps task keywords
// to per-domain workflows.
func complexWorkflowForTask(profile TaskComplexityProfile, input string) string {
	_ = profile
	_ = input
	return "task-decomposition-plan"
}

func complexTaskRoutingReason(profile TaskComplexityProfile, target string) string {
	signals := strings.Join(profile.Signals, "、")
	if signals == "" {
		signals = "多阶段工程交付"
	}
	return fmt.Sprintf("复杂度评分 %d，检测到%s；使用可恢复工作流 %s，并要求真实产物和独立验证。", profile.Score, signals, target)
}

func taskKeywordScore(input string, keywords []string) int {
	score := 0
	for _, keyword := range keywords {
		if strings.Contains(input, strings.ToLower(strings.TrimSpace(keyword))) {
			score++
		}
	}
	return score
}

func localExecutionTarget() executionTargetSummary {
	return executionTargetSummary{ID: session.LocalTargetID, Kind: "local", DisplayName: "本机", Enabled: true}
}

func (s *Server) resolveTaskExecutionTarget(requested string, result *TaskPreflightResult) {
	if result == nil {
		return
	}
	targetID := strings.ToLower(strings.TrimSpace(requested))
	if targetID == "" {
		targetID = session.LocalTargetID
	}
	store := s.executionTargetStore()
	if store == nil {
		result.Issues = append(result.Issues, preflightIssue("error", "execution_target_store_unavailable", "执行目标存储不可用", "请使用有效的数据目录重新启动 GoFlow"))
		return
	}
	target, ok, err := store.Get(targetID)
	if err != nil {
		result.Issues = append(result.Issues, preflightIssue("error", "execution_target_load_failed", fmt.Sprintf("加载执行目标失败：%v", err), "请修复或删除无效的执行目标配置"))
		return
	}
	if !ok {
		result.Issues = append(result.Issues, preflightIssue("error", "execution_target_not_found", fmt.Sprintf("未找到执行目标 %q", requested), "请选择已有执行目标，或在设置页创建新目标"))
		return
	}
	result.ExecutionTarget = executionTargetSummaryFrom(target)
	if !target.Enabled {
		result.Issues = append(result.Issues, preflightIssue("error", "execution_target_disabled", fmt.Sprintf("执行目标 %q 已停用", target.ID), "请先启用该执行目标，再启动任务"))
	}
	if target.Kind == executiontarget.KindSSH && !result.RequiresExecutionTarget {
		result.Issues = append(result.Issues, preflightIssue("error", "execution_target_not_applicable", fmt.Sprintf("资源 %q 不支持远程执行目标", result.Target), "请改用本机目标，或选择运维类资源"))
	}
}

func normalizeTargetScope(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "host") {
		return "host"
	}
	return ""
}

func (s *Server) agentTaskRequiresExecutionTarget(agentID, skillName, toolName string) bool {
	if s == nil || s.runtime == nil {
		return false
	}
	if profile, ok := s.runtime.Profile(strings.TrimSpace(agentID)); ok && normalizeTargetScope(profile.TargetScope) == "host" {
		return true
	}
	if skill, ok := s.runtimeSkillByName(strings.TrimSpace(skillName)); ok {
		if normalizeTargetScope(skill.Metadata["target_scope"]) == "host" {
			return true
		}
		for _, dependency := range skill.Tools {
			if s.toolRequiresExecutionTarget(dependency.Name) {
				return true
			}
		}
	}
	return s.toolRequiresExecutionTarget(toolName)
}

func (s *Server) workflowTaskRequiresExecutionTarget(workflowName string) bool {
	if s == nil || s.runtime == nil || s.runtime.WorkflowRunner() == nil {
		return false
	}
	doc, err := s.runtime.WorkflowRunner().LoadRunnableWorkflowGraphDocument(strings.TrimSpace(workflowName))
	if err != nil {
		return false
	}
	for _, requirement := range s.runtime.WorkflowRunner().WorkflowGraphStageRequirements(doc) {
		if s.agentTaskRequiresExecutionTarget(requirement.Agent, requirement.Skill, "") {
			return true
		}
		for _, toolName := range requirement.Tools {
			if s.toolRequiresExecutionTarget(toolName) {
				return true
			}
		}
	}
	return false
}

func (s *Server) toolRequiresExecutionTarget(toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || s == nil || s.runtime == nil {
		return false
	}
	server, ok := s.runtimeMCPServerForTool(toolName)
	return ok && normalizeTargetScope(server.TargetScope) == "host"
}

func (s *Server) agentTaskPreflightIssues(ctx context.Context, agentID, skillName, toolName string) []TaskPreflightIssue {
	issues := make([]TaskPreflightIssue, 0)
	profile, ok := s.runtime.Profile(agentID)
	if !ok {
		return []TaskPreflightIssue{resourcePreflightIssue("error", "agent_missing", "agent", agentID, fmt.Sprintf("Agent %s is not configured", fallbackPreflightValue(agentID, "(empty)")), "select an existing Agent or repair the Agent resource")}
	}
	issues = append(issues, s.providerPreflightIssues(profile.Provider, nil)...)
	skillName = strings.TrimSpace(skillName)
	toolName = strings.TrimSpace(toolName)
	catalog := taskPreflightToolCatalog{}
	if skillName != "" || toolName != "" {
		catalog = s.taskPreflightToolCatalog(ctx, &issues)
	}
	if skillName != "" {
		skill, found := s.runtimeSkillByName(skillName)
		if !found {
			issues = append(issues, resourcePreflightIssue("error", "skill_missing", "skill", skillName, fmt.Sprintf("Skill %s is not available", skillName), "select an existing Skill or repair its SKILL.md"))
		} else {
			if preferred := strings.TrimSpace(skill.PreferredAgent); preferred != "" && !strings.EqualFold(preferred, agentID) {
				issue := resourcePreflightIssue("warning", "skill_agent_mismatch", "skill", skill.Name, fmt.Sprintf("Skill %s normally uses Agent %s, but %s was selected", skill.Name, preferred, agentID), "keep the explicit Agent when intentional, or select the preferred Agent")
				issues = append(issues, issue)
			}
			issues = append(issues, s.skillToolPreflightIssues(catalog, profile, skill, "")...)
		}
	}
	if toolName != "" {
		issues = append(issues, s.toolPreflightIssues(catalog, profile, toolName, true, "")...)
	}
	return issues
}

func (s *Server) workflowTaskPreflightIssues(ctx context.Context, workflowName string, approved bool) []TaskPreflightIssue {
	workflowName = strings.TrimSpace(workflowName)
	if workflowName == "" {
		return []TaskPreflightIssue{resourcePreflightIssue("error", "workflow_required", "workflow", "", "a Workflow must be selected", "select a Workflow before starting the task")}
	}
	doc, err := s.runtime.WorkflowRunner().LoadRunnableWorkflowGraphDocument(workflowName)
	if err != nil {
		executor, ok := s.workflowExecutor(workflowName)
		if !ok {
			return []TaskPreflightIssue{resourcePreflightIssue("error", "workflow_missing", "workflow", workflowName, fmt.Sprintf("Workflow %s is not available", workflowName), "select an existing Workflow or restore its resource")}
		}
		if !executor.Valid {
			message := strings.TrimSpace(executor.Error)
			if message == "" {
				message = fmt.Sprintf("Workflow %s is not runnable", workflowName)
			}
			return []TaskPreflightIssue{resourcePreflightIssue("error", "workflow_invalid", "workflow", workflowName, message, "open the Workflow editor and fix its validation errors")}
		}
		return s.builtInWorkflowPreflightIssues(ctx, workflowName)
	}
	issues := make([]TaskPreflightIssue, 0)
	validation := s.runtime.WorkflowRunner().ValidateWorkflowGraphDocument(workflowName, doc)
	for _, item := range validation.Issues {
		severity := strings.ToLower(strings.TrimSpace(item.Level))
		if severity == "warn" {
			severity = "warning"
		}
		issue := TaskPreflightIssue{Severity: severity, Code: "workflow_graph_" + severity, ResourceKind: "workflow", ResourceName: workflowName, Stage: item.Stage, Field: item.Field, Message: item.Message}
		if severity == "error" {
			issue.Recommendation = "open the Workflow editor and fix this field before running"
		} else {
			issue.Recommendation = "review this workflow quality warning when it affects the current task"
		}
		issues = append(issues, issue)
	}
	for _, item := range s.runtime.WorkflowRunner().ValidateWorkflowGraphStartup(workflowName, doc, approved) {
		issues = append(issues, TaskPreflightIssue{Severity: item.Level, Code: "workflow_startup_contract", ResourceKind: "workflow", ResourceName: workflowName, Stage: item.Stage, Field: item.Field, Message: item.Message, Recommendation: "complete the stage execution, approval, recovery, or input metadata before running"})
	}
	catalog := s.taskPreflightToolCatalog(ctx, &issues)
	for _, requirement := range s.runtime.WorkflowRunner().WorkflowGraphStageRequirements(doc) {
		if requirement.SubWorkflow != "" {
			if child, found := s.workflowExecutor(requirement.SubWorkflow); !found || !child.Valid {
				issues = append(issues, TaskPreflightIssue{Severity: "error", Code: "sub_workflow_unavailable", ResourceKind: "workflow", ResourceName: requirement.SubWorkflow, Stage: requirement.Stage, Field: "params.workflow", Message: fmt.Sprintf("stage %s references unavailable sub-workflow %s", requirement.Stage, requirement.SubWorkflow), Recommendation: "select a runnable sub-workflow or repair the referenced Workflow"})
			}
			continue
		}
		if !requirement.Executable {
			continue
		}
		profile, found := s.runtime.Profile(requirement.Agent)
		if !found {
			issues = append(issues, TaskPreflightIssue{Severity: "error", Code: "stage_agent_missing", ResourceKind: "agent", ResourceName: requirement.Agent, Stage: requirement.Stage, Field: "agent", Message: fmt.Sprintf("stage %s references missing Agent %s", requirement.Stage, requirement.Agent), Recommendation: "select an existing Agent for this stage"})
			continue
		}
		issues = append(issues, addPreflightStage(s.providerPreflightIssues(profile.Provider, nil), requirement.Stage)...)
		if requirement.Provider != "" {
			issues = append(issues, addPreflightStage(s.providerPreflightIssues(requirement.Provider, nil), requirement.Stage)...)
		}
		if requirement.Skill != "" {
			skill, found := s.runtimeSkillByName(requirement.Skill)
			if !found {
				issues = append(issues, TaskPreflightIssue{Severity: "error", Code: "stage_skill_missing", ResourceKind: "skill", ResourceName: requirement.Skill, Stage: requirement.Stage, Field: "skill", Message: fmt.Sprintf("stage %s references missing Skill %s", requirement.Stage, requirement.Skill), Recommendation: "select an existing Skill for this stage"})
			} else if !requirement.DisableTools {
				issues = append(issues, s.skillToolPreflightIssues(catalog, profile, skill, requirement.Stage)...)
			}
		}
		for _, toolName := range requirement.Tools {
			issues = append(issues, s.toolPreflightIssues(catalog, profile, toolName, true, requirement.Stage)...)
		}
	}
	return issues
}

func (s *Server) builtInWorkflowPreflightIssues(ctx context.Context, workflowName string) []TaskPreflightIssue {
	var agents, skills []string
	switch strings.ToLower(strings.TrimSpace(workflowName)) {
	case "plan-fix-audit":
		agents = []string{"planner", "fixer", "auditor"}
		skills = []string{"execution-plan", "code-writing", "code-audit"}
	case "skill-chain":
		agents = []string{"chat"}
		for _, skill := range s.runtime.SkillList() {
			skills = append(skills, skill.Name)
			if strings.TrimSpace(skill.PreferredAgent) != "" {
				agents = append(agents, skill.PreferredAgent)
			}
		}
	default:
		return []TaskPreflightIssue{resourcePreflightIssue("error", "workflow_load_failed", "workflow", workflowName, fmt.Sprintf("Workflow %s could not be loaded", workflowName), "repair or recreate the Workflow resource")}
	}
	issues := make([]TaskPreflightIssue, 0)
	catalog := s.taskPreflightToolCatalog(ctx, &issues)
	for _, agentID := range agents {
		profile, ok := s.runtime.Profile(agentID)
		if !ok {
			issues = append(issues, resourcePreflightIssue("error", "workflow_agent_missing", "agent", agentID, fmt.Sprintf("built-in Workflow %s requires Agent %s", workflowName, agentID), "restore the required Agent resource"))
			continue
		}
		issues = append(issues, s.providerPreflightIssues(profile.Provider, nil)...)
	}
	for _, skillName := range skills {
		skill, ok := s.runtimeSkillByName(skillName)
		if !ok {
			issues = append(issues, resourcePreflightIssue("error", "workflow_skill_missing", "skill", skillName, fmt.Sprintf("built-in Workflow %s requires Skill %s", workflowName, skillName), "restore the required Skill resource"))
			continue
		}
		profile, ok := s.runtime.Profile(skill.PreferredAgent)
		if !ok {
			profile, _ = s.runtime.Profile("chat")
		}
		issues = append(issues, s.skillToolPreflightIssues(catalog, profile, skill, "")...)
	}
	return issues
}

func (s *Server) workflowExecutor(name string) (agent.WorkflowExecutorOption, bool) {
	for _, item := range s.runtime.WorkflowRunner().WorkflowExecutors() {
		if strings.EqualFold(strings.TrimSpace(item.Name), strings.TrimSpace(name)) {
			return item, true
		}
	}
	return agent.WorkflowExecutorOption{}, false
}

func (s *Server) providerPreflightIssues(name string, seen map[string]bool) []TaskPreflightIssue {
	name = strings.TrimSpace(name)
	if name == "" {
		return []TaskPreflightIssue{resourcePreflightIssue("error", "provider_required", "provider", "", "no Provider is selected", "select a configured Provider")}
	}
	if seen == nil {
		seen = make(map[string]bool)
	}
	key := strings.ToLower(name)
	if seen[key] {
		return []TaskPreflightIssue{resourcePreflightIssue("error", "provider_fallback_cycle", "provider", name, fmt.Sprintf("Provider fallback chain contains a cycle at %s", name), "remove the cyclic fallback reference")}
	}
	seen[key] = true
	provider, ok := s.runtime.Provider(name)
	if !ok {
		if s.runtime.ProviderAvailable(name) {
			delete(seen, key)
			return nil
		}
		return []TaskPreflightIssue{resourcePreflightIssue("error", "provider_missing", "provider", name, fmt.Sprintf("Provider %s is not configured", name), "configure this Provider or select another Agent")}
	}
	issues := make([]TaskPreflightIssue, 0)
	if strings.TrimSpace(provider.BaseURL) == "" {
		issues = append(issues, resourcePreflightIssue("error", "provider_base_url_missing", "provider", name, fmt.Sprintf("Provider %s has no base URL", name), "set the Provider API endpoint in Settings"))
	}
	if strings.TrimSpace(provider.Model) == "" {
		issues = append(issues, resourcePreflightIssue("error", "provider_model_missing", "provider", name, fmt.Sprintf("Provider %s has no model", name), "set the model id in Settings"))
	}
	if strings.TrimSpace(provider.APIKey) == "" {
		issues = append(issues, resourcePreflightIssue("warning", "provider_api_key_missing", "provider", name, fmt.Sprintf("Provider %s has no API key", name), "set an API key when this endpoint requires authentication"))
	}
	if s.runtime.LocalOnly() && !llm.IsLocalBaseURL(provider.BaseURL) {
		issues = append(issues, resourcePreflightIssue("error", "provider_blocked_local_only", "provider", name, fmt.Sprintf("local-only workspace blocks Provider %s at %s", name, provider.BaseURL), "select a loopback or Unix-socket Provider, or change the workspace data-egress policy"))
	}
	if fallback := strings.TrimSpace(provider.FallbackProvider); fallback != "" {
		issues = append(issues, s.providerPreflightIssues(fallback, seen)...)
	}
	delete(seen, key)
	return issues
}

func (s *Server) taskPreflightToolCatalog(ctx context.Context, issues *[]TaskPreflightIssue) taskPreflightToolCatalog {
	catalog := taskPreflightToolCatalog{names: s.runtime.ToolNames(), health: s.runtime.MCPHealthStatus(ctx)}
	tools, err := s.runtime.Tools(ctx)
	if err != nil {
		*issues = append(*issues, preflightIssue("error", "tool_discovery_failed", fmt.Sprintf("tool discovery failed: %v", err), "check MCP health and repair the failing server"))
	} else {
		catalog.tools = tools
	}
	return catalog
}

func (c taskPreflightToolCatalog) find(name string) (schema.Tool, bool, bool) {
	name = strings.TrimSpace(name)
	matches := make([]schema.Tool, 0, 1)
	for _, tool := range c.tools {
		full := strings.Trim(strings.TrimSpace(tool.Server)+"/"+strings.TrimSpace(tool.Name), "/")
		if strings.EqualFold(name, full) || strings.EqualFold(name, tool.Name) {
			matches = append(matches, tool)
		}
	}
	if len(matches) == 1 {
		return matches[0], true, false
	}
	if len(matches) > 1 {
		return schema.Tool{}, false, true
	}
	for _, candidate := range c.names {
		if !toolPreflightNameEquivalent(name, candidate) {
			continue
		}
		server, short := splitPreflightToolName(candidate)
		return schema.Tool{Name: short, Server: server}, true, false
	}
	return schema.Tool{}, false, false
}

func (s *Server) skillToolPreflightIssues(catalog taskPreflightToolCatalog, profile config.AgentProfile, skill schema.Skill, stage string) []TaskPreflightIssue {
	issues := make([]TaskPreflightIssue, 0)
	for _, dependency := range skill.Tools {
		issues = append(issues, s.toolPreflightIssues(catalog, profile, dependency.Name, dependency.Required, stage)...)
	}
	return issues
}

func (s *Server) toolPreflightIssues(catalog taskPreflightToolCatalog, profile config.AgentProfile, name string, required bool, stage string) []TaskPreflightIssue {
	severity := "warning"
	if required {
		severity = "error"
	}
	tool, found, ambiguous := catalog.find(name)
	if ambiguous {
		return []TaskPreflightIssue{{Severity: severity, Code: "tool_ambiguous", ResourceKind: "mcp", ResourceName: name, Stage: stage, Field: "tool", Message: fmt.Sprintf("tool name %s is ambiguous", name), Recommendation: "use the qualified server/tool name"}}
	}
	if !found {
		issues := []TaskPreflightIssue{{Severity: severity, Code: "tool_missing", ResourceKind: "mcp", ResourceName: name, Stage: stage, Field: "tool", Message: fmt.Sprintf("tool %s is not currently available", name), Recommendation: "enable and repair the MCP server that provides this tool"}}
		if server, ok := s.runtimeMCPServerForTool(name); ok {
			issues = append(issues, s.mcpServerPreflightIssues(server, false, stage, catalog.health, severity)...)
		}
		return issues
	}
	issues := make([]TaskPreflightIssue, 0)
	if err := policy.EnforceToolPolicy(profile, tool); err != nil {
		issues = append(issues, TaskPreflightIssue{Severity: severity, Code: "tool_not_allowed", ResourceKind: "agent", ResourceName: profile.Name, Stage: stage, Field: "allowed_tools", Message: fmt.Sprintf("Agent cannot use tool %s: %v", name, err), Recommendation: "select an Agent that permits this tool or narrow the Workflow tool requirement"})
	}
	serverName := strings.TrimSpace(tool.Server)
	if serverName == "" {
		serverName, _ = splitPreflightToolName(name)
	}
	if serverName != "" {
		if server, ok := s.runtimeMCPServerForTool(serverName + "/tool"); ok {
			issues = append(issues, s.mcpServerPreflightIssues(server, true, stage, catalog.health, severity)...)
		} else {
			issues = append(issues, TaskPreflightIssue{Severity: severity, Code: "mcp_missing", ResourceKind: "mcp", ResourceName: serverName, Stage: stage, Field: "mcp_server", Message: fmt.Sprintf("MCP server %s is not configured", serverName), Recommendation: "restore or enable the MCP resource that provides this tool"})
		}
	}
	return issues
}

func (s *Server) mcpServerPreflightIssues(server config.MCPServerRef, toolAvailable bool, stage string, health map[string]string, severity string) []TaskPreflightIssue {
	issues := make([]TaskPreflightIssue, 0)
	if !server.Enabled {
		issues = append(issues, TaskPreflightIssue{Severity: severity, Code: "mcp_disabled", ResourceKind: "mcp", ResourceName: server.Name, Stage: stage, Field: "enabled", Message: fmt.Sprintf("MCP server %s is disabled", server.Name), Recommendation: "enable the MCP server or remove the dependent tool"})
	}
	if err := config.ValidateMCPServerPlatform(server); err != nil {
		issues = append(issues, TaskPreflightIssue{Severity: severity, Code: "mcp_platform_unsupported", ResourceKind: "mcp", ResourceName: server.Name, Stage: stage, Field: "isolation", Message: err.Error(), Recommendation: "select an isolation mode supported by this host platform"})
	}
	status := preflightMapValueFold(health, server.Name)
	if status != "" && mcpPreflightHealthUnavailable(status) {
		issues = append(issues, TaskPreflightIssue{Severity: severity, Code: "mcp_degraded", ResourceKind: "mcp", ResourceName: server.Name, Stage: stage, Field: "health", Message: fmt.Sprintf("MCP server %s is unavailable: %s", server.Name, status), Recommendation: "repair the server command or dependency, then refresh MCP discovery"})
	}
	if !toolAvailable && strings.TrimSpace(server.Command) != "" {
		if _, err := exec.LookPath(server.Command); err != nil {
			issues = append(issues, TaskPreflightIssue{Severity: severity, Code: "mcp_command_missing", ResourceKind: "mcp", ResourceName: server.Name, Stage: stage, Field: "command", Message: fmt.Sprintf("MCP server %s command %s is not available", server.Name, server.Command), Recommendation: "install the command or update the MCP command path"})
		}
	}
	return issues
}

func (s *Server) requireTaskPreflight(w http.ResponseWriter, ctx context.Context, req taskPreflightRequest) bool {
	result := s.taskPreflight(ctx, req)
	if result.Valid {
		return true
	}
	writeJSONStatus(w, http.StatusUnprocessableEntity, result)
	return false
}

func preflightIssue(severity, code, message, recommendation string) TaskPreflightIssue {
	return TaskPreflightIssue{Severity: severity, Code: code, Message: message, Recommendation: recommendation}
}

func resourcePreflightIssue(severity, code, kind, name, message, recommendation string) TaskPreflightIssue {
	issue := preflightIssue(severity, code, message, recommendation)
	issue.ResourceKind = kind
	issue.ResourceName = strings.TrimSpace(name)
	return issue
}

func addPreflightStage(issues []TaskPreflightIssue, stage string) []TaskPreflightIssue {
	for i := range issues {
		if issues[i].Stage == "" {
			issues[i].Stage = stage
		}
	}
	return issues
}

func taskPreflightHasErrors(issues []TaskPreflightIssue) bool {
	for _, issue := range issues {
		if strings.EqualFold(strings.TrimSpace(issue.Severity), "error") {
			return true
		}
	}
	return false
}

func normalizeTaskPreflightIssues(issues []TaskPreflightIssue) []TaskPreflightIssue {
	out := make([]TaskPreflightIssue, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		issue.Severity = strings.ToLower(strings.TrimSpace(issue.Severity))
		if issue.Severity == "warn" {
			issue.Severity = "warning"
		}
		key := strings.Join([]string{issue.Severity, issue.Code, issue.ResourceKind, issue.ResourceName, issue.Stage, issue.Field, issue.Message}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, issue)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return preflightSeverityRank(out[i].Severity) < preflightSeverityRank(out[j].Severity)
	})
	return out
}

func preflightSeverityRank(severity string) int {
	switch severity {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

func splitPreflightToolName(name string) (string, string) {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if slash := strings.Index(name, "/"); slash >= 0 {
		return strings.TrimSpace(name[:slash]), strings.TrimSpace(name[slash+1:])
	}
	return "", name
}

func toolPreflightNameEquivalent(left, right string) bool {
	if strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right)) {
		return true
	}
	leftServer, leftName := splitPreflightToolName(left)
	rightServer, rightName := splitPreflightToolName(right)
	if !strings.EqualFold(leftName, rightName) {
		return false
	}
	return leftServer == "" || rightServer == "" || strings.EqualFold(leftServer, rightServer)
}

func preflightMapValueFold(values map[string]string, key string) string {
	for name, value := range values {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(key)) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mcpPreflightHealthUnavailable(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	for _, prefix := range []string{"degraded", "error", "cooldown", "disabled", "unavailable", "failed"} {
		if strings.HasPrefix(status, prefix) {
			return true
		}
	}
	return false
}

func fallbackPreflightValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
