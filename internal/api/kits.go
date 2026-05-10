package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/scaffold"
	skillpkg "github.com/FyMatt/GoFlow-Agent/internal/skill"
	"gopkg.in/yaml.v3"
)

const kitResourceKind = "goflow.kit"
const kitResourceVersion = 1
const kitBundleKind = "goflow.kit_bundle"
const kitBundleVersion = 1

type kitResourceDocument struct {
	Kind              string              `json:"kind,omitempty" yaml:"kind,omitempty"`
	Version           int                 `json:"version,omitempty" yaml:"version,omitempty"`
	MinVersion        int                 `json:"min_supported_version,omitempty" yaml:"min_supported_version,omitempty"`
	Name              string              `json:"name" yaml:"name"`
	Title             string              `json:"title,omitempty" yaml:"title,omitempty"`
	Description       string              `json:"description,omitempty" yaml:"description,omitempty"`
	Category          string              `json:"category,omitempty" yaml:"category,omitempty"`
	Tags              []string            `json:"tags,omitempty" yaml:"tags,omitempty"`
	Agents            []string            `json:"agents,omitempty" yaml:"agents,omitempty"`
	Providers         []string            `json:"providers,omitempty" yaml:"providers,omitempty"`
	Skills            []string            `json:"skills,omitempty" yaml:"skills,omitempty"`
	Tools             []string            `json:"tools,omitempty" yaml:"tools,omitempty"`
	Workflows         []string            `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	WorkflowTemplates []string            `json:"workflow_templates,omitempty" yaml:"workflow_templates,omitempty"`
	TeamTemplates     []string            `json:"team_templates,omitempty" yaml:"team_templates,omitempty"`
	PolicyRules       []string            `json:"policy_rules,omitempty" yaml:"policy_rules,omitempty"`
	RequiredEnv       []string            `json:"required_env,omitempty" yaml:"required_env,omitempty"`
	Examples          []kitExample        `json:"examples,omitempty" yaml:"examples,omitempty"`
	Metadata          map[string]string   `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Path              string              `json:"path,omitempty" yaml:"-"`
	Validation        kitValidationResult `json:"validation,omitempty" yaml:"-"`
}

type kitExample struct {
	Title       string `json:"title,omitempty" yaml:"title,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Request     string `json:"request,omitempty" yaml:"request,omitempty"`
	Workflow    string `json:"workflow,omitempty" yaml:"workflow,omitempty"`
	Agent       string `json:"agent,omitempty" yaml:"agent,omitempty"`
}

type kitValidationResult struct {
	Valid      bool                 `json:"valid"`
	Name       string               `json:"name,omitempty"`
	Normalized *kitResourceDocument `json:"normalized,omitempty"`
	Issues     []kitValidationItem  `json:"issues,omitempty"`
}

type kitValidationItem struct {
	Severity       string `json:"severity"`
	Code           string `json:"code"`
	Field          string `json:"field,omitempty"`
	Message        string `json:"message"`
	Ref            string `json:"ref,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
}

type kitSummary struct {
	Name                 string              `json:"name"`
	Title                string              `json:"title,omitempty"`
	Description          string              `json:"description,omitempty"`
	Category             string              `json:"category,omitempty"`
	Tags                 []string            `json:"tags,omitempty"`
	Path                 string              `json:"path,omitempty"`
	Agents               int                 `json:"agents,omitempty"`
	Skills               int                 `json:"skills,omitempty"`
	Tools                int                 `json:"tools,omitempty"`
	Workflows            int                 `json:"workflows,omitempty"`
	Providers            int                 `json:"providers,omitempty"`
	WorkflowTemplates    int                 `json:"workflow_templates,omitempty"`
	TeamTemplates        int                 `json:"team_templates,omitempty"`
	PolicyRules          int                 `json:"policy_rules,omitempty"`
	ProviderRefs         []string            `json:"provider_refs,omitempty"`
	AgentRefs            []string            `json:"agent_refs,omitempty"`
	SkillRefs            []string            `json:"skill_refs,omitempty"`
	ToolRefs             []string            `json:"tool_refs,omitempty"`
	WorkflowRefs         []string            `json:"workflow_refs,omitempty"`
	WorkflowTemplateRefs []string            `json:"workflow_template_refs,omitempty"`
	TeamTemplateRefs     []string            `json:"team_template_refs,omitempty"`
	PolicyRuleRefs       []string            `json:"policy_rule_refs,omitempty"`
	Valid                bool                `json:"valid"`
	Issues               []kitValidationItem `json:"issues,omitempty"`
}

type kitScaffoldPreset struct {
	Name                string              `json:"name"`
	DefaultKitName      string              `json:"default_kit_name"`
	Title               string              `json:"title"`
	Description         string              `json:"description,omitempty"`
	Category            string              `json:"category,omitempty"`
	Tags                []string            `json:"tags,omitempty"`
	Providers           []string            `json:"providers,omitempty"`
	Agents              []string            `json:"agents,omitempty"`
	Skills              []string            `json:"skills,omitempty"`
	Tools               []string            `json:"tools,omitempty"`
	Workflows           []string            `json:"workflows,omitempty"`
	WorkflowTemplates   []string            `json:"workflow_templates,omitempty"`
	TeamTemplates       []string            `json:"team_templates,omitempty"`
	PolicyRules         []string            `json:"policy_rules,omitempty"`
	RequiredEnv         []string            `json:"required_env,omitempty"`
	Examples            []kitExample        `json:"examples,omitempty"`
	Metadata            map[string]string   `json:"metadata,omitempty"`
	RecommendedWorkflow string              `json:"recommended_workflow,omitempty"`
	RecommendedAgent    string              `json:"recommended_agent,omitempty"`
	Validation          kitValidationResult `json:"validation,omitempty"`
}

type kitScaffoldRequest struct {
	Name              string            `json:"name,omitempty"`
	Title             string            `json:"title,omitempty"`
	Description       string            `json:"description,omitempty"`
	Category          string            `json:"category,omitempty"`
	Tags              []string          `json:"tags,omitempty"`
	Providers         []string          `json:"providers,omitempty"`
	Agents            []string          `json:"agents,omitempty"`
	Skills            []string          `json:"skills,omitempty"`
	Tools             []string          `json:"tools,omitempty"`
	Workflows         []string          `json:"workflows,omitempty"`
	WorkflowTemplates []string          `json:"workflow_templates,omitempty"`
	TeamTemplates     []string          `json:"team_templates,omitempty"`
	PolicyRules       []string          `json:"policy_rules,omitempty"`
	RequiredEnv       []string          `json:"required_env,omitempty"`
	Examples          []kitExample      `json:"examples,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Overwrite         bool              `json:"overwrite,omitempty"`
	Materialize       bool              `json:"materialize,omitempty"`
}

type kitScaffoldResponse struct {
	Preset          string                   `json:"preset"`
	Created         bool                     `json:"created"`
	Updated         bool                     `json:"updated"`
	Materialized    bool                     `json:"materialized,omitempty"`
	RestartRequired bool                     `json:"restart_required,omitempty"`
	Kit             kitResourceDocument      `json:"kit"`
	Resources       []kitBundleSavedResource `json:"resources,omitempty"`
	Warnings        []kitValidationItem      `json:"warnings,omitempty"`
	Messages        []string                 `json:"messages,omitempty"`
	ActivationSteps []string                 `json:"activation_steps,omitempty"`
	NextSteps       []string                 `json:"next_steps,omitempty"`
}

type kitBundleDocument struct {
	Kind              string                        `json:"kind" yaml:"kind"`
	Version           int                           `json:"version" yaml:"version"`
	Kit               kitResourceDocument           `json:"kit" yaml:"kit"`
	Agents            []agentResourceDocument       `json:"agents,omitempty" yaml:"agents,omitempty"`
	Providers         []providerResourceDocument    `json:"providers,omitempty" yaml:"providers,omitempty"`
	Skills            []skillResourceDocument       `json:"skills,omitempty" yaml:"skills,omitempty"`
	Tools             []toolResourceDocument        `json:"tools,omitempty" yaml:"tools,omitempty"`
	Workflows         []agent.WorkflowGraphDocument `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	WorkflowTemplates []agent.WorkflowTemplate      `json:"workflow_templates,omitempty" yaml:"workflow_templates,omitempty"`
	TeamTemplates     []agent.TeamTemplate          `json:"team_templates,omitempty" yaml:"team_templates,omitempty"`
	PolicyRules       []policyRuleResourceDocument  `json:"policy_rules,omitempty" yaml:"policy_rules,omitempty"`
	Warnings          []string                      `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

type kitBundleImportResult struct {
	Kit        kitResourceDocument      `json:"kit,omitempty"`
	Saved      []kitBundleSavedResource `json:"saved,omitempty"`
	Skipped    []kitBundleSavedResource `json:"skipped,omitempty"`
	Errors     []kitBundleSavedResource `json:"errors,omitempty"`
	Warnings   []string                 `json:"warnings,omitempty"`
	Validation kitValidationResult      `json:"validation,omitempty"`
	Restart    bool                     `json:"restart_required,omitempty"`
}

type kitBundleSavedResource struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type KitBundleImportSummary struct {
	KitName          string                    `json:"kit_name,omitempty"`
	Saved            []KitBundleResourceStatus `json:"saved,omitempty"`
	Skipped          []KitBundleResourceStatus `json:"skipped,omitempty"`
	Errors           []KitBundleResourceStatus `json:"errors,omitempty"`
	Warnings         []string                  `json:"warnings,omitempty"`
	RestartRequired  bool                      `json:"restart_required,omitempty"`
	ValidationValid  bool                      `json:"validation_valid"`
	ValidationIssues []KitBundleIssue          `json:"validation_issues,omitempty"`
}

type KitBundleResourceStatus struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type KitBundleIssue struct {
	Severity       string `json:"severity"`
	Code           string `json:"code"`
	Field          string `json:"field,omitempty"`
	Message        string `json:"message"`
	Ref            string `json:"ref,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
}

