package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	slashpath "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/llm"
	"github.com/FyMatt/GoFlow-Agent/internal/scaffold"
	"github.com/FyMatt/GoFlow-Agent/internal/skill"
	"github.com/FyMatt/GoFlow-Agent/internal/version"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

var resourceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

const maxSkillResourceTextBytes = 1 << 20

const (
	resourceCatalogSchema        = "goflow.api.resources"
	resourceCatalogSchemaVersion = 1
)

var editableSkillResourceDirs = map[string]struct{}{
	"references": {},
	"scripts":    {},
	"assets":     {},
	"templates":  {},
	"agents":     {},
}

type skillResourceDocument struct {
	Name             string                 `json:"name" yaml:"name"`
	Description      string                 `json:"description" yaml:"description"`
	Version          string                 `json:"version" yaml:"version"`
	Author           string                 `json:"author" yaml:"author"`
	Format           string                 `json:"format,omitempty" yaml:"format,omitempty"`
	Mode             string                 `json:"mode" yaml:"mode"`
	PreferredAgent   string                 `json:"preferred_agent" yaml:"preferred_agent"`
	AllowedToolKinds []string               `json:"allowed_tool_kinds" yaml:"allowed_tool_kinds"`
	OutputKind       string                 `json:"output_kind" yaml:"output_kind"`
	Priority         int                    `json:"priority,omitempty" yaml:"priority,omitempty"`
	MaxIterations    int                    `json:"max_iterations,omitempty" yaml:"max_iterations,omitempty"`
	NextSkills       []string               `json:"next_skills,omitempty" yaml:"next_skills,omitempty"`
	Tools            []schema.SkillTool     `json:"tools,omitempty" yaml:"tools,omitempty"`
	Params           []schema.SkillParam    `json:"params,omitempty" yaml:"params,omitempty"`
	Scripts          []schema.SkillScript   `json:"scripts,omitempty" yaml:"scripts,omitempty"`
	Activation       schema.Activation      `json:"activation" yaml:"activation"`
	Metadata         map[string]string      `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Instructions     string                 `json:"instructions"`
	Resources        []schema.SkillResource `json:"resources,omitempty" yaml:"-"`
	Path             string                 `json:"path,omitempty"`
}

type skillFileResourceDocument struct {
	Path    string `json:"path"`
	Kind    string `json:"kind,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Content string `json:"content,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
}

type resourceValidationIssue struct {
	Severity       string `json:"severity"`
	Code           string `json:"code,omitempty"`
	Field          string `json:"field,omitempty"`
	Message        string `json:"message"`
	Recommendation string `json:"recommendation,omitempty"`
}

type resourceValidationResult struct {
	Valid           bool                      `json:"valid"`
	Resource        string                    `json:"resource"`
	Name            string                    `json:"name,omitempty"`
	Normalized      any                       `json:"normalized,omitempty"`
	RestartRequired bool                      `json:"restart_required,omitempty"`
	ApplyState      string                    `json:"apply_state,omitempty"`
	ApplyMessage    string                    `json:"apply_message,omitempty"`
	Issues          []resourceValidationIssue `json:"issues,omitempty"`
}

type providerTestResult struct {
	Valid     bool                      `json:"valid"`
	Resource  string                    `json:"resource"`
	Name      string                    `json:"name,omitempty"`
	Status    string                    `json:"status"`
	Message   string                    `json:"message"`
	Provider  string                    `json:"provider,omitempty"`
	Model     string                    `json:"model,omitempty"`
	LatencyMS int64                     `json:"latency_ms,omitempty"`
	Issues    []resourceValidationIssue `json:"issues,omitempty"`
}

type resourceCatalogResponse struct {
	Meta     resourceCatalogMetadata `json:"meta"`
	Counts   resourceCatalogCounts   `json:"counts"`
	Summary  resourceCatalogSummary  `json:"summary"`
	Families []resourceFamilySummary `json:"families"`
}

type resourceCatalogMetadata struct {
	Schema                    string `json:"schema"`
	SchemaVersion             int    `json:"schema_version"`
	MinSupportedSchemaVersion int    `json:"min_supported_schema_version"`
	RuntimeVersion            string `json:"runtime_version"`
	CacheKey                  string `json:"cache_key"`
}

type resourceCatalogCounts struct {
	Families               int `json:"families"`
	TotalResources         int `json:"total_resources"`
	RestartRequired        int `json:"restart_required"`
	HotReloadable          int `json:"hot_reloadable"`
	Scaffoldable           int `json:"scaffoldable"`
	WithValidation         int `json:"with_validation"`
	WithDiagnostics        int `json:"with_diagnostics"`
	WithCustomActions      int `json:"with_custom_actions"`
	InvalidResources       int `json:"invalid_resources,omitempty"`
	FileBackedFamilies     int `json:"file_backed_families"`
	ActiveApplyFamilies    int `json:"active_apply_families"`
	RestartApplyFamilies   int `json:"restart_apply_families"`
	HotReloadApplyFamilies int `json:"hot_reload_apply_families"`
}

type resourceCatalogSummary struct {
	RestartRequired bool     `json:"restart_required"`
	DiagnosticsPath string   `json:"diagnostics_path,omitempty"`
	ApplyStates     []string `json:"apply_states,omitempty"`
	Notes           []string `json:"notes,omitempty"`
}

type resourceFamilySummary struct {
	Kind                  string               `json:"kind"`
	DisplayName           string               `json:"display_name,omitempty"`
	Description           string               `json:"description,omitempty"`
	Count                 int                  `json:"count"`
	InvalidCount          int                  `json:"invalid_count,omitempty"`
	CollectionPath        string               `json:"collection_path,omitempty"`
	DetailPath            string               `json:"detail_path,omitempty"`
	ValidatePath          string               `json:"validate_path,omitempty"`
	ScaffoldPath          string               `json:"scaffold_path,omitempty"`
	StorageRoot           string               `json:"storage_root,omitempty"`
	FileBacked            bool                 `json:"file_backed"`
	CanList               bool                 `json:"can_list"`
	CanCreate             bool                 `json:"can_create"`
	CanUpdate             bool                 `json:"can_update"`
	CanDelete             bool                 `json:"can_delete"`
	CanValidate           bool                 `json:"can_validate"`
	CanScaffold           bool                 `json:"can_scaffold"`
	HotReloadSupported    bool                 `json:"hot_reload_supported"`
	RestartRequiredOnSave bool                 `json:"restart_required_on_save"`
	ApplyStateOnSave      string               `json:"apply_state_on_save,omitempty"`
	ApplyMessage          string               `json:"apply_message,omitempty"`
	DiagnosticsPath       string               `json:"diagnostics_path,omitempty"`
	RelatedCapabilities   []string             `json:"related_capabilities,omitempty"`
	Actions               []helpResourceAction `json:"actions,omitempty"`
	Notes                 []string             `json:"notes,omitempty"`
}

type policyRuleValidationResult struct {
	Valid      bool                        `json:"valid"`
	Name       string                      `json:"name,omitempty"`
	Normalized *policyRuleResourceDocument `json:"normalized,omitempty"`
	Issues     []resourceValidationIssue   `json:"issues,omitempty"`
}

type skillResourceMeta struct {
	Name             string               `yaml:"name"`
	Description      string               `yaml:"description"`
	Version          string               `yaml:"version"`
	Author           string               `yaml:"author"`
	Format           string               `yaml:"format,omitempty"`
	Mode             string               `yaml:"mode"`
	PreferredAgent   string               `yaml:"preferred_agent"`
	AllowedToolKinds []string             `yaml:"allowed_tool_kinds"`
	OutputKind       string               `yaml:"output_kind"`
	Priority         int                  `yaml:"priority,omitempty"`
	MaxIterations    int                  `yaml:"max_iterations,omitempty"`
	NextSkills       []string             `yaml:"next_skills,omitempty"`
	Tools            []schema.SkillTool   `yaml:"tools,omitempty"`
	Params           []schema.SkillParam  `yaml:"params,omitempty"`
	Scripts          []schema.SkillScript `yaml:"scripts,omitempty"`
	Activation       schema.Activation    `yaml:"activation"`
	Metadata         map[string]string    `yaml:"metadata,omitempty"`
}

type agentResourceDocument struct {
	ID               string   `json:"id" yaml:"id,omitempty"`
	Name             string   `json:"name" yaml:"name"`
	Description      string   `json:"description" yaml:"description"`
	SystemPrompt     string   `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Provider         string   `json:"provider" yaml:"provider"`
	Model            string   `json:"model,omitempty" yaml:"model,omitempty"`
	Temperature      float64  `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	MaxTokens        int      `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	MaxIterations    int      `json:"max_iterations" yaml:"max_iterations"`
	AllowedToolKinds []string `json:"allowed_tool_kinds" yaml:"allowed_tool_kinds"`
	AllowedTools     []string `json:"allowed_tools,omitempty" yaml:"allowed_tools,omitempty"`
	ToolPolicy       string   `json:"tool_policy" yaml:"tool_policy"`
	Mode             string   `json:"mode" yaml:"mode"`
	Path             string   `json:"path,omitempty" yaml:"-"`
	RestartRequired  bool     `json:"restart_required,omitempty" yaml:"-"`
	ApplyState       string   `json:"apply_state,omitempty" yaml:"-"`
	ApplyMessage     string   `json:"apply_message,omitempty" yaml:"-"`
}

type providerResourceDocument struct {
	ID               string  `json:"id" yaml:"id,omitempty"`
	Type             string  `json:"type,omitempty" yaml:"-"`
	Provider         string  `json:"provider" yaml:"provider"`
	BaseURL          string  `json:"base_url" yaml:"base_url"`
	APIKey           string  `json:"api_key,omitempty" yaml:"api_key,omitempty"`
	EnvKey           string  `json:"env_key,omitempty" yaml:"-"`
	APIKeySet        bool    `json:"api_key_set,omitempty" yaml:"-"`
	Model            string  `json:"model" yaml:"model"`
	DefaultModel     string  `json:"default_model,omitempty" yaml:"-"`
	FallbackProvider string  `json:"fallback_provider,omitempty" yaml:"fallback_provider,omitempty"`
	Timeout          string  `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Temperature      float64 `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	MaxTokens        int     `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	RetryCount       int     `json:"retry_count,omitempty" yaml:"retry_count,omitempty"`
	RetryBackoff     string  `json:"retry_backoff,omitempty" yaml:"retry_backoff,omitempty"`
	Path             string  `json:"path,omitempty" yaml:"-"`
	RestartRequired  bool    `json:"restart_required,omitempty" yaml:"-"`
	ApplyState       string  `json:"apply_state,omitempty" yaml:"-"`
	ApplyMessage     string  `json:"apply_message,omitempty" yaml:"-"`
}

type toolResourceDocument struct {
	Name                string                  `json:"name" yaml:"name"`
	Language            string                  `json:"language" yaml:"language"`
	Description         string                  `json:"description,omitempty" yaml:"description,omitempty"`
	Command             string                  `json:"command" yaml:"command"`
	Args                []string                `json:"args" yaml:"args"`
	Enabled             bool                    `json:"enabled" yaml:"enabled"`
	Timeout             string                  `json:"timeout" yaml:"timeout"`
	WorkDir             string                  `json:"workdir" yaml:"workdir"`
	EnvAllowlist        []string                `json:"env_allowlist,omitempty" yaml:"env_allowlist,omitempty"`
	NetworkDisabled     bool                    `json:"network_disabled,omitempty" yaml:"network_disabled,omitempty"`
	Isolation           string                  `json:"isolation,omitempty" yaml:"isolation,omitempty"`
	IsolationProfile    string                  `json:"isolation_profile,omitempty" yaml:"isolation_profile,omitempty"`
	IsolationOptions    map[string]string       `json:"isolation_options,omitempty" yaml:"isolation_options,omitempty"`
	RestartLimit        int                     `json:"restart_limit,omitempty" yaml:"restart_limit,omitempty"`
	Cooldown            string                  `json:"cooldown,omitempty" yaml:"cooldown,omitempty"`
	MaxConcurrentCalls  int                     `json:"max_concurrent_calls,omitempty" yaml:"max_concurrent_calls,omitempty"`
	AllowedCommandPaths []string                `json:"allowed_command_paths,omitempty" yaml:"allowed_command_paths,omitempty"`
	AllowedCommands     []string                `json:"allowed_commands,omitempty" yaml:"allowed_commands,omitempty"`
	MaxRequestBytes     int                     `json:"max_request_bytes,omitempty" yaml:"max_request_bytes,omitempty"`
	MaxResponseBytes    int                     `json:"max_response_bytes,omitempty" yaml:"max_response_bytes,omitempty"`
	Code                string                  `json:"code" yaml:"-"`
	Path                string                  `json:"path,omitempty" yaml:"-"`
	ConfigPath          string                  `json:"config_path,omitempty" yaml:"-"`
	RestartRequired     bool                    `json:"restart_required,omitempty" yaml:"-"`
	ApplyState          string                  `json:"apply_state,omitempty" yaml:"-"`
	ApplyMessage        string                  `json:"apply_message,omitempty" yaml:"-"`
	Risk                *schema.ToolRiskProfile `json:"risk,omitempty" yaml:"-"`
}

