package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/scaffold"
	"gopkg.in/yaml.v3"
)

type toolScaffoldPreset struct {
	Name            string                `json:"name"`
	DefaultName     string                `json:"default_name"`
	Title           string                `json:"title"`
	Description     string                `json:"description"`
	Category        string                `json:"category"`
	Tags            []string              `json:"tags,omitempty"`
	Language        string                `json:"language"`
	Isolation       string                `json:"isolation"`
	RiskLevel       string                `json:"risk_level,omitempty"`
	RequiresRestart bool                  `json:"requires_restart"`
	Capabilities    []string              `json:"capabilities,omitempty"`
	SafetyGuards    []string              `json:"safety_guards,omitempty"`
	GeneratedPaths  []string              `json:"generated_paths,omitempty"`
	ActivationSteps []string              `json:"activation_steps,omitempty"`
	Recommendations []string              `json:"recommendations,omitempty"`
	Document        *toolResourceDocument `json:"document,omitempty"`
}

type toolScaffoldRequest struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Overwrite   bool   `json:"overwrite" yaml:"overwrite"`
}

type toolScaffoldResponse struct {
	Created bool                 `json:"created"`
	Updated bool                 `json:"updated,omitempty"`
	Preset  toolScaffoldPreset   `json:"preset"`
	Tool    toolResourceDocument `json:"tool"`
}