func ExportKitBundle(runtimeRef *agent.Runtime, name, format string, includeSecrets bool) ([]byte, string, string, error) {
	if runtimeRef == nil {
		return nil, "", "", fmt.Errorf("runtime is not configured")
	}
	server := NewServer(runtimeRef)
	bundle, err := server.buildKitBundle(name, includeSecrets)
	if err != nil {
		return nil, "", "", err
	}
	return renderKitBundleDocument(bundle, format)
}

func ImportKitBundleData(runtimeRef *agent.Runtime, data []byte, contentType string, overwrite bool) (KitBundleImportSummary, error) {
	if runtimeRef == nil {
		return KitBundleImportSummary{}, fmt.Errorf("runtime is not configured")
	}
	bundle, err := decodeKitBundleData(data, contentType)
	if err != nil {
		return KitBundleImportSummary{}, err
	}
	result := NewServer(runtimeRef).importKitBundle(bundle, overwrite)
	return kitBundleImportSummaryFromResult(result), nil
}

func (s *Server) handleKitCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.kitResourceList())
}

func (s *Server) handleKitItem(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/kits/"), "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	name := parts[0]
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(parts) == 2 && parts[1] == "export" {
		s.handleKitBundleExport(w, r, name)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	doc, ok := s.kitResourceByName(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	doc.Validation = s.validateKitResource(doc)
	writeJSON(w, doc)
}

func (s *Server) handleKitResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/kits/validate" {
		s.handleKitResourceValidate(w, r, "")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.kitResourceList())
}

func (s *Server) handleKitResourceItem(w http.ResponseWriter, r *http.Request) {
	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/kits/"), "/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 && parts[0] == "import" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleKitBundleImport(w, r)
		return
	}
	if parts[0] == "scaffolds" {
		s.handleKitScaffolds(w, r, parts[1:])
		return
	}
	if len(parts) == 1 && parts[0] == "validate" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleKitResourceValidate(w, r, "")
		return
	}
	name := parts[0]
	if len(parts) == 2 && parts[1] == "export" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleKitBundleExport(w, r, name)
		return
	}
	if len(parts) == 2 && parts[1] == "validate" {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodPost {
			s.handleKitResourceValidate(w, r, name)
			return
		}
		doc, ok := s.kitResourceByName(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, s.validateKitResource(doc))
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, ok := s.kitResourceByName(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		doc.Validation = s.validateKitResource(doc)
		writeJSON(w, doc)
	case http.MethodPut:
		var doc kitResourceDocument
		if err := yaml.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid yaml/json", http.StatusBadRequest)
			return
		}
		saved, err := s.saveKitResource(name, doc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.deleteKitResource(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleKitResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	var doc kitResourceDocument
	if err := yaml.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, kitValidationResult{
			Valid: false,
			Name:  normalizeResourceName(name),
			Issues: []kitValidationItem{{
				Severity:       "error",
				Code:           "invalid_body",
				Field:          "body",
				Message:        "invalid yaml/json",
				Ref:            "body",
				Recommendation: kitValidationRecommendationForCode("invalid_body"),
			}},
		})
		return
	}
	normalized := normalizeResourceName(firstResourceValidationName(name, doc.Name))
	if !resourceNamePattern.MatchString(normalized) {
		writeJSON(w, kitValidationResult{
			Valid: false,
			Name:  normalized,
			Issues: []kitValidationItem{{
				Severity:       "error",
				Code:           "invalid_name",
				Field:          "name",
				Message:        fmt.Sprintf("invalid kit name %q: use lowercase letters, numbers, hyphen, or underscore", normalized),
				Ref:            "name",
				Recommendation: kitValidationRecommendationForCode("invalid_name"),
			}},
		})
		return
	}
	doc = normalizeKitResource(doc, normalized)
	result := s.validateKitResource(doc)
	result.Name = doc.Name
	result.Normalized = &doc
	writeJSON(w, result)
}

