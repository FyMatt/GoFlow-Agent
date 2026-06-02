package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type Server struct {
	runtime            *agent.Runtime
	workspace          *workspace.State
	workspaceRebinder  workspaceRebinder
	mux                *http.ServeMux
	activeAgentMu      sync.Mutex
	activeAgentRuns    map[string]context.CancelFunc
	agentExecMu        sync.Mutex
	activeWorkflowMu   sync.Mutex
	activeWorkflowRuns map[string]context.CancelFunc
	workflowExecMu     sync.Mutex
}

type workspaceRebinder func(context.Context, string) (*agent.Runtime, *workspace.State, error)

type sessionSnapshotProvider interface {
	SessionSnapshot() session.Snapshot
}

func NewServer(runtime *agent.Runtime) *Server {
	return NewServerWithWorkspace(runtime, nil)
}

func NewServerWithWorkspace(runtime *agent.Runtime, workspaceState *workspace.State) *Server {
	return NewServerWithWorkspaceRebinder(runtime, workspaceState, nil)
}

func NewServerWithWorkspaceRebinder(runtime *agent.Runtime, workspaceState *workspace.State, rebinder workspaceRebinder) *Server {
	s := &Server{runtime: runtime, workspace: workspaceState, workspaceRebinder: rebinder, mux: http.NewServeMux()}
	s.mux.HandleFunc("/favicon.ico", s.handleFavicon)
	s.mux.HandleFunc("/", s.handleConsole)
	s.mux.HandleFunc("/console", s.handleConsole)
	s.mux.HandleFunc("/console/", s.handleConsole)
	s.mux.Handle("/assets/", s.handleConsoleAssets())
	s.mux.HandleFunc("/workspace", s.handleWorkspacePage)
	s.mux.HandleFunc("/workspace/", s.handleWorkspacePage)
	s.mux.HandleFunc("/workflows", s.handleWorkflowEditor)
	s.mux.HandleFunc("/workflows/", s.handleWorkflowEditor)
	s.mux.HandleFunc("/api/runtime", s.handleRuntimeStatus)
	s.mux.HandleFunc("/api/runtime/cost", s.handleRuntimeCost)
	s.mux.HandleFunc("/api/runtime/agent", s.handleRuntimeAgent)
	s.mux.HandleFunc("/api/runtime/trace", s.handleRuntimeTrace)
	s.mux.HandleFunc("/api/capabilities", s.handleCapabilities)
	s.mux.HandleFunc("/api/help", s.handleHelp)
	s.mux.HandleFunc("/api/config/diagnostics", s.handleConfigDiagnostics)
	s.mux.HandleFunc("/api/update-policy", s.handleUpdatePolicy)
	s.mux.HandleFunc("/api/update-policy/check", s.handleUpdateCheck)
	s.mux.HandleFunc("/api/workspace", s.handleWorkspace)
	s.mux.HandleFunc("/api/workspace/", s.handleWorkspaceAction)
	s.mux.HandleFunc("/api/workspace-files", s.handleWorkspaceFiles)
	s.mux.HandleFunc("/api/run", s.handleRun)
	s.mux.HandleFunc("/api/run/stream", s.handleRunStream)
	s.mux.HandleFunc("/api/runs", s.handleRunCollection)
	s.mux.HandleFunc("/api/runs/", s.handleRunItem)
	s.mux.HandleFunc("/api/agent-runs", s.handleAgentRunCollection)
	s.mux.HandleFunc("/api/agent-runs/", s.handleAgentRunItem)
	s.mux.HandleFunc("/api/session", s.handleSession)
	s.mux.HandleFunc("/api/session/compact", s.handleSessionCompact)
	s.mux.HandleFunc("/api/memory/project", s.handleMemoryProject)
	s.mux.HandleFunc("/api/memory/search", s.handleMemorySearch)
	s.mux.HandleFunc("/api/memory/rebuild", s.handleMemoryRebuild)
	s.mux.HandleFunc("/api/memory/solutions/", s.handleMemorySolutionAction)
	s.mux.HandleFunc("/api/memory", s.handleMemoryDashboard)
	s.mux.HandleFunc("/api/artifacts", s.handleArtifactObjectCollection)
	s.mux.HandleFunc("/api/artifacts/", s.handleArtifactObjectItem)
	s.mux.HandleFunc("/api/session-artifacts", s.handleSessionArtifactCollection)
	s.mux.HandleFunc("/api/session-artifacts/", s.handleSessionArtifactItem)
	s.mux.HandleFunc("/api/workflow-graphs", s.handleWorkflowGraphCollection)
	s.mux.HandleFunc("/api/workflow-graphs/", s.handleWorkflowGraphItem)
	s.mux.HandleFunc("/api/workflow-templates", s.handleWorkflowTemplateCollection)
	s.mux.HandleFunc("/api/workflow-templates/", s.handleWorkflowTemplateItem)
	s.mux.HandleFunc("/api/team-templates", s.handleTeamTemplateCollection)
	s.mux.HandleFunc("/api/team-templates/", s.handleTeamTemplateItem)
	s.mux.HandleFunc("/api/kits", s.handleKitCollection)
	s.mux.HandleFunc("/api/kits/", s.handleKitItem)
	s.mux.HandleFunc("/api/workflow-runs", s.handleWorkflowRunCollection)
	s.mux.HandleFunc("/api/workflow-runs/", s.handleWorkflowRunItem)
	s.mux.HandleFunc("/api/workflow-schemas", s.handleWorkflowSchemaCollection)
	s.mux.HandleFunc("/api/workflow-schemas/", s.handleWorkflowSchemaItem)
	s.mux.HandleFunc("/api/collaboration/messages", s.handleCollaborationMessages)
	s.mux.HandleFunc("/api/collaboration/blackboard", s.handleBlackboardCollection)
	s.mux.HandleFunc("/api/collaboration/blackboard/", s.handleBlackboardItem)
	s.mux.HandleFunc("/api/team-state", s.handleTeamState)
	s.mux.HandleFunc("/api/workflow-options", s.handleWorkflowOptions)
	s.mux.HandleFunc("/api/workflow-expression-functions", s.handleWorkflowExpressionFunctions)
	s.mux.HandleFunc("/api/workflows/", s.handleWorkflow)
	s.mux.HandleFunc("/api/resources", s.handleResourceCatalog)
	s.mux.HandleFunc("/api/resources/skills", s.handleSkillResourceCollection)
	s.mux.HandleFunc("/api/resources/skills/", s.handleSkillResourceItem)
	s.mux.HandleFunc("/api/resources/providers", s.handleProviderResourceCollection)
	s.mux.HandleFunc("/api/resources/providers/", s.handleProviderResourceItem)
	s.mux.HandleFunc("/api/resources/agents", s.handleAgentResourceCollection)
	s.mux.HandleFunc("/api/resources/agents/", s.handleAgentResourceItem)
	s.mux.HandleFunc("/api/resources/tools", s.handleToolResourceCollection)
	s.mux.HandleFunc("/api/resources/tools/", s.handleToolResourceItem)
	s.mux.HandleFunc("/api/resources/workflow-schemas", s.handleWorkflowSchemaResourceCollection)
	s.mux.HandleFunc("/api/resources/workflow-schemas/", s.handleWorkflowSchemaResourceItem)
	s.mux.HandleFunc("/api/resources/workflow-templates", s.handleWorkflowTemplateResourceCollection)
	s.mux.HandleFunc("/api/resources/workflow-templates/", s.handleWorkflowTemplateResourceItem)
	s.mux.HandleFunc("/api/resources/workflow-node-metadata", s.handleWorkflowNodeMetadataResourceCollection)
	s.mux.HandleFunc("/api/resources/workflow-node-metadata/", s.handleWorkflowNodeMetadataResourceItem)
	s.mux.HandleFunc("/api/resources/expression-helpers", s.handleExpressionHelperResourceCollection)
	s.mux.HandleFunc("/api/resources/expression-helpers/", s.handleExpressionHelperResourceItem)
	s.mux.HandleFunc("/api/resources/team-templates", s.handleTeamTemplateResourceCollection)
	s.mux.HandleFunc("/api/resources/team-templates/", s.handleTeamTemplateResourceItem)
	s.mux.HandleFunc("/api/resources/policy-rules", s.handlePolicyRuleResourceCollection)
	s.mux.HandleFunc("/api/resources/policy-rules/", s.handlePolicyRuleResourceItem)
	s.mux.HandleFunc("/api/resources/kits", s.handleKitResourceCollection)
	s.mux.HandleFunc("/api/resources/kits/", s.handleKitResourceItem)
	s.mux.HandleFunc("/api/approvals/approve-all", s.handleApproveAll)
	s.mux.HandleFunc("/api/approvals/", s.handleApprovalAction)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

type runRequest struct {
	Input      string `json:"input"`
	Approve    *bool  `json:"approve,omitempty"`
	Background bool   `json:"background,omitempty"`
}

func decodeRunRequest(r *http.Request) (runRequest, error) {
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return runRequest{}, err
	}
	return req, nil
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req, err := decodeRunRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if !s.ensureWorkspaceConfirmedForRun(w, req.Input) {
		return
	}
	if req.Background {
		accepted, err := s.startAgentBackground(req.Input, "start")
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if !s.agentExecMu.TryLock() {
		http.Error(w, "another agent run is already active", http.StatusConflict)
		return
	}
	defer s.agentExecMu.Unlock()
	input, err := s.expandAtReferences(r.Context(), req.Input, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := s.runtime.RunStream(r.Context(), input, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	snapshot := s.sessionSnapshotWithApprovalRisk()
	if requestWantsFullSession(r) {
		snapshot = s.runtime.HydrateSnapshot(snapshot)
	}
	_ = json.NewEncoder(w).Encode(sessionSnapshotForAPI(snapshot, requestWantsFullSession(r)))
}

type sessionCompactRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (s *Server) handleSessionCompact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runtime == nil {
		http.Error(w, "runtime not configured", http.StatusServiceUnavailable)
		return
	}
	var req sessionCompactRequest
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil && err.Error() != "EOF" {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
	}
	summary, err := s.runtime.CompactContext(r.Context(), req.Reason)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, summary)
}

func (s *Server) handleWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workflowName, stream := parseWorkflowPath(r.URL.Path)
	if stream {
		s.handleWorkflowStream(w, r, workflowName)
		return
	}
	req, err := decodeRunRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if !s.ensureWorkspaceConfirmedForJSON(w, "workflow execution runs workspace-scoped stages") {
		return
	}
	input, err := s.expandAtReferences(r.Context(), req.Input, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Background {
		accepted, err := s.startWorkflowBackground(workflowName, input, "", workflowRequestApprove(req), "start")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	result, err := s.runWorkflowWithCancel(r.Context(), workflowName, input, "", workflowRequestApprove(req), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleApprovalAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/approvals/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		if len(parts) == 3 && parts[2] == "stream" {
			s.handleApprovalActionStream(w, r, parts[0], parts[1])
			return
		}
		http.NotFound(w, r)
		return
	}
	callID, action := parts[0], parts[1]
	pending := pendingApprovalSummary(s.sessionSnapshotWithApprovalRisk().PendingApprovals, callID)
	if strings.TrimSpace(pending.AgentRunID) != "" && strings.TrimSpace(pending.WorkflowName) == "" {
		runAction, ok := legacyApprovalAgentRunAction(action)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if !s.agentRunToolApprovalResumable(callID) {
			http.Error(w, agentRunToolApprovalActionReason(false), http.StatusConflict)
			return
		}
		if err := s.runAgentRunToolApprovalAction(r.Context(), pending.AgentRunID, callID, runAction, nil); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if updated, ok := s.runtime.AgentRun(pending.AgentRunID); ok {
			writeJSON(w, s.agentRunWithApprovalRisk(updated))
			return
		}
		http.NotFound(w, r)
		return
	}
	var (
		result schema.ToolResult
		err    error
	)
	switch action {
	case "approve":
		result, err = s.runtime.ApproveToolCall(r.Context(), callID)
	case "approve-remember":
		result, err = s.runtime.ApproveToolCallAndRemember(r.Context(), callID)
	case "deny":
		result, err = s.runtime.DenyToolCall(callID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleApproveAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	results, err := s.runtime.ApproveAllPendingToolCalls(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) handleRunStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req, err := decodeRunRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if !s.ensureWorkspaceConfirmedForStream(w, req.Input) {
		return
	}
	if req.Background {
		accepted, err := s.startAgentBackground(req.Input, "start")
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if !s.agentExecMu.TryLock() {
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: "another agent run is already active", IsError: true})
		return
	}
	defer s.agentExecMu.Unlock()
	writer, ok := newSSEWriter(w)
	if !ok {
		return
	}
	write := s.streamHandlerWithApprovalRisk(writer.write)
	input, err := s.expandAtReferences(r.Context(), req.Input, write)
	if err != nil {
		_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		return
	}
	_, err = s.runtime.RunStream(r.Context(), input, write)
	if err != nil {
		_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
	}
}

func (s *Server) handleWorkflowStream(w http.ResponseWriter, r *http.Request, workflowName string) {
	req, err := decodeRunRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if !s.ensureWorkspaceConfirmedForWorkflowStream(w) {
		return
	}
	writer, ok := newSSEWriter(w)
	if !ok {
		return
	}
	write := s.streamHandlerWithApprovalRisk(writer.write)
	input, err := s.expandAtReferences(r.Context(), req.Input, write)
	if err != nil {
		_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		return
	}
	result, err := s.runWorkflowWithCancel(r.Context(), workflowName, input, "", workflowRequestApprove(req), write)
	if err != nil {
		_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		return
	}
	_ = writer.writeWorkflowResult(result)
}

func (s *Server) handleApprovalActionStream(w http.ResponseWriter, r *http.Request, callID, action string) {
	writer, ok := newSSEWriter(w)
	if !ok {
		return
	}
	write := s.streamHandlerWithApprovalRisk(writer.write)
	pending := pendingApprovalSummary(s.sessionSnapshotWithApprovalRisk().PendingApprovals, callID)
	if strings.TrimSpace(pending.AgentRunID) != "" && strings.TrimSpace(pending.WorkflowName) == "" {
		runAction, ok := legacyApprovalAgentRunAction(action)
		if !ok {
			_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: "unknown approval action", IsError: true})
			return
		}
		if err := s.runAgentRunToolApprovalAction(r.Context(), pending.AgentRunID, callID, runAction, write); err != nil {
			_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		}
		return
	}
	if strings.TrimSpace(pending.WorkflowName) != "" {
		approve, remember, ok := approvalAction(action)
		if !ok {
			_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: "unknown approval action", IsError: true})
			return
		}
		if approve && remember {
			if err := s.runtime.RememberPendingWorkflowToolApproval(callID); err != nil {
				_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
				return
			}
		}
		result, err := s.runtime.WorkflowRunner().Resume(r.Context(), pending.WorkflowName, callID, approve, write)
		if err != nil {
			_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
			return
		}
		_ = writer.writeWorkflowResult(result)
		return
	}
	var (
		result schema.ToolResult
		err    error
	)
	switch action {
	case "approve":
		result, err = s.runtime.ApproveToolCall(r.Context(), callID)
	case "approve-remember":
		result, err = s.runtime.ApproveToolCallAndRemember(r.Context(), callID)
	case "deny":
		result, err = s.runtime.DenyToolCall(callID)
	default:
		_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: "unknown approval action", IsError: true})
		return
	}
	if err != nil {
		_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		return
	}
	_ = write(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, Content: result.Content, IsError: result.IsError, Suspended: result.Suspended})
	resumeResult, resumed, err := s.runtime.ResumeApprovedOrdinaryToolCall(r.Context(), callID, write)
	if err != nil {
		_ = write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		return
	}
	if resumed {
		_ = write(schema.StreamEvent{Type: schema.StreamEventFinalMessage, Content: resumeResult.Output, AgentID: resumeResult.AgentID, Mode: resumeResult.Mode})
	}
}

