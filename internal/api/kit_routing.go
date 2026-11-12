package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/scaffold"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func overrideKitRouting(defaults, overrides *scaffold.KitRouting) *scaffold.KitRouting {
	if overrides != nil {
		return scaffold.NormalizeKitRouting(overrides)
	}
	return scaffold.NormalizeKitRouting(defaults)
}

func kitClaimedMaturity(doc kitResourceDocument) string {
	if doc.VerticalPack != nil {
		return doc.VerticalPack.Maturity
	}
	return ""
}

func (s *Server) autoKitTaskPreflight(ctx context.Context, req taskPreflightRequest) TaskPreflightResult {
	// Explicit selections remain authoritative, including a skill/tool on the active Agent.
	if strings.TrimSpace(req.Workflow) != "" {
		req.Type = "workflow"
		return s.taskPreflight(ctx, req)
	}
	if strings.TrimSpace(req.Agent) != "" || strings.TrimSpace(req.Skill) != "" || strings.TrimSpace(req.Tool) != "" {
		req.Type = "agent"
		return s.taskPreflight(ctx, req)
	}
	var enabled int
	var matches []kitResourceDocument
	for _, summary := range s.kitResourceList() {
		doc, ok := s.kitResourceByName(summary.Name)
		if !ok || !doc.Routing.IsEnabled() {
			continue
		}
		enabled++
		for _, term := range doc.Routing.MatchTerms {
			if strings.TrimSpace(term) != "" && strings.Contains(strings.ToLower(req.Input), strings.ToLower(term)) {
				if len(matches) == 0 || doc.Routing.Priority > matches[0].Routing.Priority {
					matches = []kitResourceDocument{doc}
				} else if doc.Routing.Priority == matches[0].Routing.Priority {
					matches = append(matches, doc)
				}
				break
			}
		}
	}
	if enabled == 0 {
		recommendation := recommendLocalTaskTarget(req.Input, s.runtime.ActiveAgent())
		req.Type = recommendation.Kind
		if req.Type == "workflow" {
			req.Workflow = recommendation.Target
		} else {
			req.Agent = recommendation.Target
		}
		result := s.taskPreflight(ctx, req)
		result.Domain, result.Complexity, result.ComplexityProfile, result.Reason = recommendation.Domain, recommendation.Complexity, recommendation.Profile, recommendation.Reason
		result.Orchestration = s.taskPreflightOrchestration(result.Kind, result.Target, result.Domain)
		return result
	}
	if len(matches) == 0 {
		// No kit claims the task. A focused request still has an obvious owner —
		// the active agent — so the task page keeps working without guessing a
		// domain. Complex work stops here and asks the user to choose.
		profile := analyzeTaskComplexity(req.Input)
		if profile.Level != "complex" {
			req.Type = "agent"
			req.Agent = firstWorkflowRunQueryValue(strings.TrimSpace(s.runtime.ActiveAgent()), "chat")
			// A change request cannot be served by a read-only default agent: the
			// run would end with "no write tools" and no delivery.
			if taskLooksLikeChangeRequest(req.Input) {
				req.Agent = "software-engineer"
			}
			result := s.taskPreflight(ctx, req)
			result.Complexity = profile.Level
			result.ComplexityProfile = profile
			result.Reason = "没有资源包匹配；简单任务交由当前智能体处理，复杂任务请显式选择入口"
			result.Orchestration = s.taskPreflightOrchestration(result.Kind, result.Target, result.Domain)
			return result
		}
	}
	if len(matches) != 1 {
		code, message := "kit_routing_no_match", "没有已启用的资源包路由匹配此任务"
		if len(matches) > 1 {
			var names []string
			for _, doc := range matches {
				names = append(names, doc.Name)
			}
			code, message = "kit_routing_ambiguous", fmt.Sprintf("多个资源包以相同最高优先级 %d 匹配此任务：%s", matches[0].Routing.Priority, strings.Join(names, ", "))
		}
		result := TaskPreflightResult{Kind: "auto", ExecutionTarget: localExecutionTarget(), Issues: []TaskPreflightIssue{resourcePreflightIssue("error", code, "kit", "", message, "请选择明确的智能体或工作流，或调整资源包的 match_terms")}}
		if strings.TrimSpace(req.Input) == "" {
			result.Issues = append(result.Issues, preflightIssue("error", "input_required", "task input is required", "describe the task before starting it"))
		}
		return result
	}
	doc := matches[0]
	req.Type, req.Agent, req.Workflow = "agent", doc.Routing.EntryAgent, doc.Routing.EntryWorkflow
	if req.Workflow != "" {
		req.Type = "workflow"
	}
	result := s.taskPreflight(ctx, req)
	result.Domain = doc.Name
	if doc.VerticalPack != nil && doc.VerticalPack.Domain != "" {
		result.Domain = doc.VerticalPack.Domain
	}
	result.Reason = fmt.Sprintf("selected by Kit %s routing contract v%d at priority %d", doc.Name, doc.Routing.Version, doc.Routing.Priority)
	result.Issues = append(result.Issues, s.kitRoutingDependencyIssues(ctx, doc)...)
	for _, tool := range doc.Routing.RequiredTools {
		result.RequiresExecutionTarget = result.RequiresExecutionTarget || s.toolRequiresExecutionTarget(tool)
	}
	s.resolveTaskExecutionTarget(req.TargetID, &result)
	result.ResourcePins = session.MergeResourcePins(result.ResourcePins, s.kitRoutingResourcePins(doc))
	result.Orchestration = s.taskPreflightOrchestration(result.Kind, result.Target, result.Domain)
	result.Issues = normalizeTaskPreflightIssues(result.Issues)
	result.Valid = !taskPreflightHasErrors(result.Issues)
	return result
}