func (s *Server) handleKitScaffolds(w http.ResponseWriter, r *http.Request, parts []string) {
	switch r.Method {
	case http.MethodGet:
		if len(parts) == 0 {
			writeJSON(w, s.kitScaffoldPresets())
			return
		}
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		preset, ok := s.kitScaffoldPreset(parts[0])
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, preset)
	case http.MethodPost:
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		preset, ok := s.kitScaffoldPreset(parts[0])
		if !ok {
			http.NotFound(w, r)
			return
		}
		var req kitScaffoldRequest
		if r.Body != nil {
			data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(string(data)) != "" {
				if err := json.Unmarshal(data, &req); err != nil {
					if yamlErr := yaml.Unmarshal(data, &req); yamlErr != nil {
						http.Error(w, "invalid json/yaml", http.StatusBadRequest)
						return
					}
				}
			}
		}
		if truthyQuery(r.URL.Query().Get("overwrite")) {
			req.Overwrite = true
		}
		if truthyQuery(r.URL.Query().Get("materialize")) || truthyQuery(r.URL.Query().Get("full")) {
			req.Materialize = true
		}
		if req.Materialize {
			response, status, err := s.materializeKitScaffold(preset, req)
			if err != nil {
				http.Error(w, err.Error(), status)
				return
			}
			writeJSONStatus(w, status, response)
			return
		}
		doc := kitDocumentFromScaffold(preset, req)
		if _, exists := s.kitResourceByName(doc.Name); exists && !req.Overwrite {
			http.Error(w, fmt.Sprintf("kit %q already exists; pass overwrite=true to replace it", doc.Name), http.StatusConflict)
			return
		}
		existed := false
		if _, ok := s.kitResourceByName(doc.Name); ok {
			existed = true
		}
		saved, err := s.saveKitResource(doc.Name, doc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response := kitScaffoldResponse{
			Preset:   preset.Name,
			Created:  !existed,
			Updated:  existed,
			Kit:      saved,
			Warnings: kitValidationIssuesBySeverity(saved.Validation.Issues, "warning"),
			NextSteps: []string{
				"Review validation issues and set any required environment variables.",
				"Export this kit as a bundle when you want to share its referenced resources.",
				"Restart or reload the runtime after adding new provider, agent, or MCP tool modules.",
			},
		}
		status := http.StatusCreated
		if existed {
			status = http.StatusOK
		}
		writeJSONStatus(w, status, response)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleKitBundleExport(w http.ResponseWriter, r *http.Request, name string) {
	includeSecrets := truthyQuery(r.URL.Query().Get("include_secrets"))
	bundle, err := s.buildKitBundle(name, includeSecrets)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, contentType, ext, err := renderKitBundleDocument(bundle, r.URL.Query().Get("format"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-kit-bundle.%s"`, bundle.Kit.Name, ext))
	_, _ = w.Write(data)
}

func (s *Server) handleKitBundleImport(w http.ResponseWriter, r *http.Request) {
	bundle, err := decodeKitBundleDocument(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	overwrite := !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("overwrite")), "false")
	result := s.importKitBundle(bundle, overwrite)
	status := http.StatusCreated
	if len(result.Errors) > 0 {
		status = http.StatusMultiStatus
	}
	writeJSONStatus(w, status, result)
}

func (s *Server) kitResourceList() []kitSummary {
	return kitResourceListForRuntime(s.runtime)
}

func kitResourceListForRuntime(runtimeRef *agent.Runtime) []kitSummary {
	if runtimeRef == nil || strings.TrimSpace(runtimeRef.RuntimeHome()) == "" {
		return nil
	}
	server := &Server{runtime: runtimeRef}
	root, err := server.kitResourceRoot()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]kitSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && !isYAMLFile(entry.Name()) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if entry.IsDir() {
			name = entry.Name()
		}
		doc, ok := server.kitResourceByName(name)
		if !ok {
			continue
		}
		validation := server.validateKitResource(doc)
		out = append(out, kitSummary{
			Name:                 doc.Name,
			Title:                doc.Title,
			Description:          doc.Description,
			Category:             doc.Category,
			Tags:                 append([]string(nil), doc.Tags...),
			Path:                 doc.Path,
			Agents:               len(doc.Agents),
			Skills:               len(doc.Skills),
			Tools:                len(doc.Tools),
			Workflows:            len(doc.Workflows),
			Providers:            len(doc.Providers),
			WorkflowTemplates:    len(doc.WorkflowTemplates),
			TeamTemplates:        len(doc.TeamTemplates),
			PolicyRules:          len(doc.PolicyRules),
			ProviderRefs:         append([]string(nil), doc.Providers...),
			AgentRefs:            append([]string(nil), doc.Agents...),
			SkillRefs:            append([]string(nil), doc.Skills...),
			ToolRefs:             append([]string(nil), doc.Tools...),
			WorkflowRefs:         append([]string(nil), doc.Workflows...),
			WorkflowTemplateRefs: append([]string(nil), doc.WorkflowTemplates...),
			TeamTemplateRefs:     append([]string(nil), doc.TeamTemplates...),
			PolicyRuleRefs:       append([]string(nil), doc.PolicyRules...),
			Valid:                validation.Valid,
			Issues:               validation.Issues,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Server) kitResourceByName(name string) (kitResourceDocument, bool) {
	normalized := normalizeResourceName(name)
	if normalized == "" {
		return kitResourceDocument{}, false
	}
	path, err := s.kitResourcePath(normalized)
	if err != nil {
		return kitResourceDocument{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		legacy, legacyErr := s.kitLegacyResourcePath(normalized)
		if legacyErr != nil {
			return kitResourceDocument{}, false
		}
		data, err = os.ReadFile(legacy)
		if err != nil {
			return kitResourceDocument{}, false
		}
		path = legacy
	}
	var doc kitResourceDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return kitResourceDocument{}, false
	}
	doc = normalizeKitResource(doc, normalized)
	doc.Path = path
	return doc, true
}

func (s *Server) saveKitResource(name string, doc kitResourceDocument) (kitResourceDocument, error) {
	normalized := normalizeResourceName(name)
	if !resourceNamePattern.MatchString(normalized) {
		return kitResourceDocument{}, fmt.Errorf("invalid kit name %q: use lowercase letters, numbers, hyphen, or underscore", normalized)
	}
	doc = normalizeKitResource(doc, normalized)
	if err := validateKitResourceDocument(doc); err != nil {
		return kitResourceDocument{}, err
	}
	path, err := s.kitResourcePath(normalized)
	if err != nil {
		return kitResourceDocument{}, err
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return kitResourceDocument{}, fmt.Errorf("render kit resource: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return kitResourceDocument{}, fmt.Errorf("create kit directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return kitResourceDocument{}, fmt.Errorf("write kit resource: %w", err)
	}
	var parsed kitResourceDocument
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		_ = os.Remove(tmp)
		return kitResourceDocument{}, fmt.Errorf("validate kit resource: %w", err)
	}
	if err := validateKitResourceDocument(normalizeKitResource(parsed, normalized)); err != nil {
		_ = os.Remove(tmp)
		return kitResourceDocument{}, fmt.Errorf("validate kit resource: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return kitResourceDocument{}, fmt.Errorf("replace kit resource: %w", err)
	}
	doc.Path = path
	doc.Validation = s.validateKitResource(doc)
	return doc, nil
}

func (s *Server) deleteKitResource(name string) error {
	normalized := normalizeResourceName(name)
	if !resourceNamePattern.MatchString(normalized) {
		return fmt.Errorf("invalid kit name %q: use lowercase letters, numbers, hyphen, or underscore", normalized)
	}
	path, err := s.kitResourcePath(normalized)
	if err != nil {
		return err
	}
	if _, ok := s.kitResourceByName(normalized); !ok {
		return fmt.Errorf("kit %q not found", normalized)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete kit resource: %w", err)
	}
	_ = os.Remove(filepath.Dir(path))
	return nil
}

func (s *Server) kitScaffoldPresets() []kitScaffoldPreset {
	presets := s.kitScaffoldPresetDefinitions()
	out := make([]kitScaffoldPreset, 0, len(presets))
	for _, preset := range presets {
		preset.Validation = s.validateKitResource(kitDocumentFromScaffold(preset, kitScaffoldRequest{}))
		out = append(out, preset)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Server) kitScaffoldPreset(name string) (kitScaffoldPreset, bool) {
	normalized := normalizeResourceName(name)
	for _, preset := range s.kitScaffoldPresetDefinitions() {
		if normalizeResourceName(preset.Name) == normalized {
			preset.Validation = s.validateKitResource(kitDocumentFromScaffold(preset, kitScaffoldRequest{}))
			return preset, true
		}
	}
	return kitScaffoldPreset{}, false
}

func (s *Server) kitScaffoldPresetDefinitions() []kitScaffoldPreset {
	if s == nil || s.runtime == nil || strings.TrimSpace(s.runtime.RuntimeHome()) == "" {
		return defaultKitScaffoldPresets()
	}
	presets, err := scaffold.KitPresetsFromDirs(filepath.Join(s.runtime.RuntimeHome(), "templates", "kits", "scaffolds"))
	if err != nil {
		return defaultKitScaffoldPresets()
	}
	return kitScaffoldPresetsFromShared(presets)
}

func defaultKitScaffoldPresets() []kitScaffoldPreset {
	return kitScaffoldPresetsFromShared(scaffold.BuiltInKitPresets())
}

func kitScaffoldPresetsFromShared(presets []scaffold.KitPreset) []kitScaffoldPreset {
	out := make([]kitScaffoldPreset, 0, len(presets))
	for _, preset := range presets {
		out = append(out, kitScaffoldPresetFromShared(preset))
	}
	return out
}

func kitScaffoldPresetFromShared(preset scaffold.KitPreset) kitScaffoldPreset {
	examples := make([]kitExample, 0, len(preset.Examples))
	for _, example := range preset.Examples {
		examples = append(examples, kitExample{
			Title:       example.Title,
			Description: example.Description,
			Request:     example.Request,
			Workflow:    example.Workflow,
			Agent:       example.Agent,
		})
	}
	return kitScaffoldPreset{
		Name:                preset.Name,
		DefaultKitName:      preset.DefaultKitName,
		Title:               preset.Title,
		Description:         preset.Description,
		Category:            preset.Category,
		Tags:                append([]string(nil), preset.Tags...),
		Providers:           append([]string(nil), preset.Providers...),
		Agents:              append([]string(nil), preset.Agents...),
		Skills:              append([]string(nil), preset.Skills...),
		Tools:               append([]string(nil), preset.Tools...),
		Workflows:           append([]string(nil), preset.Workflows...),
		WorkflowTemplates:   append([]string(nil), preset.WorkflowTemplates...),
		TeamTemplates:       append([]string(nil), preset.TeamTemplates...),
		PolicyRules:         append([]string(nil), preset.PolicyRules...),
		RequiredEnv:         append([]string(nil), preset.RequiredEnv...),
		RecommendedWorkflow: preset.RecommendedWorkflow,
		RecommendedAgent:    preset.RecommendedAgent,
		Examples:            examples,
		Metadata:            cloneKitStringMap(preset.Metadata),
	}
}

func cloneKitStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func kitDocumentFromScaffold(preset kitScaffoldPreset, req kitScaffoldRequest) kitResourceDocument {
	name := normalizeResourceName(firstWorkflowRunQueryValue(req.Name, preset.DefaultKitName, preset.Name+"-kit"))
	doc := kitResourceDocument{
		Name:              name,
		Title:             firstWorkflowRunQueryValue(req.Title, preset.Title, name),
		Description:       firstWorkflowRunQueryValue(req.Description, preset.Description),
		Category:          firstWorkflowRunQueryValue(req.Category, preset.Category),
		Tags:              mergeKitStringRefs(preset.Tags, req.Tags, false),
		Providers:         overrideKitRefs(preset.Providers, req.Providers, false),
		Agents:            overrideKitRefs(preset.Agents, req.Agents, false),
		Skills:            overrideKitRefs(preset.Skills, req.Skills, false),
		Tools:             overrideKitRefs(preset.Tools, req.Tools, true),
		Workflows:         overrideKitRefs(preset.Workflows, req.Workflows, false),
		WorkflowTemplates: overrideKitRefs(preset.WorkflowTemplates, req.WorkflowTemplates, false),
		TeamTemplates:     overrideKitRefs(preset.TeamTemplates, req.TeamTemplates, false),
		PolicyRules:       overrideKitRefs(preset.PolicyRules, req.PolicyRules, false),
		RequiredEnv:       mergeKitStringRefs(preset.RequiredEnv, req.RequiredEnv, true),
		Examples:          append([]kitExample(nil), preset.Examples...),
		Metadata:          mergeKitMetadata(preset.Metadata, req.Metadata),
	}
	if len(req.Examples) > 0 {
		doc.Examples = append(doc.Examples, req.Examples...)
	}
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]string)
	}
	doc.Metadata["scaffold_preset"] = preset.Name
	if preset.RecommendedWorkflow != "" {
		doc.Metadata["recommended_workflow"] = preset.RecommendedWorkflow
	}
	if preset.RecommendedAgent != "" {
		doc.Metadata["recommended_agent"] = preset.RecommendedAgent
	}
	return normalizeKitResource(doc, name)
}

type kitMaterializedResourceNames struct {
	Kit              string
	Agent            string
	Skill            string
	Tool             string
	Workflow         string
	WorkflowTemplate string
	TeamTemplate     string
	PolicyRule       string
}

func kitMaterializedNames(base string) kitMaterializedResourceNames {
	base = normalizeResourceName(base)
	return kitMaterializedResourceNames{
		Kit:              base,
		Agent:            base + "-agent",
		Skill:            base + "-skill",
		Tool:             base + "-helper",
		Workflow:         base + "-workflow",
		WorkflowTemplate: base + "-template",
		TeamTemplate:     base + "-team",
		PolicyRule:       base + "-gate",
	}
}

func (s *Server) materializeKitScaffold(preset kitScaffoldPreset, req kitScaffoldRequest) (kitScaffoldResponse, int, error) {
	doc := kitDocumentFromScaffold(preset, req)
	names := kitMaterializedNames(doc.Name)
	bundle, err := s.materializedKitBundle(preset, req, doc, names)
	if err != nil {
		return kitScaffoldResponse{}, http.StatusBadRequest, err
	}
	if !req.Overwrite {
		conflicts := s.kitMaterializationConflicts(bundle)
		if len(conflicts) > 0 {
			return kitScaffoldResponse{}, http.StatusConflict, fmt.Errorf("materialized kit resources already exist: %s; pass overwrite=true to replace them", kitBundleConflictSummary(conflicts))
		}
	}
	_, kitExisted := s.kitResourceByName(bundle.Kit.Name)
	result := s.importKitBundle(bundle, req.Overwrite)
	if len(result.Errors) > 0 {
		return kitScaffoldResponse{}, http.StatusBadRequest, fmt.Errorf("materialized kit scaffold failed: %s", kitBundleConflictSummary(result.Errors))
	}
	if result.Kit.Name == "" {
		result.Kit = bundle.Kit
		result.Validation = s.validateKitResource(bundle.Kit)
	}
	resources := append([]kitBundleSavedResource(nil), result.Saved...)
	resources = append(resources, result.Skipped...)
	response := kitScaffoldResponse{
		Preset:          preset.Name,
		Created:         !kitExisted,
		Updated:         kitExisted,
		Materialized:    true,
		RestartRequired: result.Restart,
		Kit:             result.Kit,
		Resources:       resources,
		Warnings:        kitValidationIssuesBySeverity(result.Validation.Issues, "warning"),
		Messages:        result.Warnings,
		ActivationSteps: kitMaterializedActivationSteps(names),
		NextSteps: []string{
			"Review generated resources and required environment variables.",
			"Restart GoFlow so the generated agent and MCP server module are loaded.",
			"Open the generated workflow in Studio and adjust node inputs, policy gate, and team roles for your domain.",
		},
	}
	status := http.StatusCreated
	if kitExisted {
		status = http.StatusOK
	}
	return response, status, nil
}

func (s *Server) materializedKitBundle(preset kitScaffoldPreset, req kitScaffoldRequest, doc kitResourceDocument, names kitMaterializedResourceNames) (kitBundleDocument, error) {
	providers := overrideKitRefs(preset.Providers, req.Providers, false)
	if len(providers) == 0 {
		providers = []string{"primary"}
	}
	doc.Name = names.Kit
	doc.Providers = providers
	doc.Agents = []string{names.Agent}
	doc.Skills = []string{names.Skill}
	doc.Tools = []string{names.Tool}
	doc.Workflows = []string{names.Workflow}
	doc.WorkflowTemplates = []string{names.WorkflowTemplate}
	doc.TeamTemplates = []string{names.TeamTemplate}
	doc.PolicyRules = []string{names.PolicyRule}
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]string)
	}
	doc.Metadata["materialized"] = "true"
	doc.Metadata["generated_agent"] = names.Agent
	doc.Metadata["generated_skill"] = names.Skill
	doc.Metadata["generated_tool"] = names.Tool
	doc.Metadata["generated_workflow"] = names.Workflow
	doc.Metadata["generated_workflow_template"] = names.WorkflowTemplate
	doc.Metadata["generated_team_template"] = names.TeamTemplate
	doc.Metadata["generated_policy_rule"] = names.PolicyRule
	doc.Metadata["recommended_agent"] = names.Agent
	doc.Metadata["recommended_workflow"] = names.Workflow
	doc.Metadata["recommended_team"] = names.TeamTemplate
	doc.Examples = []kitExample{{
		Title:       "Run the generated starter workflow",
		Description: "Uses the generated agent, skill, containerized helper tool, team template, and policy gate.",
		Request:     firstKitScaffoldValue(presetExampleRequest(preset), "Describe the task, scope, expected output, and acceptance criteria."),
		Workflow:    names.Workflow,
		Agent:       names.Agent,
	}}
	doc = normalizeKitResource(doc, names.Kit)
	templatePreset := materializedKitPresetForTemplate(preset, doc)
	kitDoc, err := s.renderMaterializedKitResource(templatePreset, names)
	if err != nil {
		return kitBundleDocument{}, err
	}

	tool, err := s.materializedKitToolDocument(preset, names)
	if err != nil {
		return kitBundleDocument{}, err
	}
	agentDoc, err := s.renderMaterializedAgentResource(templatePreset, names)
	if err != nil {
		return kitBundleDocument{}, err
	}
	skillDoc, err := s.renderMaterializedSkillResource(templatePreset, names)
	if err != nil {
		return kitBundleDocument{}, err
	}
	workflow, err := s.renderMaterializedWorkflowResource(templatePreset, names)
	if err != nil {
		return kitBundleDocument{}, err
	}
	template, err := s.renderMaterializedWorkflowTemplateResource(templatePreset, names)
	if err != nil {
		return kitBundleDocument{}, err
	}
	team, err := s.renderMaterializedTeamTemplateResource(templatePreset, names)
	if err != nil {
		return kitBundleDocument{}, err
	}
	policy, err := s.renderMaterializedPolicyRuleResource(templatePreset, names)
	if err != nil {
		return kitBundleDocument{}, err
	}
	return kitBundleDocument{
		Kind:              kitBundleKind,
		Version:           kitBundleVersion,
		Kit:               kitDoc,
		Agents:            []agentResourceDocument{agentDoc},
		Skills:            []skillResourceDocument{skillDoc},
		Tools:             []toolResourceDocument{tool},
		TeamTemplates:     []agent.TeamTemplate{team},
		PolicyRules:       []policyRuleResourceDocument{policy},
		Workflows:         []agent.WorkflowGraphDocument{workflow},
		WorkflowTemplates: []agent.WorkflowTemplate{template},
		Warnings: []string{
			"Generated agent and MCP tool modules require a runtime restart before active execution.",
			"The generated helper tool uses Docker/Podman-style container isolation defaults and read-only workspace access.",
		},
	}, nil
}

