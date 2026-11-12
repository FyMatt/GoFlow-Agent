package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
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
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/llm"
	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
	"github.com/FyMatt/GoFlow-Agent/internal/scaffold"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
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

// defaultResourceCatalogKinds is the deliberately small user-facing resource
// surface. Provider configuration belongs in Settings, while the remaining
// legacy resource families stay addressable through explicit kind filters for
// runtime and persisted-workflow compatibility.
var defaultResourceCatalogKinds = map[string]struct{}{
	"agent":    {},
	"subagent": {},
	"workflow": {},
	"skill":    {},
	"tool":     {},
	"mcp":      {},
}

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

type providerModelListResult struct {
	Valid    bool                      `json:"valid"`
	Resource string                    `json:"resource"`
	Name     string                    `json:"name,omitempty"`
	Status   string                    `json:"status"`
	Message  string                    `json:"message"`
	Provider string                    `json:"provider,omitempty"`
	BaseURL  string                    `json:"base_url,omitempty"`
	Models   []llm.ModelRef            `json:"models,omitempty"`
	Issues   []resourceValidationIssue `json:"issues,omitempty"`
}

type providerTestResult struct {
	Valid        bool                           `json:"valid"`
	Resource     string                         `json:"resource"`
	Name         string                         `json:"name,omitempty"`
	Status       string                         `json:"status"`
	Message      string                         `json:"message"`
	Provider     string                         `json:"provider,omitempty"`
	Model        string                         `json:"model,omitempty"`
	LatencyMS    int64                          `json:"latency_ms,omitempty"`
	Capabilities []providerCapabilityTestResult `json:"capabilities,omitempty"`
	Issues       []resourceValidationIssue      `json:"issues,omitempty"`
}

type providerCapabilityTestResult struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Supported bool   `json:"supported"`
	Message   string `json:"message,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

type resourceCatalogResponse struct {
	Meta     resourceCatalogMetadata `json:"meta"`
	Counts   resourceCatalogCounts   `json:"counts"`
	Summary  resourceCatalogSummary  `json:"summary"`
	Families []resourceFamilySummary `json:"families"`
	Items    []resourceCatalogItem   `json:"items,omitempty"`
}

type resourceCatalogItem struct {
	Kind             string                        `json:"kind"`
	Name             string                        `json:"name"`
	Title            string                        `json:"title,omitempty"`
	Description      string                        `json:"description,omitempty"`
	Version          string                        `json:"version"`
	DeclaredVersion  string                        `json:"declared_version,omitempty"`
	VersionSource    string                        `json:"version_source"`
	Capabilities     []string                      `json:"capabilities,omitempty"`
	Dependencies     []resourceCatalogDependency   `json:"dependencies,omitempty"`
	Health           string                        `json:"health"`
	HealthDetail     string                        `json:"health_detail,omitempty"`
	ValidationStatus string                        `json:"validation_status"`
	Valid            bool                          `json:"valid"`
	Issues           []resourceValidationIssue     `json:"issues,omitempty"`
	ApplyState       string                        `json:"apply_state,omitempty"`
	DetailPath       string                        `json:"detail_path"`
	UseInTask        resourceCatalogTaskTarget     `json:"use_in_task"`
	DryRunFixture    *resourceCatalogDryRunFixture `json:"dry_run_fixture,omitempty"`
	Builtin          bool                          `json:"builtin,omitempty"`
	Editable         bool                          `json:"editable"`
	ToolCount        int                           `json:"tool_count,omitempty"`
}

type resourceCatalogDependency struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

type resourceCatalogTaskTarget struct {
	Route   string `json:"route"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
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

