package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type memoryProjectUpdateRequest struct {
	Content string `json:"content"`
}

type memorySolutionLifecycleRequest struct {
	Reason       string `json:"reason,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

func (s *Server) handleMemoryDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store := s.runtime.MemoryStore()
	if store == nil {
		http.Error(w, "memory store not configured", http.StatusServiceUnavailable)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("tasks")))
	if limit <= 0 {
		limit = 12
	}
	if s.runtime != nil {
		s.runtime.BackfillWorkflowTaskMemory(60)
	}
	if _, _, err := store.EnsureProjectProfile(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dashboard, err := store.Dashboard(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, dashboard)
}

func (s *Server) handleMemoryProject(w http.ResponseWriter, r *http.Request) {
	store := s.runtime.MemoryStore()
	if store == nil {
		http.Error(w, "memory store not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if _, _, err := store.EnsureProjectProfile(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		project, err := store.Project()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, project)
	case http.MethodPut:
		var req memoryProjectUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		project, err := store.UpdateProject(req.Content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, project)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store := s.runtime.MemoryStore()
	if store == nil {
		http.Error(w, "memory store not configured", http.StatusServiceUnavailable)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	query := strings.TrimSpace(firstWorkflowRunQueryValue(r.URL.Query().Get("q"), r.URL.Query().Get("query"), r.URL.Query().Get("search")))
	results, err := store.Search(query, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, results)
}

func (s *Server) handleMemoryRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store := s.runtime.MemoryStore()
	if store == nil {
		http.Error(w, "memory store not configured", http.StatusServiceUnavailable)
		return
	}
	index, err := store.RebuildFiles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, index)
}

func (s *Server) handleMemorySolutionAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store := s.runtime.MemoryStore()
	if store == nil {
		http.Error(w, "memory store not configured", http.StatusServiceUnavailable)
		return
	}
	id, action := parseMemorySolutionActionPath(r.URL.Path)
	if id == "" || action == "" {
		http.NotFound(w, r)
		return
	}
	var req memorySolutionLifecycleRequest
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil && err.Error() != "EOF" {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
	}
	var (
		kb  any
		err error
	)
	switch action {
	case "retire":
		kb, err = store.RetireSolution(id, req.Reason, req.SupersededBy)
	case "restore":
		kb, err = store.RestoreSolution(id)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, kb)
}

func parseMemorySolutionActionPath(rawPath string) (string, string) {
	rest := strings.TrimPrefix(rawPath, "/api/memory/solutions/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", ""
	}
	id := strings.TrimSpace(parts[0])
	action := strings.ToLower(strings.TrimSpace(parts[1]))
	if id == "" || action == "" {
		return "", ""
	}
	return id, action
}

func (s *Server) handleArtifactObjectItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hash := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/artifacts/"), "/")
	if hash == "" {
		http.NotFound(w, r)
		return
	}
	object, ok, err := s.runtime.ArtifactObject(hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !workflowRunQueryBool(r.URL.Query().Get("include_content")) && !workflowRunQueryBool(r.URL.Query().Get("content")) {
		object.Content = ""
	}
	writeJSON(w, object)
}

func (s *Server) handleArtifactObjectCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	index, err := s.runtime.ArtifactObjects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit > 0 && len(index.Objects) > limit {
		index.Objects = index.Objects[:limit]
	}
	writeJSON(w, index)
}