func (s *Server) renderMaterializedKitResource(preset scaffold.KitPreset, names kitMaterializedResourceNames) (kitResourceDocument, error) {
	runtimeHome := ""
	if s != nil && s.runtime != nil {
		runtimeHome = s.runtime.RuntimeHome()
	}
	content, err := scaffold.RenderMaterializedKitYAML(runtimeHome, preset, materializedSharedNames(names))
	if err != nil {
		return kitResourceDocument{}, err
	}
	var doc kitResourceDocument
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return kitResourceDocument{}, fmt.Errorf("parse materialized kit scaffold: %w", err)
	}
	doc = normalizeKitResource(doc, names.Kit)
	return doc, nil
}

func (s *Server) renderMaterializedAgentResource(preset scaffold.KitPreset, names kitMaterializedResourceNames) (agentResourceDocument, error) {
	runtimeHome := ""
	if s != nil && s.runtime != nil {
		runtimeHome = s.runtime.RuntimeHome()
	}
	content, err := scaffold.RenderMaterializedAgentConfig(runtimeHome, preset, materializedSharedNames(names))
	if err != nil {
		return agentResourceDocument{}, err
	}
	parsed := make(map[string]agentResourceDocument)
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		return agentResourceDocument{}, fmt.Errorf("parse materialized agent scaffold: %w", err)
	}
	doc, ok := parsed[names.Agent]
	if !ok {
		return agentResourceDocument{}, fmt.Errorf("materialized agent scaffold missing %q", names.Agent)
	}
	doc.ID = names.Agent
	doc.Path = ""
	return doc, nil
}

