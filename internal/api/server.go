package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type Server struct {
	runtime *agent.Runtime
	mux     *http.ServeMux
}

func NewServer(runtime *agent.Runtime) *Server {
	s := &Server{runtime: runtime, mux: http.NewServeMux()}
	s.mux.HandleFunc("/api/run", s.handleRun)
	s.mux.HandleFunc("/api/run/stream", s.handleRunStream)
	s.mux.HandleFunc("/api/session", s.handleSession)
	s.mux.HandleFunc("/api/workflows/", s.handleWorkflow)
	s.mux.HandleFunc("/api/approvals/approve-all", s.handleApproveAll)
	s.mux.HandleFunc("/api/approvals/", s.handleApprovalAction)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

type runRequest struct {
	Input string `json:"input"`
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
	result, err := s.runtime.RunStream(r.Context(), req.Input, nil)
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
	_ = json.NewEncoder(w).Encode(s.runtime.SessionSnapshot())
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
	result, err := s.runtime.WorkflowRunner().Run(r.Context(), workflowName, req.Input, true, nil)
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
	var (
		result schema.ToolResult
		err    error
	)
	switch action {
	case "approve":
		result, err = s.runtime.ApproveToolCall(r.Context(), callID)
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
	writer, ok := newSSEWriter(w)
	if !ok {
		return
	}
	_, err = s.runtime.RunStream(r.Context(), req.Input, writer.write)
	if err != nil {
		_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
	}
}

func (s *Server) handleWorkflowStream(w http.ResponseWriter, r *http.Request, workflowName string) {
	req, err := decodeRunRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	writer, ok := newSSEWriter(w)
	if !ok {
		return
	}
	result, err := s.runtime.WorkflowRunner().Run(r.Context(), workflowName, req.Input, true, writer.write)
	if err != nil {
		_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		return
	}
	_ = writer.writeWorkflowResult(result)
}

func (s *Server) handleApprovalActionStream(w http.ResponseWriter, r *http.Request, callID, action string) {
	writer, ok := newSSEWriter(w)
	if !ok {
		return
	}
	pending := pendingApprovalSummary(s.runtime.SessionSnapshot().PendingApprovals, callID)
	if strings.TrimSpace(pending.WorkflowName) != "" {
		approve, ok := approvalAction(action)
		if !ok {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: "unknown approval action", IsError: true})
			return
		}
		result, err := s.runtime.WorkflowRunner().Resume(r.Context(), pending.WorkflowName, callID, approve, writer.write)
		if err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
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
	case "deny":
		result, err = s.runtime.DenyToolCall(callID)
	default:
		_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: "unknown approval action", IsError: true})
		return
	}
	if err != nil {
		_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		return
	}
	_ = writer.write(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, Content: result.Content, IsError: result.IsError, Suspended: result.Suspended})
	resumeResult, resumed, err := s.runtime.ResumeApprovedOrdinaryToolCall(r.Context(), callID, writer.write)
	if err != nil {
		_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		return
	}
	if resumed {
		_ = writer.write(schema.StreamEvent{Type: schema.StreamEventFinalMessage, Content: resumeResult.Output, AgentID: resumeResult.AgentID, Mode: resumeResult.Mode})
	}
}

func SessionSnapshotResponse(snapshot session.Snapshot) session.Snapshot {
	return snapshot
}

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

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

func (s sseWriter) writeWorkflowResult(result agent.WorkflowResult) error {
	converted := convertWorkflowResult(result)
	content, _ := json.Marshal(converted)
	return s.write(schema.StreamEvent{
		Type:            schema.StreamEventWorkflowResult,
		Content:         string(content),
		WorkflowName:    result.Name,
		WorkflowStatus:  result.Status,
		NextStage:       string(result.NextStage),
		PendingApproval: result.PendingApproval,
		WorkflowResult:  &converted,
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

func pendingApprovalSummary(items []session.PendingApprovalSnapshot, callID string) session.PendingApprovalSnapshot {
	for _, item := range items {
		if item.CallID == callID {
			return item
		}
	}
	return session.PendingApprovalSnapshot{}
}

func approvalAction(action string) (bool, bool) {
	switch action {
	case "approve":
		return true, true
	case "deny":
		return false, true
	default:
		return false, false
	}
}

func convertWorkflowResult(result agent.WorkflowResult) schema.WorkflowResult {
	stages := make([]schema.WorkflowStageResult, 0, len(result.CompletedStages))
	for _, stage := range result.CompletedStages {
		stages = append(stages, schema.WorkflowStageResult{
			Stage:   string(stage.Stage),
			AgentID: stage.Agent,
			Result:  stage.Result,
		})
	}
	return schema.WorkflowResult{
		Name:            result.Name,
		Status:          result.Status,
		PendingApproval: result.PendingApproval,
		ApprovalPrompt:  result.ApprovalPrompt,
		CompletedStages: stages,
		NextStage:       string(result.NextStage),
		FinalSummary:    result.FinalSummary,
	}
}