// Kit resource catalog entries expose the scaffold materialize action; the Kit
// endpoint returns resources, restart_required, and activation_steps for the
// generated resource graph.

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
	Kind             string   `json:"kind" yaml:"kind,omitempty"`
	TargetScope      string   `json:"target_scope,omitempty" yaml:"target_scope,omitempty"`
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
	ID                    string   `json:"id" yaml:"id,omitempty"`
	Type                  string   `json:"type,omitempty" yaml:"-"`
	Provider              string   `json:"provider" yaml:"provider"`
	BaseURL               string   `json:"base_url" yaml:"base_url"`
	WireAPI               string   `json:"wire_api,omitempty" yaml:"wire_api,omitempty"`
	APIKey                string   `json:"api_key,omitempty" yaml:"api_key,omitempty"`
	EnvKey                string   `json:"env_key,omitempty" yaml:"-"`
	APIKeySet             bool     `json:"api_key_set,omitempty" yaml:"-"`
	Model                 string   `json:"model" yaml:"model"`
	DefaultModel          string   `json:"default_model,omitempty" yaml:"-"`
	FallbackProvider      string   `json:"fallback_provider,omitempty" yaml:"fallback_provider,omitempty"`
	ProviderMessageFields []string `json:"provider_message_fields,omitempty" yaml:"provider_message_fields,omitempty"`
	Vision                bool     `json:"vision,omitempty" yaml:"vision,omitempty"`
	Timeout               string   `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Temperature           float64  `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	MaxTokens             int      `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	ReasoningEffort       string   `json:"reasoning_effort,omitempty" yaml:"reasoning_effort,omitempty"`
	RetryCount            int      `json:"retry_count,omitempty" yaml:"retry_count,omitempty"`
	RetryBackoff          string   `json:"retry_backoff,omitempty" yaml:"retry_backoff,omitempty"`
	Path                  string   `json:"path,omitempty" yaml:"-"`
	RestartRequired       bool     `json:"restart_required,omitempty" yaml:"-"`
	ApplyState            string   `json:"apply_state,omitempty" yaml:"-"`
	ApplyMessage          string   `json:"apply_message,omitempty" yaml:"-"`
}

type toolResourceDocument struct {
	Name                string                  `json:"name" yaml:"name"`
	TargetScope         string                  `json:"target_scope,omitempty" yaml:"target_scope,omitempty"`
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
	TargetScope         string            `yaml:"target_scope,omitempty"`
	Command             string            `yaml:"command"`
	Args                []string          `yaml:"args"`
	Enabled             bool              `yaml:"enabled"`
	Timeout             string            `yaml:"timeout"`
	WorkDir             string            `yaml:"workdir"`
	EnvAllowlist        []string          `yaml:"env_allowlist,omitempty"`
	NetworkDisabled     bool              `yaml:"network_disabled,omitempty"`
	Isolation           string            `yaml:"isolation,omitempty"`
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
	if len(allowedKinds) == 0 {
		allowedKinds = cloneResourceCatalogKinds(defaultResourceCatalogKinds)
	}
	searchText := resourceCatalogSearchText(query)
	capability := resourceCatalogQueryValue(query, "capability")
	health := resourceCatalogQueryValue(query, "health")
	validation := resourceCatalogQueryValue(query, "validation")
	filterKey := resourceCatalogFilterKey(allowedKinds, searchText, capability, health, validation)
	families := s.resourceFamilySummaries(allowedKinds, searchText)
	items := s.resourceCatalogItems(allowedKinds)
	items = filterResourceCatalogItems(items, searchText, capability, health, validation)
	applyVisibleResourceCatalogCounts(families, items)
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
				"默认资源库展示智能体、子智能体、工作流、技能、单个 MCP 工具和 MCP 连接；模型供应商在设置页单独管理。",
				"兼容性资源类型仍可通过显式的 kind 查询参数访问。",
				"资源库总览为只读目录；创建、修改、删除和验证请使用对应资源类型接口。",
				"智能体、子智能体、技能和 MCP 连接支持运行时热加载；供应商配置仍需重启后绑定新的模型客户端。",
			},
		},
		Families: families,
		Items:    items,
	}
}

func applyVisibleResourceCatalogCounts(families []resourceFamilySummary, items []resourceCatalogItem) {
	counts := make(map[string]int)
	invalid := make(map[string]int)
	for _, item := range items {
		kind := strings.TrimSpace(item.Kind)
		if kind == "" {
			continue
		}
		counts[kind]++
		if !item.Valid {
			invalid[kind]++
		}
	}
	for index := range families {
		kind := strings.TrimSpace(families[index].Kind)
		families[index].Count = counts[kind]
		families[index].InvalidCount = invalid[kind]
	}
}

func cloneResourceCatalogKinds(source map[string]struct{}) map[string]struct{} {
	clone := make(map[string]struct{}, len(source))
	for kind := range source {
		clone[kind] = struct{}{}
	}
	return clone
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

func resourceCatalogQueryValue(query url.Values, key string) string {
	if query == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(query.Get(key)))
}

func resourceCatalogFilterKey(allowed map[string]struct{}, filters ...string) string {
	kinds := sortedResourceCatalogKeys(allowed)
	return strings.Join(append([]string{strings.Join(kinds, ",")}, filters...), "|")
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

func (s *Server) resourceCatalogItems(allowed map[string]struct{}) []resourceCatalogItem {
	if s == nil || s.runtime == nil {
		return nil
	}
	items := make([]resourceCatalogItem, 0)
	if resourceCatalogKindAllowed(allowed, "agent") {
		for _, doc := range s.agentResourceListByKind(config.AgentKindAgent) {
			items = append(items, s.agentCatalogItem("agent", doc))
		}
	}
	if resourceCatalogKindAllowed(allowed, "subagent") {
		for _, doc := range s.agentResourceListByKind(config.AgentKindSubagent) {
			items = append(items, s.agentCatalogItem("subagent", doc))
		}
	}
	if resourceCatalogKindAllowed(allowed, "workflow") {
		runner := s.runtime.WorkflowRunner()
		titles := make(map[string]string)
		for _, template := range runner.WorkflowTemplates() {
			titles[template.Name] = template.Title
		}
		for _, summary := range runner.ListWorkflowGraphs() {
			items = append(items, s.workflowCatalogItem(summary, titles[summary.Name]))
		}
	}
	if resourceCatalogKindAllowed(allowed, "skill") {
		for _, item := range s.runtime.SkillList() {
			items = append(items, s.skillCatalogItem(item))
		}
	}
	if resourceCatalogKindAllowed(allowed, "tool") {
		if tools, err := s.runtime.Tools(context.Background()); err == nil {
			for _, tool := range tools {
				items = append(items, s.toolCatalogItem(tool))
			}
		}
	}
	if resourceCatalogKindAllowed(allowed, "mcp") {
		health := s.runtime.MCPHealthStatus(context.Background())
		for _, doc := range s.mcpResourceList() {
			if !userVisibleMCPResource(doc.Name) {
				continue
			}
			items = append(items, s.mcpCatalogItem(doc, health))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items
}

func userVisibleMCPResource(name string) bool {
	switch normalizeResourceName(name) {
	case "python_notes", "skill_runner":
		return false
	default:
		return true
	}
}

func resourceCatalogKindAllowed(allowed map[string]struct{}, kind string) bool {
	if len(allowed) == 0 {
		return true
	}
	_, ok := allowed[kind]
	return ok
}

func (s *Server) agentCatalogItem(kind string, doc agentResourceDocument) resourceCatalogItem {
	publicDoc := comparableAgentResource(doc)
	version := session.ResourceVersion(publicDoc)
	valid := true
	var issues []resourceValidationIssue
	if _, err := s.validateAgentResourceDryRun(doc.ID, doc); err != nil {
		valid = false
		issues = append(issues, resourceValidationIssue{Severity: "error", Message: err.Error(), Recommendation: "Open the resource and correct the highlighted configuration."})
	}
	dependencies := make([]resourceCatalogDependency, 0, 1)
	if provider := strings.TrimSpace(doc.Provider); provider != "" {
		status, detail := s.providerDependencyStatus(provider)
		dependencies = append(dependencies, resourceCatalogDependency{Kind: "provider", Name: provider, Required: true, Status: status, Detail: detail})
	}
	issues = append(issues, resourceCatalogDependencyIssues(dependencies)...)
	capabilities := append([]string{kind}, doc.AllowedToolKinds...)
	if mode := strings.TrimSpace(doc.Mode); mode != "" {
		capabilities = append(capabilities, mode)
	}
	health, healthDetail := catalogHealthFromDependencies(valid, dependencies)
	applyState := doc.ApplyState
	if _, loaded := s.runtime.Profile(doc.ID); loaded && valid && health == "healthy" {
		applyState = "active"
	}
	return newResourceCatalogItem(resourceCatalogItem{
		Kind:             kind,
		Name:             doc.ID,
		Title:            doc.Name,
		Description:      doc.Description,
		Version:          version,
		VersionSource:    "content_sha256",
		Capabilities:     capabilities,
		Dependencies:     dependencies,
		Health:           health,
		HealthDetail:     healthDetail,
		ValidationStatus: catalogValidationStatus(valid),
		Valid:            valid,
		Issues:           issues,
		ApplyState:       applyState,
		DetailPath:       "/api/resources/" + kind + "s/" + url.PathEscape(doc.ID),
	})
}

func (s *Server) workflowCatalogItem(summary agent.WorkflowGraphSummary, templateTitle ...string) resourceCatalogItem {
	runner := s.runtime.WorkflowRunner()
	title := ""
	if len(templateTitle) > 0 {
		title = strings.TrimSpace(templateTitle[0])
	} else if template, ok := runner.WorkflowTemplate(summary.Name); ok {
		title = strings.TrimSpace(template.Title)
	}
	if title == "" {
		title = resourceCatalogFriendlyTitle("workflow", summary.Name, summary.Name)
	}
	doc, err := runner.LoadWorkflowGraphDocument(summary.Name)
	valid := summary.Valid && err == nil
	var issues []resourceValidationIssue
	if !valid {
		message := strings.TrimSpace(summary.Error)
		if message == "" && err != nil {
			message = err.Error()
		}
		if message == "" {
			message = "workflow validation failed"
		}
		issues = append(issues, resourceValidationIssue{Severity: "error", Message: message, Recommendation: "Open the workflow designer and repair invalid nodes or dependencies."})
	}
	versionValue := any(summary)
	capabilities := []string{"workflow"}
	var dependencies []resourceCatalogDependency
	if err == nil {
		versionValue = doc
		if len(doc.Stages) > 1 {
			capabilities = append(capabilities, "multi_stage")
		}
		for _, stage := range doc.Stages {
			nodeType := strings.ToLower(strings.TrimSpace(stage.NodeType))
			if nodeType == "" {
				nodeType = "agent"
			}
			capabilities = append(capabilities, nodeType)
			if stage.Approval || stage.Execution.RequiresApproval {
				capabilities = append(capabilities, "approval")
			}
		}
		for _, requirement := range runner.WorkflowGraphStageRequirements(doc) {
			if requirement.Agent != "" {
				status := "ready"
				if _, ok := s.runtime.Profile(requirement.Agent); !ok {
					status = "missing"
				}
				dependencies = append(dependencies, resourceCatalogDependency{Kind: "agent", Name: requirement.Agent, Required: true, Status: status})
			}
			if requirement.Provider != "" {
				status, detail := s.providerDependencyStatus(requirement.Provider)
				dependencies = append(dependencies, resourceCatalogDependency{Kind: "provider", Name: requirement.Provider, Required: true, Status: status, Detail: detail})
			}
			if requirement.Skill != "" {
				status := "ready"
				if _, ok := s.runtimeSkillByName(requirement.Skill); !ok {
					status = "missing"
				}
				dependencies = append(dependencies, resourceCatalogDependency{Kind: "skill", Name: requirement.Skill, Required: true, Status: status})
			}
			for _, tool := range requirement.Tools {
				dependencies = append(dependencies, resourceCatalogDependency{Kind: "tool", Name: tool, Required: true, Status: s.toolDependencyStatus(tool)})
			}
			if requirement.SubWorkflow != "" {
				status := "ready"
				if _, err := runner.LoadWorkflowGraphDocument(requirement.SubWorkflow); err != nil {
					status = "missing"
				}
				dependencies = append(dependencies, resourceCatalogDependency{Kind: "workflow", Name: requirement.SubWorkflow, Required: true, Status: status})
			}
		}
	}
	dependencies = normalizeResourceCatalogDependencies(dependencies)
	issues = append(issues, resourceCatalogDependencyIssues(dependencies)...)
	health, healthDetail := catalogHealthFromDependencies(valid, dependencies)
	return newResourceCatalogItem(resourceCatalogItem{
		Kind:             "workflow",
		Name:             summary.Name,
		Title:            title,
		Description:      summary.Description,
		Version:          session.ResourceVersion(versionValue),
		VersionSource:    "content_sha256",
		Capabilities:     capabilities,
		Dependencies:     dependencies,
		Health:           health,
		HealthDetail:     healthDetail,
		ValidationStatus: catalogValidationStatus(valid),
		Valid:            valid,
		Issues:           issues,
		ApplyState:       "active",
		DetailPath:       "/api/workflow-graphs/" + url.PathEscape(summary.Name),
		DryRunFixture:    referenceWorkflowDryRunFixture(summary.Name),
	})
}

func (s *Server) skillCatalogItem(item schema.Skill) resourceCatalogItem {
	title := firstWorkflowRunQueryValue(item.Metadata["title"], item.Metadata["display_name"])
	if title == "" {
		title = resourceCatalogFriendlyTitle("skill", item.Name, item.Name)
	}
	publicDoc := skillResourceFromSchema(item)
	publicDoc.Path = ""
	dependencies := make([]resourceCatalogDependency, 0, len(item.Tools)+len(item.NextSkills)+1)
	if name := strings.TrimSpace(item.PreferredAgent); name != "" {
		status := "ready"
		if _, ok := s.runtime.Profile(name); !ok {
			status = "missing"
		}
		dependencies = append(dependencies, resourceCatalogDependency{Kind: "agent", Name: name, Required: true, Status: status})
	}
	for _, tool := range item.Tools {
		dependencies = append(dependencies, resourceCatalogDependency{Kind: "tool", Name: tool.Name, Required: tool.Required, Status: s.toolDependencyStatus(tool.Name)})
	}
	for _, next := range item.NextSkills {
		status := "ready"
		if _, ok := s.runtimeSkillByName(next); !ok {
			status = "missing"
		}
		dependencies = append(dependencies, resourceCatalogDependency{Kind: "skill", Name: next, Required: false, Status: status})
	}
	dependencies = normalizeResourceCatalogDependencies(dependencies)
	issues := resourceCatalogDependencyIssues(dependencies)
	capabilities := append([]string{"skill"}, item.AllowedToolKinds...)
	if item.OutputKind != "" {
		capabilities = append(capabilities, item.OutputKind)
	}
	if item.Mode != "" {
		capabilities = append(capabilities, item.Mode)
	}
	health, healthDetail := catalogHealthFromDependencies(true, dependencies)
	return newResourceCatalogItem(resourceCatalogItem{
		Kind:             "skill",
		Name:             item.Name,
		Title:            title,
		Description:      item.Description,
		Version:          session.ResourceVersion(publicDoc),
		DeclaredVersion:  item.Version,
		VersionSource:    "content_sha256",
		Capabilities:     capabilities,
		Dependencies:     dependencies,
		Health:           health,
		HealthDetail:     healthDetail,
		ValidationStatus: "valid",
		Valid:            true,
		Issues:           issues,
		ApplyState:       "active",
		DetailPath:       "/api/resources/skills/" + url.PathEscape(item.Name),
	})
}

func (s *Server) mcpCatalogItem(doc mcpResourceDocument, health map[string]string) resourceCatalogItem {
	publicDoc := doc
	publicDoc.Path = ""
	publicDoc.RestartRequired = false
	publicDoc.ApplyState = ""
	publicDoc.ApplyMessage = ""
	publicDoc.Builtin = false
	publicDoc.ReadOnly = false
	publicDoc.ToolCount = 0
	valid := strings.TrimSpace(doc.Name) != "" && strings.TrimSpace(doc.Command) != ""
	var issues []resourceValidationIssue
	if !valid {
		issues = append(issues, resourceValidationIssue{Severity: "error", Field: "command", Message: "MCP connection requires a name and command.", Recommendation: "Open the resource and configure the local MCP command."})
	}
	rawHealth := resourceCatalogMCPHealth(health, doc.Name)
	normalizedHealth := normalizeResourceCatalogMCPHealth(doc.Enabled, rawHealth)
	if !valid {
		normalizedHealth = "error"
	}
	dependencies := []resourceCatalogDependency{{Kind: "runtime", Name: doc.Command, Required: true, Status: resourceCatalogRuntimeDependencyStatus(normalizedHealth), Detail: rawHealth}}
	issues = append(issues, resourceCatalogDependencyIssues(dependencies)...)
	applyState := doc.ApplyState
	if valid && normalizedHealth == "healthy" {
		applyState = "active"
	}
	return newResourceCatalogItem(resourceCatalogItem{
		Kind:             "mcp",
		Name:             doc.Name,
		Title:            resourceCatalogFriendlyTitle("mcp", doc.Name, doc.Name),
		Description:      resourceCatalogMCPDescription(doc.Name, doc.Command, doc.Args),
		Version:          session.ResourceVersion(publicDoc),
		VersionSource:    "content_sha256",
		Capabilities:     []string{"mcp", "tool_connection", "stdio"},
		Dependencies:     dependencies,
		Health:           normalizedHealth,
		HealthDetail:     rawHealth,
		ValidationStatus: catalogValidationStatus(valid),
		Valid:            valid,
		Issues:           issues,
		ApplyState:       applyState,
		DetailPath:       "/api/resources/mcp/" + url.PathEscape(doc.Name),
		Builtin:          doc.Builtin,
		Editable:         !doc.ReadOnly,
		ToolCount:        doc.ToolCount,
	})
}

func (s *Server) toolCatalogItem(tool schema.Tool) resourceCatalogItem {
	name := qualifiedToolName(tool)
	server := strings.TrimSpace(tool.Server)
	dependencies := make([]resourceCatalogDependency, 0, 1)
	if server != "" {
		dependencies = append(dependencies, resourceCatalogDependency{Kind: "mcp", Name: server, Required: true, Status: s.toolDependencyStatus(name)})
	}
	health, healthDetail := catalogHealthFromDependencies(strings.TrimSpace(name) != "", dependencies)
	item := newResourceCatalogItem(resourceCatalogItem{
		Kind:             "tool",
		Name:             name,
		Title:            strings.TrimSpace(tool.Name),
		Description:      strings.TrimSpace(tool.Description),
		Version:          session.ResourceVersion(tool),
		VersionSource:    "content_sha256",
		Capabilities:     []string{"tool", strings.TrimSpace(tool.Kind), server},
		Dependencies:     dependencies,
		Health:           health,
		HealthDetail:     healthDetail,
		ValidationStatus: catalogValidationStatus(strings.TrimSpace(name) != ""),
		Valid:            strings.TrimSpace(name) != "",
		ApplyState:       "active",
		DetailPath:       "/api/resources/tools/" + url.PathEscape(name),
		Editable:         false,
	})
	item.UseInTask = resourceCatalogTaskTarget{Route: "#workflows", Kind: "tool", Name: name, Version: item.Version}
	return item
}

func resourceCatalogFriendlyTitle(kind, name, fallback string) string {
	key := strings.ToLower(strings.TrimSpace(kind)) + ":" + strings.ToLower(strings.TrimSpace(name))
	titles := map[string]string{
		"mcp:command_tools": "命令执行连接", "mcp:file_tools": "文件操作连接", "mcp:host_tools": "本地主机连接",
		"mcp:network_tools": "网络诊断连接", "mcp:office_tools": "Office 文档连接", "mcp:ssh_tools": "SSH 远程连接", "mcp:web_tools": "网页与研究连接",
		"workflow:plan-fix-audit": "规划、修复与审计", "workflow:plan-implement-audit": "规划、实现与审计",
		"workflow:skill-chain": "专业技能路由", "workflow:task-decomposition-plan": "复杂任务拆解规划",
		"skill:authorized-pentest-helper": "授权渗透辅助", "skill:authorized-red-team-validation": "授权红队验证",
		"skill:binary-vulnerability-research": "二进制漏洞研究", "skill:code-audit": "代码审计", "skill:code-writing": "代码实现",
		"skill:controlled-operations": "受控运维变更", "skill:crypto-learning": "密码学学习", "skill:execution-plan": "执行规划",
		"skill:internet-research": "互联网研究", "skill:local-host-diagnostics": "本地主机诊断", "skill:managed-local-config": "受管本地配置",
		"skill:office-document-production": "Office 文档生产", "skill:ops-study": "运维学习", "skill:remote-host-diagnostics": "远程主机诊断",
		"skill:reverse-engineering": "逆向分析", "skill:source-verification": "来源核验", "skill:vulnerability-research": "漏洞研究",
		"skill:web-vulnerability-research": "Web 漏洞研究",
	}
	if title := strings.TrimSpace(titles[key]); title != "" {
		return title
	}
	return strings.TrimSpace(fallback)
}

func resourceCatalogMCPDescription(name, command string, args []string) string {
	descriptions := map[string]string{
		"command_tools": "在任务工作区内运行结构化本地命令，用于测试、构建、检查和受控项目解释器。",
		"file_tools":    "读取、检索、精确修改和恢复任务工作区文件，并保留可审计 Diff。",
		"host_tools":    "读取本地主机、磁盘、端口、进程、服务和受管配置状态。",
		"network_tools": "执行受限 DNS、TCP、HTTP 和网络诊断，不提供任意网络扫描。",
		"office_tools":  "生成、检查和预览 Word、Excel、PowerPoint 与 PDF 交付物。",
		"ssh_tools":     "管理授权 SSH 目标的主机密钥，并执行精确白名单命令。",
		"web_tools":     "搜索、抓取、快照和分析网页、静态资源与研究来源。",
	}
	if description := descriptions[strings.ToLower(strings.TrimSpace(name))]; description != "" {
		return description
	}
	return strings.TrimSpace(strings.Join(append([]string{command}, args...), " "))
}

func newResourceCatalogItem(item resourceCatalogItem) resourceCatalogItem {
	item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
	item.Name = strings.TrimSpace(item.Name)
	item.Title = strings.TrimSpace(item.Title)
	if item.Title == "" {
		item.Title = item.Name
	}
	item.Capabilities = uniqueSortedNonEmpty(item.Capabilities)
	item.Dependencies = normalizeResourceCatalogDependencies(item.Dependencies)
	if item.Kind != "mcp" && item.Kind != "tool" {
		item.Editable = true
	}
	item.UseInTask = resourceCatalogTaskTarget{Route: "#playground", Kind: item.Kind, Name: item.Name, Version: item.Version}
	return item
}

func (s *Server) providerDependencyStatus(name string) (string, string) {
	if _, ok := s.runtime.Provider(name); !ok {
		return "missing", "Provider is not configured."
	}
	if !s.runtime.ProviderAvailable(name) {
		return "degraded", "Provider is configured but its client is not currently available."
	}
	return "ready", ""
}

func (s *Server) toolDependencyStatus(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "missing"
	}
	for _, candidate := range s.runtime.ToolNames() {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == name || (!strings.Contains(name, "/") && strings.HasSuffix(candidate, "/"+name)) {
			return "ready"
		}
	}
	serverName, _, qualified := strings.Cut(name, "/")
	if !qualified || strings.TrimSpace(serverName) == "" || s == nil || s.runtime == nil {
		return "missing"
	}
	for _, server := range s.runtime.MCPServerRefs() {
		if !strings.EqualFold(strings.TrimSpace(server.Name), serverName) {
			continue
		}
		if !server.Enabled {
			return "disabled"
		}
		// MCP servers are started and their tools discovered on demand. An
		// enabled connection satisfies the catalog dependency until discovery
		// can confirm the exact tool without making the Resources page eager.
		return "deferred"
	}
	return "missing"
}

func catalogHealthFromDependencies(valid bool, dependencies []resourceCatalogDependency) (string, string) {
	if !valid {
		return "error", "Resource validation failed."
	}
	optionalMissing := false
	for _, dependency := range dependencies {
		status := strings.ToLower(strings.TrimSpace(dependency.Status))
		if dependency.Required && (status == "missing" || status == "error" || status == "disabled") {
			return "error", "One or more required dependencies are unavailable."
		}
		if status == "degraded" || status == "unknown" || status == "missing" || status == "disabled" {
			optionalMissing = true
		}
	}
	if optionalMissing {
		return "degraded", "One or more dependencies need attention."
	}
	return "healthy", ""
}

func catalogValidationStatus(valid bool) string {
	if valid {
		return "valid"
	}
	return "invalid"
}

func resourceCatalogDependencyIssues(items []resourceCatalogDependency) []resourceValidationIssue {
	issues := make([]resourceValidationIssue, 0)
	for _, dependency := range normalizeResourceCatalogDependencies(items) {
		status := strings.ToLower(strings.TrimSpace(dependency.Status))
		if status == "ready" || status == "healthy" || status == "deferred" || status == "" {
			continue
		}
		severity := "warning"
		if dependency.Required && (status == "missing" || status == "error" || status == "disabled") {
			severity = "error"
		}
		issues = append(issues, resourceValidationIssue{
			Severity:       severity,
			Code:           "dependency_" + status,
			Field:          "dependencies." + dependency.Kind + "." + dependency.Name,
			Message:        fmt.Sprintf("%s dependency %s is %s", dependency.Kind, dependency.Name, status),
			Recommendation: resourceCatalogDependencyRecommendation(dependency),
		})
	}
	return issues
}

func resourceCatalogDependencyRecommendation(dependency resourceCatalogDependency) string {
	kind := strings.ToLower(strings.TrimSpace(dependency.Kind))
	name := strings.TrimSpace(dependency.Name)
	switch kind {
	case "provider":
		return fmt.Sprintf("Open Settings, configure and test Provider %s, then refresh Resources.", name)
	case "agent":
		return fmt.Sprintf("Restore Agent %s or select an available Agent in the Workflow stage.", name)
	case "skill":
		return fmt.Sprintf("Restore Skill %s and validate its SKILL.md dependencies.", name)
	case "tool", "mcp":
		return fmt.Sprintf("Enable the MCP connection that provides %s, repair its command if needed, then refresh tool discovery.", name)
	case "workflow":
		return fmt.Sprintf("Restore or repair referenced Workflow %s before running this Workflow.", name)
	case "runtime":
		return fmt.Sprintf("Install or repair command %s, then restart or refresh its MCP connection.", name)
	default:
		return fmt.Sprintf("Repair %s dependency %s and refresh Resources.", kind, name)
	}
}

func normalizeResourceCatalogDependencies(items []resourceCatalogDependency) []resourceCatalogDependency {
	if len(items) == 0 {
		return nil
	}
	byKey := make(map[string]resourceCatalogDependency, len(items))
	for _, item := range items {
		item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
		item.Name = strings.TrimSpace(item.Name)
		item.Status = strings.ToLower(strings.TrimSpace(item.Status))
		if item.Kind == "" || item.Name == "" {
			continue
		}
		key := item.Kind + "\x00" + strings.ToLower(item.Name)
		if existing, ok := byKey[key]; !ok || (!existing.Required && item.Required) {
			byKey[key] = item
		}
	}
	out := make([]resourceCatalogDependency, 0, len(byKey))
	for _, item := range byKey {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func resourceCatalogMCPHealth(health map[string]string, name string) string {
	for candidate, status := range health {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(name)) {
			return strings.TrimSpace(status)
		}
	}
	return "unknown"
}

func normalizeResourceCatalogMCPHealth(enabled bool, raw string) string {
	if !enabled {
		return "disabled"
	}
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.HasPrefix(value, "ready"), strings.HasPrefix(value, "healthy"), strings.HasPrefix(value, "idle"):
		return "healthy"
	case strings.HasPrefix(value, "degraded"), strings.Contains(value, "cooldown"):
		return "degraded"
	case value == "disabled":
		return "disabled"
	case value == "", value == "unknown", value == "missing":
		return "unknown"
	default:
		return "error"
	}
}

func resourceCatalogRuntimeDependencyStatus(health string) string {
	switch health {
	case "healthy":
		return "ready"
	case "disabled":
		return "disabled"
	case "error":
		return "error"
	default:
		return health
	}
}

func filterResourceCatalogItems(items []resourceCatalogItem, searchText, capability, health, validation string) []resourceCatalogItem {
	out := make([]resourceCatalogItem, 0, len(items))
	for _, item := range items {
		if searchText != "" && !resourceCatalogItemMatchesQuery(item, searchText) {
			continue
		}
		if capability != "" && !resourceCatalogStringSliceContains(item.Capabilities, capability) {
			continue
		}
		if health != "" && !strings.EqualFold(item.Health, health) {
			continue
		}
		if validation != "" && !strings.EqualFold(item.ValidationStatus, validation) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func resourceCatalogItemMatchesQuery(item resourceCatalogItem, searchText string) bool {
	haystacks := []string{item.Kind, item.Name, item.Title, item.Description, item.Version, item.DeclaredVersion, item.Health, item.HealthDetail, item.ValidationStatus, item.ApplyState, strings.Join(item.Capabilities, " ")}
	for _, dependency := range item.Dependencies {
		haystacks = append(haystacks, dependency.Kind, dependency.Name, dependency.Status, dependency.Detail)
	}
	for _, issue := range item.Issues {
		haystacks = append(haystacks, issue.Message, issue.Recommendation)
	}
	for _, value := range haystacks {
		if strings.Contains(strings.ToLower(value), searchText) {
			return true
		}
	}
	return false
}

func resourceCatalogStringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(expected)) {
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
		return len(s.agentResourceListByKind(config.AgentKindAgent)), 0
	case "subagent":
		return len(s.agentResourceListByKind(config.AgentKindSubagent)), 0
	case "provider":
		return len(s.providerResourceList()), 0
	case "tool":
		return len(s.toolResourceList()), 0
	case "mcp":
		return len(s.mcpResourceList()), 0
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
			writeJSONStatus(w, resourceErrorStatus(err, http.StatusBadRequest), invalidResourceValidation("skill", firstResourceValidationName(name, doc.Name), skillValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.deleteSkillResource(name); err != nil {
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
			return
		}
		writeJSON(w, files)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, err := s.skillFileResource(name, rel, true)
		if err != nil {
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
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
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.deleteSkillFileResource(name, rel); err != nil {
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
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
	writeJSON(w, s.agentResourceListByKind(config.AgentKindAgent))
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
		if !ok || doc.Kind != config.AgentKindAgent {
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
		doc.Kind = config.AgentKindAgent
		saved, err := s.saveAgentResource(name, doc)
		if err != nil {
			writeJSONStatus(w, resourceErrorStatus(err, http.StatusBadRequest), invalidResourceValidation("agent", firstResourceValidationName(name, doc.ID), resourceValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		doc, ok := s.agentResourceByName(name)
		if !ok || doc.Kind != config.AgentKindAgent {
			http.NotFound(w, r)
			return
		}
		if err := s.deleteAgentResource(name); err != nil {
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSubagentResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/subagents/validate" {
		s.handleSubagentResourceValidate(w, r, "")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.agentResourceListByKind(config.AgentKindSubagent))
}

func (s *Server) handleSubagentResourceItem(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/subagents/"), "/")
	parts := strings.Split(name, "/")
	if len(parts) == 1 && parts[0] == "import" {
		s.handleSubagentImport(w, r)
		return
	}
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handleSubagentResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		s.handleSubagentResourceValidate(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "export" {
		s.handleSubagentExport(w, r, parts[0])
		return
	}
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, ok := s.agentResourceByName(name)
		if !ok || doc.Kind != config.AgentKindSubagent {
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
		doc.Kind = config.AgentKindSubagent
		saved, err := s.saveAgentResource(name, doc)
		if err != nil {
			writeJSONStatus(w, resourceErrorStatus(err, http.StatusBadRequest), invalidResourceValidation("subagent", firstResourceValidationName(name, doc.ID), resourceValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		doc, ok := s.agentResourceByName(name)
		if !ok || doc.Kind != config.AgentKindSubagent {
			http.NotFound(w, r)
			return
		}
		if err := s.deleteAgentResource(name); err != nil {
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSubagentResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var doc agentResourceDocument
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, invalidResourceValidation("subagent", name, "body", "invalid json"))
		return
	}
	doc.Kind = config.AgentKindSubagent
	normalized, err := s.validateAgentResourceDryRun(name, doc)
	if err != nil {
		writeJSON(w, invalidResourceValidation("subagent", firstResourceValidationName(name, doc.ID), resourceValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, validResourceValidation("subagent", normalized.ID, normalized))
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
	doc.Kind = config.AgentKindAgent
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
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/providers/models" {
		s.handleProviderResourceModels(w, r, "")
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
	if len(parts) == 2 && parts[1] == "models" && r.Method == http.MethodPost {
		s.handleProviderResourceModels(w, r, parts[0])
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
			writeJSONStatus(w, resourceErrorStatus(err, http.StatusBadRequest), invalidResourceValidation("provider", firstResourceValidationName(name, doc.ID), resourceValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.deleteProviderResource(name); err != nil {
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
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
	if s.runtime != nil {
		resolved, resolveErr := config.ResolveSecretReference(s.runtime.DataRoot(), normalized.APIKey)
		if resolveErr != nil {
			writeJSON(w, providerTestInvalid(normalized.ID, "api_key", resolveErr.Error()))
			return
		}
		normalized.APIKey = resolved
	}
	if s.runtime != nil && s.runtime.LocalOnly() && !llm.IsLocalBaseURL(providerResourceLLMConfig(normalized).BaseURL) {
		writeJSON(w, providerTestInvalid(normalized.ID, "base_url", "local-only workspace blocks non-local model providers"))
		return
	}
	result := testProviderResourceConnection(r.Context(), normalized)
	writeJSON(w, result)
}

func (s *Server) handleProviderResourceModels(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var doc providerResourceDocument
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, providerModelListInvalid(name, "body", "invalid json"))
		return
	}
	if strings.TrimSpace(doc.ID) == "" {
		doc.ID = name
	}
	if strings.TrimSpace(doc.APIKey) == "" {
		if existing, ok := s.loadProviderResourceSnippet(firstResourceValidationName(name, doc.ID)); ok {
			doc.APIKey = existing.APIKey
		}
	}
	applyProviderResourceDefaults(&doc)
	cfg := providerResourceLLMConfig(doc)
	if strings.TrimSpace(cfg.BaseURL) == "" {
		writeJSON(w, providerModelListInvalid(firstResourceValidationName(name, doc.ID), "base_url", "model provider setup required: configure base_url in Web Studio Settings/Resources or configs/providers/*.yaml"))
		return
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		writeJSON(w, providerModelListInvalid(firstResourceValidationName(name, doc.ID), "api_key", "model provider setup required: configure api_key in Web Studio Settings/Resources or configs/providers/*.yaml"))
		return
	}
	if s.runtime != nil {
		resolved, resolveErr := config.ResolveSecretReference(s.runtime.DataRoot(), cfg.APIKey)
		if resolveErr != nil {
			writeJSON(w, providerModelListInvalid(doc.ID, "api_key", resolveErr.Error()))
			return
		}
		cfg.APIKey = resolved
	}
	if s.runtime != nil && s.runtime.LocalOnly() && !llm.IsLocalBaseURL(cfg.BaseURL) {
		writeJSON(w, providerModelListInvalid(doc.ID, "base_url", "local-only workspace blocks non-local model providers"))
		return
	}
	cfg.Timeout = 20 * time.Second
	cfg.RetryCount = 0
	client := llm.NewClient(cfg)
	models, err := client.ListModels(r.Context())
	if err != nil {
		writeJSON(w, providerModelListInvalid(firstResourceValidationName(name, doc.ID), providerTestErrorField(err.Error()), err.Error()))
		return
	}
	if len(models) == 0 {
		writeJSON(w, providerModelListInvalid(firstResourceValidationName(name, doc.ID), "model", "provider did not return a model list"))
		return
	}
	writeJSON(w, providerModelListResult{
		Valid:    true,
		Resource: "provider",
		Name:     firstResourceValidationName(name, doc.ID),
		Status:   "ok",
		Message:  "model catalog loaded",
		Provider: cfg.Provider,
		BaseURL:  cfg.BaseURL,
		Models:   models,
	})
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
			writeJSONStatus(w, resourceErrorStatus(err, http.StatusBadRequest), invalidResourceValidation("tool", firstResourceValidationName(name, doc.Name), resourceValidationField(err.Error()), err.Error()))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if strings.Contains(name, "/") {
			http.Error(w, "delete supports file-backed custom tool modules only", http.StatusBadRequest)
			return
		}
		if err := s.deleteToolResource(name); err != nil {
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
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
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
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
	if err := s.externalizeProviderSecret(&doc); err != nil {
		return providerResourceDocument{}, err
	}
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
	if err := s.runtime.SetResourceTombstone("providers", id, false); err != nil {
		return providerResourceDocument{}, fmt.Errorf("restore provider visibility: %w", err)
	}
	saved := doc
	saved.Path = path
	saved.APIKeySet = strings.TrimSpace(saved.APIKey) != ""
	saved.APIKey = ""
	markProviderResourceRestartRequired(&saved)
	return saved, nil
}

func (s *Server) externalizeProviderSecret(doc *providerResourceDocument) error {
	if s == nil || s.runtime == nil || doc == nil {
		return nil
	}
	value := strings.TrimSpace(doc.APIKey)
	if value == "" || strings.HasPrefix(value, "${") || strings.HasPrefix(value, "secret://") {
		return nil
	}
	reference := "secret://providers/" + normalizeResourceName(doc.ID) + "/api_key"
	path, err := config.SecretReferencePath(s.runtime.DataRoot(), reference)
	if err != nil {
		return fmt.Errorf("resolve provider secret path: %w", err)
	}
	if err := persistence.WriteFileAtomic(path, []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("store provider secret: %w", err)
	}
	doc.APIKey = reference
	return nil
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
			if s.builtinProviderModuleExists(id) {
				return s.runtime.SetResourceTombstone("providers", id, true)
			}
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

func (s *Server) agentResourceListByKind(kind string) []agentResourceDocument {
	kind = normalizeAgentResourceKind(kind)
	all := s.agentResourceList()
	out := make([]agentResourceDocument, 0, len(all))
	for _, doc := range all {
		if doc.Kind == kind {
			out = append(out, doc)
		}
	}
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
	if err := s.ensureResourceNotInUse("agent", id); err != nil {
		return agentResourceDocument{}, err
	}
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
	if err := s.runtime.SetResourceTombstone("agents", id, false); err != nil {
		return agentResourceDocument{}, fmt.Errorf("restore agent visibility: %w", err)
	}
	doc.Path = path
	if err := s.runtime.UpsertAgentProfile(id, parsed[id]); err != nil {
		markAgentResourceRestartRequired(&doc)
		doc.ApplyMessage = err.Error()
		return doc, nil
	}
	markAgentResourceActive(&doc)
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
	if err := s.ensureResourceNotInUse("agent", name); err != nil {
		return err
	}
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
			if s.builtinAgentModuleExists(id) {
				return s.runtime.SetResourceTombstone("agents", id, true)
			}
			if _, runtimeAgent := s.runtime.Profile(id); runtimeAgent {
				return fmt.Errorf("agent %q is defined in the main config; delete only supports configs/agents/*.yaml modules", id)
			}
		}
		return fmt.Errorf("agent %q not found", id)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete agent config: %w", err)
	}
	if s.runtime != nil {
		_ = s.runtime.RemoveAgentProfile(id)
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
			doc := toolResourceDocument{Name: name, Language: "mcp", Description: "Discovered MCP tool", TargetScope: s.targetScopeForTool(name)}
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
				TargetScope: s.targetScopeForTool(name),
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
			TargetScope: s.targetScopeForTool(qualified),
			Risk:        s.toolRiskProfileForTool(tool),
		}, true
	}
	return toolResourceDocument{}, false
}

func (s *Server) targetScopeForTool(name string) string {
	if server, ok := s.runtimeMCPServerForTool(name); ok {
		return normalizeTargetScope(server.TargetScope)
	}
	return ""
}

func (s *Server) saveToolResource(name string, doc toolResourceDocument) (toolResourceDocument, error) {
	if err := s.ensureResourceNotInUse("mcp", name); err != nil {
		return toolResourceDocument{}, err
	}
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
	if s.runtime != nil {
		if err := s.runtime.UpsertMCPServer(parsed.MCPServers[0]); err != nil {
			markToolResourceRestartRequired(&doc)
			doc.ApplyMessage = err.Error()
			return doc, nil
		}
	}
	markToolResourceActive(&doc)
	return doc, nil
}

func (s *Server) validateToolResourceDryRun(name string, doc toolResourceDocument) (toolResourceDocument, error) {
	normalized := normalizeResourceName(firstResourceValidationName(name, doc.Name))
	if !resourceNamePattern.MatchString(normalized) {
		return toolResourceDocument{}, fmt.Errorf("invalid tool name %q: use lowercase letters, numbers, hyphen, or underscore", normalized)
	}
	doc.Name = normalized
	s.applyToolResourceDefaults(&doc)
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
	if err := s.ensureResourceNotInUse("mcp", name); err != nil {
		return err
	}
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
	if s.runtime != nil {
		_ = s.runtime.RemoveMCPServer(normalized)
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
	if err := s.ensureResourceNotInUse("skill", name); err != nil {
		return skillFileResourceDocument{}, err
	}
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
	if err := s.ensureResourceNotInUse("skill", name); err != nil {
		return err
	}
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
	root := strings.TrimSpace(s.runtime.UserSkillRoot())
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
	if err := s.ensureResourceNotInUse("skill", firstWorkflowRunQueryValue(name, doc.Name)); err != nil {
		return skillResourceDocument{}, err
	}
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
	if err := s.runtime.SetResourceTombstone("skills", name, false); err != nil {
		return skillResourceDocument{}, fmt.Errorf("restore skill visibility: %w", err)
	}
	if err := s.runtime.ReloadSkills(); err != nil {
		return skillResourceDocument{}, fmt.Errorf("reload skills: %w", err)
	}
	saved := skillResourceFromSchema(*parsed)
	saved.Path = path
	return saved, nil
}

func (s *Server) deleteSkillResource(name string) error {
	if err := s.ensureResourceNotInUse("skill", name); err != nil {
		return err
	}
	name = normalizeResourceName(name)
	dir, err := s.skillDirectory(name)
	if err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			if s.builtinSkillExists(name) {
				if err := s.runtime.SetResourceTombstone("skills", name, true); err != nil {
					return fmt.Errorf("hide built-in skill: %w", err)
				}
				return s.runtime.ReloadSkills()
			}
			return fmt.Errorf("skill %q does not exist", name)
		}
		return fmt.Errorf("stat skill directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("skill %q is not a removable skill directory", normalizeResourceName(name))
	}
	if _, err := os.Lstat(filepath.Join(dir, "SKILL.md")); err != nil {
		return fmt.Errorf("skill %q has no SKILL.md", normalizeResourceName(name))
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	if s.builtinSkillExists(name) {
		if err := s.runtime.SetResourceTombstone("skills", name, true); err != nil {
			return fmt.Errorf("hide built-in skill: %w", err)
		}
	}
	if err := s.runtime.ReloadSkills(); err != nil {
		return fmt.Errorf("reload skills: %w", err)
	}
	return nil
}

func (s *Server) builtinSkillExists(name string) bool {
	if s == nil || s.runtime == nil {
		return false
	}
	root := strings.TrimSpace(s.runtime.BuiltinSkillRoot())
	if root == "" || strings.EqualFold(filepath.Clean(root), filepath.Clean(s.runtime.UserSkillRoot())) {
		return false
	}
	info, err := os.Lstat(filepath.Join(root, normalizeResourceName(name), "SKILL.md"))
	return err == nil && info.Mode().IsRegular()
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
		Kind:             normalizeAgentResourceKind(profile.Kind),
		TargetScope:      normalizeTargetScope(profile.TargetScope),
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
		ID:                    id,
		Type:                  provider.Provider,
		Provider:              provider.Provider,
		BaseURL:               provider.BaseURL,
		WireAPI:               provider.WireAPI,
		Model:                 provider.Model,
		DefaultModel:          provider.Model,
		FallbackProvider:      provider.FallbackProvider,
		ProviderMessageFields: append([]string(nil), provider.ProviderMessageFields...),
		Vision:                provider.Vision,
		Timeout:               durationString(provider.Timeout),
		Temperature:           provider.Temperature,
		MaxTokens:             provider.MaxTokens,
		ReasoningEffort:       provider.ReasoningEffort,
		RetryCount:            provider.RetryCount,
		RetryBackoff:          durationString(provider.RetryBackoff),
		APIKeySet:             strings.TrimSpace(provider.APIKey) != "",
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
	doc.WireAPI = normalizeProviderWireAPI(doc.WireAPI)
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
	doc.ProviderMessageFields = normalizeProviderMessageFields(doc.ProviderMessageFields)
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
		Provider:              doc.Provider,
		BaseURL:               doc.BaseURL,
		WireAPI:               doc.WireAPI,
		APIKey:                doc.APIKey,
		Model:                 doc.Model,
		FallbackProvider:      doc.FallbackProvider,
		ProviderMessageFields: append([]string(nil), doc.ProviderMessageFields...),
		Vision:                doc.Vision,
		Timeout:               doc.Timeout,
		Temperature:           doc.Temperature,
		MaxTokens:             doc.MaxTokens,
		ReasoningEffort:       doc.ReasoningEffort,
		RetryCount:            doc.RetryCount,
		RetryBackoff:          doc.RetryBackoff,
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
	var client interfaces.LLMClient
	switch strings.TrimSpace(cfg.Provider) {
	case "", "openai-compatible":
		client = llm.NewClient(cfg)
	case "anthropic":
		client = llm.NewAnthropicClient(cfg)
	default:
		return providerTestInvalid(doc.ID, "provider", fmt.Sprintf("unsupported provider %q", cfg.Provider))
	}
	started := time.Now()
	chat := testProviderCapability(parent, cfg.Timeout, "chat", func(ctx context.Context) error {
		response, err := client.Chat(ctx, schema.ChatRequest{
			Model:       cfg.Model,
			MaxTokens:   64,
			Temperature: 0,
			Messages: []schema.Message{{
				Role:    "user",
				Content: "Reply with exactly GOFLOW_OK.",
			}},
		})
		if err != nil {
			return err
		}
		if strings.TrimSpace(response.Message.Content) == "" && len(response.ToolCalls) == 0 && len(response.Message.ProviderFields) == 0 && response.StopReason != schema.StopReasonMaxTokens && response.StopReason != schema.StopReasonLength {
			return fmt.Errorf("provider returned an empty chat response")
		}
		return nil
	})
	capabilities := []providerCapabilityTestResult{chat}
	if !chat.Supported {
		return providerTestResult{
			Valid:        false,
			Resource:     "provider",
			Name:         doc.ID,
			Status:       "error",
			Message:      chat.Message,
			Provider:     cfg.Provider,
			Model:        cfg.Model,
			LatencyMS:    time.Since(started).Milliseconds(),
			Capabilities: capabilities,
			Issues:       []resourceValidationIssue{resourceValidationIssueFor("provider", providerTestErrorField(chat.Message), chat.Message)},
		}
	}

	capabilities = append(capabilities,
		testProviderStreamingCapability(parent, cfg.Timeout, client, cfg.Model),
		testProviderToolCallingCapability(parent, cfg.Timeout, client, cfg.Model),
		testProviderStructuredOutputCapability(parent, cfg.Timeout, client, cfg.Model),
		testProviderVisionCapability(parent, cfg.Timeout, client, cfg.Model),
	)
	status := "ok"
	message := "provider capabilities verified"
	for _, capability := range capabilities {
		if !capability.Supported {
			status = "limited"
			message = "provider connection succeeded with limited capabilities"
			break
		}
	}
	return providerTestResult{
		Valid:        true,
		Resource:     "provider",
		Name:         doc.ID,
		Status:       status,
		Message:      message,
		Provider:     cfg.Provider,
		Model:        cfg.Model,
		LatencyMS:    time.Since(started).Milliseconds(),
		Capabilities: capabilities,
	}
}

func testProviderVisionCapability(parent context.Context, timeout time.Duration, client interfaces.LLMClient, model string) providerCapabilityTestResult {
	if !providerClientSupports(client, "vision") {
		return unsupportedProviderCapability("vision", "当前 Provider 适配器不支持图片输入")
	}
	return testProviderCapability(parent, timeout, "vision", func(ctx context.Context) error {
		dataURL, err := providerVisionProbeDataURL()
		if err != nil {
			return err
		}
		response, err := client.Chat(ctx, schema.ChatRequest{
			Model:       model,
			MaxTokens:   64,
			Temperature: 0,
			Messages: []schema.Message{{
				Role:    "user",
				Content: "请用一个词描述图片的主色。",
				ContentParts: []schema.MessageContentPart{{
					Type:    "image",
					DataURL: dataURL,
				}},
			}},
		})
		if err != nil {
			return err
		}
		if strings.TrimSpace(response.Message.Content) == "" && len(response.Message.ProviderFields) == 0 {
			return fmt.Errorf("provider returned an empty vision response")
		}
		return nil
	})
}

func providerVisionProbeDataURL() (string, error) {
	const size = 64
	probe := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			probe.SetRGBA(x, y, color.RGBA{R: 220, G: 38, B: 38, A: 255})
		}
	}

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, probe); err != nil {
		return "", fmt.Errorf("encode provider vision probe image: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes()), nil
}

func testProviderStreamingCapability(parent context.Context, timeout time.Duration, client interfaces.LLMClient, model string) providerCapabilityTestResult {
	if !providerClientSupports(client, "stream") {
		return unsupportedProviderCapability("streaming", "native streaming is not implemented by this provider adapter")
	}
	return testProviderCapability(parent, timeout, "streaming", func(ctx context.Context) error {
		events := 0
		response, err := client.StreamChat(ctx, schema.ChatRequest{
			Model:       model,
			MaxTokens:   128,
			Temperature: 0,
			Messages:    []schema.Message{{Role: "user", Content: "Reply with exactly GOFLOW_STREAM_OK."}},
		}, func(event schema.StreamEvent) error {
			if event.Type == schema.StreamEventText || event.Type == schema.StreamEventToolCall {
				events++
			}
			return nil
		})
		if err != nil {
			return err
		}
		if events == 0 && len(response.Message.ProviderFields) == 0 {
			return fmt.Errorf("provider did not emit native stream events")
		}
		return nil
	})
}

func testProviderToolCallingCapability(parent context.Context, timeout time.Duration, client interfaces.LLMClient, model string) providerCapabilityTestResult {
	if !providerClientSupports(client, "tool_calling") {
		return unsupportedProviderCapability("tool_calling", "tool calling is not implemented by this provider adapter")
	}
	return testProviderCapability(parent, timeout, "tool_calling", func(ctx context.Context) error {
		response, err := client.Chat(ctx, schema.ChatRequest{
			Model:       model,
			MaxTokens:   256,
			Temperature: 0,
			Messages:    []schema.Message{{Role: "user", Content: "You are reviewing a proposed repository documentation change before execution. The file docs/operations.md needs a concise Windows rollback section added because operators currently cannot recover from a failed local upgrade. Analyze the request and call the supplied planning tool exactly once. Set path to docs/operations.md, action to add_rollback_section, and risk to low. Do not answer with prose because the downstream workflow consumes only the structured tool arguments."}},
			Tools: []schema.Tool{{
				Name:        "goflow_plan_change",
				Description: "Record a proposed repository change for downstream workflow validation without executing it.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"action":{"type":"string"},"risk":{"type":"string","enum":["low","medium","high"]}},"required":["path","action","risk"],"additionalProperties":false}`),
			}},
		})
		if err != nil {
			return err
		}
		for _, call := range response.ToolCalls {
			var arguments struct {
				Path   string `json:"path"`
				Action string `json:"action"`
				Risk   string `json:"risk"`
			}
			if call.Name == "goflow_plan_change" && json.Unmarshal(call.Arguments, &arguments) == nil &&
				arguments.Path == "docs/operations.md" && arguments.Action == "add_rollback_section" && arguments.Risk == "low" {
				return nil
			}
		}
		return fmt.Errorf("provider did not return the required tool call")
	})
}

