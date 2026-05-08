package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"gopkg.in/yaml.v3"
)

type teamTemplateScaffoldPreset struct {
	Name                  string              `json:"name"`
	DefaultName           string              `json:"default_name"`
	Title                 string              `json:"title"`
	Description           string              `json:"description"`
	Category              string              `json:"category,omitempty"`
	Tags                  []string            `json:"tags,omitempty"`
	BaseTemplate          string              `json:"base_template"`
	RecommendedWorkflow   string              `json:"recommended_workflow,omitempty"`
	RecommendedEntryAgent string              `json:"recommended_entry_agent,omitempty"`
	RoleCount             int                 `json:"roles,omitempty"`
	QuorumPresetCount     int                 `json:"quorum_presets,omitempty"`
	Document              *agent.TeamTemplate `json:"document,omitempty"`
}

type teamTemplateScaffoldRequest struct {
	Name                  string   `json:"name" yaml:"name"`
	Title                 string   `json:"title" yaml:"title"`
	Description           string   `json:"description" yaml:"description"`
	Category              string   `json:"category" yaml:"category"`
	Tags                  []string `json:"tags" yaml:"tags"`
	RecommendedWorkflow   string   `json:"recommended_workflow" yaml:"recommended_workflow"`
	RecommendedEntryAgent string   `json:"recommended_entry_agent" yaml:"recommended_entry_agent"`
	Overwrite             bool     `json:"overwrite" yaml:"overwrite"`
}

type teamTemplateScaffoldResponse struct {
	Created bool                       `json:"created"`
	Updated bool                       `json:"updated,omitempty"`
	Preset  teamTemplateScaffoldPreset `json:"preset"`
	Team    agent.TeamTemplate         `json:"team"`
}

