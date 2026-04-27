package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

type runtimeAgentRequest struct {
	Agent string `json:"agent"`
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