func testProviderStructuredOutputCapability(parent context.Context, timeout time.Duration, client interfaces.LLMClient, model string) providerCapabilityTestResult {
	if !providerClientSupports(client, "structured_output") {
		return unsupportedProviderCapability("structured_output", "native structured output is not implemented by this provider adapter")
	}
	return testProviderCapability(parent, timeout, "structured_output", func(ctx context.Context) error {
		response, err := client.Chat(ctx, schema.ChatRequest{
			Model:          model,
			MaxTokens:      256,
			Temperature:    0,
			ResponseFormat: &schema.ResponseFormat{Type: "json_object"},
			Messages:       []schema.Message{{Role: "user", Content: `Return one JSON object with the exact shape {"ok":true}.`}},
		})
		if err != nil {
			return err
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(response.Message.Content)), &object); err != nil {
			return fmt.Errorf("provider returned invalid structured output: %w", err)
		}
		if len(object) == 0 {
			return fmt.Errorf("provider returned an empty structured object")
		}
		return nil
	})
}

func testProviderCapability(parent context.Context, timeout time.Duration, name string, probe func(context.Context) error) providerCapabilityTestResult {
	ctx, cancel := context.WithTimeout(parent, providerTestTimeout(timeout))
	defer cancel()
	started := time.Now()
	err := probe(ctx)
	result := providerCapabilityTestResult{Name: name, Status: "supported", Supported: err == nil, LatencyMS: time.Since(started).Milliseconds()}
	if err != nil {
		result.Status = "failed"
		result.Message = err.Error()
	}
	return result
}

