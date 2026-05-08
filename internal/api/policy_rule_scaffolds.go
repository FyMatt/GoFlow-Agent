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

type policyRuleScaffoldPreset struct {
	Name        string                          `json:"name"`
	DefaultName string                          `json:"default_name"`
	Title       string                          `json:"title"`
	Description string                          `json:"description"`
	Category    string                          `json:"category,omitempty"`
	Tags        []string                        `json:"tags,omitempty"`
	Operator    string                          `json:"operator"`
	Defaults    map[string]string               `json:"defaults,omitempty"`
	Params      []agent.WorkflowNodeFieldOption `json:"params,omitempty"`
	Document    *policyRuleResourceDocument     `json:"document,omitempty"`
}

type policyRuleScaffoldRequest struct {
	Name        string            `json:"name" yaml:"name"`
	Label       string            `json:"label" yaml:"label"`
	Description string            `json:"description" yaml:"description"`
	Reason      string            `json:"reason" yaml:"reason"`
	Defaults    map[string]string `json:"defaults" yaml:"defaults"`
	Overwrite   bool              `json:"overwrite" yaml:"overwrite"`
}

type policyRuleScaffoldResponse struct {
	Created bool                       `json:"created"`
	Updated bool                       `json:"updated,omitempty"`
	Preset  policyRuleScaffoldPreset   `json:"preset"`
	Rule    policyRuleResourceDocument `json:"rule"`
}

func (s *Server) handlePolicyRuleScaffolds(w http.ResponseWriter, r *http.Request, parts []string) {
	switch r.Method {
	case http.MethodGet:
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			writeJSON(w, s.policyRuleScaffoldPresets(false))
			return
		}
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		preset, ok := s.policyRuleScaffoldPreset(parts[0], true)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if name := normalizeResourceName(r.URL.Query().Get("name")); name != "" {
			doc, err := policyRuleFromScaffold(preset, policyRuleScaffoldRequest{Name: name})
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
		preset, ok := s.policyRuleScaffoldPreset(parts[0], true)
		if !ok {
			http.NotFound(w, r)
			return
		}
		req, err := decodePolicyRuleScaffoldRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if truthyQuery(r.URL.Query().Get("overwrite")) {
			req.Overwrite = true
		}
		doc, err := policyRuleFromScaffold(preset, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, existed := s.policyRuleResourceByName(doc.Name)
		if existed && !req.Overwrite {
			http.Error(w, fmt.Sprintf("policy rule %q already exists; pass overwrite=true to replace it", doc.Name), http.StatusConflict)
			return
		}
		saved, err := s.savePolicyRuleResource(doc.Name, doc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response := policyRuleScaffoldResponse{Created: !existed, Updated: existed, Preset: preset, Rule: saved}
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

func decodePolicyRuleScaffoldRequest(r *http.Request) (policyRuleScaffoldRequest, error) {
	var req policyRuleScaffoldRequest
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

func (s *Server) policyRuleScaffoldPreset(name string, includeDocument bool) (policyRuleScaffoldPreset, bool) {
	name = normalizeResourceName(name)
	for _, preset := range s.policyRuleScaffoldPresets(includeDocument) {
		if preset.Name == name {
			return preset, true
		}
	}
	return policyRuleScaffoldPreset{}, false
}

func (s *Server) policyRuleScaffoldPresets(includeDocument bool) []policyRuleScaffoldPreset {
	presets := []policyRuleScaffoldPreset{
		{
			Name:        "risk-threshold",
			DefaultName: "high-risk-gate",
			Title:       "Risk Threshold Gate",
			Description: "Pass when a referenced risk/severity value is at least the configured threshold.",
			Category:    "security",
			Tags:        []string{"risk", "security", "branching"},
			Operator:    "risk_at_least",
			Defaults:    map[string]string{"ref": "stages.audit.outputs.risk", "minimum": "high"},
			Params: []agent.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Evidence reference", Type: "reference", Description: "Reference to a stage output containing low, medium, high, or critical."},
				{Name: "params.minimum", Label: "Minimum severity", Type: "select", Options: []string{"low", "medium", "high", "critical"}},
			},
		},
		{
			Name:        "truthy-reference",
			DefaultName: "finding-present-gate",
			Title:       "Truthy Reference Gate",
			Description: "Pass when a referenced workflow output is present and truthy.",
			Category:    "control-flow",
			Tags:        []string{"reference", "branching"},
			Operator:    "ref_truthy",
			Defaults:    map[string]string{"ref": "stages.audit.outputs.finding"},
			Params: []agent.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference", Description: "Reference that must be truthy for the guard to pass."},
			},
		},
		{
			Name:        "contains-text",
			DefaultName: "output-contains-gate",
			Title:       "Contains Text Gate",
			Description: "Pass when a referenced output contains an expected substring.",
			Category:    "control-flow",
			Tags:        []string{"text", "branching"},
			Operator:    "contains",
			Defaults:    map[string]string{"ref": "stages.review.outputs.summary", "needle": "approved"},
			Params: []agent.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference"},
				{Name: "params.needle", Label: "Expected text", Type: "text"},
			},
		},
		{
			Name:        "minimum-count",
			DefaultName: "minimum-findings-gate",
			Title:       "Minimum Count Gate",
			Description: "Pass when a referenced list or count reaches a configured minimum.",
			Category:    "quality",
			Tags:        []string{"count", "quality", "branching"},
			Operator:    "min_count",
			Defaults:    map[string]string{"ref": "stages.audit.outputs.findings", "minimum": "1"},
			Params: []agent.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "List/count reference", Type: "reference"},
				{Name: "params.minimum", Label: "Minimum count", Type: "number"},
			},
		},
		{
			Name:        "team-review-quorum",
			DefaultName: "team-review-gate",
			Title:       "Team Review Quorum Gate",
			Description: "Pass, block, or pause based on executable team approval/rejection quorum state.",
			Category:    "collaboration",
			Tags:        []string{"team", "quorum", "approval"},
			Operator:    "team_approval_gate",
			Defaults:    map[string]string{"status": "passed", "wait_for_quorum": "true"},
			Params: []agent.WorkflowNodeFieldOption{
				{Name: "params.team", Label: "Team", Type: "text", Description: "Optional team name to filter approval packets."},
				{Name: "params.status", Label: "Required status", Type: "select", Options: []string{"passed", "blocked", "pending"}},
				{Name: "params.approval_quorum", Label: "Approval quorum", Type: "number"},
				{Name: "params.approval_preset", Label: "Quorum preset", Type: "text", Description: "Reusable quorum preset from the selected team template."},
			},
		},
		{
			Name:        "expression",
			DefaultName: "custom-expression-gate",
			Title:       "Custom Expression Gate",
			Description: "Pass when a custom workflow expression evaluates truthy.",
			Category:    "advanced",
			Tags:        []string{"expression", "custom"},
			Operator:    "expression",
			Defaults:    map[string]string{"expression": "{{params.ref}} == true", "ref": "stages.audit.outputs.passed"},
			Params: []agent.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference"},
			},
		},
	}
	for i := range presets {
		if includeDocument {
			if doc, err := policyRuleFromScaffold(presets[i], policyRuleScaffoldRequest{Name: presets[i].DefaultName}); err == nil {
				presets[i].Document = &doc
			}
		}
	}
	return presets
}

