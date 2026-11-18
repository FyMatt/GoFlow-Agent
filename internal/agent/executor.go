package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
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

// RunToolCalls executes independent read-only calls concurrently while
// preserving the caller's result and event order. Any batch that contains a
// write, approval, or uncertain policy decision remains sequential.
func (e *Executor) RunToolCalls(ctx context.Context, execCtx ExecutionContext, calls []schema.ToolCall, runtimeRef approvalQueuer, handler func(event schema.StreamEvent) error) ([]schema.ToolResult, error) {
	if e.parallelReadBatchEligible(execCtx, calls, runtimeRef) {
		return e.runReadOnlyToolCallsParallel(ctx, execCtx, calls, runtimeRef, handler)
	}
	return e.runToolCallsSequential(ctx, execCtx, calls, runtimeRef, handler)
}

func (e *Executor) runToolCallsSequential(ctx context.Context, execCtx ExecutionContext, calls []schema.ToolCall, runtimeRef approvalQueuer, handler func(event schema.StreamEvent) error) ([]schema.ToolResult, error) {
	results := make([]schema.ToolResult, 0, len(calls))
	for _, call := range calls {
		call = execCtx.prepareToolCall(call)
		call = execCtx.sanitizeToolCall(call)
		tool, err := execCtx.validateToolCall(call)
		if err != nil {
			recordToolRejection(execCtx, call.Name, err.Error())
			result := execCtx.validationErrorResult(call, err)
			execCtx.annotateResult(&result, schema.Tool{Name: call.Name})
			result = materializeToolResult(execCtx.Session, result, execCtx.AgentID, execCtx.Profile.Mode, execCtx.WorkflowRunID, execCtx.WorkflowStage)
			execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: call.Name, Outcome: validationErrorOutcome(result), Detail: err.Error()})
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventError, ToolName: call.Name, ToolCallID: call.ID, Content: err.Error(), AgentID: execCtx.AgentID, IsError: true})
			}
			results = append(results, result)
			continue
		}
		if err := execCtx.rejectByRiskPolicy(tool); err != nil {
			recordToolRejection(execCtx, tool.Name, err.Error())
			result := execCtx.validationErrorResult(call, err)
			execCtx.annotateResult(&result, tool)
			result = materializeToolResult(execCtx.Session, result, execCtx.AgentID, execCtx.Profile.Mode, execCtx.WorkflowRunID, execCtx.WorkflowStage)
			execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: tool.Name, Outcome: "denied", Detail: err.Error()})
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventError, ToolName: tool.Name, ToolCallID: call.ID, Content: err.Error(), AgentID: execCtx.AgentID, IsError: true})
			}
			results = append(results, result)
			continue
		}
		if err := execCtx.rejectByNativeSandboxPolicy(call, tool); err != nil {
			recordToolRejection(execCtx, tool.Name, err.Error())
			result := execCtx.validationErrorResult(call, err)
			execCtx.annotateResult(&result, tool)
			result = materializeToolResult(execCtx.Session, result, execCtx.AgentID, execCtx.Profile.Mode, execCtx.WorkflowRunID, execCtx.WorkflowStage)
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
		if execCtx.requiresApproval(call, tool) {
			if runtimeRef != nil {
				if scoped, ok := runtimeRef.(workflowToolOperationApprovalQueuer); ok {
					scoped.queueApprovalForAgentRunWithToolOperations(call, tool, execCtx.AgentID, execCtx.AgentRunID, workflowToolOperationScope{
						RunID:    execCtx.WorkflowRunID,
						Workflow: execCtx.WorkflowName,
						Stage:    execCtx.WorkflowStage,
						Enabled:  execCtx.IdempotentToolCalls,
					})
				} else if execCtx.AgentRunID != "" {
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
			result = materializeToolResult(execCtx.Session, result, execCtx.AgentID, execCtx.Profile.Mode, execCtx.WorkflowRunID, execCtx.WorkflowStage)
			execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: tool.Name, Outcome: "approval_required", Detail: result.Content})
			results = append(results, result)
			continue
		}
		if preparer, ok := runtimeRef.(taskWorkspacePreparer); ok {
			if err := preparer.prepareExecutionWorkspace(&execCtx, call, tool); err != nil {
				result := execCtx.validationErrorResult(call, err)
				execCtx.annotateResult(&result, tool)
				result = materializeToolResult(execCtx.Session, result, execCtx.AgentID, execCtx.Profile.Mode, execCtx.WorkflowRunID, execCtx.WorkflowStage)
				execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: tool.Name, Outcome: "denied", Detail: err.Error()})
				results = append(results, result)
				continue
			}
		}
		result, replayed, err := executeToolCallWithIdempotency(ctx, e.mcp, execCtx, call, tool)
		if err != nil {
			execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: tool.Name, Outcome: "idempotency_error", Detail: err.Error()})
			return nil, err
		}
		if handler != nil && !replayed {
			if err := handler(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: tool.Name, ToolCallID: call.ID, ArgumentsSummary: summarizeToolArguments(call.Arguments), ArgumentsHash: toolArgumentsHash(call.Arguments), AgentID: execCtx.AgentID, Content: execCtx.toolKind(tool)}); err != nil {
				return nil, err
			}
		}
		execCtx.annotateResult(&result, tool)
		result = materializeToolResult(execCtx.Session, result, execCtx.AgentID, execCtx.Profile.Mode, execCtx.WorkflowRunID, execCtx.WorkflowStage)
		execCtx.recordFileReadResult(ctx, call, result)
		execCtx.recordChangeSliceToolSuccess(call, tool, result)
		execCtx.recordChangeSliceToolFailure(call, tool, result)
		status := "ok"
		if result.IsError {
			status = "tool_error"
		} else if replayed {
			status = "idempotent_replay"
		}
		execCtx.recordAudit(schema.AuditEntry{Type: "tool_call", ToolName: tool.Name, Outcome: status, Detail: result.Content})
		results = append(results, result)
	}
	return results, nil
}

