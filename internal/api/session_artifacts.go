package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func (s *Server) handleSessionArtifactCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.runtime.SessionArtifacts(sessionArtifactFilterFromRequest(r)))
}

func (s *Server) handleSessionArtifactItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/session-artifacts/"), "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	artifact, ok := s.runtime.SessionArtifact(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, artifact)
}

func sessionArtifactFilterFromRequest(r *http.Request) session.SessionArtifactFilter {
	if r == nil || r.URL == nil {
		return session.SessionArtifactFilter{}
	}
	values := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(values.Get("limit")))
	if limit < 0 {
		limit = 0
	}
	if limit > 200 {
		limit = 200
	}
	return session.SessionArtifactFilter{
		Kind:    strings.TrimSpace(values.Get("kind")),
		Tool:    strings.TrimSpace(values.Get("tool")),
		Agent:   strings.TrimSpace(values.Get("agent")),
		Query:   strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("q"), values.Get("query"), values.Get("search"))),
		Limit:   limit,
		Content: workflowRunQueryBool(values.Get("include_content")) || workflowRunQueryBool(values.Get("content")),
	}
}