func legacyApprovalAgentRunAction(action string) (string, bool) {
	switch action {
	case "approve":
		return "approve_tool", true
	case "approve-remember":
		return "approve_remember_tool", true
	case "deny":
		return "deny_tool", true
	default:
		return "", false
	}
}

func SessionSnapshotResponse(snapshot session.Snapshot) session.Snapshot {
	return snapshot
}

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

const durableRunSSEHeartbeatInterval = 15 * time.Second
const durableRunSSERetryMillis = 2000

func newSSEWriter(w http.ResponseWriter) (sseWriter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return sseWriter{}, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	return sseWriter{w: w, flusher: flusher}, true
}

func (s sseWriter) write(event schema.StreamEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event.Type, payload); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s sseWriter) writeComment(comment string) error {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		comment = "heartbeat"
	}
	comment = strings.ReplaceAll(comment, "\r", " ")
	comment = strings.ReplaceAll(comment, "\n", " ")
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", comment); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s sseWriter) writeRetry(milliseconds int) error {
	if milliseconds <= 0 {
		return nil
	}
	if _, err := fmt.Fprintf(s.w, "retry: %d\n\n", milliseconds); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s sseWriter) writeNamedJSON(name string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s sseWriter) writeWorkflowRunEvent(event session.WorkflowRunEventSnapshot) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if event.Seq > 0 {
		if _, err := fmt.Fprintf(s.w, "id: %d\n", event.Seq); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "event: workflow_run_event\ndata: %s\n\n", payload); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s sseWriter) writeAgentRunEvent(event session.AgentRunEventSnapshot) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if event.Seq > 0 {
		if _, err := fmt.Fprintf(s.w, "id: %d\n", event.Seq); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "event: agent_run_event\ndata: %s\n\n", payload); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s sseWriter) writeWorkflowResult(result agent.WorkflowResult) error {
	converted := convertWorkflowResult(result)
	content, _ := json.Marshal(converted)
	return s.write(schema.StreamEvent{
		Type:                        schema.StreamEventWorkflowResult,
		RunID:                       result.RunID,
		Content:                     string(content),
		WorkflowName:                result.Name,
		WorkflowStatus:              result.Status,
		NextStage:                   string(result.NextStage),
		PendingApproval:             result.PendingApproval,
		BudgetScope:                 result.BudgetScope,
		BudgetReason:                result.BudgetReason,
		BudgetMetric:                result.BudgetMetric,
		BudgetUsed:                  result.BudgetUsed,
		BudgetSoftLimit:             result.BudgetSoftLimit,
		BudgetHardLimit:             result.BudgetHardLimit,
		BudgetRemaining:             result.BudgetRemaining,
		BudgetPromptTokens:          result.BudgetPromptTokens,
		BudgetEstimatedPromptTokens: result.BudgetEstimatedPromptTokens,
		BudgetNetPromptTokens:       result.BudgetNetPromptTokens,
		BudgetGrossPromptTokens:     result.BudgetGrossPromptTokens,
		BudgetSavedTokens:           result.BudgetSavedTokens,
		BudgetMemorySavedTokens:     result.BudgetMemorySavedTokens,
		BudgetHistorySavedTokens:    result.BudgetHistorySavedTokens,
		BudgetArtifactSavedTokens:   result.BudgetArtifactSavedTokens,
		BudgetSkillSavedTokens:      result.BudgetSkillSavedTokens,
		BudgetToolSchemaSavedTokens: result.BudgetToolSchemaSavedTokens,
		BudgetReportedPromptTokens:  result.BudgetReportedPromptTokens,
		BudgetOutputTokens:          result.BudgetOutputTokens,
		BudgetCachedTokens:          result.BudgetCachedTokens,
		BudgetTotalTokens:           result.BudgetTotalTokens,
		BudgetLLMCalls:              result.BudgetLLMCalls,
		BudgetContinuations:         result.BudgetContinuations,
		BudgetEstimatedInputCost:    result.BudgetEstimatedInputCost,
		BudgetEstimatedOutputCost:   result.BudgetEstimatedOutputCost,
		BudgetEstimatedTotalCost:    result.BudgetEstimatedTotalCost,
		BudgetCostCurrency:          result.BudgetCostCurrency,
		BudgetPricingSource:         result.BudgetPricingSource,
		WorkflowResult:              &converted,
	})
}

