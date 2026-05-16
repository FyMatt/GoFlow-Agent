package agent

import (
	"context"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Executor runs tool calls through the MCP layer.
type Executor struct {
	mcp executionMCP
}

type executionMCP interface {
	ListTools(ctx context.Context) ([]schema.Tool, error)
	CallTool(ctx context.Context, name string, arguments []byte) (schema.ToolResult, error)
}

// NewExecutor constructs a new executor.
func NewExecutor(mcp executionMCP) *Executor {
	return &Executor{mcp: mcp}
}

// RunToolCalls executes requested tool calls sequentially.
func (e *Executor) RunToolCalls(ctx context.Context, execCtx ExecutionContext, calls []schema.ToolCall, runtimeRef approvalQueuer, handler func(event schema.StreamEvent) error) ([]schema.ToolResult, error) {
	results := make([]schema.ToolResult, 0, len(calls))
	for _, call := range calls {
		tool, err := execCtx.validateToolCall(call)
		if err != nil {
			result := execCtx.validationErrorResult(call, err)
			execCtx.annotateResult(&result, schema.Tool{Name: call.Name})
			execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: call.Name, Outcome: validationErrorOutcome(result), Detail: err.Error()})
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventError, ToolName: call.Name, ToolCallID: call.ID, Content: err.Error(), AgentID: execCtx.AgentID, IsError: true})
			}
			results = append(results, result)
			continue
		}
		if handler != nil {
			if err := handler(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: tool.Name, ToolCallID: call.ID, ArgumentsSummary: summarizeToolArguments(call.Arguments), AgentID: execCtx.AgentID, Content: execCtx.toolKind(tool)}); err != nil {
				return nil, err
			}
		}
		if err := execCtx.rejectByRiskPolicy(tool); err != nil {
			result := execCtx.validationErrorResult(call, err)
			execCtx.annotateResult(&result, tool)
			execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: tool.Name, Outcome: "denied", Detail: err.Error()})
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventError, ToolName: tool.Name, ToolCallID: call.ID, Content: err.Error(), AgentID: execCtx.AgentID, IsError: true})
			}
			results = append(results, result)
			continue
		}
		if err := execCtx.emitApproval(handler, call, tool); err != nil {
			return nil, err
		}
		if execCtx.requiresApproval(tool) {
			if runtimeRef != nil {
				if execCtx.AgentRunID != "" {
					if scoped, ok := runtimeRef.(agentRunApprovalQueuer); ok {
						scoped.queueApprovalForAgentRun(call, tool, execCtx.AgentID, execCtx.AgentRunID)
					} else {
						runtimeRef.queueApproval(call, tool, execCtx.AgentID)
					}
				} else {
					runtimeRef.queueApproval(call, tool, execCtx.AgentID)
				}
			}
			result := execCtx.approvalResult(call, tool)
			execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: tool.Name, Outcome: "approval_required", Detail: result.Content})
			results = append(results, result)
			continue
		}
		result, err := e.mcp.CallTool(ctx, call.Name, call.Arguments)
		if err != nil {
			execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: tool.Name, Outcome: "error", Detail: err.Error()})
			result = schema.ToolResult{CallID: call.ID, ToolName: tool.Name, Content: err.Error(), IsError: true}
			execCtx.annotateResult(&result, tool)
			results = append(results, result)
			continue
		}
		result.CallID = call.ID
		execCtx.annotateResult(&result, tool)
		execCtx.recordFileReadResult(ctx, call, result)
		status := "ok"
		if result.IsError {
			status = "tool_error"
		}
		execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: tool.Name, Outcome: status, Detail: result.Content})
		results = append(results, result)
	}
	return results, nil
}

type approvalQueuer interface {
	queueApproval(call schema.ToolCall, tool schema.Tool, agentID string)
}

type agentRunApprovalQueuer interface {
	queueApprovalForAgentRun(call schema.ToolCall, tool schema.Tool, agentID, agentRunID string)
}

func validationErrorOutcome(result schema.ToolResult) string {
	if result.Denied {
		return "denied"
	}
	return "invalid_arguments"
}