type policyRuleResourceDocument struct {
	Kind                string                          `json:"kind,omitempty" yaml:"kind,omitempty"`
	Version             int                             `json:"version,omitempty" yaml:"version,omitempty"`
	MinVersion          int                             `json:"min_supported_version,omitempty" yaml:"min_supported_version,omitempty"`
	MigratedFromVersion int                             `json:"migrated_from_version,omitempty" yaml:"migrated_from_version,omitempty"`
	Name                string                          `json:"name" yaml:"name"`
	Label               string                          `json:"label,omitempty" yaml:"label,omitempty"`
	Description         string                          `json:"description,omitempty" yaml:"description,omitempty"`
	Operator            string                          `json:"operator,omitempty" yaml:"operator,omitempty"`
	Expression          string                          `json:"expression,omitempty" yaml:"expression,omitempty"`
	Reason              string                          `json:"reason,omitempty" yaml:"reason,omitempty"`
	Defaults            map[string]string               `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Params              []agent.WorkflowNodeFieldOption `json:"params,omitempty" yaml:"params,omitempty"`
	Path                string                          `json:"path,omitempty" yaml:"-"`
}

type toolResourceConfigDocument struct {
	MCPServers []toolResourceServerDocument `yaml:"mcp_servers"`
}

type toolResourceServerDocument struct {
	Name                string            `yaml:"name"`
	Command             string            `yaml:"command"`
	Args                []string          `yaml:"args"`
	Enabled             bool              `yaml:"enabled"`
	Timeout             string            `yaml:"timeout"`
	WorkDir             string            `yaml:"workdir"`
	EnvAllowlist        []string          `yaml:"env_allowlist,omitempty"`
	NetworkDisabled     bool              `yaml:"network_disabled,omitempty"`
	Isolation           string            `yaml:"isolation,omitempty"`
	IsolationProfile    string            `yaml:"isolation_profile,omitempty"`
	IsolationOptions    map[string]string `yaml:"isolation_options,omitempty"`
	RestartLimit        int               `yaml:"restart_limit,omitempty"`
	Cooldown            string            `yaml:"cooldown,omitempty"`
	MaxConcurrentCalls  int               `yaml:"max_concurrent_calls,omitempty"`
	AllowedCommandPaths []string          `yaml:"allowed_command_paths,omitempty"`
	AllowedCommands     []string          `yaml:"allowed_commands,omitempty"`
	MaxRequestBytes     int               `yaml:"max_request_bytes,omitempty"`
	MaxResponseBytes    int               `yaml:"max_response_bytes,omitempty"`
}

func (s *Server) handleResourceCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.resourceCatalog(r.URL.Query()))
}

func (s *Server) resourceCatalog(query url.Values) resourceCatalogResponse {
	allowedKinds := resourceCatalogFamilyFilter(query)
	searchText := resourceCatalogSearchText(query)
	filterKey := resourceCatalogFilterKey(allowedKinds, searchText)
	families := s.resourceFamilySummaries(allowedKinds, searchText)
	counts := resourceCatalogCounts{Families: len(families)}
	applyStates := make(map[string]struct{})
	for _, family := range families {
		counts.TotalResources += family.Count
		counts.InvalidResources += family.InvalidCount
		if family.FileBacked {
			counts.FileBackedFamilies++
		}
		if family.RestartRequiredOnSave {
			counts.RestartRequired++
		}
		if family.HotReloadSupported {
			counts.HotReloadable++
		}
		if family.CanScaffold {
			counts.Scaffoldable++
		}
		if family.CanValidate {
			counts.WithValidation++
		}
		if family.DiagnosticsPath != "" {
			counts.WithDiagnostics++
		}
		if len(family.Actions) > 0 {
			counts.WithCustomActions++
		}
		state := strings.TrimSpace(family.ApplyStateOnSave)
		if state != "" {
			applyStates[state] = struct{}{}
			if state == "restart_required" {
				counts.RestartApplyFamilies++
			} else if strings.Contains(state, "hot_reload") {
				counts.HotReloadApplyFamilies++
			} else {
				counts.ActiveApplyFamilies++
			}
		}
	}
	return resourceCatalogResponse{
		Meta: resourceCatalogMetadata{
			Schema:                    resourceCatalogSchema,
			SchemaVersion:             resourceCatalogSchemaVersion,
			MinSupportedSchemaVersion: 1,
			RuntimeVersion:            version.Version,
			CacheKey:                  fmt.Sprintf("%s:v%d:%s:%d:%d:%s", resourceCatalogSchema, resourceCatalogSchemaVersion, version.Version, counts.Families, counts.TotalResources, filterKey),
		},
		Counts: counts,
		Summary: resourceCatalogSummary{
			RestartRequired: counts.RestartRequired > 0,
			DiagnosticsPath: firstResourceCatalogDiagnosticsPath(families),
			ApplyStates:     sortedResourceCatalogKeys(applyStates),
			Notes: []string{
				"Use resource family paths for CRUD operations; this endpoint is a read-only Studio catalog.",
				"Agent, provider, and MCP tool saves are file-backed but currently require runtime restart before active execution changes.",
			},
		},
		Families: families,
	}
}

func (s *Server) resourceFamilySummaries(allowed map[string]struct{}, searchText string) []resourceFamilySummary {
	capabilities := helpResourceCapabilities()
	families := make([]resourceFamilySummary, 0, len(capabilities))
	for _, capability := range capabilities {
		if len(allowed) > 0 {
			if _, ok := allowed[capability.Kind]; !ok {
				continue
			}
		}
		family := resourceFamilySummary{
			Kind:                  capability.Kind,
			DisplayName:           capability.DisplayName,
			Description:           capability.Description,
			CollectionPath:        capability.CollectionPath,
			DetailPath:            capability.DetailPath,
			ValidatePath:          capability.ValidatePath,
			ScaffoldPath:          capability.ScaffoldPath,
			StorageRoot:           capability.StorageRoot,
			FileBacked:            capability.FileBacked,
			CanList:               capability.CanList,
			CanCreate:             capability.CanCreate,
			CanUpdate:             capability.CanUpdate,
			CanDelete:             capability.CanDelete,
			CanValidate:           capability.CanValidate,
			CanScaffold:           capability.CanScaffold,
			HotReloadSupported:    capability.HotReloadSupported,
			RestartRequiredOnSave: capability.RestartRequiredOnSave,
			ApplyStateOnSave:      capability.ApplyStateOnSave,
			ApplyMessage:          capability.ApplyMessage,
			DiagnosticsPath:       capability.DiagnosticsPath,
			RelatedCapabilities:   append([]string(nil), capability.RelatedCapabilities...),
			Actions:               append([]helpResourceAction(nil), capability.Actions...),
			Notes:                 append([]string(nil), capability.Notes...),
		}
		family.Count, family.InvalidCount = s.resourceFamilyCounts(capability.Kind)
		if searchText != "" && !resourceFamilyMatchesQuery(family, searchText) {
			continue
		}
		families = append(families, family)
	}
	return families
}

func resourceCatalogFamilyFilter(query url.Values) map[string]struct{} {
	if query == nil {
		return nil
	}
	raw := append([]string(nil), query["kind"]...)
	raw = append(raw, query["kinds"]...)
	if len(raw) == 0 {
		raw = append(raw, query.Get("kind"))
	}
	allowed := make(map[string]struct{})
	for _, part := range raw {
		for _, kind := range strings.Split(part, ",") {
			kind = strings.TrimSpace(kind)
			if kind != "" {
				allowed[kind] = struct{}{}
			}
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

func resourceCatalogSearchText(query url.Values) string {
	if query == nil {
		return ""
	}
	for _, key := range []string{"q", "query", "search"} {
		if value := strings.TrimSpace(query.Get(key)); value != "" {
			return strings.ToLower(value)
		}
	}
	return ""
}

func resourceCatalogFilterKey(allowed map[string]struct{}, searchText string) string {
	kinds := sortedResourceCatalogKeys(allowed)
	return strings.Join([]string{strings.Join(kinds, ","), searchText}, "|")
}

func resourceFamilyMatchesQuery(family resourceFamilySummary, searchText string) bool {
	if searchText == "" {
		return true
	}
	haystacks := []string{
		family.Kind,
		family.DisplayName,
		family.Description,
		family.CollectionPath,
		family.DetailPath,
		family.ValidatePath,
		family.ScaffoldPath,
		family.StorageRoot,
		family.ApplyStateOnSave,
		family.ApplyMessage,
		family.DiagnosticsPath,
		strings.Join(family.RelatedCapabilities, " "),
		strings.Join(family.Notes, " "),
	}
	for _, action := range family.Actions {
		haystacks = append(haystacks, action.Name, action.Label, action.Description, action.Path, action.Returns)
	}
	for _, haystack := range haystacks {
		if strings.Contains(strings.ToLower(haystack), searchText) {
			return true
		}
	}
	return false
}

func (s *Server) resourceFamilyCounts(kind string) (int, int) {
	if s == nil || s.runtime == nil {
		return 0, 0
	}
	switch kind {
	case "agent":
		return len(s.agentResourceList()), 0
	case "provider":
		return len(s.providerResourceList()), 0
	case "tool":
		return len(s.toolResourceList()), 0
	case "skill":
		return len(s.runtime.SkillList()), 0
	case "workflow":
		items := s.runtime.WorkflowRunner().ListWorkflowGraphs()
		return len(items), countInvalidWorkflowGraphs(items)
	case "workflow_template":
		items := s.runtime.WorkflowTemplateResources()
		return len(items), countInvalidWorkflowTemplateResources(items)
	case "workflow_schema":
		items := s.runtime.WorkflowSchemaResources()
		return len(items), countInvalidWorkflowSchemaResources(items)
	case "policy_rule":
		return len(s.policyRuleResourceList()), 0
	case "team_template":
		items := s.runtime.TeamTemplateResources()
		return len(items), countInvalidTeamTemplateResources(items)
	case "kit":
		items := s.kitResourceList()
		return len(items), countInvalidKitResources(items)
	case "workflow_node_metadata":
		items := s.runtime.WorkflowNodeMetadataResources()
		return len(items), countInvalidWorkflowMetadataResources(items)
	case "expression_helper_metadata":
		items := s.runtime.WorkflowExpressionMetadataResources()
		return len(items), countInvalidWorkflowMetadataResources(items)
	default:
		return 0, 0
	}
}

func countInvalidWorkflowGraphs(items []agent.WorkflowGraphSummary) int {
	count := 0
	for _, item := range items {
		if !item.Valid {
			count++
		}
	}
	return count
}

func countInvalidWorkflowTemplateResources(items []agent.WorkflowTemplateResourceSummary) int {
	count := 0
	for _, item := range items {
		if !item.Valid {
			count++
		}
	}
	return count
}

func countInvalidWorkflowSchemaResources(items []agent.WorkflowSchemaResourceSummary) int {
	count := 0
	for _, item := range items {
		if !item.Valid {
			count++
		}
	}
	return count
}

func countInvalidTeamTemplateResources(items []agent.TeamTemplateResourceSummary) int {
	count := 0
	for _, item := range items {
		if !item.Valid {
			count++
		}
	}
	return count
}

func countInvalidWorkflowMetadataResources(items []agent.WorkflowMetadataResourceSummary) int {
	count := 0
	for _, item := range items {
		if !item.Valid {
			count++
		}
	}
	return count
}

func countInvalidKitResources(items []kitSummary) int {
	count := 0
	for _, item := range items {
		if !item.Valid {
			count++
		}
	}
	return count
}

func firstResourceCatalogDiagnosticsPath(families []resourceFamilySummary) string {
	for _, family := range families {
		if strings.TrimSpace(family.DiagnosticsPath) != "" {
			return family.DiagnosticsPath
		}
	}
	return ""
}

func sortedResourceCatalogKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (s *Server) handleSkillResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/skills/validate" {
		s.handleSkillResourceValidate(w, r, "")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.runtime.SkillList())
}

func (s *Server) handleSkillResourceItem(w http.ResponseWriter, r *http.Request) {
	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/skills/"), "/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handleSkillResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		s.handleSkillResourceValidate(w, r, parts[0])
		return
	}
	if len(parts) >= 2 && parts[1] == "files" {
		rel := ""
		if len(parts) > 2 {
			rel = strings.Join(parts[2:], "/")
		}
		s.handleSkillFileResource(w, r, parts[0], rel)
		return
	}
	if suffix == "" || strings.Contains(suffix, "/") {
		http.NotFound(w, r)
		return
	}
	name := suffix
	switch r.Method {
	case http.MethodGet:
		doc, ok := s.skillResourceByName(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, doc)
	case http.MethodPut:
		var doc skillResourceDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		saved, err := s.saveSkillResource(name, doc)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, invalidResourceValidation("skill", firstResourceValidationName(name, doc.Name), skillValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSkillResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var doc skillResourceDocument
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, invalidResourceValidation("skill", name, "body", "invalid json"))
		return
	}
	normalized, err := s.validateSkillResourceDryRun(name, doc)
	if err != nil {
		writeJSON(w, invalidResourceValidation("skill", firstResourceValidationName(name, doc.Name), skillValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, validResourceValidation("skill", normalized.Name, normalized))
}

func (s *Server) handleSkillFileResource(w http.ResponseWriter, r *http.Request, name, rel string) {
	if strings.TrimSpace(rel) == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		files, err := s.skillFileResourceList(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, files)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, err := s.skillFileResource(name, rel, true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, doc)
	case http.MethodPut:
		var doc skillFileResourceDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		saved, err := s.saveSkillFileResource(name, rel, doc.Content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.deleteSkillFileResource(name, rel); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/agents/validate" {
		s.handleAgentResourceValidate(w, r, "")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.agentResourceList())
}

func (s *Server) handleAgentResourceItem(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/agents/"), "/")
	parts := strings.Split(name, "/")
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handleAgentResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		s.handleAgentResourceValidate(w, r, parts[0])
		return
	}
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, ok := s.agentResourceByName(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, doc)
	case http.MethodPut:
		var doc agentResourceDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		saved, err := s.saveAgentResource(name, doc)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, invalidResourceValidation("agent", firstResourceValidationName(name, doc.ID), resourceValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.deleteAgentResource(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var doc agentResourceDocument
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, invalidResourceValidation("agent", name, "body", "invalid json"))
		return
	}
	normalized, err := s.validateAgentResourceDryRun(name, doc)
	if err != nil {
		writeJSON(w, invalidResourceValidation("agent", firstResourceValidationName(name, doc.ID), resourceValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, validResourceValidation("agent", normalized.ID, normalized))
}

func (s *Server) handleProviderResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/providers/validate" {
		s.handleProviderResourceValidate(w, r, "")
		return
	}
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/providers/test" {
		s.handleProviderResourceTest(w, r, "")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.providerResourceList())
}

func (s *Server) handleProviderResourceItem(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/providers/"), "/")
	parts := strings.Split(name, "/")
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handleProviderResourceValidate(w, r, "")
		return
	}
	if len(parts) == 1 && parts[0] == "test" && r.Method == http.MethodPost {
		s.handleProviderResourceTest(w, r, "")
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		s.handleProviderResourceValidate(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "test" && r.Method == http.MethodPost {
		s.handleProviderResourceTest(w, r, parts[0])
		return
	}
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, ok := s.providerResourceByName(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, doc)
	case http.MethodPut:
		var doc providerResourceDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		saved, err := s.saveProviderResource(name, doc)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, invalidResourceValidation("provider", firstResourceValidationName(name, doc.ID), resourceValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.deleteProviderResource(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProviderResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var doc providerResourceDocument
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, invalidResourceValidation("provider", name, "body", "invalid json"))
		return
	}
	normalized, err := s.validateProviderResourceDryRun(name, doc)
	if err != nil {
		writeJSON(w, invalidResourceValidation("provider", firstResourceValidationName(name, doc.ID), resourceValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, validResourceValidation("provider", normalized.ID, normalized))
}

func (s *Server) handleProviderResourceTest(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var doc providerResourceDocument
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, providerTestInvalid(name, "body", "invalid json"))
		return
	}
	normalized, err := s.validateProviderResourceDryRun(name, doc)
	if err != nil {
		writeJSON(w, providerTestInvalid(firstResourceValidationName(name, doc.ID), resourceValidationField(err.Error()), err.Error()))
		return
	}
	result := testProviderResourceConnection(r.Context(), normalized)
	writeJSON(w, result)
}

func (s *Server) handleToolResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/tools/validate" {
		s.handleToolResourceValidate(w, r, "")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.toolResourceList())
}

func (s *Server) handleToolResourceItem(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/tools/"), "/")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(name, "/")
	if parts[0] == "scaffolds" {
		s.handleToolScaffolds(w, r, parts[1:])
		return
	}
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handleToolResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		s.handleToolResourceValidate(w, r, parts[0])
		return
	}
	switch r.Method {
	case http.MethodGet:
		if strings.Contains(name, "/") {
			doc, ok := s.runtimeToolResourceByName(name)
			if !ok {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, doc)
			return
		}
		doc, ok := s.toolResourceByName(name)
		if !ok {
			doc = s.defaultToolResource(name)
			s.annotateToolResourceRisk(&doc)
		}
		writeJSON(w, doc)
	case http.MethodPut:
		if strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}
		var doc toolResourceDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		saved, err := s.saveToolResource(name, doc)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, invalidResourceValidation("tool", firstResourceValidationName(name, doc.Name), resourceValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if strings.Contains(name, "/") {
			http.Error(w, "delete supports file-backed custom tool modules only", http.StatusBadRequest)
			return
		}
		if err := s.deleteToolResource(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleToolResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var doc toolResourceDocument
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, invalidResourceValidation("tool", name, "body", "invalid json"))
		return
	}
	normalized, err := s.validateToolResourceDryRun(name, doc)
	if err != nil {
		writeJSON(w, invalidResourceValidation("tool", firstResourceValidationName(name, doc.Name), resourceValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, validResourceValidation("tool", normalized.Name, normalized))
}

func (s *Server) handlePolicyRuleResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/policy-rules/validate" {
		s.handlePolicyRuleResourceValidate(w, r, "")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.policyRuleResourceList())
}

func (s *Server) handlePolicyRuleResourceItem(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/policy-rules/"), "/")
	parts := strings.Split(name, "/")
	if len(parts) > 0 && parts[0] == "scaffolds" {
		s.handlePolicyRuleScaffolds(w, r, parts[1:])
		return
	}
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handlePolicyRuleResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		s.handlePolicyRuleResourceValidate(w, r, parts[0])
		return
	}
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, ok := s.policyRuleResourceByName(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, doc)
	case http.MethodPut:
		var doc policyRuleResourceDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		saved, err := s.savePolicyRuleResource(name, doc)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, invalidPolicyRuleValidation(firstResourceValidationName(name, doc.Name), policyRuleValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.deletePolicyRuleResource(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePolicyRuleResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var doc policyRuleResourceDocument
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, invalidPolicyRuleValidation(name, "body", "invalid json"))
		return
	}
	normalized, err := s.validatePolicyRuleResourceDryRun(name, doc)
	if err != nil {
		writeJSON(w, invalidPolicyRuleValidation(firstResourceValidationName(name, doc.Name), policyRuleValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, policyRuleValidationResult{
		Valid:      true,
		Name:       normalized.Name,
		Normalized: &normalized,
	})
}

func (s *Server) skillResourceByName(name string) (skillResourceDocument, bool) {
	normalized := normalizeResourceName(name)
	for _, item := range s.runtime.SkillList() {
		if normalizeResourceName(item.Name) == normalized {
			return skillResourceFromSchema(item), true
		}
	}
	return skillResourceDocument{}, false
}

func (s *Server) providerResourceList() []providerResourceDocument {
	if s == nil || s.runtime == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]providerResourceDocument, 0)
	for _, id := range s.runtime.ProviderNames() {
		provider, ok := s.runtime.Provider(id)
		if !ok {
			continue
		}
		doc, hasModule := s.loadProviderResourceSnippet(id)
		if !hasModule {
			doc = providerResourceFromConfig(id, provider, false)
			if path, err := s.providerResourcePath(id); err == nil {
				doc.Path = path
			}
		}
		s.annotateProviderResourceApplyState(&doc)
		out = append(out, doc)
		seen[normalizeResourceName(id)] = struct{}{}
	}
	if root, err := s.providerResourceRoot(); err == nil {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if entry.IsDir() || !isYAMLFile(entry.Name()) {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			if _, ok := seen[normalizeResourceName(id)]; ok {
				continue
			}
			if doc, ok := s.providerResourceByName(id); ok {
				out = append(out, doc)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) providerResourceByName(name string) (providerResourceDocument, bool) {
	id := normalizeResourceName(name)
	if id == "" {
		return providerResourceDocument{}, false
	}
	if doc, ok := s.loadProviderResourceSnippet(id); ok {
		s.annotateProviderResourceApplyState(&doc)
		return doc, true
	}
	if s == nil || s.runtime == nil {
		return providerResourceDocument{}, false
	}
	if provider, ok := s.runtime.Provider(id); ok {
		doc := providerResourceFromConfig(id, provider, false)
		if path, err := s.providerResourcePath(id); err == nil {
			doc.Path = path
		}
		s.annotateProviderResourceApplyState(&doc)
		return doc, true
	}
	return providerResourceDocument{}, false
}

func (s *Server) saveProviderResource(name string, doc providerResourceDocument) (providerResourceDocument, error) {
	doc, err := s.validateProviderResourceDryRun(name, doc)
	if err != nil {
		return providerResourceDocument{}, err
	}
	id := doc.ID
	path, err := s.providerResourcePath(id)
	if err != nil {
		return providerResourceDocument{}, err
	}
	content, err := renderProviderResource(doc)
	if err != nil {
		return providerResourceDocument{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return providerResourceDocument{}, fmt.Errorf("create provider config directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return providerResourceDocument{}, fmt.Errorf("write provider config: %w", err)
	}
	parsed, err := readProviderResourceMap([]byte(content))
	if err != nil {
		_ = os.Remove(tmp)
		return providerResourceDocument{}, fmt.Errorf("validate provider config: %w", err)
	}
	if _, ok := parsed[id]; !ok {
		_ = os.Remove(tmp)
		return providerResourceDocument{}, fmt.Errorf("provider config snippet missing %q", id)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return providerResourceDocument{}, fmt.Errorf("replace provider config: %w", err)
	}
	saved := doc
	saved.Path = path
	saved.APIKeySet = strings.TrimSpace(saved.APIKey) != ""
	markProviderResourceRestartRequired(&saved)
	return saved, nil
}

func (s *Server) validateProviderResourceDryRun(name string, doc providerResourceDocument) (providerResourceDocument, error) {
	id := normalizeResourceName(firstResourceValidationName(name, doc.ID))
	if !resourceNamePattern.MatchString(id) {
		return providerResourceDocument{}, fmt.Errorf("invalid provider name %q: use lowercase letters, numbers, hyphen, or underscore", id)
	}
	doc.ID = id
	if strings.TrimSpace(doc.APIKey) == "" {
		if existing, ok := s.loadProviderResourceSnippet(id); ok {
			doc.APIKey = existing.APIKey
		}
	}
	normalizeProviderResourceAliases(&doc)
	applyProviderResourceDefaults(&doc)
	if err := validateProviderResource(doc, s.providerNamesExcept(id)); err != nil {
		return providerResourceDocument{}, err
	}
	if _, err := s.providerResourcePath(id); err != nil {
		return providerResourceDocument{}, err
	}
	content, err := renderProviderResource(doc)
	if err != nil {
		return providerResourceDocument{}, err
	}
	parsed, err := readProviderResourceMap([]byte(content))
	if err != nil {
		return providerResourceDocument{}, fmt.Errorf("validate provider config: %w", err)
	}
	if _, ok := parsed[id]; !ok {
		return providerResourceDocument{}, fmt.Errorf("provider config snippet missing %q", id)
	}
	doc.APIKeySet = strings.TrimSpace(doc.APIKey) != ""
	markProviderResourceRestartRequired(&doc)
	return doc, nil
}

func (s *Server) deleteProviderResource(name string) error {
	id := normalizeResourceName(name)
	if !resourceNamePattern.MatchString(id) {
		return fmt.Errorf("invalid provider name %q: use lowercase letters, numbers, hyphen, or underscore", id)
	}
	path, err := s.providerResourcePath(id)
	if err != nil {
		return err
	}
	if _, ok := s.loadProviderResourceSnippet(id); !ok {
		if s != nil && s.runtime != nil {
			if _, runtimeProvider := s.runtime.Provider(id); runtimeProvider {
				return fmt.Errorf("provider %q is defined in the main config; delete only supports configs/providers/*.yaml modules", id)
			}
		}
		return fmt.Errorf("provider %q not found", id)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete provider config: %w", err)
	}
	return nil
}

func (s *Server) agentResourceList() []agentResourceDocument {
	if s == nil || s.runtime == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]agentResourceDocument, 0)
	for _, id := range s.runtime.AgentNames() {
		profile, ok := s.runtime.Profile(id)
		if !ok {
			continue
		}
		doc, hasModule := s.loadAgentResourceSnippet(id)
		if !hasModule {
			doc = agentResourceFromProfile(id, profile)
			if path, err := s.agentResourcePath(id); err == nil {
				doc.Path = path
			}
		}
		s.annotateAgentResourceApplyState(&doc)
		out = append(out, doc)
		seen[normalizeResourceName(id)] = struct{}{}
	}
	if root, err := s.agentResourceRoot(); err == nil {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if entry.IsDir() || !isYAMLFile(entry.Name()) {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			if _, ok := seen[normalizeResourceName(id)]; ok {
				continue
			}
			if doc, ok := s.agentResourceByName(id); ok {
				out = append(out, doc)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) agentResourceByName(name string) (agentResourceDocument, bool) {
	id := normalizeResourceName(name)
	if id == "" {
		return agentResourceDocument{}, false
	}
	if doc, ok := s.loadAgentResourceSnippet(id); ok {
		s.annotateAgentResourceApplyState(&doc)
		return doc, true
	}
	if s == nil || s.runtime == nil {
		return agentResourceDocument{}, false
	}
	if profile, ok := s.runtime.Profile(id); ok {
		doc := agentResourceFromProfile(id, profile)
		if path, err := s.agentResourcePath(id); err == nil {
			doc.Path = path
		}
		s.annotateAgentResourceApplyState(&doc)
		return doc, true
	}
	return agentResourceDocument{}, false
}

func (s *Server) saveAgentResource(name string, doc agentResourceDocument) (agentResourceDocument, error) {
	doc, err := s.validateAgentResourceDryRun(name, doc)
	if err != nil {
		return agentResourceDocument{}, err
	}
	id := doc.ID
	path, err := s.agentResourcePath(id)
	if err != nil {
		return agentResourceDocument{}, err
	}
	content, err := renderAgentResource(doc)
	if err != nil {
		return agentResourceDocument{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return agentResourceDocument{}, fmt.Errorf("create agent config directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return agentResourceDocument{}, fmt.Errorf("write agent config: %w", err)
	}
	var parsed map[string]config.AgentProfile
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		_ = os.Remove(tmp)
		return agentResourceDocument{}, fmt.Errorf("validate agent config: %w", err)
	}
	if _, ok := parsed[id]; !ok {
		_ = os.Remove(tmp)
		return agentResourceDocument{}, fmt.Errorf("agent config snippet missing %q", id)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return agentResourceDocument{}, fmt.Errorf("replace agent config: %w", err)
	}
	doc.Path = path
	markAgentResourceRestartRequired(&doc)
	return doc, nil
}

func (s *Server) validateAgentResourceDryRun(name string, doc agentResourceDocument) (agentResourceDocument, error) {
	id := normalizeResourceName(firstResourceValidationName(name, doc.ID))
	if !resourceNamePattern.MatchString(id) {
		return agentResourceDocument{}, fmt.Errorf("invalid agent name %q: use lowercase letters, numbers, hyphen, or underscore", id)
	}
	doc.ID = id
	applyAgentResourceDefaults(&doc)
	if err := validateAgentResource(doc, s.providerNamesExcept("")); err != nil {
		return agentResourceDocument{}, err
	}
	if _, err := s.agentResourcePath(id); err != nil {
		return agentResourceDocument{}, err
	}
	content, err := renderAgentResource(doc)
	if err != nil {
		return agentResourceDocument{}, err
	}
	var parsed map[string]config.AgentProfile
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		return agentResourceDocument{}, fmt.Errorf("validate agent config: %w", err)
	}
	if _, ok := parsed[id]; !ok {
		return agentResourceDocument{}, fmt.Errorf("agent config snippet missing %q", id)
	}
	markAgentResourceRestartRequired(&doc)
	return doc, nil
}

func (s *Server) deleteAgentResource(name string) error {
	id := normalizeResourceName(name)
	if !resourceNamePattern.MatchString(id) {
		return fmt.Errorf("invalid agent name %q: use lowercase letters, numbers, hyphen, or underscore", id)
	}
	path, err := s.agentResourcePath(id)
	if err != nil {
		return err
	}
	if _, ok := s.loadAgentResourceSnippet(id); !ok {
		if s != nil && s.runtime != nil {
			if _, runtimeAgent := s.runtime.Profile(id); runtimeAgent {
				return fmt.Errorf("agent %q is defined in the main config; delete only supports configs/agents/*.yaml modules", id)
			}
		}
		return fmt.Errorf("agent %q not found", id)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete agent config: %w", err)
	}
	return nil
}

func (s *Server) toolResourceList() []toolResourceDocument {
	if s == nil || s.runtime == nil {
		return nil
	}
	out := make([]toolResourceDocument, 0)
	seen := make(map[string]struct{})
	runtimeTools, err := s.runtime.Tools(context.Background())
	if err != nil {
		for _, name := range s.runtime.ToolNames() {
			doc := toolResourceDocument{Name: name, Language: "mcp", Description: "Discovered MCP tool"}
			doc.Risk = s.toolRiskProfileForName(name)
			out = append(out, doc)
			seen[normalizeResourceName(name)] = struct{}{}
		}
	} else {
		for _, tool := range runtimeTools {
			name := qualifiedToolName(tool)
			if name == "" {
				continue
			}
			doc := toolResourceDocument{
				Name:        name,
				Language:    "mcp",
				Description: strings.TrimSpace(tool.Description),
				Risk:        s.toolRiskProfileForTool(tool),
			}
			out = append(out, doc)
			seen[normalizeResourceName(name)] = struct{}{}
		}
	}
	if root, err := s.runtimeResourceRoot("mcp_servers"); err == nil {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".py" {
				continue
			}
			name := normalizeResourceName(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			if doc, ok := s.toolResourceByName(name); ok {
				if doc.Risk == nil {
					doc.Risk = s.toolRiskProfileForName(name)
				}
				out = append(out, doc)
				seen[name] = struct{}{}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) toolResourceByName(name string) (toolResourceDocument, bool) {
	normalized := normalizeResourceName(name)
	if normalized == "" {
		return toolResourceDocument{}, false
	}
	codePath, configPath, err := s.toolResourcePaths(normalized)
	if err != nil {
		return toolResourceDocument{}, false
	}
	code, codeErr := os.ReadFile(codePath)
	if codeErr != nil {
		return toolResourceDocument{}, false
	}
	doc := s.defaultToolResource(normalized)
	doc.Code = string(code)
	doc.Path = codePath
	doc.ConfigPath = configPath
	if data, err := os.ReadFile(configPath); err == nil {
		var cfg toolResourceConfigDocument
		if err := yaml.Unmarshal(data, &cfg); err == nil && len(cfg.MCPServers) > 0 {
			saved := toolResourceFromServerDocument(cfg.MCPServers[0])
			normalizeToolResourceIsolationProfile(&saved)
			saved.Code = doc.Code
			saved.Path = codePath
			saved.ConfigPath = configPath
			saved.Language = fallbackString(saved.Language, doc.Language)
			if strings.TrimSpace(saved.Description) == "" {
				saved.Description = doc.Description
			}
			s.annotateToolResourceApplyState(&saved)
			s.annotateToolResourceRisk(&saved)
			return saved, true
		}
	}
	s.annotateToolResourceApplyState(&doc)
	s.annotateToolResourceRisk(&doc)
	return doc, true
}

func (s *Server) runtimeToolResourceByName(name string) (toolResourceDocument, bool) {
	name = strings.TrimSpace(name)
	if name == "" || s == nil || s.runtime == nil {
		return toolResourceDocument{}, false
	}
	tools, err := s.runtime.Tools(context.Background())
	if err != nil {
		return toolResourceDocument{}, false
	}
	for _, tool := range tools {
		qualified := qualifiedToolName(tool)
		if !strings.EqualFold(name, qualified) && !strings.EqualFold(name, tool.Name) {
			continue
		}
		return toolResourceDocument{
			Name:        qualified,
			Language:    "mcp",
			Description: strings.TrimSpace(tool.Description),
			Risk:        s.toolRiskProfileForTool(tool),
		}, true
	}
	return toolResourceDocument{}, false
}

func (s *Server) saveToolResource(name string, doc toolResourceDocument) (toolResourceDocument, error) {
	doc, err := s.validateToolResourceDryRun(name, doc)
	if err != nil {
		return toolResourceDocument{}, err
	}
	normalized := doc.Name
	codePath, configPath, err := s.toolResourcePaths(normalized)
	if err != nil {
		return toolResourceDocument{}, err
	}
	configContent, err := renderToolResourceConfig(doc)
	if err != nil {
		return toolResourceDocument{}, err
	}
	if err := os.MkdirAll(filepath.Dir(codePath), 0o755); err != nil {
		return toolResourceDocument{}, fmt.Errorf("create tool directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return toolResourceDocument{}, fmt.Errorf("create tool config directory: %w", err)
	}
	codeTmp := codePath + ".tmp"
	configTmp := configPath + ".tmp"
	if err := os.WriteFile(codeTmp, []byte(doc.Code), 0o644); err != nil {
		return toolResourceDocument{}, fmt.Errorf("write tool code: %w", err)
	}
	if err := os.WriteFile(configTmp, []byte(configContent), 0o644); err != nil {
		_ = os.Remove(codeTmp)
		return toolResourceDocument{}, fmt.Errorf("write tool config: %w", err)
	}
	var parsed struct {
		MCPServers []config.MCPServerRef `yaml:"mcp_servers"`
	}
	if err := yaml.Unmarshal([]byte(configContent), &parsed); err != nil {
		_ = os.Remove(codeTmp)
		_ = os.Remove(configTmp)
		return toolResourceDocument{}, fmt.Errorf("validate tool config: %w", err)
	}
	if len(parsed.MCPServers) != 1 || strings.TrimSpace(parsed.MCPServers[0].Name) != normalized {
		_ = os.Remove(codeTmp)
		_ = os.Remove(configTmp)
		return toolResourceDocument{}, fmt.Errorf("tool config snippet missing mcp server %q", normalized)
	}
	config.ApplyMCPServerDefaults(&parsed.MCPServers[0])
	if err := config.ValidateMCPServerRef(parsed.MCPServers[0]); err != nil {
		_ = os.Remove(codeTmp)
		_ = os.Remove(configTmp)
		return toolResourceDocument{}, fmt.Errorf("validate tool config: %w", err)
	}
	if err := os.Rename(codeTmp, codePath); err != nil {
		_ = os.Remove(codeTmp)
		_ = os.Remove(configTmp)
		return toolResourceDocument{}, fmt.Errorf("replace tool code: %w", err)
	}
	if err := os.Rename(configTmp, configPath); err != nil {
		_ = os.Remove(configTmp)
		return toolResourceDocument{}, fmt.Errorf("replace tool config: %w", err)
	}
	doc.Path = codePath
	doc.ConfigPath = configPath
	markToolResourceRestartRequired(&doc)
	return doc, nil
}

func (s *Server) validateToolResourceDryRun(name string, doc toolResourceDocument) (toolResourceDocument, error) {
	normalized := normalizeResourceName(firstResourceValidationName(name, doc.Name))
	if !resourceNamePattern.MatchString(normalized) {
		return toolResourceDocument{}, fmt.Errorf("invalid tool name %q: use lowercase letters, numbers, hyphen, or underscore", normalized)
	}
	doc.Name = normalized
	s.applyToolResourceDefaults(&doc)
	normalizeToolResourceIsolationProfile(&doc)
	if strings.TrimSpace(doc.Code) == "" {
		return toolResourceDocument{}, fmt.Errorf("tool code is required")
	}
	codePath, configPath, err := s.toolResourcePaths(normalized)
	if err != nil {
		return toolResourceDocument{}, err
	}
	configContent, err := renderToolResourceConfig(doc)
	if err != nil {
		return toolResourceDocument{}, err
	}
	var parsed struct {
		MCPServers []config.MCPServerRef `yaml:"mcp_servers"`
	}
	if err := yaml.Unmarshal([]byte(configContent), &parsed); err != nil {
		return toolResourceDocument{}, fmt.Errorf("validate tool config: %w", err)
	}
	if len(parsed.MCPServers) != 1 || strings.TrimSpace(parsed.MCPServers[0].Name) != normalized {
		return toolResourceDocument{}, fmt.Errorf("tool config snippet missing mcp server %q", normalized)
	}
	config.ApplyMCPServerDefaults(&parsed.MCPServers[0])
	if err := config.ValidateMCPServerRef(parsed.MCPServers[0]); err != nil {
		return toolResourceDocument{}, fmt.Errorf("validate tool config: %w", err)
	}
	doc.Path = codePath
	doc.ConfigPath = configPath
	markToolResourceRestartRequired(&doc)
	s.annotateToolResourceRisk(&doc)
	return doc, nil
}

func (s *Server) deleteToolResource(name string) error {
	normalized := normalizeResourceName(name)
	if !resourceNamePattern.MatchString(normalized) {
		return fmt.Errorf("invalid tool name %q: use lowercase letters, numbers, hyphen, or underscore", normalized)
	}
	codePath, configPath, err := s.toolResourcePaths(normalized)
	if err != nil {
		return err
	}
	codeExists := fileExists(codePath)
	configExists := fileExists(configPath)
	if !codeExists && !configExists {
		if s != nil && s.runtime != nil {
			for _, tool := range s.runtime.ToolNames() {
				if normalizeResourceName(tool) == normalized {
					return fmt.Errorf("tool %q is discovered from the active MCP runtime; delete only supports mcp_servers/*.py and configs/mcp_servers/*.yaml modules", normalized)
				}
			}
		}
		return fmt.Errorf("tool %q not found", normalized)
	}
	if codeExists {
		if err := os.Remove(codePath); err != nil {
			return fmt.Errorf("delete tool code: %w", err)
		}
	}
	if configExists {
		if err := os.Remove(configPath); err != nil {
			return fmt.Errorf("delete tool config: %w", err)
		}
	}
	return nil
}

func (s *Server) policyRuleResourceList() []policyRuleResourceDocument {
	root, err := s.policyRuleResourceRoot()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]policyRuleResourceDocument, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if doc, ok := s.policyRuleResourceByName(name); ok {
			out = append(out, doc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) policyRuleResourceByName(name string) (policyRuleResourceDocument, bool) {
	normalized := normalizeResourceName(name)
	if normalized == "" {
		return policyRuleResourceDocument{}, false
	}
	path, err := s.policyRuleResourcePath(normalized)
	if err != nil {
		return policyRuleResourceDocument{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return policyRuleResourceDocument{}, false
	}
	var doc policyRuleResourceDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return policyRuleResourceDocument{}, false
	}
	doc = normalizePolicyRuleResource(doc, normalized)
	if err := validatePolicyRuleResource(doc); err != nil {
		return policyRuleResourceDocument{}, false
	}
	doc = policyRuleResourceFromDefinition(agent.MigrateWorkflowPolicyRuleDefinition(policyRuleDefinitionFromResource(doc)), path)
	return doc, true
}

func (s *Server) savePolicyRuleResource(name string, doc policyRuleResourceDocument) (policyRuleResourceDocument, error) {
	doc, err := s.validatePolicyRuleResourceDryRun(name, doc)
	if err != nil {
		return policyRuleResourceDocument{}, err
	}
	normalized := doc.Name
	path, err := s.policyRuleResourcePath(normalized)
	if err != nil {
		return policyRuleResourceDocument{}, err
	}
	content, err := renderPolicyRuleResource(doc)
	if err != nil {
		return policyRuleResourceDocument{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return policyRuleResourceDocument{}, fmt.Errorf("create policy rule directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return policyRuleResourceDocument{}, fmt.Errorf("write policy rule: %w", err)
	}
	var parsed policyRuleResourceDocument
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		_ = os.Remove(tmp)
		return policyRuleResourceDocument{}, fmt.Errorf("validate policy rule: %w", err)
	}
	parsed = normalizePolicyRuleResource(parsed, normalized)
	if err := validatePolicyRuleResource(parsed); err != nil {
		_ = os.Remove(tmp)
		return policyRuleResourceDocument{}, fmt.Errorf("validate policy rule: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return policyRuleResourceDocument{}, fmt.Errorf("replace policy rule: %w", err)
	}
	parsed.Path = path
	return parsed, nil
}

func (s *Server) validatePolicyRuleResourceDryRun(name string, doc policyRuleResourceDocument) (policyRuleResourceDocument, error) {
	normalized := normalizeResourceName(firstResourceValidationName(name, doc.Name))
	if !resourceNamePattern.MatchString(normalized) {
		return policyRuleResourceDocument{}, fmt.Errorf("invalid policy rule name %q: use lowercase letters, numbers, hyphen, or underscore", normalized)
	}
	doc = normalizePolicyRuleResource(doc, normalized)
	if err := validatePolicyRuleResource(doc); err != nil {
		return policyRuleResourceDocument{}, err
	}
	if _, err := s.policyRuleResourcePath(normalized); err != nil {
		return policyRuleResourceDocument{}, err
	}
	content, err := renderPolicyRuleResource(doc)
	if err != nil {
		return policyRuleResourceDocument{}, err
	}
	var parsed policyRuleResourceDocument
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		return policyRuleResourceDocument{}, fmt.Errorf("validate policy rule: %w", err)
	}
	parsed = normalizePolicyRuleResource(parsed, normalized)
	if err := validatePolicyRuleResource(parsed); err != nil {
		return policyRuleResourceDocument{}, fmt.Errorf("validate policy rule: %w", err)
	}
	return parsed, nil
}

func (s *Server) deletePolicyRuleResource(name string) error {
	normalized := normalizeResourceName(name)
	if !resourceNamePattern.MatchString(normalized) {
		return fmt.Errorf("invalid policy rule name %q: use lowercase letters, numbers, hyphen, or underscore", normalized)
	}
	path, err := s.policyRuleResourcePath(normalized)
	if err != nil {
		return err
	}
	if _, ok := s.policyRuleResourceByName(normalized); !ok {
		return fmt.Errorf("policy rule %q not found", normalized)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete policy rule: %w", err)
	}
	return nil
}

func (s *Server) skillFileResourceList(name string) ([]skillFileResourceDocument, error) {
	parsed, ok, err := s.loadSkillResourceFromDisk(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		if doc, found := s.skillResourceByName(name); found {
			return skillFileResourceDocumentsFromSchema(doc.Resources), nil
		}
		return nil, fmt.Errorf("skill %q not found", normalizeResourceName(name))
	}
	return skillFileResourceDocumentsFromSchema(parsed.Resources), nil
}

func (s *Server) skillFileResource(name, rel string, includeContent bool) (skillFileResourceDocument, error) {
	dir, cleanRel, path, err := s.skillFilePath(name, rel)
	if err != nil {
		return skillFileResourceDocument{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return skillFileResourceDocument{}, fmt.Errorf("skill resource %q not found", cleanRel)
		}
		return skillFileResourceDocument{}, fmt.Errorf("stat skill resource: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return skillFileResourceDocument{}, fmt.Errorf("skill resource %q is a symlink and cannot be read through this API", cleanRel)
	}
	if info.IsDir() {
		return skillFileResourceDocument{}, fmt.Errorf("skill resource %q is a directory", cleanRel)
	}
	if !referenceWithinBase(dir, path) {
		return skillFileResourceDocument{}, fmt.Errorf("skill resource path escapes skill directory")
	}
	doc := skillFileResourceDocument{
		Path: cleanRel,
		Kind: skillResourceKind(cleanRel),
		Size: info.Size(),
	}
	if !includeContent {
		return doc, nil
	}
	if info.Size() > maxSkillResourceTextBytes {
		doc.Binary = true
		return doc, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return skillFileResourceDocument{}, fmt.Errorf("read skill resource: %w", err)
	}
	if bytes.Contains(data, []byte{0}) || !utf8.Valid(data) {
		doc.Binary = true
		return doc, nil
	}
	doc.Content = string(data)
	return doc, nil
}

func (s *Server) saveSkillFileResource(name, rel, content string) (skillFileResourceDocument, error) {
	if len([]byte(content)) > maxSkillResourceTextBytes {
		return skillFileResourceDocument{}, fmt.Errorf("skill resource content exceeds %d bytes", maxSkillResourceTextBytes)
	}
	if _, ok, err := s.loadSkillResourceFromDisk(name); err != nil {
		return skillFileResourceDocument{}, err
	} else if !ok {
		return skillFileResourceDocument{}, fmt.Errorf("skill %q not found", normalizeResourceName(name))
	}
	dir, cleanRel, path, err := s.skillFilePath(name, rel)
	if err != nil {
		return skillFileResourceDocument{}, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return skillFileResourceDocument{}, fmt.Errorf("skill resource %q is a symlink and cannot be replaced through this API", cleanRel)
		}
		if info.IsDir() {
			return skillFileResourceDocument{}, fmt.Errorf("skill resource %q is a directory", cleanRel)
		}
	} else if !os.IsNotExist(err) {
		return skillFileResourceDocument{}, fmt.Errorf("stat skill resource: %w", err)
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return skillFileResourceDocument{}, fmt.Errorf("create skill resource directory: %w", err)
	}
	if err := ensureSkillResourceParentWithin(dir, parent); err != nil {
		return skillFileResourceDocument{}, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return skillFileResourceDocument{}, fmt.Errorf("write skill resource: %w", err)
	}
	if err := s.runtime.ReloadSkills(); err != nil {
		return skillFileResourceDocument{}, fmt.Errorf("reload skills: %w", err)
	}
	return s.skillFileResource(name, cleanRel, true)
}

func (s *Server) deleteSkillFileResource(name, rel string) error {
	if _, ok, err := s.loadSkillResourceFromDisk(name); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("skill %q not found", normalizeResourceName(name))
	}
	_, cleanRel, path, err := s.skillFilePath(name, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("skill resource %q not found", cleanRel)
		}
		return fmt.Errorf("stat skill resource: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill resource %q is a symlink and cannot be deleted through this API", cleanRel)
	}
	if info.IsDir() {
		return fmt.Errorf("skill resource %q is a directory", cleanRel)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete skill resource: %w", err)
	}
	if err := s.runtime.ReloadSkills(); err != nil {
		return fmt.Errorf("reload skills: %w", err)
	}
	return nil
}

func (s *Server) loadSkillResourceFromDisk(name string) (schema.Skill, bool, error) {
	dir, err := s.skillDirectory(name)
	if err != nil {
		return schema.Skill{}, false, err
	}
	path := filepath.Join(dir, "SKILL.md")
	parsed, err := skill.ParseFile(path)
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "read skill:") && strings.Contains(err.Error(), "no such file") {
			return schema.Skill{}, false, nil
		}
		return schema.Skill{}, false, err
	}
	return *parsed, true, nil
}

func (s *Server) skillDirectory(name string) (string, error) {
	name = normalizeResourceName(name)
	if !resourceNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid skill name %q: use lowercase letters, numbers, hyphen, or underscore", name)
	}
	root := strings.TrimSpace(s.runtime.SkillRoot())
	if root == "" {
		return "", fmt.Errorf("skill root is not configured")
	}
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve skill root: %w", err)
	}
	dir := filepath.Clean(filepath.Join(root, name))
	if !referenceWithinBase(root, dir) {
		return "", fmt.Errorf("skill path escapes skill root")
	}
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("skill directory %q is a symlink", name)
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("stat skill directory: %w", err)
	}
	return dir, nil
}

func (s *Server) skillFilePath(name, rel string) (string, string, string, error) {
	dir, err := s.skillDirectory(name)
	if err != nil {
		return "", "", "", err
	}
	cleanRel, err := normalizeSkillFileRelativePath(rel)
	if err != nil {
		return "", "", "", err
	}
	path := filepath.Clean(filepath.Join(dir, filepath.FromSlash(cleanRel)))
	if !referenceWithinBase(dir, path) {
		return "", "", "", fmt.Errorf("skill resource path escapes skill directory")
	}
	return dir, cleanRel, path, nil
}

func normalizeSkillFileRelativePath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("skill resource path is required")
	}
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("skill resource path contains a null byte")
	}
	if strings.Contains(rel, "\\") {
		return "", fmt.Errorf("skill resource path must use forward slashes")
	}
	if slashpath.IsAbs(rel) || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("skill resource path must be relative")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", fmt.Errorf("skill resource path cannot contain ..")
		}
	}
	cleanRel := slashpath.Clean(rel)
	if cleanRel == "." || cleanRel == "" {
		return "", fmt.Errorf("skill resource path is required")
	}
	if strings.EqualFold(cleanRel, "SKILL.md") {
		return "", fmt.Errorf("SKILL.md is managed by the skill document API")
	}
	kind := skillResourceKind(cleanRel)
	if _, ok := editableSkillResourceDirs[kind]; !ok {
		return "", fmt.Errorf("skill resource path must start with one of: agents, assets, references, scripts, templates")
	}
	return cleanRel, nil
}

func skillResourceKind(rel string) string {
	if index := strings.Index(rel, "/"); index >= 0 {
		return rel[:index]
	}
	return rel
}

func skillFileResourceDocumentsFromSchema(resources []schema.SkillResource) []skillFileResourceDocument {
	out := make([]skillFileResourceDocument, 0, len(resources))
	for _, resource := range resources {
		out = append(out, skillFileResourceDocument{
			Path: resource.Path,
			Kind: resource.Kind,
			Size: resource.Size,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func ensureSkillResourceParentWithin(dir, parent string) error {
	dir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("resolve skill resource directory: %w", err)
	}
	parent, err = filepath.Abs(filepath.Clean(parent))
	if err != nil {
		return fmt.Errorf("resolve skill resource directory: %w", err)
	}
	if !referenceWithinBase(dir, parent) {
		return fmt.Errorf("skill resource directory escapes skill directory")
	}
	rel, err := filepath.Rel(dir, parent)
	if err != nil {
		return fmt.Errorf("resolve skill resource directory: %w", err)
	}
	if rel == "." {
		return nil
	}
	current := dir
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return fmt.Errorf("stat skill resource directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill resource directory %q is a symlink", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("skill resource parent %q is not a directory", current)
		}
	}
	return nil
}

func (s *Server) saveSkillResource(name string, doc skillResourceDocument) (skillResourceDocument, error) {
	doc, err := s.validateSkillResourceDryRun(name, doc)
	if err != nil {
		return skillResourceDocument{}, err
	}
	name = doc.Name
	dir, err := s.skillDirectory(name)
	if err != nil {
		return skillResourceDocument{}, err
	}
	path := filepath.Join(dir, "SKILL.md")
	content, err := renderSkillResource(doc)
	if err != nil {
		return skillResourceDocument{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return skillResourceDocument{}, fmt.Errorf("create skill directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return skillResourceDocument{}, fmt.Errorf("write skill: %w", err)
	}
	parsed, err := skill.ParseFile(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return skillResourceDocument{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return skillResourceDocument{}, fmt.Errorf("replace skill: %w", err)
	}
	if err := s.runtime.ReloadSkills(); err != nil {
		return skillResourceDocument{}, fmt.Errorf("reload skills: %w", err)
	}
	saved := skillResourceFromSchema(*parsed)
	saved.Path = path
	return saved, nil
}

func (s *Server) validateSkillResourceDryRun(name string, doc skillResourceDocument) (skillResourceDocument, error) {
	name = normalizeResourceName(firstResourceValidationName(name, doc.Name))
	if !resourceNamePattern.MatchString(name) {
		return skillResourceDocument{}, fmt.Errorf("invalid skill name %q: use lowercase letters, numbers, hyphen, or underscore", name)
	}
	doc.Name = name
	applySkillResourceDefaults(&doc)
	if _, err := s.skillDirectory(name); err != nil {
		return skillResourceDocument{}, err
	}
	content, err := renderSkillResource(doc)
	if err != nil {
		return skillResourceDocument{}, err
	}
	base, err := s.runtimeResourceRoot(".goflow", "tmp")
	if err != nil {
		return skillResourceDocument{}, err
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return skillResourceDocument{}, fmt.Errorf("create skill validation temp root: %w", err)
	}
	tmpRoot, err := os.MkdirTemp(base, "skill-validate-*")
	if err != nil {
		return skillResourceDocument{}, fmt.Errorf("create skill validation temp dir: %w", err)
	}
	defer os.RemoveAll(tmpRoot)
	tmpDir := filepath.Join(tmpRoot, name)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return skillResourceDocument{}, fmt.Errorf("create skill validation directory: %w", err)
	}
	tmpPath := filepath.Join(tmpDir, "SKILL.md")
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return skillResourceDocument{}, fmt.Errorf("write skill validation file: %w", err)
	}
	parsed, err := skill.ParseFile(tmpPath)
	if err != nil {
		return skillResourceDocument{}, err
	}
	return skillResourceFromSchema(*parsed), nil
}

func skillResourceFromSchema(item schema.Skill) skillResourceDocument {
	return skillResourceDocument{
		Name:             item.Name,
		Description:      item.Description,
		Version:          item.Version,
		Author:           item.Author,
		Format:           item.Format,
		Mode:             item.Mode,
		PreferredAgent:   item.PreferredAgent,
		AllowedToolKinds: append([]string(nil), item.AllowedToolKinds...),
		OutputKind:       item.OutputKind,
		Priority:         item.Priority,
		MaxIterations:    item.MaxIterations,
		NextSkills:       append([]string(nil), item.NextSkills...),
		Tools:            append([]schema.SkillTool(nil), item.Tools...),
		Params:           append([]schema.SkillParam(nil), item.Params...),
		Scripts:          append([]schema.SkillScript(nil), item.Scripts...),
		Activation:       item.Activation,
		Metadata:         copyMap(item.Metadata),
		Instructions:     item.Instructions,
		Resources:        append([]schema.SkillResource(nil), item.Resources...),
		Path:             item.Path,
	}
}

func agentResourceFromProfile(id string, profile config.AgentProfile) agentResourceDocument {
	return agentResourceDocument{
		ID:               id,
		Name:             profile.Name,
		Description:      profile.Description,
		SystemPrompt:     profile.SystemPrompt,
		Provider:         profile.Provider,
		Model:            profile.Model,
		Temperature:      profile.Temperature,
		MaxTokens:        profile.MaxTokens,
		MaxIterations:    profile.MaxIterations,
		AllowedToolKinds: toolKindsToStrings(profile.AllowedToolKinds),
		AllowedTools:     append([]string(nil), profile.AllowedTools...),
		ToolPolicy:       profile.ToolPolicy.String(),
		Mode:             profile.Mode,
	}
}

func providerResourceFromConfig(id string, provider config.LLMConfig, revealAPIKey bool) providerResourceDocument {
	doc := providerResourceDocument{
		ID:               id,
		Type:             provider.Provider,
		Provider:         provider.Provider,
		BaseURL:          provider.BaseURL,
		Model:            provider.Model,
		DefaultModel:     provider.Model,
		FallbackProvider: provider.FallbackProvider,
		Timeout:          durationString(provider.Timeout),
		Temperature:      provider.Temperature,
		MaxTokens:        provider.MaxTokens,
		RetryCount:       provider.RetryCount,
		RetryBackoff:     durationString(provider.RetryBackoff),
		APIKeySet:        strings.TrimSpace(provider.APIKey) != "",
	}
	if revealAPIKey {
		doc.APIKey = provider.APIKey
	}
	return doc
}

func applyProviderResourceDefaults(doc *providerResourceDocument) {
	doc.ID = normalizeResourceName(doc.ID)
	normalizeProviderResourceAliases(doc)
	if strings.TrimSpace(doc.Provider) == "" {
		doc.Provider = "openai-compatible"
	}
	if strings.TrimSpace(doc.Timeout) == "" {
		doc.Timeout = "60s"
	}
	if strings.TrimSpace(doc.RetryBackoff) == "" {
		doc.RetryBackoff = "2s"
	}
	if doc.RetryCount < 0 {
		doc.RetryCount = 0
	}
	if doc.MaxTokens < 0 {
		doc.MaxTokens = 0
	}
}

func normalizeProviderResourceAliases(doc *providerResourceDocument) {
	if doc == nil {
		return
	}
	if strings.TrimSpace(doc.Provider) == "" {
		doc.Provider = strings.TrimSpace(doc.Type)
	}
	if strings.TrimSpace(doc.Model) == "" {
		doc.Model = strings.TrimSpace(doc.DefaultModel)
	}
	if strings.TrimSpace(doc.APIKey) == "" {
		envKey := strings.TrimSpace(doc.EnvKey)
		if envKey != "" {
			doc.APIKey = envKey
			if !strings.HasPrefix(envKey, "${") {
				doc.APIKey = "${" + envKey + "}"
			}
		}
	}
}

func validateProviderResource(doc providerResourceDocument, existing map[string]struct{}) error {
	if strings.TrimSpace(doc.Provider) == "" {
		return fmt.Errorf("provider type is required")
	}
	if strings.TrimSpace(doc.Timeout) != "" {
		if _, err := time.ParseDuration(doc.Timeout); err != nil {
			return fmt.Errorf("provider timeout is invalid: %w", err)
		}
	}
	if strings.TrimSpace(doc.RetryBackoff) != "" {
		if _, err := time.ParseDuration(doc.RetryBackoff); err != nil {
			return fmt.Errorf("provider retry_backoff is invalid: %w", err)
		}
	}
	fallback := strings.TrimSpace(doc.FallbackProvider)
	if fallback == "" {
		return nil
	}
	if fallback == doc.ID {
		return fmt.Errorf("provider fallback_provider cannot reference itself")
	}
	if _, ok := existing[fallback]; !ok {
		return fmt.Errorf("provider references unknown fallback_provider %q", fallback)
	}
	return nil
}

func renderProviderResource(doc providerResourceDocument) (string, error) {
	meta := map[string]providerResourceDocument{doc.ID: {
		Provider:         doc.Provider,
		BaseURL:          doc.BaseURL,
		APIKey:           doc.APIKey,
		Model:            doc.Model,
		FallbackProvider: doc.FallbackProvider,
		Timeout:          doc.Timeout,
		Temperature:      doc.Temperature,
		MaxTokens:        doc.MaxTokens,
		RetryCount:       doc.RetryCount,
		RetryBackoff:     doc.RetryBackoff,
	}}
	data, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("render provider config: %w", err)
	}
	return "# Loaded automatically from configs/providers/*.yaml.\n" + string(data), nil
}

func testProviderResourceConnection(parent context.Context, doc providerResourceDocument) providerTestResult {
	cfg := providerResourceLLMConfig(doc)
	if strings.TrimSpace(cfg.APIKey) == "" {
		return providerTestInvalid(doc.ID, "api_key", "model provider setup required: configure api_key in Web Studio Settings/Resources or configs/providers/*.yaml")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return providerTestInvalid(doc.ID, "base_url", "model provider setup required: configure base_url in Web Studio Settings/Resources or configs/providers/*.yaml")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return providerTestInvalid(doc.ID, "model", "model provider setup required: configure model in Web Studio Settings/Resources or configs/providers/*.yaml")
	}
	cfg.Timeout = providerTestTimeout(cfg.Timeout)
	cfg.RetryCount = 0
	var client interface {
		Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error)
	}
	switch strings.TrimSpace(cfg.Provider) {
	case "", "openai-compatible":
		client = llm.NewClient(cfg)
	case "anthropic":
		client = llm.NewAnthropicClient(cfg)
	default:
		return providerTestInvalid(doc.ID, "provider", fmt.Sprintf("unsupported provider %q", cfg.Provider))
	}
	ctx, cancel := context.WithTimeout(parent, cfg.Timeout)
	defer cancel()
	started := time.Now()
	_, err := client.Chat(ctx, schema.ChatRequest{
		Model:       cfg.Model,
		MaxTokens:   8,
		Temperature: 0,
		Messages: []schema.Message{{
			Role:    "user",
			Content: "ping",
		}},
	})
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return providerTestResult{
			Valid:     false,
			Resource:  "provider",
			Name:      doc.ID,
			Status:    "error",
			Message:   err.Error(),
			Provider:  cfg.Provider,
			Model:     cfg.Model,
			LatencyMS: latency,
			Issues:    []resourceValidationIssue{resourceValidationIssueFor("provider", providerTestErrorField(err.Error()), err.Error())},
		}
	}
	return providerTestResult{
		Valid:     true,
		Resource:  "provider",
		Name:      doc.ID,
		Status:    "ok",
		Message:   "provider connection succeeded",
		Provider:  cfg.Provider,
		Model:     cfg.Model,
		LatencyMS: latency,
	}
}

func providerTestInvalid(name, field, message string) providerTestResult {
	return providerTestResult{
		Valid:    false,
		Resource: "provider",
		Name:     firstResourceValidationName(name),
		Status:   "error",
		Message:  message,
		Issues:   []resourceValidationIssue{resourceValidationIssueFor("provider", field, message)},
	}
}

func providerResourceLLMConfig(doc providerResourceDocument) config.LLMConfig {
	timeout, _ := time.ParseDuration(strings.TrimSpace(doc.Timeout))
	retryBackoff, _ := time.ParseDuration(strings.TrimSpace(doc.RetryBackoff))
	return config.LLMConfig{
		Provider:         strings.TrimSpace(doc.Provider),
		BaseURL:          os.ExpandEnv(strings.TrimSpace(doc.BaseURL)),
		APIKey:           os.ExpandEnv(strings.TrimSpace(doc.APIKey)),
		Model:            os.ExpandEnv(strings.TrimSpace(doc.Model)),
		FallbackProvider: strings.TrimSpace(doc.FallbackProvider),
		Timeout:          timeout,
		Temperature:      doc.Temperature,
		MaxTokens:        doc.MaxTokens,
		RetryCount:       doc.RetryCount,
		RetryBackoff:     retryBackoff,
	}
}

func providerTestTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 || timeout > 15*time.Second {
		return 15 * time.Second
	}
	return timeout
}

func providerTestErrorField(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "base_url"):
		return "base_url"
	case strings.Contains(lower, "api_key"):
		return "api_key"
	case strings.Contains(lower, "model"):
		return "model"
	case strings.Contains(lower, "unsupported provider"):
		return "provider"
	default:
		return "connection"
	}
}

func readProviderResourceMap(data []byte) (map[string]providerResourceDocument, error) {
	var wrapped struct {
		Providers map[string]providerResourceDocument `yaml:"providers"`
	}
	if err := yaml.Unmarshal(data, &wrapped); err != nil {
		return nil, err
	}
	if len(wrapped.Providers) > 0 {
		return wrapped.Providers, nil
	}
	var direct map[string]providerResourceDocument
	if err := yaml.Unmarshal(data, &direct); err != nil {
		return nil, err
	}
	return direct, nil
}

func applyAgentResourceDefaults(doc *agentResourceDocument) {
	doc.ID = normalizeResourceName(doc.ID)
	if strings.TrimSpace(doc.Name) == "" {
		doc.Name = strings.ReplaceAll(doc.ID, "-", " ")
	}
	if strings.TrimSpace(doc.Description) == "" {
		doc.Description = "Custom GoFlow agent profile."
	}
	if strings.TrimSpace(doc.Provider) == "" {
		doc.Provider = "primary"
	}
	if strings.TrimSpace(doc.Mode) == "" {
		doc.Mode = "chat"
	}
	if strings.TrimSpace(doc.ToolPolicy) == "" {
		doc.ToolPolicy = string(config.ToolPolicyConfirm)
	}
	if doc.MaxIterations <= 0 {
		doc.MaxIterations = 6
	}
	if len(doc.AllowedToolKinds) == 0 {
		doc.AllowedToolKinds = []string{string(config.ToolKindRead)}
	}
}

func validateAgentResource(doc agentResourceDocument, providers map[string]struct{}) error {
	if strings.TrimSpace(doc.Provider) == "" {
		return fmt.Errorf("agent provider is required")
	}
	if len(providers) > 0 {
		if _, ok := providers[strings.TrimSpace(doc.Provider)]; !ok {
			return fmt.Errorf("agent references unknown provider %q", doc.Provider)
		}
	}
	switch strings.TrimSpace(doc.Mode) {
	case "chat", "plan", "audit", "fix":
	default:
		return fmt.Errorf("agent mode must be one of chat, plan, audit, or fix")
	}
	switch config.ToolPolicy(strings.TrimSpace(doc.ToolPolicy)) {
	case config.ToolPolicyAllow, config.ToolPolicyDeny, config.ToolPolicyConfirm:
	default:
		return fmt.Errorf("agent tool_policy must be one of allow, deny, or confirm")
	}
	if doc.MaxIterations <= 0 {
		return fmt.Errorf("agent max_iterations must be greater than 0")
	}
	validKinds := map[string]struct{}{
		string(config.ToolKindRead):    {},
		string(config.ToolKindWrite):   {},
		string(config.ToolKindExec):    {},
		string(config.ToolKindNetwork): {},
		string(config.ToolKindUnknown): {},
	}
	for _, kind := range doc.AllowedToolKinds {
		if _, ok := validKinds[strings.TrimSpace(kind)]; !ok {
			return fmt.Errorf("agent allowed_tool_kinds contains unsupported kind %q", kind)
		}
	}
	return nil
}

func renderAgentResource(doc agentResourceDocument) (string, error) {
	meta := map[string]agentResourceDocument{doc.ID: {
		Name:             doc.Name,
		Description:      doc.Description,
		SystemPrompt:     doc.SystemPrompt,
		Provider:         doc.Provider,
		Model:            doc.Model,
		Temperature:      doc.Temperature,
		MaxTokens:        doc.MaxTokens,
		MaxIterations:    doc.MaxIterations,
		AllowedToolKinds: doc.AllowedToolKinds,
		AllowedTools:     doc.AllowedTools,
		ToolPolicy:       doc.ToolPolicy,
		Mode:             doc.Mode,
	}}
	data, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("render agent config: %w", err)
	}
	return "# Loaded automatically from configs/agents/*.yaml.\n" + string(data), nil
}

func (s *Server) annotateAgentResourceApplyState(doc *agentResourceDocument) {
	if doc == nil {
		return
	}
	id := normalizeResourceName(doc.ID)
	if id == "" {
		return
	}
	path, err := s.agentResourcePath(id)
	if err != nil || !fileExists(path) {
		return
	}
	if strings.TrimSpace(doc.Path) == "" {
		doc.Path = path
	}
	if s == nil || s.runtime == nil {
		markAgentResourceRestartRequired(doc)
		return
	}
	active, ok := s.runtime.Profile(id)
	if !ok {
		markAgentResourceRestartRequired(doc)
		return
	}
	if !sameAgentResourceDocument(*doc, agentResourceFromProfile(id, active)) {
		markAgentResourceRestartRequired(doc)
		return
	}
	markAgentResourceActive(doc)
}

func (s *Server) annotateProviderResourceApplyState(doc *providerResourceDocument) {
	if doc == nil {
		return
	}
	id := normalizeResourceName(doc.ID)
	if id == "" {
		return
	}
	path, err := s.providerResourcePath(id)
	if err != nil || !fileExists(path) {
		return
	}
	if strings.TrimSpace(doc.Path) == "" {
		doc.Path = path
	}
	if s == nil || s.runtime == nil {
		markProviderResourceRestartRequired(doc)
		return
	}
	active, ok := s.runtime.Provider(id)
	if !ok {
		markProviderResourceRestartRequired(doc)
		return
	}
	if !sameProviderResourceDocument(*doc, providerResourceFromConfig(id, active, false)) {
		markProviderResourceRestartRequired(doc)
		return
	}
	markProviderResourceActive(doc)
}

func (s *Server) annotateToolResourceApplyState(doc *toolResourceDocument) {
	if doc == nil {
		return
	}
	name := normalizeResourceName(doc.Name)
	if name == "" {
		return
	}
	codePath, configPath, err := s.toolResourcePaths(name)
	if err != nil || (!fileExists(codePath) && !fileExists(configPath)) {
		return
	}
	if strings.TrimSpace(doc.Path) == "" {
		doc.Path = codePath
	}
	if strings.TrimSpace(doc.ConfigPath) == "" {
		doc.ConfigPath = configPath
	}
	active := s.toolServerActive(name)
	if active {
		if !doc.Enabled {
			markToolResourceRestartRequired(doc)
			return
		}
		markToolResourceActive(doc)
		return
	}
	if doc.Enabled {
		markToolResourceRestartRequired(doc)
		return
	}
	markToolResourceSaved(doc)
}

func (s *Server) annotateToolResourceRisk(doc *toolResourceDocument) {
	if doc == nil {
		return
	}
	if doc.Risk != nil {
		return
	}
	doc.Risk = s.toolRiskProfileForName(doc.Name)
}

func (s *Server) toolRiskProfileForName(name string) *schema.ToolRiskProfile {
	name = strings.TrimSpace(name)
	if name == "" || s == nil || s.runtime == nil {
		return nil
	}
	if tools, err := s.runtime.Tools(context.Background()); err == nil {
		for _, tool := range tools {
			qualified := qualifiedToolName(tool)
			if strings.EqualFold(name, qualified) || strings.EqualFold(name, tool.Name) || normalizeResourceName(name) == normalizeResourceName(qualified) {
				return s.toolRiskProfileForTool(tool)
			}
		}
	}
	serverName := strings.TrimSpace(name)
	if strings.Contains(serverName, "/") {
		serverName = strings.TrimSpace(strings.SplitN(serverName, "/", 2)[0])
	}
	tool := schema.Tool{Name: name, Server: serverName, Kind: string(config.ToolKindUnknown)}
	return s.toolRiskProfileForTool(tool)
}

func (s *Server) toolRiskProfileForTool(tool schema.Tool) *schema.ToolRiskProfile {
	kind := strings.ToLower(strings.TrimSpace(tool.Kind))
	if kind == "" {
		kind = string(config.ToolKindUnknown)
	}
	server := strings.TrimSpace(tool.Server)
	serverProfile := s.mcpServerSummaryByName(server)
	profile := &schema.ToolRiskProfile{
		QualifiedName:              qualifiedToolName(tool),
		Server:                     server,
		Kind:                       kind,
		Capabilities:               toolRiskCapabilities(kind),
		WorkspaceScopedInputs:      toolSchemaHasWorkspacePathInputs(tool.InputSchema),
		Destructive:                toolRiskDestructive(tool),
		RequiresApproval:           toolKindUsuallyRequiresApproval(kind),
		ExternalSandboxRecommended: true,
		RiskLevel:                  toolKindRiskLevel(kind),
	}
	if serverProfile != nil {
		profile.Isolation = serverProfile.Isolation
		profile.IsolationLevel = serverProfile.IsolationLevel
		profile.Sandboxed = serverProfile.Sandboxed
		profile.NetworkDisabled = serverProfile.NetworkDisabled
		profile.NetworkEnforced = serverProfile.NetworkEnforced
		profile.FilesystemSandboxed = serverProfile.FilesystemSandboxed
		profile.PrivilegeSandboxed = serverProfile.PrivilegeSandboxed
		profile.ResourceLimited = serverProfile.ResourceLimited
		profile.SandboxFeatures = append(profile.SandboxFeatures, serverProfile.SandboxFeatures...)
		profile.MissingSandboxFeatures = append(profile.MissingSandboxFeatures, serverProfile.MissingSandboxFeatures...)
		if serverProfile.WindowsIsolation != nil {
			windowsIsolation := *serverProfile.WindowsIsolation
			profile.WindowsIsolation = &windowsIsolation
		}
		profile.EnvAllowlistSet = serverProfile.EnvAllowlistSet
		profile.EnvAllowlist = append(profile.EnvAllowlist, serverProfile.EnvAllowlist...)
		profile.SensitiveEnv = append(profile.SensitiveEnv, serverProfile.SensitiveEnv...)
		profile.CanAccessHostOutside = serverProfile.CanAccessHostOutside
		profile.SecurityBoundary = serverProfile.SecurityBoundary
		profile.Warnings = append(profile.Warnings, serverProfile.Warnings...)
		profile.Recommendations = append(profile.Recommendations, serverProfile.Recommendations...)
		if serverProfile.Sandboxed && kind == string(config.ToolKindRead) && !profile.Destructive {
			profile.RiskLevel = "low"
			profile.ExternalSandboxRecommended = false
		} else if serverProfile.NetworkEnforced && kind == string(config.ToolKindNetwork) && !profile.Destructive {
			profile.RiskLevel = "medium"
			profile.ExternalSandboxRecommended = false
		} else if serverProfile.Sandboxed && profile.RiskLevel == "high" {
			profile.RiskLevel = "medium"
		}
	} else {
		profile.Isolation = "unknown"
		profile.IsolationLevel = "unknown"
		profile.CanAccessHostOutside = true
		profile.SecurityBoundary = "MCP server isolation could not be matched to runtime configuration."
		profile.Warnings = append(profile.Warnings, "tool server isolation is unknown")
		profile.Recommendations = append(profile.Recommendations, "define this MCP server in config with explicit isolation and command/env boundaries")
	}
	if profile.Destructive {
		profile.RequiresApproval = true
		profile.Warnings = append(profile.Warnings, "tool appears destructive based on its name or capability kind")
	}
	if !profile.Sandboxed && toolKindNeedsRealSandbox(kind) {
		profile.Warnings = append(profile.Warnings, "tool uses write, exec, network, or unknown capabilities without a real OS/container sandbox")
	}
	if profile.WorkspaceScopedInputs {
		profile.Capabilities = append(profile.Capabilities, "workspace_path_input")
		profile.WorkspaceScopeEnforced = toolRiskWorkspaceScopeLikelyEnforced(tool)
		if !profile.WorkspaceScopeEnforced {
			profile.Warnings = append(profile.Warnings, "tool accepts file or directory path inputs; GoFlow can pass GOFLOW_WORKSPACE_ROOT but cannot prove the tool enforces workspace scoping")
			profile.Recommendations = append(profile.Recommendations, "ensure this MCP server resolves path inputs under GOFLOW_WORKSPACE_ROOT and rejects traversal or symlink escapes")
		}
	}
	profile.Warnings = uniqueSortedNonEmpty(profile.Warnings)
	profile.Recommendations = uniqueSortedNonEmpty(profile.Recommendations)
	return profile
}

func (s *Server) mcpServerSummaryByName(name string) *mcpServerSummary {
	name = strings.TrimSpace(name)
	if name == "" || s == nil || s.runtime == nil {
		return nil
	}
	health := s.runtime.MCPHealthStatus(context.Background())
	for _, summary := range s.mcpServerSummaries(health) {
		if strings.EqualFold(summary.Name, name) {
			item := summary
			return &item
		}
	}
	return nil
}

func qualifiedToolName(tool schema.Tool) string {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return ""
	}
	server := strings.TrimSpace(tool.Server)
	if server == "" {
		return name
	}
	return server + "/" + name
}

func toolRiskCapabilities(kind string) []string {
	switch config.ToolKind(strings.ToLower(strings.TrimSpace(kind))) {
	case config.ToolKindRead:
		return []string{"filesystem_or_metadata_read"}
	case config.ToolKindWrite:
		return []string{"filesystem_or_state_write"}
	case config.ToolKindExec:
		return []string{"process_execution"}
	case config.ToolKindNetwork:
		return []string{"network_access"}
	default:
		return []string{"unknown_capability"}
	}
}

func toolRiskDestructive(tool schema.Tool) bool {
	kind := strings.ToLower(strings.TrimSpace(tool.Kind))
	if kind == string(config.ToolKindExec) || kind == string(config.ToolKindWrite) || kind == string(config.ToolKindUnknown) {
		return true
	}
	name := strings.ToLower(qualifiedToolName(tool))
	for _, marker := range []string{"delete", "remove", "write", "patch", "move", "rename", "exec", "run", "shell", "upload", "deploy"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func toolKindUsuallyRequiresApproval(kind string) bool {
	switch config.ToolKind(strings.ToLower(strings.TrimSpace(kind))) {
	case config.ToolKindWrite, config.ToolKindExec, config.ToolKindNetwork, config.ToolKindUnknown:
		return true
	default:
		return false
	}
}

func toolKindNeedsRealSandbox(kind string) bool {
	switch config.ToolKind(strings.ToLower(strings.TrimSpace(kind))) {
	case config.ToolKindWrite, config.ToolKindExec, config.ToolKindNetwork, config.ToolKindUnknown:
		return true
	default:
		return false
	}
}

func toolSchemaHasWorkspacePathInputs(inputSchema json.RawMessage) bool {
	if len(inputSchema) == 0 {
		return false
	}
	return toolSchemaHasWorkspacePathInputsAt("", inputSchema, 0)
}

func toolSchemaHasWorkspacePathInputsAt(name string, raw json.RawMessage, depth int) bool {
	if depth > 8 || len(raw) == 0 {
		return false
	}
	if toolSchemaPropertyLooksPathLike(name, raw) {
		return true
	}
	var schemaDoc struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		PatternProperties    map[string]json.RawMessage `json:"patternProperties"`
		Items                json.RawMessage            `json:"items"`
		AdditionalProperties json.RawMessage            `json:"additionalProperties"`
		OneOf                []json.RawMessage          `json:"oneOf"`
		AnyOf                []json.RawMessage          `json:"anyOf"`
		AllOf                []json.RawMessage          `json:"allOf"`
		Definitions          map[string]json.RawMessage `json:"definitions"`
		Defs                 map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schemaDoc); err != nil {
		return false
	}
	for childName, childRaw := range schemaDoc.Properties {
		if toolSchemaHasWorkspacePathInputsAt(childName, childRaw, depth+1) {
			return true
		}
	}
	for pattern, childRaw := range schemaDoc.PatternProperties {
		if toolSchemaHasWorkspacePathInputsAt(pattern, childRaw, depth+1) {
			return true
		}
	}
	if len(schemaDoc.Items) > 0 && toolSchemaHasWorkspacePathInputsAt(name, schemaDoc.Items, depth+1) {
		return true
	}
	if len(schemaDoc.AdditionalProperties) > 0 && toolSchemaHasWorkspacePathInputsAt(name, schemaDoc.AdditionalProperties, depth+1) {
		return true
	}
	for _, variants := range [][]json.RawMessage{schemaDoc.OneOf, schemaDoc.AnyOf, schemaDoc.AllOf} {
		for _, childRaw := range variants {
			if toolSchemaHasWorkspacePathInputsAt(name, childRaw, depth+1) {
				return true
			}
		}
	}
	for childName, childRaw := range schemaDoc.Definitions {
		if toolSchemaHasWorkspacePathInputsAt(childName, childRaw, depth+1) {
			return true
		}
	}
	for childName, childRaw := range schemaDoc.Defs {
		if toolSchemaHasWorkspacePathInputsAt(childName, childRaw, depth+1) {
			return true
		}
	}
	return false
}

func toolSchemaPropertyLooksPathLike(name string, raw json.RawMessage) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, marker := range []string{"path", "file", "filename", "directory", "dir", "folder", "workdir"} {
		if name == marker || strings.HasSuffix(name, "_"+marker) || strings.HasSuffix(name, "-"+marker) || strings.Contains(name, marker+"_") || strings.Contains(name, marker+"-") {
			return true
		}
	}
	var property struct {
		Type        string `json:"type"`
		Format      string `json:"format"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &property); err != nil {
		return false
	}
	text := strings.ToLower(property.Format + " " + property.Description)
	return strings.Contains(text, "path") || strings.Contains(text, "file path") || strings.Contains(text, "workspace")
}