func (s *Server) kitRoutingDependencyIssues(ctx context.Context, doc kitResourceDocument) []TaskPreflightIssue {
	var issues []TaskPreflightIssue
	for _, item := range s.validateKitResource(doc).Issues {
		if item.Severity != "error" && item.Code != "env_missing" {
			continue
		}
		issues = append(issues, TaskPreflightIssue{Severity: "error", Code: "kit_" + item.Code, ResourceKind: "kit", ResourceName: doc.Name, Field: item.Field, Message: item.Message, Recommendation: item.Recommendation})
	}
	for _, name := range doc.Agents {
		issues = append(issues, s.agentTaskPreflightIssues(ctx, name, "", "")...)
	}
	for _, name := range doc.Providers {
		issues = append(issues, s.providerPreflightIssues(name, nil)...)
	}
	for _, name := range doc.Skills {
		if _, ok := s.runtimeSkillByName(name); !ok {
			issues = append(issues, resourcePreflightIssue("error", "kit_skill_unavailable", "skill", name, "Kit requires loaded Skill "+name, "reload the runtime after installing this Skill"))
		}
	}
	for _, name := range doc.Workflows {
		if _, err := s.runtime.WorkflowRunner().LoadRunnableWorkflowGraphDocument(name); err == nil {
			continue
		}
		if executor, ok := s.workflowExecutor(name); !ok || !executor.Valid {
			issues = append(issues, resourcePreflightIssue("error", "kit_workflow_unavailable", "workflow", name, "Kit requires runnable Workflow "+name, "repair or reload the referenced Workflow"))
		}
	}
	if len(doc.Tools) == 0 && len(doc.Routing.RequiredTools) == 0 {
		return issues
	}
	catalog := s.taskPreflightToolCatalog(ctx, &issues)
	for _, name := range doc.Tools {
		if strings.Contains(name, "/") {
			// Library references need not be executable by this entry. This profile
			// only checks discovery/health; actual invocation retains its own policy.
			issues = append(issues, s.toolPreflightIssues(catalog, config.AgentProfile{}, name, false, "")...)
			continue
		}
		server, ok := s.runtimeMCPServerForTool(name + "/tool")
		if !ok {
			issues = append(issues, resourcePreflightIssue("error", "kit_mcp_missing", "mcp", name, "Kit requires configured MCP server "+name, "install and enable the required MCP server"))
		} else {
			issues = append(issues, s.mcpServerPreflightIssues(server, false, "", catalog.health, "error")...)
		}
	}
	for _, name := range doc.Routing.RequiredTools {
		if doc.Routing.EntryAgent != "" {
			if profile, ok := s.runtime.Profile(doc.Routing.EntryAgent); ok {
				issues = append(issues, s.toolPreflightIssues(catalog, profile, name, true, "")...)
			}
			continue
		}
		// A workflow capability must be usable by an executable stage; declaring it cannot expand a stage's policy.
		docGraph, err := s.runtime.WorkflowRunner().LoadRunnableWorkflowGraphDocument(doc.Routing.EntryWorkflow)
		usable := false
		var failures []TaskPreflightIssue
		if err == nil {
			for _, stage := range s.runtime.WorkflowRunner().WorkflowGraphStageRequirements(docGraph) {
				if !stage.Executable || stage.DisableTools {
					continue
				}
				profile, ok := s.runtime.Profile(stage.Agent)
				if !ok {
					continue
				}
				if len(stage.Tools) > 0 {
					tool, found, _ := catalog.find(name)
					if !found {
						failures = append(failures, s.toolPreflightIssues(catalog, profile, name, true, stage.Stage)...)
						continue
					}
					if err := policy.EnforceToolPolicy(config.AgentProfile{AllowedTools: stage.Tools}, tool); err != nil {
						continue
					}
				}
				checks := s.toolPreflightIssues(catalog, profile, name, true, stage.Stage)
				if !taskPreflightHasErrors(checks) {
					usable = true
					break
				}
				failures = append(failures, checks...)
			}
		}
		if !usable {
			issues = append(issues, failures...)
			issues = append(issues, resourcePreflightIssue("error", "kit_capability_unavailable", "kit", doc.Name, "入口中没有可执行阶段获准使用必需工具 "+name, "请修复工具依赖，或选择权限与所需工具相符的入口阶段"))
		}
	}
	return issues
}

