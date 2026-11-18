package agent

import (
	"context"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

const (
	RunPermissionConfirm   = "confirm"
	RunPermissionWorkspace = "workspace"
	RunPermissionFull      = "full"
)

// RunControls are request-scoped model and approval choices selected by the operator.
type RunControls struct {
	ReasoningEffort string
	PermissionMode  string
}

type runControlsContextKey struct{}

func NormalizeRunReasoningEffort(value string) string {
	return config.NormalizeReasoningEffort(value)
}

func NormalizeRunPermissionMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case RunPermissionWorkspace:
		return RunPermissionWorkspace
	case RunPermissionFull:
		return RunPermissionFull
	default:
		return RunPermissionConfirm
	}
}

func WithRunControls(ctx context.Context, controls RunControls) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	controls.ReasoningEffort = NormalizeRunReasoningEffort(controls.ReasoningEffort)
	controls.PermissionMode = NormalizeRunPermissionMode(controls.PermissionMode)
	return context.WithValue(ctx, runControlsContextKey{}, controls)
}

func runControlsFromContext(ctx context.Context, runtimeRef *Runtime) RunControls {
	if ctx != nil {
		if controls, ok := ctx.Value(runControlsContextKey{}).(RunControls); ok {
			controls.ReasoningEffort = NormalizeRunReasoningEffort(controls.ReasoningEffort)
			controls.PermissionMode = NormalizeRunPermissionMode(controls.PermissionMode)
			return controls
		}
	}
	controls := RunControls{PermissionMode: RunPermissionConfirm}
	if runtimeRef == nil || runtimeRef.session == nil {
		return controls
	}
	if runID := agentRunIDFromContext(ctx); runID != "" {
		if run, ok := runtimeRef.session.AgentRun(runID); ok {
			controls.ReasoningEffort = NormalizeRunReasoningEffort(run.ReasoningEffort)
			controls.PermissionMode = NormalizeRunPermissionMode(run.PermissionMode)
			return controls
		}
	}
	if runID := runtimeRef.currentWorkflowRunID(); runID != "" {
		if run, ok := runtimeRef.session.WorkflowRun(runID); ok {
			controls.ReasoningEffort = NormalizeRunReasoningEffort(run.ReasoningEffort)
			controls.PermissionMode = NormalizeRunPermissionMode(run.PermissionMode)
		}
	}
	return controls
}
