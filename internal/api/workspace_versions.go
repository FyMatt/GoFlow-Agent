package api

import (
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/taskstaging"
)

type workspaceVersionsResponse struct {
	Mode      string                `json:"mode"`
	Retention int                   `json:"retention"`
	Versions  []taskstaging.Version `json:"versions"`
}

func (s *Server) handleWorkspaceVersions(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.runtime == nil {
		http.Error(w, "runtime is unavailable", http.StatusServiceUnavailable)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workspace-versions"), "/")
	if path == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		versions, err := s.runtime.WorkspaceVersions()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, workspaceVersionsResponse{Mode: "live", Retention: taskstaging.DefaultVersionRetention, Versions: versions})
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "restore" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	versions, err := s.runtime.WorkspaceVersions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var selected *taskstaging.Version
	for index := range versions {
		if versions[index].RunID == parts[0] {
			selected = &versions[index]
			break
		}
	}
	if selected == nil {
		http.Error(w, "workspace version not found", http.StatusNotFound)
		return
	}
	if !selected.CanRestore {
		reason := strings.TrimSpace(selected.Reason)
		if reason == "" {
			reason = "workspace version cannot be restored from the current file state"
		}
		http.Error(w, reason, http.StatusConflict)
		return
	}
	state, err := s.runtime.RestoreTaskStaging(selected.RunID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{
		"status":     "restored",
		"run_id":     selected.RunID,
		"version_id": selected.VersionID,
		"recovery":   state,
	})
}