func unsupportedProviderCapability(name, message string) providerCapabilityTestResult {
	return providerCapabilityTestResult{Name: name, Status: "unsupported", Message: message}
}

func providerClientSupports(client interfaces.LLMClient, capability string) bool {
	for _, candidate := range client.Capabilities() {
		if candidate == capability {
			return true
		}
	}
	return false
}

func providerModelListInvalid(name, field, message string) providerModelListResult {
	return providerModelListResult{
		Valid:    false,
		Resource: "provider",
		Name:     firstResourceValidationName(name),
		Status:   "error",
		Message:  message,
		Issues:   []resourceValidationIssue{resourceValidationIssueFor("provider", field, message)},
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
		Provider:              strings.TrimSpace(doc.Provider),
		BaseURL:               os.ExpandEnv(strings.TrimSpace(doc.BaseURL)),
		WireAPI:               strings.TrimSpace(doc.WireAPI),
		APIKey:                os.ExpandEnv(strings.TrimSpace(doc.APIKey)),
		Model:                 os.ExpandEnv(strings.TrimSpace(doc.Model)),
		FallbackProvider:      strings.TrimSpace(doc.FallbackProvider),
		ProviderMessageFields: normalizeProviderMessageFields(doc.ProviderMessageFields),
		Vision:                doc.Vision,
		Timeout:               timeout,
		Temperature:           doc.Temperature,
		MaxTokens:             doc.MaxTokens,
		ReasoningEffort:       config.NormalizeReasoningEffort(doc.ReasoningEffort),
		RetryCount:            doc.RetryCount,
		RetryBackoff:          retryBackoff,
	}
}