func (s *Server) renderMaterializedSkillResource(preset scaffold.KitPreset, names kitMaterializedResourceNames) (skillResourceDocument, error) {
	runtimeHome := ""
	if s != nil && s.runtime != nil {
		runtimeHome = s.runtime.RuntimeHome()
	}
	content, err := scaffold.RenderMaterializedSkillMarkdown(runtimeHome, preset, materializedSharedNames(names))
	if err != nil {
		return skillResourceDocument{}, err
	}
	return parseMaterializedSkillResource(runtimeHome, names.Skill, content)
}

func (s *Server) renderMaterializedWorkflowResource(preset scaffold.KitPreset, names kitMaterializedResourceNames) (agent.WorkflowGraphDocument, error) {
	runtimeHome := ""
	if s != nil && s.runtime != nil {
		runtimeHome = s.runtime.RuntimeHome()
	}
	content, err := scaffold.RenderMaterializedWorkflowYAML(runtimeHome, preset, materializedSharedNames(names))
	if err != nil {
		return agent.WorkflowGraphDocument{}, err
	}
	var doc agent.WorkflowGraphDocument
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return agent.WorkflowGraphDocument{}, fmt.Errorf("parse materialized workflow scaffold: %w", err)
	}
	if strings.TrimSpace(doc.Name) == "" {
		doc.Name = names.Workflow
	}
	return doc, nil
}

func (s *Server) renderMaterializedWorkflowTemplateResource(preset scaffold.KitPreset, names kitMaterializedResourceNames) (agent.WorkflowTemplate, error) {
	runtimeHome := ""
	if s != nil && s.runtime != nil {
		runtimeHome = s.runtime.RuntimeHome()
	}
	content, err := scaffold.RenderMaterializedWorkflowTemplateYAML(runtimeHome, preset, materializedSharedNames(names))
	if err != nil {
		return agent.WorkflowTemplate{}, err
	}
	var doc agent.WorkflowTemplate
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return agent.WorkflowTemplate{}, fmt.Errorf("parse materialized workflow template scaffold: %w", err)
	}
	if strings.TrimSpace(doc.Name) == "" {
		doc.Name = names.WorkflowTemplate
	}
	if strings.TrimSpace(doc.Title) == "" {
		doc.Title = strings.TrimSpace(preset.Title + " Workflow Template")
	}
	if strings.TrimSpace(doc.Description) == "" {
		doc.Description = "Reusable linked workflow template generated from a GoFlow kit scaffold."
	}
	if strings.TrimSpace(doc.Category) == "" {
		doc.Category = firstKitScaffoldValue(preset.Category, "custom")
	}
	if len(doc.Tags) == 0 {
		doc.Tags = mergeKitStringRefs(preset.Tags, []string{"materialized", "starter", "linked"}, false)
	}
	if strings.TrimSpace(doc.Graph.Name) == "" {
		doc.Graph.Name = doc.Name
	}
	return doc, nil
}

func (s *Server) renderMaterializedTeamTemplateResource(preset scaffold.KitPreset, names kitMaterializedResourceNames) (agent.TeamTemplate, error) {
	runtimeHome := ""
	if s != nil && s.runtime != nil {
		runtimeHome = s.runtime.RuntimeHome()
	}
	content, err := scaffold.RenderMaterializedTeamTemplateYAML(runtimeHome, preset, materializedSharedNames(names))
	if err != nil {
		return agent.TeamTemplate{}, err
	}
	var doc agent.TeamTemplate
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return agent.TeamTemplate{}, fmt.Errorf("parse materialized team template scaffold: %w", err)
	}
	if strings.TrimSpace(doc.Name) == "" {
		doc.Name = names.TeamTemplate
	}
	if strings.TrimSpace(doc.Title) == "" {
		doc.Title = strings.TrimSpace(preset.Title + " Team")
	}
	if strings.TrimSpace(doc.Description) == "" {
		doc.Description = "Linked starter team template generated with the kit scaffold."
	}
	if strings.TrimSpace(doc.Category) == "" {
		doc.Category = firstKitScaffoldValue(preset.Category, "custom")
	}
	if len(doc.Tags) == 0 {
		doc.Tags = mergeKitStringRefs(preset.Tags, []string{"materialized", "starter", "team"}, false)
	}
	if strings.TrimSpace(doc.RecommendedWorkflow) == "" {
		doc.RecommendedWorkflow = names.Workflow
	}
	if strings.TrimSpace(doc.RecommendedEntryAgent) == "" {
		doc.RecommendedEntryAgent = names.Agent
	}
	return doc, nil
}

func (s *Server) renderMaterializedPolicyRuleResource(preset scaffold.KitPreset, names kitMaterializedResourceNames) (policyRuleResourceDocument, error) {
	runtimeHome := ""
	if s != nil && s.runtime != nil {
		runtimeHome = s.runtime.RuntimeHome()
	}
	content, err := scaffold.RenderMaterializedPolicyRuleYAML(runtimeHome, preset, materializedSharedNames(names))
	if err != nil {
		return policyRuleResourceDocument{}, err
	}
	var definition agent.WorkflowPolicyRuleDefinition
	if err := yaml.Unmarshal([]byte(content), &definition); err != nil {
		return policyRuleResourceDocument{}, fmt.Errorf("parse materialized policy rule scaffold: %w", err)
	}
	return policyRuleResourceFromDefinition(agent.MigrateWorkflowPolicyRuleDefinition(definition), ""), nil
}

func parseMaterializedSkillResource(runtimeHome, name, content string) (skillResourceDocument, error) {
	base := runtimeHome
	if strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	base = filepath.Join(base, ".goflow", "tmp")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return skillResourceDocument{}, fmt.Errorf("create materialized skill temp root: %w", err)
	}
	tmpRoot, err := os.MkdirTemp(base, "materialized-skill-*")
	if err != nil {
		return skillResourceDocument{}, fmt.Errorf("create materialized skill temp dir: %w", err)
	}
	defer os.RemoveAll(tmpRoot)
	tmpDir := filepath.Join(tmpRoot, name)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return skillResourceDocument{}, fmt.Errorf("create materialized skill directory: %w", err)
	}
	tmpPath := filepath.Join(tmpDir, "SKILL.md")
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return skillResourceDocument{}, fmt.Errorf("write materialized skill file: %w", err)
	}
	parsed, err := skillpkg.ParseFile(tmpPath)
	if err != nil {
		return skillResourceDocument{}, err
	}
	doc := skillResourceFromSchema(*parsed)
	doc.Path = ""
	return doc, nil
}

func materializedSharedNames(names kitMaterializedResourceNames) scaffold.MaterializedKitNames {
	return scaffold.MaterializedKitNames{
		Kit:              names.Kit,
		Agent:            names.Agent,
		Skill:            names.Skill,
		Tool:             names.Tool,
		Workflow:         names.Workflow,
		WorkflowTemplate: names.WorkflowTemplate,
		TeamTemplate:     names.TeamTemplate,
		PolicyRule:       names.PolicyRule,
	}
}

func materializedKitPresetForTemplate(preset kitScaffoldPreset, doc kitResourceDocument) scaffold.KitPreset {
	examples := make([]scaffold.KitExample, 0, len(doc.Examples))
	for _, example := range doc.Examples {
		examples = append(examples, scaffold.KitExample{
			Title:       example.Title,
			Description: example.Description,
			Request:     example.Request,
			Workflow:    example.Workflow,
			Agent:       example.Agent,
		})
	}
	return scaffold.KitPreset{
		Name:                preset.Name,
		DefaultKitName:      doc.Name,
		Title:               doc.Title,
		Description:         doc.Description,
		Category:            doc.Category,
		Tags:                append([]string(nil), doc.Tags...),
		Providers:           append([]string(nil), doc.Providers...),
		Agents:              append([]string(nil), doc.Agents...),
		Skills:              append([]string(nil), doc.Skills...),
		Tools:               append([]string(nil), doc.Tools...),
		Workflows:           append([]string(nil), doc.Workflows...),
		WorkflowTemplates:   append([]string(nil), doc.WorkflowTemplates...),
		TeamTemplates:       append([]string(nil), doc.TeamTemplates...),
		PolicyRules:         append([]string(nil), doc.PolicyRules...),
		RequiredEnv:         append([]string(nil), doc.RequiredEnv...),
		RecommendedWorkflow: doc.Metadata["recommended_workflow"],
		RecommendedAgent:    doc.Metadata["recommended_agent"],
		Examples:            examples,
		Metadata:            copyMap(doc.Metadata),
	}
}