func (s *Server) handleTeamTemplateScaffolds(w http.ResponseWriter, r *http.Request, parts []string) {
	switch r.Method {
	case http.MethodGet:
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			writeJSON(w, s.teamTemplateScaffoldPresets(false))
			return
		}
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		preset, ok := s.teamTemplateScaffoldPreset(parts[0], true)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if name := normalizeResourceName(r.URL.Query().Get("name")); name != "" {
			doc, err := teamTemplateFromScaffold(preset, teamTemplateScaffoldRequest{Name: name})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			preset.Document = &doc
		}
		writeJSON(w, preset)
	case http.MethodPost:
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		preset, ok := s.teamTemplateScaffoldPreset(parts[0], true)
		if !ok {
			http.NotFound(w, r)
			return
		}
		req, err := decodeTeamTemplateScaffoldRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if truthyQuery(r.URL.Query().Get("overwrite")) {
			req.Overwrite = true
		}
		doc, err := teamTemplateFromScaffold(preset, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, existed := s.runtime.TeamTemplate(doc.Name)
		if existed && !req.Overwrite {
			http.Error(w, fmt.Sprintf("team template %q already exists; pass overwrite=true to replace it", doc.Name), http.StatusConflict)
			return
		}
		saved, err := s.runtime.SaveTeamTemplateResource(doc.Name, doc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response := teamTemplateScaffoldResponse{Created: !existed, Updated: existed, Preset: preset, Team: saved}
		w.Header().Set("Content-Type", "application/json")
		if existed {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(response)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func decodeTeamTemplateScaffoldRequest(r *http.Request) (teamTemplateScaffoldRequest, error) {
	var req teamTemplateScaffoldRequest
	if r.Body == nil {
		return req, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return req, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return req, nil
	}
	if err := json.Unmarshal(data, &req); err != nil {
		if yamlErr := yaml.Unmarshal(data, &req); yamlErr != nil {
			return req, fmt.Errorf("invalid json/yaml")
		}
	}
	return req, nil
}

func (s *Server) teamTemplateScaffoldPreset(name string, includeDocument bool) (teamTemplateScaffoldPreset, bool) {
	name = normalizeResourceName(name)
	for _, preset := range s.teamTemplateScaffoldPresets(includeDocument) {
		if preset.Name == name {
			return preset, true
		}
	}
	return teamTemplateScaffoldPreset{}, false
}

func (s *Server) teamTemplateScaffoldPresets(includeDocument bool) []teamTemplateScaffoldPreset {
	presets := []teamTemplateScaffoldPreset{
		{
			Name:                  "software-review",
			DefaultName:           "custom-software-review-team",
			Title:                 "Custom Software Review Team",
			Description:           "Planner, implementer, reviewer, and reporter team for code changes with reusable review quorum.",
			Category:              "software",
			Tags:                  []string{"software", "review", "quality"},
			BaseTemplate:          "software-task-team",
			RecommendedWorkflow:   "plan-fix-audit",
			RecommendedEntryAgent: "planner",
		},
		{
			Name:                  "security-review",
			DefaultName:           "custom-security-review-team",
			Title:                 "Custom Security Review Team",
			Description:           "Scope, analysis, audit, and policy-review team for evidence-backed security reviews.",
			Category:              "security",
			Tags:                  []string{"security", "audit", "evidence"},
			BaseTemplate:          "audit-security-team",
			RecommendedWorkflow:   "human-input-security-review",
			RecommendedEntryAgent: "auditor",
		},
		{
			Name:                  "web-research",
			DefaultName:           "custom-web-research-team",
			Title:                 "Custom Web Research Team",
			Description:           "Asset collection, JavaScript review, and risk-analysis team for scoped web research.",
			Category:              "security",
			Tags:                  []string{"web", "assets", "risk"},
			BaseTemplate:          "web-research-team",
			RecommendedWorkflow:   "web-research-risk",
			RecommendedEntryAgent: "auditor",
		},
		{
			Name:                  "binary-triage",
			DefaultName:           "custom-binary-triage-team",
			Title:                 "Custom Binary Triage Team",
			Description:           "Static binary triage, reverse-analysis, vulnerability review, and report handoff team.",
			Category:              "security",
			Tags:                  []string{"binary", "reverse", "triage"},
			BaseTemplate:          "binary-triage-team",
			RecommendedWorkflow:   "binary-triage",
			RecommendedEntryAgent: "auditor",
		},
		{
			Name:                  "documentation",
			DefaultName:           "custom-documentation-team",
			Title:                 "Custom Documentation Team",
			Description:           "Plan, draft, review, and publication-handoff team for documentation changes.",
			Category:              "documentation",
			Tags:                  []string{"docs", "review", "publish"},
			BaseTemplate:          "documentation-team",
			RecommendedWorkflow:   "docs-review-publish",
			RecommendedEntryAgent: "planner",
		},
		{
			Name:                  "operations-runbook",
			DefaultName:           "custom-operations-runbook-team",
			Title:                 "Custom Operations Runbook Team",
			Description:           "Operational planning, runbook authoring, risk review, and operator handoff team.",
			Category:              "operations",
			Tags:                  []string{"ops", "runbook", "approval"},
			BaseTemplate:          "operations-runbook-team",
			RecommendedWorkflow:   "operations-runbook",
			RecommendedEntryAgent: "planner",
		},
		{
			Name:                  "customer-support",
			DefaultName:           "custom-customer-support-team",
			Title:                 "Custom Customer Support Team",
			Description:           "Triage, context gathering, response drafting, and support-review handoff team.",
			Category:              "support",
			Tags:                  []string{"support", "triage", "response"},
			BaseTemplate:          "customer-support-team",
			RecommendedWorkflow:   "customer-support-triage",
			RecommendedEntryAgent: "chat",
		},
	}
	for i := range presets {
		if doc, err := teamTemplateFromScaffold(presets[i], teamTemplateScaffoldRequest{Name: presets[i].DefaultName}); err == nil {
			presets[i].RoleCount = len(doc.RoleTemplates)
			presets[i].QuorumPresetCount = len(doc.QuorumPresets)
			if includeDocument {
				presets[i].Document = &doc
			}
		}
	}
	return presets
}

func teamTemplateFromScaffold(preset teamTemplateScaffoldPreset, req teamTemplateScaffoldRequest) (agent.TeamTemplate, error) {
	name := normalizeResourceName(firstWorkflowRunQueryValue(req.Name, preset.DefaultName, preset.Name))
	if !resourceNamePattern.MatchString(name) {
		return agent.TeamTemplate{}, fmt.Errorf("invalid team template name %q: use lowercase letters, numbers, hyphen, or underscore", name)
	}
	base, ok := agent.LoadTeamTemplate(preset.BaseTemplate)
	if !ok {
		return agent.TeamTemplate{}, fmt.Errorf("base team template %q is unavailable", preset.BaseTemplate)
	}
	base.Name = name
	base.Title = firstWorkflowRunQueryValue(req.Title, preset.Title, base.Title)
	base.Description = firstWorkflowRunQueryValue(req.Description, preset.Description, base.Description)
	base.Category = firstWorkflowRunQueryValue(req.Category, preset.Category, base.Category)
	base.Tags = append([]string(nil), firstTeamTemplateTags(req.Tags, preset.Tags, base.Tags)...)
	base.RecommendedWorkflow = firstWorkflowRunQueryValue(req.RecommendedWorkflow, preset.RecommendedWorkflow, base.RecommendedWorkflow)
	base.RecommendedEntryAgent = firstWorkflowRunQueryValue(req.RecommendedEntryAgent, preset.RecommendedEntryAgent, base.RecommendedEntryAgent)
	base.Source = ""
	base.Path = ""
	base.Custom = false
	base.Kind = ""
	base.Version = 0
	base.MinVersion = 0
	base.MigratedFromVersion = 0
	if len(base.QuorumPresets) == 0 {
		base.QuorumPresets = defaultTeamTemplateQuorumPresets(base.RoleTemplates)
	}
	base.Roles = len(base.RoleTemplates)
	base.QuorumPresetCount = len(base.QuorumPresets)
	return base, nil
}

func firstTeamTemplateTags(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func defaultTeamTemplateQuorumPresets(roles []agent.TeamRoleTemplate) []agent.TeamQuorumPreset {
	if len(roles) == 0 {
		return nil
	}
	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		if strings.TrimSpace(role.Name) != "" {
			roleNames = append(roleNames, role.Name)
		}
	}
	if len(roleNames) == 0 {
		return nil
	}
	required := 1
	if len(roleNames) >= 3 {
		required = 2
	}
	return []agent.TeamQuorumPreset{{
		Name:         "default-review",
		Title:        "Default review quorum",
		Description:  "Reusable review gate preset generated from this team scaffold.",
		Required:     required,
		Roles:        roleNames,
		RejectBlocks: true,
		Default:      true,
	}}
}
