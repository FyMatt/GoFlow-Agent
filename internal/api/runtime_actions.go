package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

type runtimeAgentRequest struct {
	Agent string `json:"agent"`
}

type runtimeTraceRequest struct {
	Trace   *bool `json:"trace,omitempty"`
	Enabled *bool `json:"enabled,omitempty"`
}

func (s *Server) handleRuntimeAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req runtimeAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	agentID := strings.TrimSpace(req.Agent)
	if agentID == "" {
		http.Error(w, "agent is required", http.StatusBadRequest)
		return
	}
	if err := s.runtime.SetActiveAgent(agentID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, s.runtimeStatus(r))
}

func (s *Server) handleRuntimeTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req runtimeTraceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	enabled, ok := runtimeTraceRequestValue(req)
	if !ok {
		http.Error(w, "trace or enabled is required", http.StatusBadRequest)
		return
	}
	s.runtime.SetTrace(enabled)
	writeJSON(w, s.runtimeStatus(r))
}

func runtimeTraceRequestValue(req runtimeTraceRequest) (bool, bool) {
	switch {
	case req.Trace != nil:
		return *req.Trace, true
	case req.Enabled != nil:
		return *req.Enabled, true
	default:
		return false, false
	}
}