func (s *Server) materializedKitToolDocument(preset kitScaffoldPreset, names kitMaterializedResourceNames) (toolResourceDocument, error) {
	runtimeHome := ""
	if s != nil && s.runtime != nil {
		runtimeHome = s.runtime.RuntimeHome()
	}
	content, err := scaffold.RenderMaterializedToolConfig(runtimeHome, materializedKitPresetForTemplate(preset, kitResourceDocument{Name: names.Kit}), materializedSharedNames(names))
	if err != nil {
		return toolResourceDocument{}, err
	}
	var cfg toolResourceConfigDocument
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return toolResourceDocument{}, fmt.Errorf("parse materialized tool config scaffold: %w", err)
	}
	if len(cfg.MCPServers) != 1 {
		return toolResourceDocument{}, fmt.Errorf("materialized tool config scaffold must contain exactly one mcp server")
	}
	doc := toolResourceFromServerDocument(cfg.MCPServers[0])
	doc.Name = names.Tool
	doc.Description = fmt.Sprintf("Read-only containerized helper MCP server for the %s kit.", names.Kit)
	doc.Path = ""
	doc.ConfigPath = ""
	doc.Code = ""
	if preset.Name == "binary-analysis" {
		doc.Description = fmt.Sprintf("Read-only static binary triage helper MCP server for the %s kit.", names.Kit)
		code, err := scaffold.RenderBinaryAnalysisPythonMCPToolCode(s.runtimeHomeForScaffold(), names.Tool)
		if err != nil {
			code, _ = scaffold.RenderBinaryAnalysisPythonMCPToolCode("", names.Tool)
		}
		doc.Code = code
	} else {
		code, err := scaffold.RenderPythonMCPToolCode(s.runtimeHomeForScaffold(), names.Tool)
		if err != nil {
			code, _ = scaffold.RenderPythonMCPToolCode("", names.Tool)
		}
		doc.Code = code
	}
	return doc, nil
}

func (s *Server) kitMaterializationConflicts(bundle kitBundleDocument) []kitBundleSavedResource {
	conflicts := make([]kitBundleSavedResource, 0)
	add := func(kind, name string, exists bool) {
		name = normalizeResourceName(name)
		if exists {
			conflicts = append(conflicts, kitBundleSavedResource{Kind: kind, Name: name, Status: "exists", Message: "already exists"})
		}
	}
	for _, item := range bundle.Agents {
		name := agentBundleName(item)
		_, exists := s.agentResourceByName(name)
		add("agent", name, exists)
	}
	for _, item := range bundle.Skills {
		name := normalizeResourceName(item.Name)
		add("skill", name, s.skillResourceExists(name))
	}
	for _, item := range bundle.Tools {
		name := normalizeResourceName(item.Name)
		_, exists := s.toolResourceByName(name)
		add("tool", name, exists)
	}
	runner := s.runtime.WorkflowRunner()
	for _, item := range bundle.TeamTemplates {
		name := normalizeResourceName(item.Name)
		_, exists := runner.TeamTemplate(name)
		add("team_template", name, exists)
	}
	for _, item := range bundle.PolicyRules {
		name := normalizeResourceName(item.Name)
		_, exists := s.policyRuleResourceByName(name)
		add("policy_rule", name, exists)
	}
	for _, item := range bundle.Workflows {
		name := normalizeResourceName(item.Name)
		_, err := runner.LoadWorkflowGraphDocument(name)
		add("workflow", name, err == nil)
	}
	for _, item := range bundle.WorkflowTemplates {
		name := normalizeResourceName(item.Name)
		_, exists := runner.WorkflowTemplate(name)
		add("workflow_template", name, exists)
	}
	_, kitExists := s.kitResourceByName(bundle.Kit.Name)
	add("kit", bundle.Kit.Name, kitExists)
	return conflicts
}

func kitBundleConflictSummary(items []kitBundleSavedResource) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = strings.TrimSpace(item.Message)
		}
		parts = append(parts, strings.TrimSpace(item.Kind)+"/"+name)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func kitMaterializedActivationSteps(names kitMaterializedResourceNames) []string {
	return []string{
		fmt.Sprintf("Restart GoFlow or reload modules so configs/agents/%s.yaml and configs/mcp_servers/%s.yaml are active.", names.Agent, names.Tool),
		fmt.Sprintf("Run the generated workflow %q with a request and optional scope input.", names.Workflow),
		fmt.Sprintf("Open %q in Workflow Studio to customize node positions, policy routing, and team roles.", names.WorkflowTemplate),
		"Switch the helper tool scaffold to a write-capable container preset only when the workflow truly needs workspace mutation.",
	}
}

func firstKitScaffoldValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func presetExampleRequest(preset kitScaffoldPreset) string {
	for _, example := range preset.Examples {
		if strings.TrimSpace(example.Request) != "" {
			return strings.TrimSpace(example.Request)
		}
	}
	return ""
}

func overrideKitRefs(defaults, overrides []string, keepCase bool) []string {
	if len(overrides) > 0 {
		return mergeKitStringRefs(nil, overrides, keepCase)
	}
	return mergeKitStringRefs(nil, defaults, keepCase)
}

func mergeKitStringRefs(left, right []string, keepCase bool) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	out := make([]string, 0, len(left)+len(right))
	for _, value := range append(append([]string(nil), left...), right...) {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		if !keepCase {
			normalized = normalizeResourceName(normalized)
		}
		key := strings.ToLower(normalized)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func mergeKitMetadata(left, right map[string]string) map[string]string {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	out := make(map[string]string, len(left)+len(right))
	for key, value := range left {
		if strings.TrimSpace(key) != "" {
			out[key] = value
		}
	}
	for key, value := range right {
		if strings.TrimSpace(key) != "" {
			out[key] = value
		}
	}
	return out
}

func kitValidationIssuesBySeverity(items []kitValidationItem, severity string) []kitValidationItem {
	if len(items) == 0 {
		return nil
	}
	severity = strings.ToLower(strings.TrimSpace(severity))
	out := make([]kitValidationItem, 0)
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item.Severity)) == severity {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) buildKitBundle(name string, includeSecrets bool) (kitBundleDocument, error) {
	doc, ok := s.kitResourceByName(name)
	if !ok {
		return kitBundleDocument{}, fmt.Errorf("kit %q not found", name)
	}
	bundle := kitBundleDocument{
		Kind:    kitBundleKind,
		Version: kitBundleVersion,
		Kit:     sanitizeKitForBundle(doc),
	}
	warn := func(kind, ref string) {
		bundle.Warnings = append(bundle.Warnings, fmt.Sprintf("%s %q is referenced by the kit but no custom resource document was found", kind, ref))
	}
	for _, ref := range doc.Providers {
		item, ok := s.providerResourceByName(ref)
		if !ok {
			warn("provider", ref)
			continue
		}
		if includeSecrets && strings.TrimSpace(item.APIKey) == "" {
			if provider, found := s.runtime.Provider(ref); found {
				item = providerResourceFromConfig(normalizeResourceName(ref), provider, true)
			}
		}
		if !includeSecrets {
			item.APIKey = ""
		}
		item.Path = ""
		bundle.Providers = append(bundle.Providers, item)
	}
	for _, ref := range doc.Agents {
		item, ok := s.agentResourceByName(ref)
		if !ok {
			warn("agent", ref)
			continue
		}
		item.Path = ""
		bundle.Agents = append(bundle.Agents, item)
	}
	for _, ref := range doc.Skills {
		item, ok := s.skillResourceByName(ref)
		if !ok {
			if parsed, found, err := s.loadSkillResourceFromDisk(ref); err == nil && found {
				item = skillResourceFromSchema(parsed)
				ok = true
			}
		}
		if !ok {
			warn("skill", ref)
			continue
		}
		item.Path = ""
		bundle.Skills = append(bundle.Skills, item)
	}
	for _, ref := range doc.Tools {
		item, ok := s.toolResourceByName(ref)
		if !ok || strings.TrimSpace(item.Code) == "" {
			warn("tool", ref)
			continue
		}
		item.Path = ""
		item.ConfigPath = ""
		bundle.Tools = append(bundle.Tools, item)
	}
	runner := s.runtime.WorkflowRunner()
	for _, ref := range doc.Workflows {
		item, err := runner.LoadWorkflowGraphDocument(ref)
		if err != nil {
			warn("workflow", ref)
			continue
		}
		bundle.Workflows = append(bundle.Workflows, item)
	}
	for _, ref := range doc.WorkflowTemplates {
		item, ok := runner.WorkflowTemplate(ref)
		if !ok {
			warn("workflow_template", ref)
			continue
		}
		bundle.WorkflowTemplates = append(bundle.WorkflowTemplates, item)
	}
	for _, ref := range doc.TeamTemplates {
		item, ok := runner.TeamTemplate(ref)
		if !ok {
			warn("team_template", ref)
			continue
		}
		bundle.TeamTemplates = append(bundle.TeamTemplates, item)
	}
	for _, ref := range doc.PolicyRules {
		item, ok := s.policyRuleResourceByName(ref)
		if !ok {
			warn("policy_rule", ref)
			continue
		}
		item.Path = ""
		bundle.PolicyRules = append(bundle.PolicyRules, item)
	}
	sort.Strings(bundle.Warnings)
	return bundle, nil
}

