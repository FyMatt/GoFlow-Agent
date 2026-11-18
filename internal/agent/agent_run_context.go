package agent

import (
	"context"
	"strings"
)

type agentRunIDContextKey struct{}
type agentTargetContextKey struct{}
type agentSkillContextKey struct{}
type executionTargetContextKey struct{}

// WithAgentRunID attaches a durable ordinary agent run id to an execution context.
func WithAgentRunID(ctx context.Context, runID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, agentRunIDContextKey{}, runID)
}

func agentRunIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	runID, _ := ctx.Value(agentRunIDContextKey{}).(string)
	return strings.TrimSpace(runID)
}

// WithAgentTarget fixes an explicit Agent selection for one request. Automatic
// intent routing remains available when no target is attached.
func WithAgentTarget(ctx context.Context, agentID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ctx
	}
	return context.WithValue(ctx, agentTargetContextKey{}, agentID)
}

func agentTargetFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	agentID, _ := ctx.Value(agentTargetContextKey{}).(string)
	return strings.TrimSpace(agentID)
}

// WithAgentSkill fixes an explicitly selected Skill for one Agent request.
func WithAgentSkill(ctx context.Context, skillName string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return ctx
	}
	return context.WithValue(ctx, agentSkillContextKey{}, skillName)
}

func agentSkillFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	skillName, _ := ctx.Value(agentSkillContextKey{}).(string)
	return strings.TrimSpace(skillName)
}

// WithExecutionTarget attaches the trusted execution Target selected during
// task preflight. Host-scoped tools must use this same target for the run.
func WithExecutionTarget(ctx context.Context, targetID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	targetID = strings.ToLower(strings.TrimSpace(targetID))
	if targetID == "" {
		return ctx
	}
	return context.WithValue(ctx, executionTargetContextKey{}, targetID)
}

func executionTargetIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	targetID, _ := ctx.Value(executionTargetContextKey{}).(string)
	return strings.ToLower(strings.TrimSpace(targetID))
}

func executionTargetSystemPrompt(prompt, targetID string) string {
	targetID = strings.ToLower(strings.TrimSpace(targetID))
	if targetID == "" {
		targetID = "local"
	}
	return strings.TrimSpace(prompt) + "\n\nExecution target ID: " + targetID + ". For every host-scoped tool call, pass this exact target_id and never substitute another host."
}