func (s *Server) kitRoutingResourcePins(doc kitResourceDocument) []session.ResourcePin {
	pins := []session.ResourcePin{session.NewResourcePin("kit", doc.Name, struct {
		Origin string              `json:"origin"`
		Kit    kitResourceDocument `json:"kit"`
	}{Origin: s.kitResourceOrigin(doc.Path), Kit: sanitizeKitForBundle(doc)})}
	for _, name := range doc.Agents {
		pins = append(pins, s.agentAndProviderResourcePins(name)...)
	}
	for _, name := range doc.Providers {
		pins = append(pins, s.providerAndFallbackResourcePins(name, nil)...)
	}
	for _, name := range doc.Skills {
		if skill, ok := s.runtimeSkillByName(name); ok {
			pins = append(pins, s.skillAndMCPResourcePins(skill)...)
		}
	}
	for _, name := range doc.Workflows {
		pins = append(pins, s.workflowTaskResourcePins(name)...)
	}
	for _, name := range append(append([]string(nil), doc.Tools...), doc.Routing.RequiredTools...) {
		if !strings.Contains(name, "/") {
			name += "/tool"
		}
		if server, ok := s.runtimeMCPServerForTool(name); ok {
			pins = append(pins, mcpServerResourcePin(server))
		}
	}
	for _, name := range doc.WorkflowTemplates {
		if item, ok := s.runtime.WorkflowRunner().WorkflowTemplate(name); ok {
			pins = append(pins, session.NewResourcePin("workflow_template", name, item))
		}
	}
	for _, name := range doc.TeamTemplates {
		if item, ok := s.runtime.WorkflowRunner().TeamTemplate(name); ok {
			pins = append(pins, session.NewResourcePin("team_template", name, item))
		}
	}
	for _, name := range doc.PolicyRules {
		if item, ok := s.policyRuleResourceByName(name); ok {
			item.Path = ""
			pins = append(pins, session.NewResourcePin("policy_rule", name, item))
		}
	}
	return session.NormalizeResourcePins(pins)
}

// taskLooksLikeChangeRequest reports whether the request asks to modify the
// workspace. Such a task needs write-capable tools even when no kit claims it.
func taskLooksLikeChangeRequest(input string) bool {
	lowered := strings.ToLower(strings.TrimSpace(input))
	for _, term := range []string{
		"修改", "美化", "样式", "布局", "动画", "实现", "修复", "重构", "调整", "新增", "添加", "优化", "替换", "改成", "更新",
		"fix", "implement", "refactor", "restyle", "update", "change", "modify", "add ",
	} {
		if strings.Contains(lowered, term) {
			return true
		}
	}
	return false
}