func (s *Server) importKitBundle(bundle kitBundleDocument, overwrite bool) kitBundleImportResult {
	bundle = normalizeKitBundle(bundle)
	result := kitBundleImportResult{Warnings: append([]string(nil), bundle.Warnings...)}
	if len(bundle.Providers) > 0 || len(bundle.Agents) > 0 || len(bundle.Tools) > 0 {
		result.Restart = true
		result.Warnings = append(result.Warnings, "provider, agent, and MCP tool resource modules are written to disk and become active after restarting or reloading the runtime")
	}
	save := func(kind, name string, exists bool, fn func() error) {
		name = normalizeResourceName(name)
		if name == "" {
			result.Errors = append(result.Errors, kitBundleSavedResource{Kind: kind, Status: "error", Message: "resource name is required"})
			return
		}
		if exists && !overwrite {
			result.Skipped = append(result.Skipped, kitBundleSavedResource{Kind: kind, Name: name, Status: "skipped", Message: "already exists"})
			return
		}
		if err := fn(); err != nil {
			result.Errors = append(result.Errors, kitBundleSavedResource{Kind: kind, Name: name, Status: "error", Message: err.Error()})
			return
		}
		status := "created"
		if exists {
			status = "updated"
		}
		result.Saved = append(result.Saved, kitBundleSavedResource{Kind: kind, Name: name, Status: status})
	}
	for _, item := range bundle.Providers {
		name := providerBundleName(item)
		_, exists := s.providerResourceByName(name)
		doc := item
		save("provider", name, exists, func() error {
			_, err := s.saveProviderResource(name, doc)
			return err
		})
	}
	for _, item := range bundle.Agents {
		name := agentBundleName(item)
		_, exists := s.agentResourceByName(name)
		doc := item
		save("agent", name, exists, func() error {
			_, err := s.saveAgentResource(name, doc)
			return err
		})
	}
	for _, item := range bundle.Skills {
		name := normalizeResourceName(item.Name)
		exists := s.skillResourceExists(name)
		doc := item
		save("skill", name, exists, func() error {
			_, err := s.saveSkillResource(name, doc)
			return err
		})
	}
	for _, item := range bundle.Tools {
		name := normalizeResourceName(item.Name)
		_, exists := s.toolResourceByName(name)
		doc := item
		save("tool", name, exists, func() error {
			_, err := s.saveToolResource(name, doc)
			return err
		})
	}
	runner := s.runtime.WorkflowRunner()
	for _, item := range bundle.TeamTemplates {
		name := normalizeResourceName(item.Name)
		_, exists := runner.TeamTemplate(name)
		doc := item
		save("team_template", name, exists, func() error {
			_, err := s.runtime.SaveTeamTemplateResource(name, doc)
			return err
		})
	}
	for _, item := range bundle.PolicyRules {
		name := normalizeResourceName(item.Name)
		_, exists := s.policyRuleResourceByName(name)
		doc := item
		save("policy_rule", name, exists, func() error {
			_, err := s.savePolicyRuleResource(name, doc)
			return err
		})
	}
	for _, item := range bundle.Workflows {
		name := normalizeResourceName(item.Name)
		_, err := runner.LoadWorkflowGraphDocument(name)
		exists := err == nil
		doc := item
		save("workflow", name, exists, func() error {
			return runner.SaveWorkflowGraphDocument(name, doc)
		})
	}
	for _, item := range bundle.WorkflowTemplates {
		name := normalizeResourceName(item.Name)
		_, exists := runner.WorkflowTemplate(name)
		doc := item
		save("workflow_template", name, exists, func() error {
			_, err := s.runtime.SaveWorkflowTemplateResource(name, doc)
			return err
		})
	}
	kitName := normalizeResourceName(bundle.Kit.Name)
	_, kitExists := s.kitResourceByName(kitName)
	kitDoc := bundle.Kit
	save("kit", kitName, kitExists, func() error {
		saved, err := s.saveKitResource(kitName, kitDoc)
		if err == nil {
			result.Kit = saved
			result.Validation = saved.Validation
		}
		return err
	})
	if result.Kit.Name == "" {
		if kit, ok := s.kitResourceByName(kitName); ok {
			result.Kit = kit
			result.Validation = s.validateKitResource(kit)
		}
	}
	return result
}

func renderKitBundleDocument(bundle kitBundleDocument, format string) ([]byte, string, string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		data, err := json.MarshalIndent(bundle, "", "  ")
		return append(data, '\n'), "application/json", "json", err
	case "yaml", "yml":
		data, err := yaml.Marshal(bundle)
		return data, "application/yaml; charset=utf-8", "yaml", err
	default:
		return nil, "", "", fmt.Errorf("unsupported kit bundle export format: %s", format)
	}
}

func decodeKitBundleDocument(r *http.Request) (kitBundleDocument, error) {
	var bundle kitBundleDocument
	data, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		return bundle, fmt.Errorf("read kit bundle: %w", err)
	}
	return decodeKitBundleData(data, r.Header.Get("Content-Type"))
}

func decodeKitBundleData(data []byte, contentType string) (kitBundleDocument, error) {
	var bundle kitBundleDocument
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return bundle, fmt.Errorf("kit bundle body is required")
	}
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "yaml") || strings.Contains(contentType, "yml") {
		if err := yaml.Unmarshal(data, &bundle); err != nil {
			return bundle, fmt.Errorf("invalid kit bundle yaml: %w", err)
		}
		bundle = normalizeKitBundle(bundle)
		return bundle, validateKitBundle(bundle)
	}
	if err := json.Unmarshal(data, &bundle); err != nil {
		if yamlErr := yaml.Unmarshal(data, &bundle); yamlErr != nil {
			return bundle, fmt.Errorf("invalid kit bundle json: %w", err)
		}
	}
	bundle = normalizeKitBundle(bundle)
	return bundle, validateKitBundle(bundle)
}

func kitBundleImportSummaryFromResult(result kitBundleImportResult) KitBundleImportSummary {
	summary := KitBundleImportSummary{
		KitName:         result.Kit.Name,
		Warnings:        append([]string(nil), result.Warnings...),
		RestartRequired: result.Restart,
		ValidationValid: result.Validation.Valid,
	}
	summary.Saved = kitBundleResourceStatuses(result.Saved)
	summary.Skipped = kitBundleResourceStatuses(result.Skipped)
	summary.Errors = kitBundleResourceStatuses(result.Errors)
	for _, issue := range result.Validation.Issues {
		summary.ValidationIssues = append(summary.ValidationIssues, KitBundleIssue{
			Severity:       issue.Severity,
			Code:           issue.Code,
			Field:          issue.Field,
			Message:        issue.Message,
			Ref:            issue.Ref,
			Recommendation: issue.Recommendation,
		})
	}
	return summary
}

func kitBundleResourceStatuses(values []kitBundleSavedResource) []KitBundleResourceStatus {
	if len(values) == 0 {
		return nil
	}
	out := make([]KitBundleResourceStatus, 0, len(values))
	for _, value := range values {
		out = append(out, KitBundleResourceStatus{
			Kind:    value.Kind,
			Name:    value.Name,
			Status:  value.Status,
			Message: value.Message,
		})
	}
	return out
}

func validateKitBundle(bundle kitBundleDocument) error {
	if strings.TrimSpace(bundle.Kind) != kitBundleKind {
		return fmt.Errorf("unsupported kit bundle kind %q", bundle.Kind)
	}
	if bundle.Version > kitBundleVersion {
		return fmt.Errorf("kit bundle version %d is newer than supported version %d", bundle.Version, kitBundleVersion)
	}
	if strings.TrimSpace(bundle.Kit.Name) == "" {
		return fmt.Errorf("kit bundle requires kit.name")
	}
	return validateKitResourceDocument(bundle.Kit)
}

func normalizeKitBundle(bundle kitBundleDocument) kitBundleDocument {
	if strings.TrimSpace(bundle.Kind) == "" {
		bundle.Kind = kitBundleKind
	}
	if bundle.Version == 0 {
		bundle.Version = kitBundleVersion
	}
	bundle.Kit = normalizeKitResource(bundle.Kit, bundle.Kit.Name)
	for i := range bundle.Providers {
		if strings.TrimSpace(bundle.Providers[i].ID) == "" && i < len(bundle.Kit.Providers) {
			bundle.Providers[i].ID = bundle.Kit.Providers[i]
		}
	}
	for i := range bundle.Agents {
		if strings.TrimSpace(bundle.Agents[i].ID) == "" && i < len(bundle.Kit.Agents) {
			bundle.Agents[i].ID = bundle.Kit.Agents[i]
		}
	}
	return bundle
}

func sanitizeKitForBundle(doc kitResourceDocument) kitResourceDocument {
	doc.Path = ""
	doc.Validation = kitValidationResult{}
	return doc
}

func providerBundleName(doc providerResourceDocument) string {
	if strings.TrimSpace(doc.ID) != "" {
		return normalizeResourceName(doc.ID)
	}
	return normalizeResourceName(doc.Provider)
}

func agentBundleName(doc agentResourceDocument) string {
	if strings.TrimSpace(doc.ID) != "" {
		return normalizeResourceName(doc.ID)
	}
	return normalizeResourceName(doc.Name)
}