func policyRuleFromScaffold(preset policyRuleScaffoldPreset, req policyRuleScaffoldRequest) (policyRuleResourceDocument, error) {
	name := normalizeResourceName(firstWorkflowRunQueryValue(req.Name, preset.DefaultName, preset.Name))
	if !resourceNamePattern.MatchString(name) {
		return policyRuleResourceDocument{}, fmt.Errorf("invalid policy rule name %q: use lowercase letters, numbers, hyphen, or underscore", name)
	}
	defaults := copyMap(preset.Defaults)
	for key, value := range req.Defaults {
		if strings.TrimSpace(key) != "" {
			defaults[strings.TrimSpace(key)] = value
		}
	}
	doc := policyRuleResourceDocument{
		Name:        name,
		Label:       firstWorkflowRunQueryValue(req.Label, preset.Title, policyRuleScaffoldHumanLabel(name)),
		Description: firstWorkflowRunQueryValue(req.Description, preset.Description),
		Operator:    preset.Operator,
		Defaults:    defaults,
		Reason:      firstWorkflowRunQueryValue(req.Reason, "policy rule did not pass"),
		Params:      append([]agent.WorkflowNodeFieldOption(nil), preset.Params...),
	}
	if doc.Operator == "expression" {
		doc.Expression = firstWorkflowRunQueryValue(defaults["expression"], "{{params.ref}} == true")
		delete(doc.Defaults, "expression")
	}
	return normalizePolicyRuleResource(doc, name), nil
}

func policyRuleScaffoldHumanLabel(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "-", " "))
	name = strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	if name == "" {
		return "Custom policy rule"
	}
	words := strings.Fields(name)
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