func toolRiskWorkspaceScopeLikelyEnforced(tool schema.Tool) bool {
	server := strings.ToLower(strings.TrimSpace(tool.Server))
	name := strings.ToLower(strings.TrimSpace(tool.Name))
	qualified := strings.ToLower(qualifiedToolName(tool))
	switch {
	case server == "file_tools" || strings.HasPrefix(qualified, "file_tools/"):
		return true
	case server == "python_notes" || strings.HasPrefix(qualified, "python_notes/"):
		return true
	case strings.HasPrefix(name, "file_tools/") || strings.HasPrefix(name, "python_notes/"):
		return true
	default:
		return false
	}
}

func toolKindRiskLevel(kind string) string {
	switch config.ToolKind(strings.ToLower(strings.TrimSpace(kind))) {
	case config.ToolKindRead:
		return "low"
	case config.ToolKindNetwork:
		return "medium"
	case config.ToolKindWrite, config.ToolKindExec, config.ToolKindUnknown:
		return "high"
	default:
		return "high"
	}
}

func uniqueSortedNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func markAgentResourceRestartRequired(doc *agentResourceDocument) {
	if doc == nil {
		return
	}
	doc.RestartRequired = true
	doc.ApplyState = "restart_required"
	doc.ApplyMessage = "Agent config modules are loaded when the runtime starts; restart or rebootstrap GoFlow before this agent profile is active."
}

