package api

import (
	"net/http"
	"strings"
)

func (s *Server) handleTeamTemplateCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.runtime.TeamTemplates())
}

func (s *Server) handleTeamTemplateItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/team-templates/"), "/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	template, ok := s.runtime.TeamTemplate(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, template)
}