func parseWorkflowPath(path string) (string, bool) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/workflows/"), "/"), "/")
	if len(parts) == 2 && parts[1] == "stream" {
		return parts[0], true
	}
	if len(parts) == 0 {
		return "", false
	}
	return parts[0], false
}

func workflowRequestApprove(req runRequest) bool {
	if req.Approve == nil {
		return true
	}
	return *req.Approve
}

func pendingApprovalSummary(items []session.PendingApprovalSnapshot, callID string) session.PendingApprovalSnapshot {
	for _, item := range items {
		if item.CallID == callID {
			return item
		}
	}
	return session.PendingApprovalSnapshot{}
}

func approvalAction(action string) (bool, bool, bool) {
	switch action {
	case "approve":
		return true, false, true
	case "approve-remember":
		return true, true, true
	case "deny":
		return false, false, true
	default:
		return false, false, false
	}
}

func convertWorkflowResult(result agent.WorkflowResult) schema.WorkflowResult {
	stages := make([]schema.WorkflowStageResult, 0, len(result.CompletedStages))
	for _, stage := range result.CompletedStages {
		stages = append(stages, schema.WorkflowStageResult{
			Stage:                       string(stage.Stage),
			AgentID:                     stage.Agent,
			NodeType:                    stage.NodeType,
			Skill:                       stage.Skill,
			Tool:                        stage.Tool,
			Status:                      stage.Status,
			Attempts:                    stage.Attempts,
			Inputs:                      copyWorkflowStringMap(stage.Input),
			InputValues:                 copyWorkflowAnyMap(stage.InputValues),
			Outputs:                     copyWorkflowStringMap(stage.Output.Variables),
			OutputValues:                copyWorkflowAnyMap(stage.Output.Values),
			Metadata:                    copyWorkflowStringMap(stage.Metadata),
			Result:                      stage.Result,
			BudgetScope:                 stage.BudgetScope,
			BudgetReason:                stage.BudgetReason,
			BudgetMetric:                stage.BudgetMetric,
			BudgetUsed:                  stage.BudgetUsed,
			BudgetSoftLimit:             stage.BudgetSoftLimit,
			BudgetHardLimit:             stage.BudgetHardLimit,
			BudgetRemaining:             stage.BudgetRemaining,
			BudgetPromptTokens:          stage.BudgetPromptTokens,
			BudgetEstimatedPromptTokens: stage.BudgetEstimatedPromptTokens,
			BudgetNetPromptTokens:       stage.BudgetNetPromptTokens,
			BudgetGrossPromptTokens:     stage.BudgetGrossPromptTokens,
			BudgetSavedTokens:           stage.BudgetSavedTokens,
			BudgetMemorySavedTokens:     stage.BudgetMemorySavedTokens,
			BudgetHistorySavedTokens:    stage.BudgetHistorySavedTokens,
			BudgetArtifactSavedTokens:   stage.BudgetArtifactSavedTokens,
			BudgetSkillSavedTokens:      stage.BudgetSkillSavedTokens,
			BudgetToolSchemaSavedTokens: stage.BudgetToolSchemaSavedTokens,
			BudgetReportedPromptTokens:  stage.BudgetReportedPromptTokens,
			BudgetOutputTokens:          stage.BudgetOutputTokens,
			BudgetCachedTokens:          stage.BudgetCachedTokens,
			BudgetTotalTokens:           stage.BudgetTotalTokens,
			BudgetLLMCalls:              stage.BudgetLLMCalls,
			BudgetContinuations:         stage.BudgetContinuations,
			BudgetEstimatedInputCost:    stage.BudgetEstimatedInputCost,
			BudgetEstimatedOutputCost:   stage.BudgetEstimatedOutputCost,
			BudgetEstimatedTotalCost:    stage.BudgetEstimatedTotalCost,
			BudgetCostCurrency:          stage.BudgetCostCurrency,
			BudgetPricingSource:         stage.BudgetPricingSource,
		})
	}
	return schema.WorkflowResult{
		RunID:                       result.RunID,
		Name:                        result.Name,
		Status:                      result.Status,
		PendingApproval:             result.PendingApproval,
		PendingInput:                result.PendingInput,
		PendingFields:               copyWorkflowInputFields(result.PendingFields),
		PendingSubWorkflow:          result.PendingSubWorkflow,
		PendingSubWorkflowName:      result.PendingSubWorkflowName,
		PendingSubWorkflowRunID:     result.PendingSubWorkflowRunID,
		PendingSubWorkflowStatus:    result.PendingSubWorkflowStatus,
		ApprovalPrompt:              result.ApprovalPrompt,
		CompletedStages:             stages,
		NextStage:                   string(result.NextStage),
		FinalSummary:                result.FinalSummary,
		BudgetScope:                 result.BudgetScope,
		BudgetReason:                result.BudgetReason,
		BudgetMetric:                result.BudgetMetric,
		BudgetUsed:                  result.BudgetUsed,
		BudgetSoftLimit:             result.BudgetSoftLimit,
		BudgetHardLimit:             result.BudgetHardLimit,
		BudgetRemaining:             result.BudgetRemaining,
		BudgetPromptTokens:          result.BudgetPromptTokens,
		BudgetEstimatedPromptTokens: result.BudgetEstimatedPromptTokens,
		BudgetNetPromptTokens:       result.BudgetNetPromptTokens,
		BudgetGrossPromptTokens:     result.BudgetGrossPromptTokens,
		BudgetSavedTokens:           result.BudgetSavedTokens,
		BudgetMemorySavedTokens:     result.BudgetMemorySavedTokens,
		BudgetHistorySavedTokens:    result.BudgetHistorySavedTokens,
		BudgetArtifactSavedTokens:   result.BudgetArtifactSavedTokens,
		BudgetSkillSavedTokens:      result.BudgetSkillSavedTokens,
		BudgetToolSchemaSavedTokens: result.BudgetToolSchemaSavedTokens,
		BudgetReportedPromptTokens:  result.BudgetReportedPromptTokens,
		BudgetOutputTokens:          result.BudgetOutputTokens,
		BudgetCachedTokens:          result.BudgetCachedTokens,
		BudgetTotalTokens:           result.BudgetTotalTokens,
		BudgetLLMCalls:              result.BudgetLLMCalls,
		BudgetContinuations:         result.BudgetContinuations,
		BudgetEstimatedInputCost:    result.BudgetEstimatedInputCost,
		BudgetEstimatedOutputCost:   result.BudgetEstimatedOutputCost,
		BudgetEstimatedTotalCost:    result.BudgetEstimatedTotalCost,
		BudgetCostCurrency:          result.BudgetCostCurrency,
		BudgetPricingSource:         result.BudgetPricingSource,
	}
}

func copyWorkflowInputFields(fields []schema.WorkflowInputField) []schema.WorkflowInputField {
	if len(fields) == 0 {
		return nil
	}
	out := make([]schema.WorkflowInputField, len(fields))
	for i, field := range fields {
		out[i] = field
		if len(field.Options) > 0 {
			out[i].Options = append([]string(nil), field.Options...)
		}
	}
	return out
}

func copyWorkflowStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func copyWorkflowAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