func markAgentResourceActive(doc *agentResourceDocument) {
	if doc == nil {
		return
	}
	doc.RestartRequired = false
	doc.ApplyState = "active"
	doc.ApplyMessage = "Saved agent config module matches the active runtime."
}

func markProviderResourceRestartRequired(doc *providerResourceDocument) {
	if doc == nil {
		return
	}
	doc.RestartRequired = true
	doc.ApplyState = "restart_required"
	doc.ApplyMessage = "Provider config modules are loaded when the runtime starts; restart or rebootstrap GoFlow before this model provider is active."
}

func markProviderResourceActive(doc *providerResourceDocument) {
	if doc == nil {
		return
	}
	doc.RestartRequired = false
	doc.ApplyState = "active"
	doc.ApplyMessage = "Saved provider config module matches the active runtime."
}

func markToolResourceRestartRequired(doc *toolResourceDocument) {
	if doc == nil {
		return
	}
	doc.RestartRequired = true
	doc.ApplyState = "restart_required"
	doc.ApplyMessage = "MCP server modules are started during runtime bootstrap; restart or rebootstrap GoFlow before this tool server is available."
}

func markToolResourceActive(doc *toolResourceDocument) {
	if doc == nil {
		return
	}
	doc.RestartRequired = false
	doc.ApplyState = "active"
	doc.ApplyMessage = "Saved MCP server module appears active in the current runtime."
}