func (s *Server) handleToolScaffolds(w http.ResponseWriter, r *http.Request, parts []string) {
	switch r.Method {
	case http.MethodGet:
		if len(parts) == 0 {
			writeJSON(w, s.toolScaffoldPresets(false))
			return
		}
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		preset, ok := s.toolScaffoldPreset(parts[0], true)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if name := normalizeResourceName(r.URL.Query().Get("name")); name != "" {
			doc, err := s.toolDocumentFromScaffold(preset, toolScaffoldRequest{Name: name})
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
		preset, ok := s.toolScaffoldPreset(parts[0], true)
		if !ok {
			http.NotFound(w, r)
			return
		}
		req, err := decodeToolScaffoldRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if truthyQuery(r.URL.Query().Get("overwrite")) {
			req.Overwrite = true
		}
		doc, err := s.toolDocumentFromScaffold(preset, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		existed := false
		if _, ok := s.toolResourceByName(doc.Name); ok {
			existed = true
			if !req.Overwrite {
				http.Error(w, fmt.Sprintf("tool %q already exists; pass overwrite=true to replace it", doc.Name), http.StatusConflict)
				return
			}
		}
		saved, err := s.saveToolResource(doc.Name, doc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response := toolScaffoldResponse{Created: !existed, Updated: existed, Preset: preset, Tool: saved}
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

func decodeToolScaffoldRequest(r *http.Request) (toolScaffoldRequest, error) {
	var req toolScaffoldRequest
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

func (s *Server) toolScaffoldPreset(name string, includeDocument bool) (toolScaffoldPreset, bool) {
	name = normalizeResourceName(name)
	for _, preset := range s.toolScaffoldPresets(includeDocument) {
		if preset.Name == name {
			return preset, true
		}
	}
	return toolScaffoldPreset{}, false
}

func (s *Server) toolScaffoldPresets(includeDocument bool) []toolScaffoldPreset {
	presets := s.toolScaffoldPresetDefinitions()
	if !includeDocument {
		return presets
	}
	for i := range presets {
		doc, err := s.toolDocumentFromScaffold(presets[i], toolScaffoldRequest{Name: presets[i].DefaultName})
		if err == nil {
			presets[i].Document = &doc
		}
	}
	return presets
}

func (s *Server) toolScaffoldPresetDefinitions() []toolScaffoldPreset {
	if s == nil || s.runtime == nil || strings.TrimSpace(s.runtime.RuntimeHome()) == "" {
		return defaultToolScaffoldPresets()
	}
	presets, err := scaffold.ToolPresetsFromDirs(filepath.Join(s.runtime.RuntimeHome(), "templates", "tools", "scaffolds"))
	if err != nil {
		return defaultToolScaffoldPresets()
	}
	return toolScaffoldPresetsFromShared(presets)
}

func defaultToolScaffoldPresets() []toolScaffoldPreset {
	return toolScaffoldPresetsFromShared(scaffold.BuiltInToolPresets())
}

func toolScaffoldPresetsFromShared(presets []scaffold.ToolPreset) []toolScaffoldPreset {
	out := make([]toolScaffoldPreset, 0, len(presets))
	for _, preset := range presets {
		out = append(out, toolScaffoldPresetFromShared(preset))
	}
	return out
}

func toolScaffoldPresetFromShared(preset scaffold.ToolPreset) toolScaffoldPreset {
	doc := toolScaffoldPreset{
		Name:            preset.Name,
		DefaultName:     preset.DefaultName,
		Title:           preset.Title,
		Description:     preset.Description,
		Category:        preset.Category,
		Tags:            append([]string(nil), preset.Tags...),
		Language:        preset.Language,
		Isolation:       preset.Isolation,
		RiskLevel:       preset.RiskLevel,
		RequiresRestart: preset.RequiresRestart,
		Capabilities:    append([]string(nil), preset.Capabilities...),
		SafetyGuards:    append([]string(nil), preset.SafetyGuards...),
		GeneratedPaths:  append([]string(nil), preset.GeneratedPaths...),
		ActivationSteps: append([]string(nil), preset.ActivationSteps...),
		Recommendations: append([]string(nil), preset.Recommendations...),
	}
	applyToolScaffoldPresetDefaults(&doc)
	return doc
}

func applyToolScaffoldPresetDefaults(preset *toolScaffoldPreset) {
	if preset == nil {
		return
	}
	if strings.TrimSpace(preset.Language) == "" {
		preset.Language = "python"
	}
	if strings.TrimSpace(preset.Isolation) == "" {
		preset.Isolation = "auto"
	}
	if len(preset.GeneratedPaths) == 0 {
		preset.GeneratedPaths = commonPythonMCPScaffoldGeneratedPaths()
	}
	if len(preset.ActivationSteps) == 0 {
		preset.ActivationSteps = commonPythonMCPScaffoldActivationSteps()
	}
	if len(preset.SafetyGuards) == 0 {
		preset.SafetyGuards = commonPythonMCPScaffoldSafetyGuards()
	}
}

func commonPythonMCPScaffoldSafetyGuards() []string {
	return []string{
		"strict JSON schemas with additionalProperties=false",
		"GOFLOW_WORKSPACE_ROOT path scoping",
		"path traversal rejection",
		"symlink escape rejection for reads and write parents",
		"workspace-root write refusal",
		"bounded UTF-8 text reads",
		"tool-level is_error responses",
		"MCP command allowlist in generated config",
	}
}

func commonPythonMCPScaffoldGeneratedPaths() []string {
	return []string{
		"mcp_servers/<name>.py",
		"configs/mcp_servers/<name>.yaml",
	}
}

func commonPythonMCPScaffoldActivationSteps() []string {
	return []string{
		"review generated code and tool descriptions",
		"keep agent allowed_tools narrow before enabling broad access",
		"restart GoFlow or rebootstrap MCP servers so the new config is loaded",
		"confirm the server appears in /tools or the Studio tool catalog",
	}
}

func (s *Server) toolDocumentFromScaffold(preset toolScaffoldPreset, req toolScaffoldRequest) (toolResourceDocument, error) {
	name := normalizeResourceName(firstWorkflowRunQueryValue(req.Name, preset.DefaultName, preset.Name))
	if !resourceNamePattern.MatchString(name) {
		return toolResourceDocument{}, fmt.Errorf("invalid tool name %q: use lowercase letters, numbers, hyphen, or underscore", name)
	}
	doc := s.defaultToolResource(name)
	doc.Description = firstWorkflowRunQueryValue(req.Description, preset.Description, doc.Description)
	doc.Isolation = "auto"
	code, err := scaffold.RenderPythonMCPToolCode(s.runtimeHomeForScaffold(), name)
	if err != nil {
		code, _ = scaffold.RenderPythonMCPToolCode("", name)
	}
	doc.Code = code
	return doc, nil
}