func providerTestTimeout(timeout time.Duration) time.Duration {
	const maximum = 5 * time.Minute
	if timeout <= 0 {
		return maximum
	}
	if timeout > maximum {
		return maximum
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
	doc.Kind = normalizeAgentResourceKind(doc.Kind)
	doc.TargetScope = strings.ToLower(strings.TrimSpace(doc.TargetScope))
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
	switch doc.Kind {
	case config.AgentKindAgent, config.AgentKindSubagent:
	default:
		return fmt.Errorf("agent kind must be agent or subagent")
	}
	if scope := strings.ToLower(strings.TrimSpace(doc.TargetScope)); scope != "" && scope != "host" {
		return fmt.Errorf("agent target_scope must be empty or host")
	}
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
		Kind:             doc.Kind,
		TargetScope:      doc.TargetScope,
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
		profile.Warnings = append(profile.Warnings, "tool uses write, exec, network, or unknown capabilities without an enforced native sandbox")
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
	case server == "command_tools" || strings.HasPrefix(qualified, "command_tools/"):
		return true
	case strings.HasPrefix(name, "file_tools/") || strings.HasPrefix(name, "python_notes/") || strings.HasPrefix(name, "command_tools/"):
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
	doc.Kind = normalizeAgentResourceKind(doc.Kind)
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

func normalizeAgentResourceKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return config.AgentKindAgent
	}
	return kind
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
	doc.WireAPI = normalizeProviderWireAPI(doc.WireAPI)
	doc.APIKey = ""
	doc.APIKeySet = false
	doc.Model = strings.TrimSpace(doc.Model)
	doc.FallbackProvider = strings.TrimSpace(doc.FallbackProvider)
	doc.ProviderMessageFields = normalizeProviderMessageFields(doc.ProviderMessageFields)
	doc.Timeout = comparableDurationString(doc.Timeout)
	doc.RetryBackoff = comparableDurationString(doc.RetryBackoff)
	doc.Path = ""
	doc.RestartRequired = false
	doc.ApplyState = ""
	doc.ApplyMessage = ""
	return doc
}