func markToolResourceSaved(doc *toolResourceDocument) {
	if doc == nil {
		return
	}
	doc.RestartRequired = false
	doc.ApplyState = "saved"
	doc.ApplyMessage = "Saved MCP server module is disabled and will not start until it is enabled."
}

func sameAgentResourceDocument(a, b agentResourceDocument) bool {
	a = comparableAgentResource(a)
	b = comparableAgentResource(b)
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func comparableAgentResource(doc agentResourceDocument) agentResourceDocument {
	doc.ID = normalizeResourceName(doc.ID)
	doc.Name = strings.TrimSpace(doc.Name)
	doc.Description = strings.TrimSpace(doc.Description)
	doc.SystemPrompt = strings.TrimSpace(doc.SystemPrompt)
	doc.Provider = strings.TrimSpace(doc.Provider)
	doc.Model = strings.TrimSpace(doc.Model)
	doc.ToolPolicy = strings.TrimSpace(doc.ToolPolicy)
	doc.Mode = strings.TrimSpace(doc.Mode)
	doc.AllowedToolKinds = normalizedComparableStrings(doc.AllowedToolKinds)
	doc.AllowedTools = normalizedComparableStrings(doc.AllowedTools)
	doc.Path = ""
	doc.RestartRequired = false
	doc.ApplyState = ""
	doc.ApplyMessage = ""
	return doc
}

func sameProviderResourceDocument(a, b providerResourceDocument) bool {
	a = comparableProviderResource(a)
	b = comparableProviderResource(b)
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func comparableProviderResource(doc providerResourceDocument) providerResourceDocument {
	doc.ID = normalizeResourceName(doc.ID)
	doc.Provider = strings.TrimSpace(doc.Provider)
	doc.BaseURL = strings.TrimSpace(doc.BaseURL)
	doc.APIKey = ""
	doc.APIKeySet = false
	doc.Model = strings.TrimSpace(doc.Model)
	doc.FallbackProvider = strings.TrimSpace(doc.FallbackProvider)
	doc.Timeout = comparableDurationString(doc.Timeout)
	doc.RetryBackoff = comparableDurationString(doc.RetryBackoff)
	doc.Path = ""
	doc.RestartRequired = false
	doc.ApplyState = ""
	doc.ApplyMessage = ""
	return doc
}

func normalizedComparableStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func comparableDurationString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return value
	}
	return durationString(duration)
}

