package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/scaffold"
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
	presets := s.policyRuleScaffoldPresetDefinitions()
	for i := range presets {
		if includeDocument {
			if doc, err := policyRuleFromScaffold(presets[i], policyRuleScaffoldRequest{Name: presets[i].DefaultName}); err == nil {
				presets[i].Document = &doc
			}
		}
	}
	return presets
}

func (s *Server) policyRuleScaffoldPresetDefinitions() []policyRuleScaffoldPreset {
	if s == nil || s.runtime == nil || strings.TrimSpace(s.runtime.RuntimeHome()) == "" {
		return policyRuleScaffoldPresetsFromShared(scaffold.BuiltInPolicyRulePresets())
	}
	presets, err := scaffold.PolicyRulePresetsFromDirs(filepath.Join(s.runtime.RuntimeHome(), "templates", "policies", "scaffolds"))
	if err != nil {
		return policyRuleScaffoldPresetsFromShared(scaffold.BuiltInPolicyRulePresets())
	}
	return policyRuleScaffoldPresetsFromShared(presets)
}

func policyRuleScaffoldPresetsFromShared(presets []scaffold.PolicyRulePreset) []policyRuleScaffoldPreset {
	out := make([]policyRuleScaffoldPreset, 0, len(presets))
	for _, preset := range presets {
		out = append(out, policyRuleScaffoldPresetFromShared(preset))
	}
	return out
}

func policyRuleScaffoldPresetFromShared(preset scaffold.PolicyRulePreset) policyRuleScaffoldPreset {
	defaults := copyMap(preset.Defaults)
	if strings.TrimSpace(preset.Expression) != "" {
		if defaults == nil {
			defaults = make(map[string]string)
		}
		defaults["expression"] = preset.Expression
	}
	return policyRuleScaffoldPreset{
		Name:        preset.Name,
		DefaultName: preset.DefaultName,
		Title:       preset.Title,
		Description: preset.Description,
		Category:    preset.Category,
		Tags:        append([]string(nil), preset.Tags...),
		Operator:    preset.Operator,
		Defaults:    defaults,
		Params:      workflowNodeFieldsFromPolicyPresetFields(preset.Params),
	}
}

func workflowNodeFieldsFromPolicyPresetFields(fields []scaffold.PolicyRuleFieldOption) []agent.WorkflowNodeFieldOption {
	out := make([]agent.WorkflowNodeFieldOption, 0, len(fields))
	for _, field := range fields {
		out = append(out, agent.WorkflowNodeFieldOption{
			Name:        field.Name,
			Label:       field.Label,
			Type:        field.Type,
			Description: field.Description,
			Placeholder: field.Placeholder,
			Default:     field.Default,
			Required:    field.Required,
			Options:     append([]string(nil), field.Options...),
			Examples:    append([]string(nil), field.Examples...),
			Hints:       append([]string(nil), field.Hints...),
		})
	}
	return out
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