func (s *Server) skillResourceExists(name string) bool {
	if _, ok := s.skillResourceByName(name); ok {
		return true
	}
	_, ok, err := s.loadSkillResourceFromDisk(name)
	return err == nil && ok
}

func truthyQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func (s *Server) validateKitResource(doc kitResourceDocument) kitValidationResult {
	result := kitValidationResult{Valid: true, Name: doc.Name}
	add := func(severity, code, message, ref string) {
		result.Issues = append(result.Issues, kitValidationItem{
			Severity:       severity,
			Code:           code,
			Field:          kitValidationFieldForCode(code),
			Message:        message,
			Ref:            ref,
			Recommendation: kitValidationRecommendationForCode(code),
		})
		if severity == "error" {
			result.Valid = false
		}
	}
	if err := validateKitResourceDocument(doc); err != nil {
		add("error", "kit_invalid", err.Error(), doc.Name)
	}
	for _, name := range doc.Providers {
		if _, ok := s.providerResourceByName(name); !ok {
			add("error", "provider_missing", "provider "+name+" is not configured", name)
		}
	}
	for _, name := range doc.Agents {
		if _, ok := s.agentResourceByName(name); !ok {
			add("error", "agent_missing", "agent "+name+" is not configured", name)
		}
	}
	skillSet := make(map[string]struct{})
	for _, item := range s.runtime.SkillList() {
		skillSet[normalizeResourceName(item.Name)] = struct{}{}
	}
	for _, name := range doc.Skills {
		if _, ok := skillSet[normalizeResourceName(name)]; ok {
			continue
		}
		if _, ok, err := s.loadSkillResourceFromDisk(name); err != nil || !ok {
			add("error", "skill_missing", "skill "+name+" is not available", name)
		}
	}
	toolSet := make(map[string]struct{})
	for _, name := range s.runtime.ToolNames() {
		toolSet[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	for _, name := range doc.Tools {
		if _, ok := toolSet[strings.ToLower(strings.TrimSpace(name))]; ok {
			continue
		}
		if _, ok := s.toolResourceByName(name); !ok {
			add("warning", "tool_unseen", "tool "+name+" is not currently discovered", name)
		}
	}
	workflowSet := make(map[string]struct{})
	runner := s.runtime.WorkflowRunner()
	for _, name := range []string{"plan-fix-audit", "skill-chain"} {
		workflowSet[normalizeResourceName(name)] = struct{}{}
	}
	for _, item := range runner.ListWorkflowGraphs() {
		workflowSet[normalizeResourceName(item.Name)] = struct{}{}
	}
	for _, name := range doc.Workflows {
		if _, ok := workflowSet[normalizeResourceName(name)]; !ok {
			add("error", "workflow_missing", "workflow "+name+" is not available", name)
		}
	}
	templateSet := make(map[string]struct{})
	for _, item := range runner.WorkflowTemplates() {
		templateSet[normalizeResourceName(item.Name)] = struct{}{}
	}
	for _, name := range doc.WorkflowTemplates {
		if _, ok := templateSet[normalizeResourceName(name)]; !ok {
			add("error", "workflow_template_missing", "workflow template "+name+" is not available", name)
		}
	}
	teamSet := make(map[string]struct{})
	for _, item := range runner.TeamTemplates() {
		teamSet[normalizeResourceName(item.Name)] = struct{}{}
	}
	for _, name := range doc.TeamTemplates {
		if _, ok := teamSet[normalizeResourceName(name)]; !ok {
			add("error", "team_template_missing", "team template "+name+" is not available", name)
		}
	}
	policySet := make(map[string]struct{})
	for _, item := range runner.WorkflowPolicyRules() {
		policySet[normalizeResourceName(item.Name)] = struct{}{}
	}
	for _, name := range doc.PolicyRules {
		if _, ok := policySet[normalizeResourceName(name)]; !ok {
			add("error", "policy_rule_missing", "policy rule "+name+" is not available", name)
		}
	}
	for _, name := range doc.RequiredEnv {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			add("warning", "env_missing", "environment variable "+name+" is not set", name)
		}
	}
	return result
}

func kitValidationFieldForCode(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_body":
		return "body"
	case "invalid_name":
		return "name"
	case "kit_invalid":
		return "name"
	case "provider_missing":
		return "providers"
	case "agent_missing":
		return "agents"
	case "skill_missing":
		return "skills"
	case "tool_unseen":
		return "tools"
	case "workflow_missing":
		return "workflows"
	case "workflow_template_missing":
		return "workflow_templates"
	case "team_template_missing":
		return "team_templates"
	case "policy_rule_missing":
		return "policy_rules"
	case "env_missing":
		return "required_env"
	default:
		return ""
	}
}

func kitValidationRecommendationForCode(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_body":
		return "fix the YAML or JSON body and try validation again"
	case "invalid_name":
		return "use lowercase letters, numbers, hyphen, or underscore"
	case "kit_invalid":
		return "fix the kit manifest metadata before saving or importing it"
	case "provider_missing":
		return "create or import the referenced provider module, or remove it from the kit"
	case "agent_missing":
		return "create or import the referenced agent module, or remove it from the kit"
	case "skill_missing":
		return "create or import the referenced skill, or remove it from the kit"
	case "tool_unseen":
		return "enable or scaffold the referenced tool if the kit requires it"
	case "workflow_missing":
		return "create or import the referenced workflow graph, or remove it from the kit"
	case "workflow_template_missing":
		return "create or import the referenced workflow template, or remove it from the kit"
	case "team_template_missing":
		return "create or import the referenced team template, or remove it from the kit"
	case "policy_rule_missing":
		return "create or import the referenced policy rule, or remove it from the kit"
	case "env_missing":
		return "set the required environment variable before activating this kit"
	default:
		return "review the kit validation issue before activation"
	}
}

func validateKitResourceDocument(doc kitResourceDocument) error {
	if strings.TrimSpace(doc.Kind) != "" && strings.TrimSpace(doc.Kind) != kitResourceKind {
		return fmt.Errorf("unsupported kit kind %q", doc.Kind)
	}
	version := doc.Version
	if version == 0 {
		version = kitResourceVersion
	}
	if version > kitResourceVersion {
		return fmt.Errorf("kit version %d is newer than supported version %d", version, kitResourceVersion)
	}
	if doc.MinVersion > kitResourceVersion {
		return fmt.Errorf("kit requires reader version %d, supported version is %d", doc.MinVersion, kitResourceVersion)
	}
	if !resourceNamePattern.MatchString(normalizeResourceName(doc.Name)) {
		return fmt.Errorf("invalid kit name %q: use lowercase letters, numbers, hyphen, or underscore", doc.Name)
	}
	return nil
}

func normalizeKitResource(doc kitResourceDocument, fallbackName string) kitResourceDocument {
	name := normalizeResourceName(firstWorkflowRunQueryValue(doc.Name, fallbackName))
	doc.Kind = kitResourceKind
	doc.Version = kitResourceVersion
	if doc.MinVersion == 0 {
		doc.MinVersion = kitResourceVersion
	}
	doc.Name = name
	if strings.TrimSpace(doc.Title) == "" {
		doc.Title = name
	}
	doc.Agents = normalizeKitRefs(doc.Agents)
	doc.Providers = normalizeKitRefs(doc.Providers)
	doc.Skills = normalizeKitRefs(doc.Skills)
	doc.Tools = normalizeKitToolRefs(doc.Tools)
	doc.Workflows = normalizeKitRefs(doc.Workflows)
	doc.WorkflowTemplates = normalizeKitRefs(doc.WorkflowTemplates)
	doc.TeamTemplates = normalizeKitRefs(doc.TeamTemplates)
	doc.PolicyRules = normalizeKitRefs(doc.PolicyRules)
	doc.RequiredEnv = normalizeKitEnvRefs(doc.RequiredEnv)
	return doc
}

func normalizeKitRefs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := normalizeResourceName(value)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func normalizeKitToolRefs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		key := strings.ToLower(normalized)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func normalizeKitEnvRefs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func (s *Server) kitResourceRoot() (string, error) {
	return s.runtimeResourceRoot("kits")
}

func (s *Server) kitResourcePath(name string) (string, error) {
	root, err := s.kitResourceRoot()
	if err != nil {
		return "", err
	}
	name = normalizeResourceName(name)
	path := filepath.Clean(filepath.Join(root, name, "kit.yaml"))
	if !referenceWithinBase(root, path) {
		return "", fmt.Errorf("kit path escapes runtime kit directory")
	}
	return path, nil
}

func (s *Server) kitLegacyResourcePath(name string) (string, error) {
	root, err := s.kitResourceRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Clean(filepath.Join(root, normalizeResourceName(name)+".yaml"))
	if !referenceWithinBase(root, path) {
		return "", fmt.Errorf("kit path escapes runtime kit directory")
	}
	return path, nil
}