func (s *Server) toolServerActive(name string) bool {
	if s == nil || s.runtime == nil {
		return false
	}
	name = normalizeResourceName(name)
	if name == "" {
		return false
	}
	for server, status := range s.runtime.MCPHealthStatus(context.Background()) {
		if normalizeResourceName(server) != name {
			continue
		}
		status = strings.ToLower(strings.TrimSpace(status))
		return status != "" && status != "disabled" && status != "missing"
	}
	prefix := name + "/"
	for _, tool := range s.runtime.ToolNames() {
		tool = strings.ToLower(strings.TrimSpace(tool))
		normalizedTool := normalizeResourceName(tool)
		if normalizedTool == name || strings.HasPrefix(tool, prefix) || strings.HasPrefix(normalizedTool, prefix) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (s *Server) defaultToolResource(name string) toolResourceDocument {
	name = normalizeResourceName(name)
	fileName := name + ".py"
	code, err := scaffold.RenderPythonMCPToolCode(s.runtimeHomeForScaffold(), name)
	if err != nil {
		code, _ = scaffold.RenderPythonMCPToolCode("", name)
	}
	return toolResourceDocument{
		Name:               name,
		Language:           "python",
		Description:        "Custom Python MCP tool server.",
		Command:            "python",
		Args:               []string{"./mcp_servers/" + fileName},
		Enabled:            true,
		Timeout:            "30s",
		WorkDir:            ".",
		EnvAllowlist:       []string{"PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "TMP", "TEMP"},
		Isolation:          "process_group",
		RestartLimit:       3,
		Cooldown:           "10s",
		MaxConcurrentCalls: 1,
		AllowedCommands:    []string{"python"},
		MaxRequestBytes:    65536,
		MaxResponseBytes:   2097152,
		Code:               code,
	}
}

func (s *Server) applyToolResourceDefaults(doc *toolResourceDocument) {
	defaults := s.defaultToolResource(doc.Name)
	if strings.TrimSpace(doc.Language) == "" {
		doc.Language = defaults.Language
	}
	if strings.TrimSpace(doc.Description) == "" {
		doc.Description = defaults.Description
	}
	if strings.TrimSpace(doc.Command) == "" {
		doc.Command = defaults.Command
	}
	if len(doc.Args) == 0 {
		doc.Args = defaults.Args
	}
	if strings.TrimSpace(doc.Timeout) == "" {
		doc.Timeout = defaults.Timeout
	}
	if strings.TrimSpace(doc.WorkDir) == "" {
		doc.WorkDir = defaults.WorkDir
	}
	if len(doc.EnvAllowlist) == 0 {
		doc.EnvAllowlist = defaults.EnvAllowlist
	}
	if strings.TrimSpace(doc.Isolation) == "" {
		doc.Isolation = defaults.Isolation
	}
	if doc.RestartLimit <= 0 {
		doc.RestartLimit = defaults.RestartLimit
	}
	if strings.TrimSpace(doc.Cooldown) == "" {
		doc.Cooldown = defaults.Cooldown
	}
	if doc.MaxConcurrentCalls <= 0 {
		doc.MaxConcurrentCalls = defaults.MaxConcurrentCalls
	}
	if len(doc.AllowedCommands) == 0 && len(doc.AllowedCommandPaths) == 0 {
		doc.AllowedCommands = defaults.AllowedCommands
	}
	if doc.MaxRequestBytes <= 0 {
		doc.MaxRequestBytes = defaults.MaxRequestBytes
	}
	if doc.MaxResponseBytes <= 0 {
		doc.MaxResponseBytes = defaults.MaxResponseBytes
	}
}

func (s *Server) runtimeHomeForScaffold() string {
	if s == nil || s.runtime == nil {
		return ""
	}
	return strings.TrimSpace(s.runtime.RuntimeHome())
}

func renderToolResourceConfig(doc toolResourceDocument) (string, error) {
	configDoc := toolResourceConfigDocument{MCPServers: []toolResourceServerDocument{{
		Name:                doc.Name,
		Command:             doc.Command,
		Args:                doc.Args,
		Enabled:             doc.Enabled,
		Timeout:             doc.Timeout,
		WorkDir:             doc.WorkDir,
		EnvAllowlist:        doc.EnvAllowlist,
		NetworkDisabled:     doc.NetworkDisabled,
		Isolation:           doc.Isolation,
		IsolationProfile:    doc.IsolationProfile,
		IsolationOptions:    copyMap(doc.IsolationOptions),
		RestartLimit:        doc.RestartLimit,
		Cooldown:            doc.Cooldown,
		MaxConcurrentCalls:  doc.MaxConcurrentCalls,
		AllowedCommandPaths: doc.AllowedCommandPaths,
		AllowedCommands:     doc.AllowedCommands,
		MaxRequestBytes:     doc.MaxRequestBytes,
		MaxResponseBytes:    doc.MaxResponseBytes,
	}}}
	data, err := yaml.Marshal(configDoc)
	if err != nil {
		return "", fmt.Errorf("render tool config: %w", err)
	}
	return "# Loaded automatically from configs/mcp_servers/*.yaml on next startup.\n" + string(data), nil
}

func normalizePolicyRuleResource(doc policyRuleResourceDocument, name string) policyRuleResourceDocument {
	normalized := normalizeResourceName(name)
	doc.Name = normalized
	definition := agent.NormalizeWorkflowPolicyRuleDefinition(policyRuleDefinitionFromResource(doc))
	return policyRuleResourceFromDefinition(definition, doc.Path)
}

func validatePolicyRuleResource(doc policyRuleResourceDocument) error {
	if !resourceNamePattern.MatchString(doc.Name) {
		return fmt.Errorf("invalid policy rule name %q: use lowercase letters, numbers, hyphen, or underscore", doc.Name)
	}
	if err := agent.ValidateWorkflowPolicyRuleDefinition(policyRuleDefinitionFromResource(doc)); err != nil {
		return err
	}
	return nil
}

func renderPolicyRuleResource(doc policyRuleResourceDocument) (string, error) {
	data, err := yaml.Marshal(agent.MigrateWorkflowPolicyRuleDefinition(policyRuleDefinitionFromResource(doc)))
	if err != nil {
		return "", fmt.Errorf("render policy rule: %w", err)
	}
	return "# Loaded automatically from policies/workflow_rules/*.yaml.\n" + string(data), nil
}

func policyRuleDefinitionFromResource(doc policyRuleResourceDocument) agent.WorkflowPolicyRuleDefinition {
	return agent.WorkflowPolicyRuleDefinition{
		Kind:                doc.Kind,
		Version:             doc.Version,
		MinVersion:          doc.MinVersion,
		MigratedFromVersion: doc.MigratedFromVersion,
		Name:                doc.Name,
		Label:               doc.Label,
		Description:         doc.Description,
		Operator:            doc.Operator,
		Expression:          doc.Expression,
		Reason:              doc.Reason,
		Defaults:            copyMap(doc.Defaults),
		Params:              append([]agent.WorkflowNodeFieldOption(nil), doc.Params...),
	}
}

func policyRuleResourceFromDefinition(definition agent.WorkflowPolicyRuleDefinition, path string) policyRuleResourceDocument {
	return policyRuleResourceDocument{
		Kind:                definition.Kind,
		Version:             definition.Version,
		MinVersion:          definition.MinVersion,
		MigratedFromVersion: definition.MigratedFromVersion,
		Name:                definition.Name,
		Label:               definition.Label,
		Description:         definition.Description,
		Operator:            definition.Operator,
		Expression:          definition.Expression,
		Reason:              definition.Reason,
		Defaults:            copyMap(definition.Defaults),
		Params:              append([]agent.WorkflowNodeFieldOption(nil), definition.Params...),
		Path:                path,
	}
}

func toolResourceFromServerDocument(doc toolResourceServerDocument) toolResourceDocument {
	out := toolResourceDocument{
		Name:                doc.Name,
		Language:            "python",
		Command:             doc.Command,
		Args:                append([]string(nil), doc.Args...),
		Enabled:             doc.Enabled,
		Timeout:             doc.Timeout,
		WorkDir:             doc.WorkDir,
		EnvAllowlist:        append([]string(nil), doc.EnvAllowlist...),
		NetworkDisabled:     doc.NetworkDisabled,
		Isolation:           doc.Isolation,
		IsolationProfile:    doc.IsolationProfile,
		IsolationOptions:    copyMap(doc.IsolationOptions),
		RestartLimit:        doc.RestartLimit,
		Cooldown:            doc.Cooldown,
		MaxConcurrentCalls:  doc.MaxConcurrentCalls,
		AllowedCommandPaths: append([]string(nil), doc.AllowedCommandPaths...),
		AllowedCommands:     append([]string(nil), doc.AllowedCommands...),
		MaxRequestBytes:     doc.MaxRequestBytes,
		MaxResponseBytes:    doc.MaxResponseBytes,
	}
	normalizeToolResourceIsolationProfile(&out)
	return out
}

func normalizeToolResourceIsolationProfile(doc *toolResourceDocument) {
	if doc == nil {
		return
	}
	profile := strings.ToLower(strings.TrimSpace(doc.IsolationProfile))
	if profile == "" && doc.IsolationOptions != nil {
		profile = strings.ToLower(strings.TrimSpace(doc.IsolationOptions["profile"]))
	}
	if profile == "" {
		return
	}
	doc.IsolationProfile = profile
	if doc.IsolationOptions != nil {
		delete(doc.IsolationOptions, "profile")
	}
}

func applySkillResourceDefaults(doc *skillResourceDocument) {
	doc.Name = normalizeResourceName(doc.Name)
	if strings.TrimSpace(doc.Version) == "" {
		doc.Version = "1.0.0"
	}
	if strings.TrimSpace(doc.Author) == "" {
		doc.Author = "GoFlow Studio"
	}
	if strings.TrimSpace(doc.Mode) == "" {
		doc.Mode = "chat"
	}
	if strings.TrimSpace(doc.PreferredAgent) == "" {
		doc.PreferredAgent = "chat"
	}
	if len(doc.AllowedToolKinds) == 0 {
		doc.AllowedToolKinds = []string{"read"}
	}
	if strings.TrimSpace(doc.OutputKind) == "" {
		doc.OutputKind = "summary"
	}
	if len(doc.Activation.Keywords) == 0 {
		doc.Activation.Keywords = []string{doc.Name}
	}
	if strings.TrimSpace(doc.Description) == "" {
		doc.Description = "Custom GoFlow skill."
	}
	if strings.TrimSpace(doc.Instructions) == "" {
		doc.Instructions = "## Role\n\nDescribe what this skill should do.\n\n## Workflow\n\n1. Inspect relevant context.\n2. Execute the task safely.\n3. Summarize results and verification."
	}
}

func renderSkillResource(doc skillResourceDocument) (string, error) {
	meta := skillResourceMeta{
		Name:             doc.Name,
		Description:      doc.Description,
		Version:          doc.Version,
		Author:           doc.Author,
		Format:           doc.Format,
		Mode:             doc.Mode,
		PreferredAgent:   doc.PreferredAgent,
		AllowedToolKinds: doc.AllowedToolKinds,
		OutputKind:       doc.OutputKind,
		Priority:         doc.Priority,
		MaxIterations:    doc.MaxIterations,
		NextSkills:       doc.NextSkills,
		Tools:            doc.Tools,
		Params:           doc.Params,
		Scripts:          doc.Scripts,
		Activation:       doc.Activation,
		Metadata:         doc.Metadata,
	}
	data, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("render skill frontmatter: %w", err)
	}
	instructions := strings.TrimSpace(doc.Instructions)
	return "---\n" + string(data) + "---\n\n" + instructions + "\n", nil
}

func (s *Server) providerResourceRoot() (string, error) {
	root, err := s.runtimeResourceRoot("configs", "providers")
	if err != nil {
		return "", err
	}
	return root, nil
}

func (s *Server) providerResourcePath(name string) (string, error) {
	root, err := s.providerResourceRoot()
	if err != nil {
		return "", err
	}
	name = normalizeResourceName(name)
	path := filepath.Clean(filepath.Join(root, name+".yaml"))
	if !referenceWithinBase(root, path) {
		return "", fmt.Errorf("provider config path escapes runtime config directory")
	}
	return path, nil
}

func (s *Server) loadProviderResourceSnippet(name string) (providerResourceDocument, bool) {
	path, err := s.providerResourcePath(name)
	if err != nil {
		return providerResourceDocument{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return providerResourceDocument{}, false
	}
	parsed, err := readProviderResourceMap(data)
	if err != nil {
		return providerResourceDocument{}, false
	}
	id := normalizeResourceName(name)
	doc, ok := parsed[id]
	if !ok {
		return providerResourceDocument{}, false
	}
	doc.ID = id
	normalizeProviderResourceAliases(&doc)
	applyProviderResourceDefaults(&doc)
	doc.Path = path
	doc.APIKeySet = strings.TrimSpace(doc.APIKey) != ""
	return doc, true
}

func (s *Server) providerNamesExcept(excluded string) map[string]struct{} {
	names := make(map[string]struct{})
	if s == nil || s.runtime == nil {
		return names
	}
	excluded = normalizeResourceName(excluded)
	for _, name := range s.runtime.ProviderNames() {
		if normalizeResourceName(name) == excluded {
			continue
		}
		names[name] = struct{}{}
	}
	if root, err := s.providerResourceRoot(); err == nil {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if entry.IsDir() || !isYAMLFile(entry.Name()) {
				continue
			}
			name := normalizeResourceName(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
			if name == "" || name == excluded {
				continue
			}
			names[name] = struct{}{}
		}
	}
	return names
}

func (s *Server) agentResourceRoot() (string, error) {
	root, err := s.runtimeResourceRoot("configs", "agents")
	if err != nil {
		return "", err
	}
	return root, nil
}

func (s *Server) agentResourcePath(name string) (string, error) {
	root, err := s.agentResourceRoot()
	if err != nil {
		return "", err
	}
	name = normalizeResourceName(name)
	path := filepath.Clean(filepath.Join(root, name+".yaml"))
	if !referenceWithinBase(root, path) {
		return "", fmt.Errorf("agent config path escapes runtime config directory")
	}
	return path, nil
}

func (s *Server) loadAgentResourceSnippet(name string) (agentResourceDocument, bool) {
	path, err := s.agentResourcePath(name)
	if err != nil {
		return agentResourceDocument{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return agentResourceDocument{}, false
	}
	var parsed map[string]config.AgentProfile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return agentResourceDocument{}, false
	}
	profile, ok := parsed[normalizeResourceName(name)]
	if !ok {
		return agentResourceDocument{}, false
	}
	doc := agentResourceFromProfile(normalizeResourceName(name), profile)
	doc.Path = path
	return doc, true
}

func (s *Server) toolResourcePaths(name string) (string, string, error) {
	name = normalizeResourceName(name)
	codeRoot, err := s.runtimeResourceRoot("mcp_servers")
	if err != nil {
		return "", "", err
	}
	configRoot, err := s.runtimeResourceRoot("configs", "mcp_servers")
	if err != nil {
		return "", "", err
	}
	codePath := filepath.Clean(filepath.Join(codeRoot, name+".py"))
	configPath := filepath.Clean(filepath.Join(configRoot, name+".yaml"))
	if !referenceWithinBase(codeRoot, codePath) {
		return "", "", fmt.Errorf("tool code path escapes runtime tool directory")
	}
	if !referenceWithinBase(configRoot, configPath) {
		return "", "", fmt.Errorf("tool config path escapes runtime config directory")
	}
	return codePath, configPath, nil
}

func (s *Server) policyRuleResourceRoot() (string, error) {
	return s.runtimeResourceRoot("policies", "workflow_rules")
}

func (s *Server) policyRuleResourcePath(name string) (string, error) {
	root, err := s.policyRuleResourceRoot()
	if err != nil {
		return "", err
	}
	name = normalizeResourceName(name)
	path := filepath.Clean(filepath.Join(root, name+".yaml"))
	if !referenceWithinBase(root, path) {
		return "", fmt.Errorf("policy rule path escapes runtime policy directory")
	}
	return path, nil
}

func (s *Server) runtimeResourceRoot(parts ...string) (string, error) {
	if s == nil || s.runtime == nil {
		return "", fmt.Errorf("runtime is not configured")
	}
	root := strings.TrimSpace(s.runtime.RuntimeHome())
	if root == "" {
		return "", fmt.Errorf("runtime home is not configured")
	}
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve runtime home: %w", err)
	}
	items := append([]string{root}, parts...)
	path := filepath.Clean(filepath.Join(items...))
	if !referenceWithinBase(root, path) {
		return "", fmt.Errorf("resource path escapes runtime home")
	}
	return path, nil
}

func toolKindsToStrings(values []config.ToolKind) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			out = append(out, string(value))
		}
	}
	return out
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func normalizeResourceName(name string) string {
	return strings.Trim(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-")), "-")
}

func isYAMLFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

func validResourceValidation(resource, name string, normalized any) resourceValidationResult {
	result := resourceValidationResult{
		Valid:      true,
		Resource:   resource,
		Name:       name,
		Normalized: normalized,
	}
	switch doc := normalized.(type) {
	case agentResourceDocument:
		result.RestartRequired = doc.RestartRequired
		result.ApplyState = doc.ApplyState
		result.ApplyMessage = doc.ApplyMessage
	case providerResourceDocument:
		result.RestartRequired = doc.RestartRequired
		result.ApplyState = doc.ApplyState
		result.ApplyMessage = doc.ApplyMessage
	case toolResourceDocument:
		result.RestartRequired = doc.RestartRequired
		result.ApplyState = doc.ApplyState
		result.ApplyMessage = doc.ApplyMessage
	}
	return result
}

func invalidResourceValidation(resource, name, field, message string) resourceValidationResult {
	issue := resourceValidationIssueFor(resource, field, message)
	return resourceValidationResult{
		Valid:    false,
		Resource: resource,
		Name:     firstResourceValidationName(name),
		Issues:   []resourceValidationIssue{issue},
	}
}

func invalidPolicyRuleValidation(name, field, message string) policyRuleValidationResult {
	issue := resourceValidationIssueFor("policy_rule", field, message)
	return policyRuleValidationResult{
		Valid:  false,
		Name:   firstResourceValidationName(name),
		Issues: []resourceValidationIssue{issue},
	}
}

func resourceValidationIssueFor(resource, field, message string) resourceValidationIssue {
	code, recommendation := resourceValidationCodeAndRecommendation(resource, field, message)
	return resourceValidationIssue{
		Severity:       "error",
		Code:           code,
		Field:          field,
		Message:        message,
		Recommendation: recommendation,
	}
}

func resourceValidationCodeAndRecommendation(resource, field, message string) (string, string) {
	resource = strings.ToLower(strings.TrimSpace(resource))
	field = strings.ToLower(strings.TrimSpace(field))
	lower := strings.ToLower(message)
	switch {
	case resource == "tool" && (strings.Contains(lower, "windows appcontainer") || strings.Contains(lower, "windows_appcontainer") || strings.Contains(lower, "appcontainer")):
		return "mcp_windows_appcontainer_unimplemented", "use isolation: container for the supported enforceable sandbox boundary; AppContainer-like values are intentionally rejected"
	case resource == "tool" && strings.Contains(lower, "isolation_options.network") && strings.Contains(lower, "conflicts with network_disabled"):
		return "mcp_container_network_conflict", "set isolation_options.network to disabled/none when network_disabled is true, or clear network_disabled for an explicitly networked container"
	case resource == "tool" && strings.Contains(lower, "isolation_options"):
		return "invalid_" + strings.ReplaceAll(field, ".", "_"), "fix the MCP isolation option shown by the validation message"
	case resource == "tool" && strings.Contains(lower, "allowed_commands or allowed_command_paths"):
		return "required_allowed_commands", "add allowed_commands for a fixed executable or allowed_command_paths for a controlled directory before enabling this MCP server"
	case resource == "tool" && strings.Contains(lower, "not in allowed_commands"):
		return "mcp_command_not_allowed", "add the exact command to allowed_commands or change command to an allowed executable"
	case resource == "tool" && strings.Contains(lower, "outside allowed_command_paths"):
		return "mcp_command_path_not_allowed", "move the command under an allowed path or add a narrower allowed_command_paths entry"
	case resource == "tool" && strings.Contains(lower, "isolation") && strings.Contains(lower, "invalid"):
		return "mcp_isolation_invalid", "choose one of none, process_group, windows_job, windows_restricted_token, linux_cgroup, linux_netns, or container"
	case strings.Contains(lower, "invalid") && strings.Contains(lower, "json"):
		return "invalid_json", "fix the JSON body and try validation again"
	case strings.Contains(lower, "invalid") && strings.Contains(lower, "name"):
		return "invalid_name", "use lowercase letters, numbers, hyphen, or underscore"
	case strings.Contains(lower, "required"):
		return requiredValidationCode(field), requiredValidationRecommendation(resource, field)
	case strings.Contains(lower, "cannot reference itself"):
		return "self_reference", "choose a different referenced resource or clear this field"
	case strings.Contains(lower, "unknown provider") || strings.Contains(lower, "fallback_provider"):
		return "unknown_provider_reference", "select an existing provider or create the referenced provider module first"
	case strings.Contains(lower, "unsupported") || strings.Contains(lower, "must be one of") || strings.Contains(lower, "unknown node type") || strings.Contains(lower, "unknown helper"):
		return "unsupported_value", unsupportedValidationRecommendation(resource, field)
	case field != "" && (strings.Contains(lower, "invalid") || strings.Contains(lower, "unknown") || strings.Contains(lower, "references")):
		return "invalid_" + strings.ReplaceAll(field, ".", "_"), invalidValueRecommendation(resource, field)
	case strings.Contains(lower, "duration") || strings.Contains(lower, "parse") || strings.Contains(lower, "invalid"):
		return "invalid_value", invalidValueRecommendation(resource, field)
	case strings.Contains(lower, "tool code"):
		return "required_code", "provide the MCP tool source code before validating or saving"
	case strings.Contains(lower, "newer than supported"):
		return "unsupported_version", "lower the resource version or upgrade GoFlow before saving this resource"
	default:
		if field != "" {
			return "invalid_" + strings.ReplaceAll(field, ".", "_"), invalidValueRecommendation(resource, field)
		}
		return "validation_failed", "fix the validation error and try again"
	}
}

func requiredValidationCode(field string) string {
	field = strings.Trim(strings.ReplaceAll(strings.TrimSpace(field), ".", "_"), "_")
	if field == "" {
		return "required_field"
	}
	return "required_" + field
}

func requiredValidationRecommendation(resource, field string) string {
	switch field {
	case "body":
		return "send a non-empty JSON body"
	case "provider":
		if resource == "agent" {
			return "select an existing provider for this agent"
		}
		return "set the provider type, for example openai-compatible"
	case "base_url":
		return "set the provider base_url to the model API endpoint"
	case "model":
		return "set the provider or agent model id"
	case "code":
		return "provide the MCP tool source code"
	case "command":
		return "set the MCP server executable command"
	case "tools":
		return "fill every declared skill tool name or remove empty tool entries"
	case "name", "type":
		return "set a stable resource identifier that matches the URL path"
	default:
		return "fill the required field before validating or saving"
	}
}

func unsupportedValidationRecommendation(resource, field string) string {
	switch field {
	case "mode":
		return "choose one of chat, plan, audit, or fix"
	case "tool_policy":
		return "choose one of allow, deny, or confirm"
	case "allowed_tool_kinds":
		return "choose supported tool kinds: read, write, exec, network, or unknown"
	case "operator":
		return "choose a supported policy operator or scaffold a compatible custom rule"
	case "type":
		return "select an existing workflow node type"
	case "name":
		if resource == "expression_helper" {
			return "select an existing workflow expression helper"
		}
		return "select an existing resource name"
	default:
		return "choose a supported value for this field"
	}
}

func invalidValueRecommendation(resource, field string) string {
	switch field {
	case "timeout", "retry_backoff", "cooldown":
		return "use a Go duration string such as 30s, 2m, or 1h"
	case "max_iterations", "restart_limit", "max_request_bytes", "max_response_bytes":
		return "use a positive integer"
	case "fallback_provider":
		return "select an existing provider or clear the fallback"
	case "fields", "outputs", "args", "params", "defaults":
		return "fix the nested list/object entries shown by the validation message"
	case "allowed_commands":
		return "add the exact MCP server command or switch to allowed_command_paths"
	case "allowed_command_paths":
		return "add a controlled directory that contains the MCP server command"
	case "isolation":
		return "choose a supported MCP isolation mode for the current platform"
	default:
		if resource == "tool" && strings.HasPrefix(field, "isolation_options") {
			return "fix this MCP isolation option according to the validation message"
		}
		return "fix this field according to the validation message"
	}
}

func firstResourceValidationName(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func policyRuleValidationField(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "version"):
		return "version"
	case strings.Contains(lower, "kind"):
		return "kind"
	case strings.Contains(lower, "expression"):
		return "expression"
	case strings.Contains(lower, "operator"):
		return "operator"
	case strings.Contains(lower, "default"):
		return "defaults"
	case strings.Contains(lower, "param"):
		return "params"
	case strings.Contains(lower, "name"):
		return "name"
	default:
		return ""
	}
}