func normalizeProviderWireAPI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "responses", "response":
		return "responses"
	default:
		return "chat_completions"
	}
}

func normalizeProviderMessageFields(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
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
	for _, ref := range s.runtime.MCPServerRefs() {
		if normalizeResourceName(ref.Name) == name && ref.Enabled {
			return true
		}
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
	doc.TargetScope = strings.ToLower(strings.TrimSpace(doc.TargetScope))
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
		TargetScope:         strings.ToLower(strings.TrimSpace(doc.TargetScope)),
		Command:             doc.Command,
		Args:                doc.Args,
		Enabled:             doc.Enabled,
		Timeout:             doc.Timeout,
		WorkDir:             doc.WorkDir,
		EnvAllowlist:        doc.EnvAllowlist,
		NetworkDisabled:     doc.NetworkDisabled,
		Isolation:           doc.Isolation,
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
		TargetScope:         normalizeTargetScope(doc.TargetScope),
		Language:            "python",
		Command:             doc.Command,
		Args:                append([]string(nil), doc.Args...),
		Enabled:             doc.Enabled,
		Timeout:             doc.Timeout,
		WorkDir:             doc.WorkDir,
		EnvAllowlist:        append([]string(nil), doc.EnvAllowlist...),
		NetworkDisabled:     doc.NetworkDisabled,
		Isolation:           doc.Isolation,
		IsolationOptions:    copyMap(doc.IsolationOptions),
		RestartLimit:        doc.RestartLimit,
		Cooldown:            doc.Cooldown,
		MaxConcurrentCalls:  doc.MaxConcurrentCalls,
		AllowedCommandPaths: append([]string(nil), doc.AllowedCommandPaths...),
		AllowedCommands:     append([]string(nil), doc.AllowedCommands...),
		MaxRequestBytes:     doc.MaxRequestBytes,
		MaxResponseBytes:    doc.MaxResponseBytes,
	}
	return out
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
	root, err := s.coreResourceRoot("providers", "configs", "providers")
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
	root, err := s.coreResourceRoot("agents", "configs", "agents")
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

func (s *Server) builtinAgentModuleExists(name string) bool {
	if s == nil || s.runtime == nil || sameAPIPath(s.runtime.DataRoot(), s.runtime.RuntimeHome()) {
		return false
	}
	root := filepath.Join(s.runtime.RuntimeHome(), "configs", "agents")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		var profiles map[string]config.AgentProfile
		if yaml.Unmarshal(data, &profiles) == nil {
			if _, ok := profiles[normalizeResourceName(name)]; ok {
				return true
			}
		}
	}
	return false
}

