package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/commandpolicy"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type workflowStageToolPolicy struct {
	MaxCommandCalls       int
	MaxReadCalls          int
	AllowedCommandClasses []commandpolicy.Class
}

func (p workflowStageToolPolicy) empty() bool {
	return p.MaxCommandCalls <= 0 && p.MaxReadCalls <= 0 && len(p.AllowedCommandClasses) == 0
}

func availableWorkflowStageTools(tools []schema.Tool, completed []schema.ToolResult, execCtx ExecutionContext, stagePolicy workflowStageToolPolicy) []schema.Tool {
	if stagePolicy.empty() {
		return tools
	}
	commandCalls := completedWorkflowStageToolCalls(completed, execCtx, config.ToolKindExec, "run_command")
	readCalls := completedWorkflowStageToolCalls(completed, execCtx, config.ToolKindRead, "")
	available := make([]schema.Tool, 0, len(tools))
	for _, tool := range tools {
		if toolNameEquivalent(tool.Name, "run_command") && stagePolicy.MaxCommandCalls > 0 && commandCalls >= stagePolicy.MaxCommandCalls {
			continue
		}
		if policy.KindForTool(tool) == config.ToolKindRead && stagePolicy.MaxReadCalls > 0 && readCalls >= stagePolicy.MaxReadCalls {
			continue
		}
		available = append(available, tool)
	}
	return available
}

func enforceWorkflowStageToolPolicy(calls []schema.ToolCall, completed []schema.ToolResult, execCtx ExecutionContext, stagePolicy workflowStageToolPolicy) ([]schema.ToolCall, map[string]schema.ToolResult) {
	if len(calls) == 0 || stagePolicy.empty() {
		return calls, nil
	}
	commandCalls := completedWorkflowStageToolCalls(completed, execCtx, config.ToolKindExec, "run_command")
	readCalls := completedWorkflowStageToolCalls(completed, execCtx, config.ToolKindRead, "")
	allowedClasses := make(map[commandpolicy.Class]struct{}, len(stagePolicy.AllowedCommandClasses))
	for _, class := range stagePolicy.AllowedCommandClasses {
		allowedClasses[class] = struct{}{}
	}
	allowed := make([]schema.ToolCall, 0, len(calls))
	blocked := make(map[string]schema.ToolResult)
	for _, call := range calls {
		tool, _ := policy.NewToolCatalog(execCtx.Tools).Find(call.Name)
		kind := policy.KindForTool(tool)
		if toolNameEquivalent(call.Name, "run_command") || toolNameEquivalent(tool.Name, "run_command") {
			classification := commandpolicy.ClassifyArguments(call.Arguments)
			if len(allowedClasses) > 0 {
				if _, ok := allowedClasses[classification.Class]; !ok {
					blocked[call.ID] = workflowStageToolPolicyResult(call, fmt.Sprintf("workflow stage command policy blocks class %s; allowed classes: %s; reason: %s", classification.Class, joinCommandClasses(stagePolicy.AllowedCommandClasses), classification.Reason))
					continue
				}
			}
			if stagePolicy.MaxCommandCalls > 0 && commandCalls >= stagePolicy.MaxCommandCalls {
				blocked[call.ID] = workflowStageToolPolicyResult(call, fmt.Sprintf("workflow stage command limit reached (%d); stop calling commands and produce the stage report", stagePolicy.MaxCommandCalls))
				continue
			}
			commandCalls++
			allowed = append(allowed, call)
			continue
		}
		if kind == config.ToolKindRead && stagePolicy.MaxReadCalls > 0 {
			if readCalls >= stagePolicy.MaxReadCalls {
				blocked[call.ID] = workflowStageToolPolicyResult(call, fmt.Sprintf("workflow stage read limit reached (%d); use the supplied stage evidence and produce the stage report", stagePolicy.MaxReadCalls))
				continue
			}
			readCalls++
		}
		allowed = append(allowed, call)
	}
	return allowed, blocked
}

func completedWorkflowStageToolCalls(results []schema.ToolResult, execCtx ExecutionContext, kind config.ToolKind, toolName string) int {
	if len(results) == 0 {
		return 0
	}
	catalog := policy.NewToolCatalog(execCtx.Tools)
	count := 0
	for _, result := range results {
		if result.Denied || result.Suspended {
			continue
		}
		if toolName != "" {
			if toolNameEquivalent(result.ToolName, toolName) {
				count++
			}
			continue
		}
		tool, ok := catalog.Find(result.ToolName)
		if ok && policy.KindForTool(tool) == kind {
			count++
		}
	}
	return count
}

func workflowStageToolPolicyResult(call schema.ToolCall, reason string) schema.ToolResult {
	return schema.ToolResult{
		CallID:   call.ID,
		ToolName: call.Name,
		Content:  reason,
		IsError:  true,
		Denied:   true,
	}
}

func mergeWorkflowStageToolResults(calls []schema.ToolCall, executed []schema.ToolResult, blocked map[string]schema.ToolResult) []schema.ToolResult {
	if len(blocked) == 0 {
		return executed
	}
	byID := make(map[string]schema.ToolResult, len(executed)+len(blocked))
	for _, result := range executed {
		byID[result.CallID] = result
	}
	for id, result := range blocked {
		byID[id] = result
	}
	merged := make([]schema.ToolResult, 0, len(byID))
	for _, call := range calls {
		if result, ok := byID[call.ID]; ok {
			merged = append(merged, result)
		}
	}
	return merged
}

func workflowStageToolPolicyNudge(stagePolicy workflowStageToolPolicy, blocked map[string]schema.ToolResult) string {
	reasons := make([]string, 0, len(blocked))
	for _, result := range blocked {
		reasons = append(reasons, strings.TrimSpace(result.Content))
	}
	sort.Strings(reasons)
	message := "The workflow stage tool contract blocked one or more calls. Do not try equivalent discovery commands or shell wrappers."
	if len(stagePolicy.AllowedCommandClasses) > 0 {
		message += " Use only command classes: " + joinCommandClasses(stagePolicy.AllowedCommandClasses) + "."
	}
	message += " Continue from existing evidence; when the bounded work is done, stop calling tools and produce the required stage report."
	if len(reasons) > 0 {
		message += " Blocked reason: " + reasons[0]
	}
	return message
}

func joinCommandClasses(classes []commandpolicy.Class) string {
	values := make([]string, 0, len(classes))
	for _, class := range classes {
		value := strings.TrimSpace(string(class))
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return strings.Join(values, ",")
}
