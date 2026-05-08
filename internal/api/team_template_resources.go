package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
)

type teamTemplateValidationResult struct {
	Valid      bool                      `json:"valid"`
	Name       string                    `json:"name,omitempty"`
	Normalized *agent.TeamTemplate       `json:"normalized,omitempty"`
	Issues     []resourceValidationIssue `json:"issues,omitempty"`
}

func (s *Server) handleTeamTemplateResourceCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.TeamTemplateResources())
	case http.MethodPost:
		if strings.Trim(r.URL.Path, "/") == "api/resources/team-templates/validate" {
			s.handleTeamTemplateResourceValidate(w, r, "")
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTeamTemplateResourceItem(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/team-templates/"), "/"), "/")
	if len(parts) > 0 && parts[0] == "scaffolds" {
		s.handleTeamTemplateScaffolds(w, r, parts[1:])
		return
	}
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handleTeamTemplateResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		s.handleTeamTemplateResourceValidate(w, r, parts[0])
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSpace(parts[0])
	if name == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		template, ok := s.runtime.TeamTemplate(name)
		if !ok || !template.Custom {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, template)
	case http.MethodPut:
		template, err := decodeTeamTemplateResource(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := s.runtime.SaveTeamTemplateResource(name, template)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.runtime.DeleteTeamTemplateResource(name); err != nil {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTeamTemplateResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	template, err := decodeTeamTemplateResource(r)
	if err != nil {
		writeJSON(w, teamTemplateValidationResult{
			Valid: false,
			Name:  strings.TrimSpace(name),
			Issues: []resourceValidationIssue{
				resourceValidationIssueFor("team_template", "body", err.Error()),
			},
		})
		return
	}
	normalized, err := s.runtime.ValidateTeamTemplateResource(name, template)
	if err != nil {
		writeJSON(w, teamTemplateValidationResult{
			Valid: false,
			Name:  firstNonEmptyTeamTemplateValidationName(name, template.Name),
			Issues: []resourceValidationIssue{
				resourceValidationIssueFor("team_template", teamTemplateValidationField(err.Error()), err.Error()),
			},
		})
		return
	}
	writeJSON(w, teamTemplateValidationResult{
		Valid:      true,
		Name:       normalized.Name,
		Normalized: &normalized,
	})
}

func decodeTeamTemplateResource(r *http.Request) (agent.TeamTemplate, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return agent.TeamTemplate{}, err
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return agent.TeamTemplate{}, fmt.Errorf("team template body is required")
	}
	var template agent.TeamTemplate
	if err := json.Unmarshal(data, &template); err != nil {
		return agent.TeamTemplate{}, fmt.Errorf("invalid team template json")
	}
	return template, nil
}

func firstNonEmptyTeamTemplateValidationName(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func teamTemplateValidationField(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "version"):
		return "version"
	case strings.Contains(lower, "kind"):
		return "kind"
	case strings.Contains(lower, "name"):
		return "name"
	case strings.Contains(lower, "title"):
		return "title"
	case strings.Contains(lower, "handoff"):
		return "handoffs"
	case strings.Contains(lower, "blackboard"):
		return "blackboard_templates"
	case strings.Contains(lower, "quorum"):
		return "quorum_presets"
	case strings.Contains(lower, "role"):
		return "role_templates"
	default:
		return ""
	}
}