func (s *Server) builtinProviderModuleExists(name string) bool {
	if s == nil || s.runtime == nil || sameAPIPath(s.runtime.DataRoot(), s.runtime.RuntimeHome()) {
		return false
	}
	root := filepath.Join(s.runtime.RuntimeHome(), "configs", "providers")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		providers, err := readProviderResourceMap(data)
		if err == nil {
			if _, ok := providers[normalizeResourceName(name)]; ok {
				return true
			}
		}
	}
	return false
}

func sameAPIPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(strings.TrimSpace(left)), filepath.Clean(strings.TrimSpace(right)))
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
	root := strings.TrimSpace(s.runtime.DataRoot())
	if root == "" {
		return "", fmt.Errorf("data root is not configured")
	}
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve data root: %w", err)
	}
	items := append([]string{root}, parts...)
	path := filepath.Clean(filepath.Join(items...))
	if !referenceWithinBase(root, path) {
		return "", fmt.Errorf("resource path escapes data root")
	}
	return path, nil
}

func (s *Server) coreResourceRoot(family string, legacyParts ...string) (string, error) {
	if s == nil || s.runtime == nil {
		return "", fmt.Errorf("runtime is not configured")
	}
	dataRoot := strings.TrimSpace(s.runtime.DataRoot())
	runtimeHome := strings.TrimSpace(s.runtime.RuntimeHome())
	if dataRoot == "" || strings.EqualFold(filepath.Clean(dataRoot), filepath.Clean(runtimeHome)) {
		return s.runtimeResourceRoot(legacyParts...)
	}
	return s.runtimeResourceRoot("resources", family)
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
	case resource == "tool" && strings.Contains(lower, "managed internally"):
		return "mcp_native_isolation_internal", "use isolation: auto or native_strong"
	case resource == "tool" && strings.Contains(lower, "has been removed"):
		return "mcp_container_isolation_removed", "replace the removed mode with isolation: auto"
	case resource == "tool" && strings.Contains(lower, "isolation_options"):
		return "invalid_" + strings.ReplaceAll(field, ".", "_"), "fix the MCP isolation option shown by the validation message"
	case resource == "tool" && strings.Contains(lower, "allowed_commands or allowed_command_paths"):
		return "required_allowed_commands", "add allowed_commands for a fixed executable or allowed_command_paths for a controlled directory before enabling this MCP server"
	case resource == "tool" && strings.Contains(lower, "not in allowed_commands"):
		return "mcp_command_not_allowed", "add the exact command to allowed_commands or change command to an allowed executable"
	case resource == "tool" && strings.Contains(lower, "outside allowed_command_paths"):
		return "mcp_command_path_not_allowed", "move the command under an allowed path or add a narrower allowed_command_paths entry"
	case resource == "tool" && strings.Contains(lower, "isolation") && strings.Contains(lower, "invalid"):
		return "mcp_isolation_invalid", "choose auto, native_strong, process_group, windows_job, windows_restricted_token, linux_cgroup, linux_netns, or a trusted built-in boundary"
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