func (e *Executor) parallelReadBatchEligible(execCtx ExecutionContext, calls []schema.ToolCall, runtimeRef approvalQueuer) bool {
	if len(calls) < 2 {
		return false
	}
	for _, rawCall := range calls {
		call := execCtx.prepareToolCall(rawCall)
		call = execCtx.sanitizeToolCall(call)
		tool, err := execCtx.validateToolCall(call)
		if err != nil || policy.KindForTool(tool) != config.ToolKindRead {
			return false
		}
		if _, hasWorkspacePreparer := runtimeRef.(taskWorkspacePreparer); hasWorkspacePreparer && thirdPartyExecutionTool(call, tool) {
			return false
		}
		if execCtx.requiresApproval(call, tool) || execCtx.rejectByRiskPolicy(tool) != nil || execCtx.rejectByNativeSandboxPolicy(call, tool) != nil {
			return false
		}
	}
	return true
}

func (e *Executor) runReadOnlyToolCallsParallel(ctx context.Context, execCtx ExecutionContext, calls []schema.ToolCall, runtimeRef approvalQueuer, handler func(event schema.StreamEvent) error) ([]schema.ToolResult, error) {
	results := make([]schema.ToolResult, len(calls))
	errs := make([]error, len(calls))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(calls))
	for index, call := range calls {
		index, call := index, call
		go func() {
			defer waitGroup.Done()
			one, err := e.runToolCallsSequential(ctx, execCtx, []schema.ToolCall{call}, runtimeRef, nil)
			if err != nil {
				errs[index] = err
				return
			}
			if len(one) != 1 {
				errs[index] = fmt.Errorf("parallel tool call returned no result")
				return
			}
			results[index] = one[0]
		}()
	}
	waitGroup.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	if handler != nil {
		for index, rawCall := range calls {
			call := execCtx.prepareToolCall(rawCall)
			call = execCtx.sanitizeToolCall(call)
			tool, err := execCtx.validateToolCall(call)
			if err != nil {
				return nil, err
			}
			if err := handler(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: tool.Name, ToolCallID: call.ID, ArgumentsSummary: summarizeToolArguments(call.Arguments), ArgumentsHash: toolArgumentsHash(call.Arguments), AgentID: execCtx.AgentID, Content: execCtx.toolKind(tool)}); err != nil {
				return nil, err
			}
			_ = index
		}
	}
	return results, nil
}

type approvalQueuer interface {
	queueApproval(call schema.ToolCall, tool schema.Tool, agentID string)
}

type agentRunApprovalQueuer interface {
	queueApprovalForAgentRun(call schema.ToolCall, tool schema.Tool, agentID, agentRunID string)
}

type workflowToolOperationApprovalQueuer interface {
	queueApprovalForAgentRunWithToolOperations(call schema.ToolCall, tool schema.Tool, agentID, agentRunID string, operations workflowToolOperationScope)
}

type taskWorkspacePreparer interface {
	prepareExecutionWorkspace(execCtx *ExecutionContext, call schema.ToolCall, tool schema.Tool) error
}

func validationErrorOutcome(result schema.ToolResult) string {
	if result.Denied {
		return "denied"
	}
	return "invalid_arguments"
}