func skillValidationField(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "tool"):
		return "tools"
	case strings.Contains(lower, "name"):
		return "name"
	case strings.Contains(lower, "description"):
		return "description"
	case strings.Contains(lower, "version"):
		return "version"
	case strings.Contains(lower, "author"):
		return "author"
	case strings.Contains(lower, "activation"):
		return "activation"
	case strings.Contains(lower, "keyword"):
		return "activation.keywords"
	case strings.Contains(lower, "frontmatter"):
		return "metadata"
	default:
		return ""
	}
}

func resourceValidationField(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "fallback"):
		return "fallback_provider"
	case strings.Contains(lower, "provider"):
		return "provider"
	case strings.Contains(lower, "base_url"):
		return "base_url"
	case strings.Contains(lower, "model"):
		return "model"
	case strings.Contains(lower, "retry_backoff"):
		return "retry_backoff"
	case strings.Contains(lower, "timeout"):
		return "timeout"
	case strings.Contains(lower, "mode"):
		return "mode"
	case strings.Contains(lower, "tool_policy"):
		return "tool_policy"
	case strings.Contains(lower, "max_iterations"):
		return "max_iterations"
	case strings.Contains(lower, "allowed_tool_kinds"):
		return "allowed_tool_kinds"
	case strings.Contains(lower, "tool code"):
		return "code"
	case strings.Contains(lower, "allowed_commands"):
		return "allowed_commands"
	case strings.Contains(lower, "allowed_command_paths"):
		return "allowed_command_paths"
	case strings.Contains(lower, "isolation_options."):
		return resourceValidationIsolationOptionField(message)
	case strings.Contains(lower, "isolation"):
		return "isolation"
	case strings.Contains(lower, "command"):
		return "command"
	case strings.Contains(lower, "mcp server"):
		return "config"
	case strings.Contains(lower, "name"):
		return "name"
	default:
		return ""
	}
}

func resourceValidationIsolationOptionField(message string) string {
	lower := strings.ToLower(message)
	if index := strings.Index(lower, "isolation_options."); index >= 0 {
		rest := lower[index+len("isolation_options."):]
		end := 0
		for end < len(rest) {
			ch := rest[end]
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' {
				end++
				continue
			}
			break
		}
		if end > 0 {
			return "isolation_options." + rest[:end]
		}
	}
	return "isolation_options"
}

func durationString(value time.Duration) string {
	if value <= 0 {
		return ""
	}
	return value.String()
}

func copyMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func sortedSkillNames(skills []schema.Skill) []string {
	names := make([]string, 0, len(skills))
	for _, item := range skills {
		names = append(names, item.Name)
	}
	sort.Strings(names)
	return names
}
