package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type taskRoutingPinsKey struct{}

func taskRoutingPins(ctx context.Context) []session.ResourcePin {
	pins, _ := ctx.Value(taskRoutingPinsKey{}).([]session.ResourcePin)
	return session.CopyResourcePins(pins)
}

// resolveRunRouting resolves auto exactly once, then reuses the normal execution
// handlers so workspace confirmation, approvals, attachments and controls still apply.
func (s *Server) resolveRunRouting(w http.ResponseWriter, r *http.Request, req *runRequest, stream bool) (*http.Request, bool) {
	kind := strings.ToLower(strings.TrimSpace(req.Type))
	if kind == "" && strings.TrimSpace(req.Workflow) == "" {
		return r, false
	}
	if kind == "" {
		kind = "workflow"
	}
	result := s.taskPreflight(r.Context(), taskPreflightRequest{Type: kind, Agent: req.Agent, Workflow: req.Workflow, Skill: req.Skill, Tool: req.Tool, Input: req.Input, Approve: workflowRequestApprove(*req), TargetID: req.TargetID})
	if !result.Valid {
		writeJSONStatus(w, http.StatusUnprocessableEntity, result)
		return r, true
	}
	r = r.WithContext(context.WithValue(r.Context(), taskRoutingPinsKey{}, session.CopyResourcePins(result.ResourcePins)))
	req.Type, req.Workflow = "", ""
	if result.Kind == "agent" {
		req.Agent = result.Target
		return r, false
	}
	body, err := json.Marshal(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return r, true
	}
	forward := r.Clone(r.Context())
	forward.Body = io.NopCloser(bytes.NewReader(body))
	forward.ContentLength = int64(len(body))
	if stream {
		s.handleWorkflowStream(w, forward, result.Target)
	} else {
		forward.URL.Path = "/api/workflows/" + result.Target + "/run"
		s.handleWorkflow(w, forward)
	}
	return r, true
}

func (s *Server) runAgentWithRoutingPins(ctx context.Context, input, agentID string, req runRequest, attachments []schema.AttachmentRef, handler func(schema.StreamEvent) error) (schema.AgentResult, error) {
	pins := taskRoutingPins(ctx)
	if len(pins) == 0 {
		return s.runtime.RunStream(ctx, input, handler)
	}
	controls := runControlsFromRequest(req)
	runID, err := s.runtime.StartAgentRunWithOptionsDurable(req.Input, session.AgentRunStartOptions{AgentID: agentID, TargetID: req.TargetID, ResourcePins: pins, Attachments: attachments, ReasoningEffort: controls.ReasoningEffort, PermissionMode: controls.PermissionMode})
	if err != nil {
		return schema.AgentResult{}, err
	}
	ctx, cancel := context.WithCancel(agent.WithAgentRunID(ctx, runID))
	defer cancel()
	token := s.registerActiveAgentRun(runID, cancel)
	defer s.unregisterActiveAgentRun(runID, token)
	result, err := s.runtime.RunStream(ctx, input, s.recordAgentRunEvents(runID, handler))
	if errors.Is(err, context.Canceled) {
		s.runtime.CancelAgentRun(runID, "agent run cancelled")
	} else if err != nil {
		s.runtime.FailAgentRun(runID, err.Error())
	} else {
		s.runtime.CompleteAgentRun(runID, agentRunStatusForResult(result), result)
	}
	return result, err
}
