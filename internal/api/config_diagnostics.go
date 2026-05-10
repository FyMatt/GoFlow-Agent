package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"gopkg.in/yaml.v3"
)

type configDiagnosticsResponse struct {
	Status          string                  `json:"status"`
	ConfigPath      string                  `json:"config_path,omitempty"`
	RuntimeHome     string                  `json:"runtime_home,omitempty"`
	WorkspaceRoot   string                  `json:"workspace_root,omitempty"`
	RestartRequired bool                    `json:"restart_required,omitempty"`
	Apply           configApplySummary      `json:"apply"`
	Summary         configDiagnosticsCount  `json:"summary"`
	Diagnostics     diagnosticSeverityCount `json:"diagnostics"`
	Modules         []configModuleSummary   `json:"modules"`
	Items           []configDiagnosticItem  `json:"items"`
}

type ConfigDiagnosticsResponse = configDiagnosticsResponse
type ConfigDiagnosticsCount = configDiagnosticsCount
type DiagnosticSeverityCount = diagnosticSeverityCount
type ConfigModuleSummary = configModuleSummary
type ConfigDiagnosticItem = configDiagnosticItem

type configDiagnosticsCount struct {
	Providers   int `json:"providers"`
	Agents      int `json:"agents"`
	MCPServers  int `json:"mcp_servers"`
	Skills      int `json:"skills"`
	Workflows   int `json:"workflows"`
	PolicyRules int `json:"policy_rules"`
	Kits        int `json:"kits"`
}

type diagnosticSeverityCount struct {
	Total    int `json:"total"`
	Info     int `json:"info"`
	Warnings int `json:"warnings"`
	Errors   int `json:"errors"`
}

type configApplySummary struct {
	State                       string   `json:"state"`
	RestartRequired             bool     `json:"restart_required"`
	RuntimeRebootstrapSupported bool     `json:"runtime_rebootstrap_supported"`
	HotReloadSupported          bool     `json:"hot_reload_supported"`
	RestartCommandHint          []string `json:"restart_command_hint,omitempty"`
	RebootstrapBlockers         []string `json:"rebootstrap_blockers,omitempty"`
	AffectedKinds               []string `json:"affected_kinds,omitempty"`
	Message                     string   `json:"message,omitempty"`
}

type configModuleSummary struct {
	Kind  string   `json:"kind"`
	Root  string   `json:"root"`
	Files []string `json:"files"`
}

type configDiagnosticItem struct {
	Severity       string            `json:"severity"`
	Code           string            `json:"code"`
	Message        string            `json:"message"`
	Recommendation string            `json:"recommendation,omitempty"`
	Path           string            `json:"path,omitempty"`
	TargetKind     string            `json:"target_kind,omitempty"`
	TargetName     string            `json:"target_name,omitempty"`
	Field          string            `json:"field,omitempty"`
	Category       string            `json:"category,omitempty"`
	Optional       bool              `json:"optional,omitempty"`
	Actionable     *bool             `json:"actionable,omitempty"`
	Details        map[string]string `json:"details,omitempty"`
}

func (s *Server) handleConfigDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	diagnostics := s.configDiagnostics()
	if !configDiagnosticsIncludeOptional(r) {
		diagnostics = diagnostics.withoutOptionalNonActionableItems()
	}
	writeJSON(w, diagnostics)
}

func (s *Server) configDiagnostics() configDiagnosticsResponse {
	return ConfigDiagnostics(s.runtime, s.workspace)
}

// ConfigDiagnostics reload-validates the saved runtime config and summarizes
// modular resource health for CLI and HTTP consumers.
func ConfigDiagnostics(runtimeRef *agent.Runtime, workspaceState interface{ Root() string }) ConfigDiagnosticsResponse {
	resp := configDiagnosticsResponse{Status: "ok"}
	if runtimeRef == nil {
		resp.Apply = configApplySummaryForLoadError("", "", "runtime is not configured")
		resp.addDiagnostic("error", "runtime_missing", "runtime is not configured", "")
		resp.finalize()
		return resp
	}
	resp.RuntimeHome = runtimeRef.RuntimeHome()
	resp.ConfigPath = runtimeRef.ConfigPath()
	resp.WorkspaceRoot = runtimeRef.WorkspaceRoot()
	if strings.TrimSpace(resp.ConfigPath) == "" && strings.TrimSpace(resp.RuntimeHome) != "" {
		resp.ConfigPath = filepath.Join(resp.RuntimeHome, "configs", "goflow.yaml")
	}
	if strings.TrimSpace(resp.WorkspaceRoot) == "" {
		if workspaceState != nil {
			resp.WorkspaceRoot = workspaceState.Root()
		}
	}
	if strings.TrimSpace(resp.WorkspaceRoot) == "" {
		if cwd, err := os.Getwd(); err == nil {
			resp.WorkspaceRoot = cwd
		}
	}
	resp.Modules = configModuleSummaries(resp.RuntimeHome)
	resp.inspectModuleFiles()
	cfg, err := config.LoadForWorkspace(resp.ConfigPath, resp.WorkspaceRoot)
	if err != nil {
		resp.Apply = configApplySummaryForLoadError(resp.ConfigPath, resp.WorkspaceRoot, err.Error())
		resp.addConfigLoadFailureDiagnostics(err, resp.ConfigPath)
		resp.finalize()
		return resp
	}
	resp.Summary.Providers = len(cfg.Providers)
	resp.Summary.Agents = len(cfg.Agents)
	resp.Summary.MCPServers = len(cfg.MCP)
	resp.Summary.Skills = len(runtimeRef.SkillList())
	resp.Summary.Workflows = len(runtimeRef.WorkflowRunner().ListWorkflowGraphs())
	resp.Summary.PolicyRules = len(runtimeRef.WorkflowRunner().CustomWorkflowPolicyRules())
	resp.Summary.Kits = len(kitResourceListForRuntime(runtimeRef))
	resp.addDiagnostic("info", "config_load_ok", "runtime config loads successfully", resp.ConfigPath)
	resp.checkProviderDiagnostics(cfg)
	resp.checkAgentDiagnostics(cfg)
	resp.checkToolRiskPolicyDiagnostics(cfg)
	resp.checkMCPDiagnostics(cfg)
	resp.checkWorkflowDiagnostics(runtimeRef, cfg)
	resp.Apply = configApplySummaryForRuntime(runtimeRef, cfg, resp.ConfigPath, resp.WorkspaceRoot)
	resp.RestartRequired = resp.Apply.RestartRequired
	if resp.RestartRequired {
		resp.addDiagnosticWithRecommendation("warning", "restart_required", "saved config differs from the active runtime; restart or future rebootstrap is required before all edits take effect", resp.ConfigPath, "restart GoFlow or use a future runtime rebootstrap action before relying on saved provider, agent, or MCP server edits")
	}
	resp.finalize()
	return resp
}

func configModuleSummaries(runtimeHome string) []configModuleSummary {
	if strings.TrimSpace(runtimeHome) == "" {
		return nil
	}
	kinds := []struct {
		kind       string
		path       string
		manifest   string
		extensions []string
	}{
		{kind: "providers", path: filepath.Join(runtimeHome, "configs", "providers")},
		{kind: "agents", path: filepath.Join(runtimeHome, "configs", "agents")},
		{kind: "mcp_servers", path: filepath.Join(runtimeHome, "configs", "mcp_servers")},
		{kind: "workflows", path: filepath.Join(runtimeHome, "workflows"), manifest: "workflow.yaml"},
		{kind: "workflow_templates", path: filepath.Join(runtimeHome, "templates", "workflows")},
		{kind: "workflow_schemas", path: filepath.Join(runtimeHome, "schemas", "workflows"), extensions: []string{".json"}},
		{kind: "team_templates", path: filepath.Join(runtimeHome, "templates", "teams")},
		{kind: "workflow_node_metadata", path: filepath.Join(runtimeHome, "metadata", "workflow_nodes")},
		{kind: "workflow_node_metadata_legacy", path: filepath.Join(runtimeHome, "templates", "workflow_nodes")},
		{kind: "expression_helpers", path: filepath.Join(runtimeHome, "metadata", "expression_helpers")},
		{kind: "expression_helpers_legacy", path: filepath.Join(runtimeHome, "templates", "expression_helpers")},
		{kind: "workflow_expression_helpers_legacy", path: filepath.Join(runtimeHome, "metadata", "workflow_expressions")},
		{kind: "workflow_expression_helpers_template_legacy", path: filepath.Join(runtimeHome, "templates", "workflow_expressions")},
		{kind: "policy_rules", path: filepath.Join(runtimeHome, "policies", "workflow_rules")},
		{kind: "kits", path: filepath.Join(runtimeHome, "kits"), manifest: "kit.yaml"},
	}
	out := make([]configModuleSummary, 0, len(kinds))
	for _, item := range kinds {
		files := listConfigModuleFiles(item.path, item.manifest, item.extensions...)
		out = append(out, configModuleSummary{Kind: item.kind, Root: item.path, Files: files})
	}
	return out
}

func listConfigModuleFiles(root string, manifest string, extensions ...string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	allowed := moduleFileExtensions(extensions...)
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(manifest) != "" {
			if entry.IsDir() {
				path := filepath.Join(root, entry.Name(), manifest)
				if _, err := os.Stat(path); err == nil {
					files = append(files, path)
				}
				continue
			}
			if moduleFileExtensionAllowed(entry.Name(), allowed) {
				files = append(files, filepath.Join(root, entry.Name()))
			}
			continue
		}
		if entry.IsDir() || !moduleFileExtensionAllowed(entry.Name(), allowed) {
			continue
		}
		files = append(files, filepath.Join(root, entry.Name()))
	}
	sort.Strings(files)
	return files
}

func moduleFileExtensions(extensions ...string) map[string]struct{} {
	if len(extensions) == 0 {
		return map[string]struct{}{".yaml": {}, ".yml": {}}
	}
	out := make(map[string]struct{}, len(extensions))
	for _, ext := range extensions {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		out[ext] = struct{}{}
	}
	if len(out) == 0 {
		out[".yaml"] = struct{}{}
		out[".yml"] = struct{}{}
	}
	return out
}

func moduleFileExtensionAllowed(name string, allowed map[string]struct{}) bool {
	_, ok := allowed[strings.ToLower(filepath.Ext(name))]
	return ok
}

func (r *configDiagnosticsResponse) inspectModuleFiles() {
	for _, module := range r.Modules {
		if len(module.Files) == 0 {
			r.addOptionalModuleDirDiagnostic(module)
			continue
		}
		for _, path := range module.Files {
			if err := validateModuleSyntax(module.Kind, path); err != nil {
				r.addDiagnostic("error", "module_parse_failed", err.Error(), path)
			}
		}
	}
}

func (r *configDiagnosticsResponse) addOptionalModuleDirDiagnostic(module configModuleSummary) {
	actionable := false
	r.Items = append(r.Items, configDiagnosticItem{
		Severity:       "info",
		Code:           "module_dir_empty",
		Message:        module.Kind + " module directory has no files",
		Recommendation: "optional extension directory; no action is required unless you want to add custom resources",
		Path:           module.Root,
		TargetKind:     "module",
		TargetName:     module.Kind,
		Category:       "optional",
		Optional:       true,
		Actionable:     &actionable,
	})
}

func configDiagnosticsIncludeOptional(r *http.Request) bool {
	if r == nil {
		return true
	}
	query := r.URL.Query()
	if value := strings.TrimSpace(query.Get("include_optional")); value != "" {
		return configDiagnosticsTruthy(value)
	}
	if value := strings.TrimSpace(query.Get("optional")); value != "" {
		return configDiagnosticsTruthy(value)
	}
	if value := strings.TrimSpace(query.Get("verbose")); value != "" {
		return configDiagnosticsTruthy(value)
	}
	return true
}

func configDiagnosticsTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "1", "true", "yes", "y", "on", "verbose", "all", "include":
		return true
	case "0", "false", "no", "n", "off", "quiet", "compact", "hide", "exclude":
		return false
	default:
		return true
	}
}

func (r configDiagnosticsResponse) withoutOptionalNonActionableItems() configDiagnosticsResponse {
	if len(r.Items) == 0 {
		return r
	}
	items := make([]configDiagnosticItem, 0, len(r.Items))
	for _, item := range r.Items {
		if configDiagnosticOptionalNonActionable(item) {
			continue
		}
		items = append(items, item)
	}
	r.Items = items
	r.finalize()
	return r
}

func configDiagnosticOptionalNonActionable(item configDiagnosticItem) bool {
	if !item.Optional {
		return false
	}
	if item.Actionable != nil {
		return !*item.Actionable
	}
	return strings.EqualFold(strings.TrimSpace(item.Category), "optional") &&
		strings.EqualFold(strings.TrimSpace(item.Severity), "info")
}

func validateModuleSyntax(kind, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch kind {
	case "providers":
		var wrapped struct {
			Providers map[string]config.LLMConfig `yaml:"providers"`
		}
		if err := yaml.Unmarshal(data, &wrapped); err != nil {
			return err
		}
		if len(wrapped.Providers) > 0 {
			return nil
		}
		var direct map[string]config.LLMConfig
		return yaml.Unmarshal(data, &direct)
	case "agents":
		var wrapped struct {
			Agents map[string]config.AgentProfile `yaml:"agents"`
		}
		if err := yaml.Unmarshal(data, &wrapped); err != nil {
			return err
		}
		if len(wrapped.Agents) > 0 {
			return nil
		}
		var direct map[string]config.AgentProfile
		return yaml.Unmarshal(data, &direct)
	case "mcp_servers":
		var wrapped struct {
			MCPServers []config.MCPServerRef `yaml:"mcp_servers"`
		}
		if err := yaml.Unmarshal(data, &wrapped); err != nil {
			return err
		}
		if len(wrapped.MCPServers) > 0 {
			return nil
		}
		var direct config.MCPServerRef
		return yaml.Unmarshal(data, &direct)
	case "workflows":
		var doc agent.WorkflowGraphDocument
		return yaml.Unmarshal(data, &doc)
	case "workflow_schemas":
		var doc agent.WorkflowSchemaResource
		return json.Unmarshal(data, &doc)
	case "team_templates":
		var doc agent.TeamTemplate
		return yaml.Unmarshal(data, &doc)
	case "workflow_node_metadata", "workflow_node_metadata_legacy":
		var doc agent.WorkflowNodeTypeOption
		return yaml.Unmarshal(data, &doc)
	case "expression_helpers", "expression_helpers_legacy", "workflow_expression_helpers_legacy", "workflow_expression_helpers_template_legacy":
		var doc agent.WorkflowExpressionFunctionOption
		return yaml.Unmarshal(data, &doc)
	case "policy_rules":
		var doc agent.WorkflowPolicyRuleDefinition
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return err
		}
		return agent.ValidateWorkflowPolicyRuleDefinition(doc)
	case "kits":
		var doc kitResourceDocument
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return err
		}
		return validateKitResourceDocument(doc)
	default:
		var doc any
		return yaml.Unmarshal(data, &doc)
	}
}

var configLoadDiagnosticPatterns = []struct {
	code           string
	pattern        *regexp.Regexp
	targetKind     string
	targetGroup    int
	field          string
	recommendation string
}{
	{
		code:           "providers_missing",
		pattern:        regexp.MustCompile(`^at least one provider is required$`),
		targetKind:     "config",
		field:          "providers",
		recommendation: "create at least one provider under configs/providers or providers in goflow.yaml",
	},
	{
		code:           "agents_missing",
		pattern:        regexp.MustCompile(`^at least one agent is required$`),
		targetKind:     "config",
		field:          "agents",
		recommendation: "create at least one agent under configs/agents or agents in goflow.yaml",
	},
	{
		code:           "default_agent_required",
		pattern:        regexp.MustCompile(`^default_agent is required$`),
		targetKind:     "config",
		field:          "default_agent",
		recommendation: "set default_agent to one of the configured agent ids",
	},
	{
		code:           "default_agent_missing",
		pattern:        regexp.MustCompile(`^default_agent "([^"]+)" is not defined$`),
		targetKind:     "agent",
		targetGroup:    1,
		field:          "default_agent",
		recommendation: "create the referenced agent module or update default_agent to an existing agent id",
	},
	{
		code:           "skill_directory_required",
		pattern:        regexp.MustCompile(`^skill\.directory is required$`),
		targetKind:     "config",
		field:          "skill.directory",
		recommendation: "set skill.directory to the skills folder, usually ./skills",
	},
	{
		code:           "agent_defaults_max_iterations_invalid",
		pattern:        regexp.MustCompile(`^agent\.max_iterations must be greater than 0$`),
		targetKind:     "config",
		field:          "agent.max_iterations",
		recommendation: "set agent.max_iterations to a positive number such as 8",
	},
	{
		code:           "provider_model_required",
		pattern:        regexp.MustCompile(`^provider ([^\s]+) model is required$`),
		targetKind:     "provider",
		targetGroup:    1,
		field:          "model",
		recommendation: "set provider.model to the model id this provider should call",
	},
	{
		code:           "provider_base_url_required",
		pattern:        regexp.MustCompile(`^provider ([^\s]+) base_url is required$`),
		targetKind:     "provider",
		targetGroup:    1,
		field:          "base_url",
		recommendation: "set provider.base_url to the OpenAI-compatible API endpoint",
	},
	{
		code:           "provider_fallback_missing",
		pattern:        regexp.MustCompile(`^provider ([^\s]+) references unknown fallback_provider "([^"]+)"$`),
		targetKind:     "provider",
		targetGroup:    1,
		field:          "fallback_provider",
		recommendation: "create the referenced provider module or update fallback_provider to an existing provider id",
	},
	{
		code:           "provider_fallback_self",
		pattern:        regexp.MustCompile(`^provider ([^\s]+) fallback_provider cannot reference itself$`),
		targetKind:     "provider",
		targetGroup:    1,
		field:          "fallback_provider",
		recommendation: "clear fallback_provider or point it to a different provider",
	},
	{
		code:           "agent_provider_missing",
		pattern:        regexp.MustCompile(`^agent ([^\s]+) references unknown provider "([^"]+)"$`),
		targetKind:     "agent",
		targetGroup:    1,
		field:          "provider",
		recommendation: "create the provider module or update this agent provider to an existing provider id",
	},
	{
		code:           "agent_provider_required",
		pattern:        regexp.MustCompile(`^agent ([^\s]+) provider is required$`),
		targetKind:     "agent",
		targetGroup:    1,
		field:          "provider",
		recommendation: "set agent.provider to an existing provider id",
	},
	{
		code:           "agent_max_iterations_invalid",
		pattern:        regexp.MustCompile(`^agent ([^\s]+) max_iterations must be greater than 0$`),
		targetKind:     "agent",
		targetGroup:    1,
		field:          "max_iterations",
		recommendation: "set this agent max_iterations to a positive number",
	},
	{
		code:           "verifier_agent_required",
		pattern:        regexp.MustCompile(`^verifier\.agent is required when verifier is enabled$`),
		targetKind:     "verifier",
		field:          "agent",
		recommendation: "set verifier.agent to an existing verifier/auditor agent or disable verifier.enabled",
	},
	{
		code:           "verifier_agent_missing",
		pattern:        regexp.MustCompile(`^verifier references unknown agent "([^"]+)"$`),
		targetKind:     "verifier",
		targetGroup:    1,
		field:          "agent",
		recommendation: "create the referenced verifier agent or update verifier.agent to an existing agent id",
	},
	{
		code:           "verifier_provider_missing",
		pattern:        regexp.MustCompile(`^verifier references unknown provider "([^"]+)"$`),
		targetKind:     "verifier",
		targetGroup:    1,
		field:          "provider",
		recommendation: "create the referenced verifier provider or update verifier.provider to an existing provider id",
	},
	{
		code:           "verifier_modes_required",
		pattern:        regexp.MustCompile(`^verifier\.modes is required when verifier is enabled$`),
		targetKind:     "verifier",
		field:          "modes",
		recommendation: "set verifier.modes to one or more modes such as fix or audit",
	},
	{
		code:           "auxiliary_provider_missing",
		pattern:        regexp.MustCompile(`^(cost_control\.(router|summarizer)) references unknown provider "([^"]+)"$`),
		targetKind:     "cost_control",
		targetGroup:    1,
		field:          "provider",
		recommendation: "create the referenced low-cost provider or update this cost_control route provider",
	},
	{
		code:           "mcp_name_required",
		pattern:        regexp.MustCompile(`^enabled mcp server name is required$`),
		targetKind:     "mcp_server",
		field:          "name",
		recommendation: "set a stable name for every enabled MCP server",
	},
	{
		code:           "mcp_duplicate_name",
		pattern:        regexp.MustCompile(`^enabled mcp server ([^\s]+) is defined more than once$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "name",
		recommendation: "keep one MCP server with this name or rename duplicate entries",
	},
	{
		code:           "mcp_command_required",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) command is required$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "command",
		recommendation: "set command to the MCP server executable",
	},
	{
		code:           "mcp_workdir_required",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) workdir is required$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "workdir",
		recommendation: "set workdir to a runtime-home relative or absolute directory for this MCP server",
	},
	{
		code:           "mcp_restart_limit_invalid",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) restart_limit must be greater than 0$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "restart_limit",
		recommendation: "set restart_limit to a positive number",
	},
	{
		code:           "mcp_cooldown_invalid",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) cooldown must be greater than 0$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "cooldown",
		recommendation: "set cooldown to a positive duration",
	},
	{
		code:           "mcp_max_request_bytes_invalid",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) max_request_bytes must be greater than 0$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "max_request_bytes",
		recommendation: "set max_request_bytes to a positive byte limit",
	},
	{
		code:           "mcp_max_response_bytes_invalid",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) max_response_bytes must be greater than 0$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "max_response_bytes",
		recommendation: "set max_response_bytes to a positive byte limit",
	},
	{
		code:           "mcp_command_allowlist_missing",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) must define allowed_commands or allowed_command_paths$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "allowed_commands",
		recommendation: "add allowed_commands for a fixed executable or allowed_command_paths for a controlled directory before enabling this MCP server",
	},
	{
		code:           "mcp_command_not_allowed",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) command "([^"]+)" is not in allowed_commands$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "allowed_commands",
		recommendation: "add the exact command to allowed_commands or change command to an allowed executable",
	},
	{
		code:           "mcp_command_path_not_allowed",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) command "([^"]+)" is outside allowed_command_paths$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "allowed_command_paths",
		recommendation: "move the command under an allowed path or add a narrower allowed_command_paths entry",
	},
	{
		code:           "mcp_isolation_invalid",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) isolation "[^"]+" is invalid; expected none, process_group, windows_job, windows_restricted_token, linux_cgroup, linux_netns, or container$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "isolation",
		recommendation: "set isolation to none, process_group, windows_job, windows_restricted_token, linux_cgroup, linux_netns, or container",
	},
	{
		code:           "mcp_windows_appcontainer_unimplemented",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) isolation "[^"]+" is not supported; use isolation: container for the supported Docker/Podman sandbox boundary$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "isolation",
		recommendation: "use isolation: container for the supported enforceable sandbox boundary; AppContainer-like values are intentionally rejected",
	},
	{
		code:           "mcp_isolation_options_unsupported",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) isolation_options are only supported with linux_cgroup, linux_netns, or container$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "isolation_options",
		recommendation: "remove isolation_options or switch isolation to linux_cgroup, linux_netns, or container",
	},
	{
		code:           "mcp_container_image_missing",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) isolation container requires isolation_options\.image$|^mcp server ([^\s]+) isolation_options\.image is required for container isolation$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "isolation_options.image",
		recommendation: "set isolation_options.image to a trusted, version-pinned Docker/Podman image",
	},
	{
		code:           "mcp_container_network_conflict",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) isolation_options\.network conflicts with network_disabled; use disabled/none or clear network_disabled$`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "isolation_options.network",
		recommendation: "set isolation_options.network to disabled/none when network_disabled is true, or clear network_disabled for an explicitly networked container",
	},
	{
		code:           "mcp_container_option_invalid",
		pattern:        regexp.MustCompile(`^mcp server ([^\s]+) isolation_options\.([^\s]+) is invalid`),
		targetKind:     "mcp_server",
		targetGroup:    1,
		field:          "isolation_options",
		recommendation: "fix the invalid isolation_options field according to the container, linux_cgroup, or linux_netns option schema",
	},
}

func (r *configDiagnosticsResponse) addConfigLoadFailureDiagnostics(err error, path string) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	r.addDiagnosticWithRecommendation("error", "config_load_failed", message, path, "fix the config error and reload diagnostics; recognized errors are also reported with target_kind, target_name, field, and recommendation metadata")
	for _, item := range configLoadDiagnosticPatterns {
		matches := item.pattern.FindStringSubmatch(message)
		if len(matches) == 0 {
			continue
		}
		target := patternMatchGroup(matches, item.targetGroup)
		if target == "" && item.code == "mcp_container_image_missing" {
			target = patternMatchGroup(matches, 2)
		}
		field := item.field
		if item.code == "mcp_container_option_invalid" {
			if option := patternMatchGroup(matches, 2); option != "" {
				field = "isolation_options." + option
			}
		}
		r.addDiagnosticFor("error", item.code, message, path, item.targetKind, target, field, item.recommendation)
		return
	}
}

func patternMatchGroup(matches []string, index int) string {
	if index <= 0 || index >= len(matches) {
		return ""
	}
	return strings.TrimSpace(matches[index])
}

func (r *configDiagnosticsResponse) checkProviderDiagnostics(cfg *config.Config) {
	for name, provider := range cfg.Providers {
		if strings.TrimSpace(provider.APIKey) == "" {
			r.addDiagnosticFor("warning", "provider_api_key_missing", "provider "+name+" has no api_key after environment expansion", "", "provider", name, "api_key", "set api_key to an environment variable reference such as ${OPENAI_API_KEY}, then restart if this provider is already active")
		}
		if strings.TrimSpace(provider.FallbackProvider) != "" {
			if _, ok := cfg.Providers[provider.FallbackProvider]; !ok {
				r.addDiagnosticFor("error", "provider_fallback_missing", "provider "+name+" references unknown fallback provider "+provider.FallbackProvider, "", "provider", name, "fallback_provider", "create the referenced provider module or update fallback_provider to an existing provider id")
			}
			if provider.FallbackProvider == name {
				r.addDiagnosticFor("error", "provider_fallback_self", "provider "+name+" fallback_provider references itself", "", "provider", name, "fallback_provider", "clear fallback_provider or point it to a different provider")
			}
		}
	}
	for _, cycle := range providerFallbackCycles(cfg.Providers) {
		if len(cycle) < 2 {
			continue
		}
		name := cycle[0]
		r.addDiagnosticFor("error", "provider_fallback_cycle", "provider fallback chain contains a cycle: "+strings.Join(cycle, " -> "), "", "provider", name, "fallback_provider", "break the fallback cycle by clearing one fallback_provider or pointing it at a terminal backup provider")
	}
}

func (r *configDiagnosticsResponse) checkAgentDiagnostics(cfg *config.Config) {
	if _, ok := cfg.Agents[cfg.DefaultAgent]; !ok {
		r.addDiagnosticFor("error", "default_agent_missing", "default_agent "+cfg.DefaultAgent+" is not defined", "", "agent", cfg.DefaultAgent, "default_agent", "create the referenced agent module or update default_agent to an existing agent id")
	}
	knownTools := knownToolNameSet(cfg)
	if profile, ok := cfg.Agents[cfg.DefaultAgent]; ok {
		for _, kind := range profile.AllowedToolKinds {
			if kind == config.ToolKindWrite || kind == config.ToolKindExec {
				r.addDiagnosticFor("warning", "default_agent_broad_permissions", "default agent "+cfg.DefaultAgent+" has "+string(kind)+" capability; prefer routing write/exec work to a narrower specialist agent", "", "agent", cfg.DefaultAgent, "allowed_tool_kinds", "remove broad write/exec kinds from the default chat agent and route file-changing work to a specialist fixer agent")
			}
		}
	}
	for name, profile := range cfg.Agents {
		if _, ok := cfg.Providers[profile.Provider]; !ok {
			r.addDiagnosticFor("error", "agent_provider_missing", "agent "+name+" references unknown provider "+profile.Provider, "", "agent", name, "provider", "create the provider module or update this agent provider to an existing provider id")
		}
		if profile.ToolPolicy == config.ToolPolicyAllow {
			for _, kind := range profile.AllowedToolKinds {
				if kind == config.ToolKindWrite || kind == config.ToolKindExec || kind == config.ToolKindNetwork || kind == config.ToolKindUnknown {
					r.addDiagnosticFor("warning", "agent_risky_tool_policy_allow", "agent "+name+" allows "+string(kind)+" tools without confirmation", "", "agent", name, "tool_policy", "use tool_policy: confirm for agents with write, exec, network, or unknown tool capabilities unless this profile is strictly sandboxed")
					break
				}
			}
		}
		for _, toolName := range sortedTrimmedStrings(profile.AllowedTools) {
			if !toolNameKnown(toolName, knownTools) {
				r.addDiagnosticFor("warning", "agent_allowed_tool_unseen", "agent "+name+" allowed_tools references tool "+toolName+" that is not currently configured or discovered", "", "agent", name, "allowed_tools", "check the tool name, use the qualified server/tool form, or enable the matching MCP server before relying on this allowlist")
			}
		}
	}
}

func (r *configDiagnosticsResponse) checkToolRiskPolicyDiagnostics(cfg *config.Config) {
	if cfg == nil {
		return
	}
	policy := cfg.ToolRiskPolicy
	if !policy.RequireApprovalForUnsandboxedRiskyTools &&
		!policy.DisableRememberForUnsandboxedRiskyTools &&
		!policy.RejectUnsandboxedRiskyTools {
		r.addDiagnosticFor("info", "tool_risk_policy_disabled", "tool_risk_policy is disabled; agent tool_policy and approval scopes control risky MCP calls", "", "tool_risk_policy", "tool_risk_policy", "tool_risk_policy", "enable require_approval_for_unsandboxed_risky_tools when untrusted MCP servers may be configured")
		return
	}
	if policy.RequireApprovalForUnsandboxedRiskyTools {
		r.addDiagnosticFor("info", "tool_risk_policy_require_approval", "tool_risk_policy forces approval for unsandboxed write, exec, network, and unknown-kind tools even when an agent uses tool_policy: allow", "", "tool_risk_policy", "tool_risk_policy", "require_approval_for_unsandboxed_risky_tools", "use /api/runtime.tool_risk_policy.recognized_sandbox_boundaries to explain which isolation modes satisfy this policy by tool kind")
	}
	if policy.DisableRememberForUnsandboxedRiskyTools {
		r.addDiagnosticFor("info", "tool_risk_policy_disable_remember", "tool_risk_policy disables approve-and-remember and approve-tools auto approval for unsandboxed risky tools", "", "tool_risk_policy", "tool_risk_policy", "disable_remember_for_unsandboxed_risky_tools", "keep this enabled when remembered approvals should not bypass sandbox review for host-process tools")
	} else if policy.RequireApprovalForUnsandboxedRiskyTools && !policy.RejectUnsandboxedRiskyTools {
		r.addDiagnosticFor("warning", "tool_risk_policy_remember_allowed", "tool_risk_policy requires approval for unsandboxed risky tools but still allows approve-and-remember", "", "tool_risk_policy", "tool_risk_policy", "disable_remember_for_unsandboxed_risky_tools", "set disable_remember_for_unsandboxed_risky_tools: true when approve-and-remember should not persist approval for unsandboxed risky tools")
	}
	if policy.RejectUnsandboxedRiskyTools {
		r.addDiagnosticFor("warning", "tool_risk_policy_reject_unsandboxed", "tool_risk_policy rejects unsandboxed risky tools before execution", "", "tool_risk_policy", "tool_risk_policy", "reject_unsandboxed_risky_tools", "enable this only for deployments that require enforced sandbox boundaries such as container, or linux_netns for network-only tools")
	}
	for _, server := range cfg.MCP {
		if !server.Enabled {
			continue
		}
		name := strings.TrimSpace(server.Name)
		if name == "" {
			name = "unnamed"
		}
		isolation := strings.ToLower(strings.TrimSpace(server.Isolation))
		if isolation == "" {
			isolation = "none"
		}
		switch isolation {
		case "container":
			r.addDiagnosticFor("info", "tool_risk_policy_mcp_broad_sandbox", "mcp server "+name+" uses container isolation, which satisfies tool_risk_policy for write, exec, network, and unknown-kind tools", "", "mcp_server", name, "isolation", "still review image provenance, mounts, network mode, and resource limits before trusting the container boundary")
		case "linux_netns":
			r.addDiagnosticFor("info", "tool_risk_policy_mcp_network_sandbox", "mcp server "+name+" uses linux_netns, which satisfies tool_risk_policy for network-kind tools only", "", "mcp_server", name, "isolation", "linux_netns satisfies tool_risk_policy for network-kind tools only; use isolation: container when this server exposes write, exec, or unknown-kind tools that also need a broad sandbox boundary")
		default:
			message := "tool_risk_policy will treat risky tools from mcp server " + name + " as unsandboxed because isolation " + isolation + " is not a broad sandbox boundary"
			recommendation := "use isolation: container for a broad enforced boundary, or linux_netns only for network-only tools on Linux"
			if policy.RejectUnsandboxedRiskyTools {
				message = "tool_risk_policy strict rejection can block risky tools from mcp server " + name + " because isolation " + isolation + " is not a broad sandbox boundary"
				recommendation = "switch this server to isolation: container before enabling reject_unsandboxed_risky_tools if its tools must run"
			}
			r.addDiagnosticFor("warning", "tool_risk_policy_mcp_unsandboxed", message, "", "mcp_server", name, "isolation", recommendation)
		}
	}
}

func providerFallbackCycles(providers map[string]config.LLMConfig) [][]string {
	out := make([][]string, 0)
	reported := make(map[string]struct{})
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, start := range names {
		seen := map[string]int{}
		chain := make([]string, 0, len(providers)+1)
		current := start
		for strings.TrimSpace(current) != "" {
			index, ok := seen[current]
			if ok {
				cycle := append([]string(nil), chain[index:]...)
				cycle = append(cycle, current)
				key := canonicalCycleKey(cycle)
				if _, duplicate := reported[key]; !duplicate {
					reported[key] = struct{}{}
					out = append(out, cycle)
				}
				break
			}
			provider, ok := providers[current]
			if !ok {
				break
			}
			seen[current] = len(chain)
			chain = append(chain, current)
			current = strings.TrimSpace(provider.FallbackProvider)
		}
	}
	return out
}

func canonicalCycleKey(cycle []string) string {
	unique := make([]string, 0, len(cycle))
	seen := map[string]struct{}{}
	for _, item := range cycle {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		unique = append(unique, item)
	}
	sort.Strings(unique)
	return strings.Join(unique, "\x00")
}

func knownToolNameSet(cfg *config.Config) map[string]struct{} {
	out := make(map[string]struct{})
	if cfg == nil {
		return out
	}
	for _, server := range cfg.MCP {
		name := strings.TrimSpace(server.Name)
		if name == "" || !server.Enabled {
			continue
		}
		out[strings.ToLower(name)] = struct{}{}
	}
	return out
}

func toolNameKnown(tool string, known map[string]struct{}) bool {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if tool == "" {
		return true
	}
	if _, ok := known[tool]; ok {
		return true
	}
	if strings.Contains(tool, "/") {
		server := strings.TrimSpace(strings.SplitN(tool, "/", 2)[0])
		_, ok := known[server]
		return ok
	}
	for server := range known {
		if strings.HasPrefix(tool, server+"_") || strings.HasPrefix(tool, server+"-") {
			return true
		}
	}
	return false
}

func (r *configDiagnosticsResponse) checkMCPDiagnostics(cfg *config.Config) {
	if cfg == nil {
		return
	}
	for _, server := range cfg.MCP {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			name = "unnamed"
		}
		isolation := strings.ToLower(strings.TrimSpace(server.Isolation))
		options := normalizedIsolationOptions(server.IsolationOptions)
		networkEnforced := (isolation == "container" && containerSummaryNetworkMode(options["network"], server.NetworkDisabled) == "none") || isolation == "linux_netns"
		if server.NetworkDisabled && !networkEnforced {
			r.addDiagnosticFor("warning", "mcp_network_disabled_advisory", "mcp server "+name+" sets network_disabled; this is an advisory declaration and does not install OS-level network filtering", "", "mcp_server", name, "network_disabled", "use isolation: container with network disabled or isolation: linux_netns when the host supports it to enforce network egress blocking")
		}
		if len(sortedTrimmedStrings(server.EnvAllowlist)) == 0 {
			r.addDiagnosticFor("info", "mcp_env_allowlist_implicit_minimal", "mcp server "+name+" has no env_allowlist; only minimal platform variables plus GOFLOW_WORKSPACE_ROOT are passed to the child process", "", "mcp_server", name, "env_allowlist", "leave env_allowlist empty for maximum isolation, or explicitly add only required names such as PATH, HOME, proxy variables, or runtime cache paths")
		}
		if sensitive := sensitiveEnvAllowlistEntries(server.EnvAllowlist); len(sensitive) > 0 {
			r.addDiagnosticFor("warning", "mcp_env_allowlist_sensitive", "mcp server "+name+" env_allowlist includes sensitive variable names: "+strings.Join(sensitive, ", "), "", "mcp_server", name, "env_allowlist", "remove API keys, tokens, passwords, private keys, and cloud credentials unless this MCP server must call that service directly")
		}
		for _, allowed := range sortedTrimmedStrings(server.AllowedCommands) {
			if shellLikeCommand(allowed) {
				r.addDiagnosticFor("warning", "mcp_allowed_command_shell", "mcp server "+name+" allowed_commands includes shell-capable command "+allowed, "", "mcp_server", name, "allowed_commands", "prefer a dedicated executable or absolute allowed_command_paths entry instead of shell-capable commands for MCP startup")
			}
		}
		switch isolation {
		case "", "none":
			r.addDiagnosticFor("info", "mcp_isolation_none", "mcp server "+name+" has no process isolation adapter configured; workspace/tool policy boundaries still apply", "", "mcp_server", name, "isolation", "use container isolation for untrusted MCP servers, or keep none only for trusted local tools")
		case "process_group":
			r.addDiagnosticFor("info", "mcp_isolation_lifecycle_only", "mcp server "+name+" uses process_group lifecycle isolation; this is not a filesystem, network, privilege, or container sandbox", "", "mcp_server", name, "isolation", "use isolation: container when the tool needs a real filesystem or privilege boundary")
		case "windows_job":
			r.addDiagnosticFor("info", "mcp_isolation_lifecycle_only", "mcp server "+name+" uses windows_job lifecycle isolation; this is not a filesystem, network, privilege, or container sandbox", "", "mcp_server", name, "isolation", "use isolation: container when the tool needs a real filesystem or privilege boundary")
			r.addDiagnosticFor("info", "mcp_windows_job_lifecycle_only", "mcp server "+name+" uses a Windows Job Object for process-tree cleanup only; restricted tokens are not enabled", "", "mcp_server", name, "isolation", "treat windows_job as lifecycle cleanup only; use isolation: container when the tool needs a real Windows permission boundary")
		case "windows_restricted_token":
			r.addDiagnosticFor("info", "mcp_windows_restricted_token", "mcp server "+name+" uses a Windows restricted token with low integrity for privilege reduction plus Job Object lifecycle cleanup", "", "mcp_server", name, "isolation", "treat this as privilege reduction and process-tree cleanup only; use container isolation when filesystem or network boundaries are required")
		case "linux_cgroup":
			r.addDiagnosticFor("info", "mcp_isolation_resource_only", "mcp server "+name+" uses linux_cgroup resource controls; this does not restrict filesystem access, network access, or privileges", "", "mcp_server", name, "isolation", "combine resource controls with container isolation or another filesystem/network boundary for untrusted tools")
		case "linux_netns":
			r.addDiagnosticFor("info", "mcp_isolation_network_only", "mcp server "+name+" uses linux_netns network namespace isolation; this restricts network egress but does not restrict filesystem access or privileges", "", "mcp_server", name, "isolation", "combine linux_netns with container isolation or host sandboxing when filesystem or privilege boundaries are required")
		case "container":
			if networkEnforced {
				r.addDiagnosticFor("info", "mcp_isolation_container", "mcp server "+name+" uses container isolation with network disabled; effective boundary still depends on image, mounts, runtime policy, and configured resource limits", "", "mcp_server", name, "isolation", "review image provenance, workspace mount mode, resource limits, and capability settings before trusting this boundary")
			} else {
				r.addDiagnosticFor("warning", "mcp_isolation_container_network_open", "mcp server "+name+" uses container isolation but network egress is not disabled", "", "mcp_server", name, "isolation_options.network", "set isolation_options.network to disabled or set network_disabled: true unless this tool needs outbound network access")
			}
			image := strings.TrimSpace(options["image"])
			imageReferenceType := containerImageReferenceType(image)
			imageDetails := map[string]string{
				"image_reference_type": imageReferenceType,
				"digest_pinned":        strconv.FormatBool(imageReferenceType == "digest"),
			}
			if image != "" {
				imageDetails["image"] = image
			}
			productionProfile := strings.EqualFold(strings.TrimSpace(server.IsolationProfile), "production")
			if containerImageUsesFloatingTag(image) {
				r.addDiagnosticForDetails("warning", "mcp_container_image_floating_tag", "mcp server "+name+" container image is not pinned to a stable tag or digest", "", "mcp_server", name, "isolation_options.image", "pin the image to a versioned tag or digest, for example image: repo/tool:1.2.3 or image: repo/tool@sha256:...", imageDetails)
			} else if image != "" && !containerImageUsesDigestPinning(image) {
				r.addDiagnosticForDetails("warning", "mcp_container_image_not_digest_pinned", "mcp server "+name+" container image is not pinned by digest", "", "mcp_server", name, "isolation_options.image", "pin the image to an immutable digest for production, for example image: repo/tool@sha256:...", imageDetails)
			}
			if productionProfile && image != "" && !containerImageUsesDigestPinning(image) {
				r.addDiagnosticForDetails("warning", "mcp_container_production_image_not_digest_pinned", "mcp server "+name+" uses the production container profile without a digest-pinned image", "", "mcp_server", name, "isolation_options.image", "production-profile MCP containers should use image@sha256:... references and pre-pulled trusted images", imageDetails)
			}
			pullPolicy := strings.ToLower(strings.TrimSpace(options["pull_policy"]))
			if productionProfile && pullPolicy != "never" {
				details := cloneStringMap(imageDetails)
				details["pull_policy"] = pullPolicy
				if pullPolicy == "" {
					details["pull_policy"] = "runtime_default"
				}
				r.addDiagnosticForDetails("warning", "mcp_container_production_pull_policy_not_never", "mcp server "+name+" uses the production container profile without pull_policy: never", "", "mcp_server", name, "isolation_options.pull_policy", "set isolation_options.pull_policy: never for production-profile MCP containers after pre-pulling trusted digest-pinned images", details)
			}
			if strings.EqualFold(containerSummaryNetworkMode(options["network"], server.NetworkDisabled), "host") {
				r.addDiagnosticFor("warning", "mcp_container_host_network", "mcp server "+name+" container uses host networking", "", "mcp_server", name, "isolation_options.network", "avoid network: host for MCP servers unless the tool explicitly requires host networking and the operator accepts that boundary")
			}
			workspaceMount := strings.ToLower(strings.TrimSpace(options["workspace_mount"]))
			if workspaceMount == "" {
				workspaceMount = "rw"
			}
			if workspaceMount == "rw" || workspaceMount == "readwrite" {
				r.addDiagnosticFor("warning", "mcp_container_workspace_rw", "mcp server "+name+" mounts the workspace read-write inside the container", "", "mcp_server", name, "isolation_options.workspace_mount", "use workspace_mount: ro for read-only tools, workspace_mount: none when workspace access is unnecessary, or keep rw only for tools that must write files")
			}
			if !containerResourceLimitsComplete(options) {
				r.addDiagnosticFor("warning", "mcp_container_resource_limits_missing", "mcp server "+name+" container resource limits are incomplete", "", "mcp_server", name, "isolation_options", "set isolation_options.memory, isolation_options.memory_swap, isolation_options.cpus, and isolation_options.pids_limit to bound runaway or compromised tool processes")
			}
			if strings.TrimSpace(options["pull_policy"]) == "" {
				r.addDiagnosticFor("info", "mcp_container_pull_policy_missing", "mcp server "+name+" container does not set an image pull policy", "", "mcp_server", name, "isolation_options.pull_policy", "set isolation_options.pull_policy to missing for normal use or never for production environments that pre-pull trusted images")
			}
			user := strings.TrimSpace(options["user"])
			if user == "" && !containerHasWritableMounts(options) {
				r.addDiagnosticFor("info", "mcp_container_user_missing", "mcp server "+name+" container does not set a non-root user", "", "mcp_server", name, "isolation_options.user", "set isolation_options.user to a non-root UID:GID supported by the image when the MCP server does not need root")
			} else if user != "" && !containerUserLooksNonRoot(user) {
				r.addDiagnosticFor("warning", "mcp_container_user_root", "mcp server "+name+" container explicitly runs as root", "", "mcp_server", name, "isolation_options.user", "set isolation_options.user to a non-root UID:GID, or remove the explicit root user only when writable bind mounts require host-compatible permissions")
			}
			if !optionEnabled(options["no_new_privileges"]) {
				r.addDiagnosticFor("info", "mcp_container_no_new_privileges_missing", "mcp server "+name+" container does not set no_new_privileges", "", "mcp_server", name, "isolation_options.no_new_privileges", "set isolation_options.no_new_privileges: true when the MCP server does not need privilege transitions")
			}
			if !containerCapDropIncludesAll(options["cap_drop"]) {
				r.addDiagnosticFor("info", "mcp_container_cap_drop_missing", "mcp server "+name+" container does not drop all Linux capabilities", "", "mcp_server", name, "isolation_options.cap_drop", "set isolation_options.cap_drop: all when the MCP server does not need Linux capabilities")
			}
			if !optionEnabled(options["readonly_rootfs"]) {
				r.addDiagnosticFor("info", "mcp_container_readonly_rootfs_missing", "mcp server "+name+" container root filesystem is not marked read-only", "", "mcp_server", name, "isolation_options.readonly_rootfs", "set isolation_options.readonly_rootfs: true when the image does not need to write outside declared mounts")
			} else if strings.TrimSpace(options["tmpfs"]) == "" {
				r.addDiagnosticFor("info", "mcp_container_tmpfs_missing", "mcp server "+name+" container has readonly_rootfs enabled without declared tmpfs scratch paths", "", "mcp_server", name, "isolation_options.tmpfs", "add isolation_options.tmpfs for intentional writable scratch paths such as /tmp:rw,noexec,nosuid,size=64m when the MCP server needs temporary files")
			}
			if !optionEnabled(options["init"]) {
				r.addDiagnosticFor("info", "mcp_container_init_missing", "mcp server "+name+" container does not enable an init process", "", "mcp_server", name, "isolation_options.init", "set isolation_options.init: true when the MCP server may spawn child processes that need reaping")
			}
			if !strings.EqualFold(strings.TrimSpace(options["ipc"]), "none") {
				r.addDiagnosticFor("info", "mcp_container_ipc_not_isolated", "mcp server "+name+" container does not explicitly disable IPC sharing", "", "mcp_server", name, "isolation_options.ipc", "set isolation_options.ipc: none for ordinary MCP tools that do not need shared memory or host IPC")
			}
			if !containerUserNamespaceConfigured(options["userns"]) {
				r.addDiagnosticFor("info", "mcp_container_userns_missing", "mcp server "+name+" container does not configure a user namespace", "", "mcp_server", name, "isolation_options.userns", "set isolation_options.userns: auto or nomap on compatible Docker/Podman runtimes to reduce host UID/GID exposure")
			}
			toolSource := strings.TrimSpace(options["tool_source"])
			toolTarget := strings.TrimSpace(options["tool_target"])
			if (toolSource == "") != (toolTarget == "") {
				r.addDiagnosticFor("error", "mcp_container_tool_mount_incomplete", "mcp server "+name+" container tool mount sets only one of tool_source or tool_target", "", "mcp_server", name, "isolation_options.tool_source", "set both isolation_options.tool_source and isolation_options.tool_target, or remove both when no single-tool bind mount is needed")
			}
			toolMount := strings.ToLower(strings.TrimSpace(options["tool_mount"]))
			if toolMount == "rw" || toolMount == "readwrite" {
				r.addDiagnosticFor("warning", "mcp_container_tool_mount_rw", "mcp server "+name+" mounts the generated tool file read-write inside the container", "", "mcp_server", name, "isolation_options.tool_mount", "use tool_mount: ro for generated MCP tool files unless the container must modify the tool file itself")
			}
		}
	}
}

func shellLikeCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	command = strings.ReplaceAll(command, "\\", "/")
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(command), ".exe"))
	switch base {
	case "sh", "bash", "zsh", "fish", "cmd", "powershell", "pwsh", "wscript", "cscript":
		return true
	default:
		return false
	}
}

func containerImageUsesFloatingTag(image string) bool {
	return containerImageReferenceType(image) == "floating"
}

func containerImageUsesDigestPinning(image string) bool {
	return containerImageReferenceType(image) == "digest"
}

func containerImageReferenceType(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return "missing"
	}
	if strings.Contains(strings.ToLower(image), "@sha256:") {
		return "digest"
	}
	last := image
	if idx := strings.LastIndex(last, "/"); idx >= 0 {
		last = last[idx+1:]
	}
	tagIndex := strings.LastIndex(last, ":")
	if tagIndex < 0 || tagIndex == len(last)-1 {
		return "floating"
	}
	if strings.EqualFold(last[tagIndex+1:], "latest") {
		return "floating"
	}
	return "version_tag"
}

func containerHasWritableMounts(options map[string]string) bool {
	workspaceMount := strings.ToLower(strings.TrimSpace(options["workspace_mount"]))
	if workspaceMount == "" {
		workspaceMount = "rw"
	}
	if workspaceMount == "rw" || workspaceMount == "readwrite" {
		return true
	}
	toolMount := strings.ToLower(strings.TrimSpace(options["tool_mount"]))
	return toolMount == "rw" || toolMount == "readwrite"
}

func containerResourceLimitsComplete(options map[string]string) bool {
	return strings.TrimSpace(options["memory"]) != "" &&
		strings.TrimSpace(options["memory_swap"]) != "" &&
		strings.TrimSpace(options["cpus"]) != "" &&
		strings.TrimSpace(options["pids_limit"]) != ""
}

func containerCapDropIncludesAll(value string) bool {
	for _, item := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		if strings.EqualFold(strings.TrimSpace(item), "all") {
			return true
		}
	}
	return false
}

func containerUserLooksNonRoot(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	user, _, _ := strings.Cut(value, ":")
	return !containerUserIdentityLooksRoot(user)
}

func containerUserIdentityLooksRoot(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "root" {
		return true
	}
	id, err := strconv.ParseUint(value, 10, 64)
	return err == nil && id == 0
}

func (r *configDiagnosticsResponse) checkWorkflowDiagnostics(runtimeRef *agent.Runtime, cfg *config.Config) {
	if runtimeRef == nil {
		return
	}
	agentNames := make(map[string]struct{}, len(cfg.Agents))
	for name := range cfg.Agents {
		agentNames[name] = struct{}{}
	}
	skillNames := make(map[string]struct{})
	for _, item := range runtimeRef.SkillList() {
		skillNames[item.Name] = struct{}{}
	}
	toolNames := make(map[string]struct{})
	for _, name := range runtimeRef.ToolNames() {
		toolNames[name] = struct{}{}
	}
	runner := runtimeRef.WorkflowRunner()
	for _, summary := range runner.ListWorkflowGraphs() {
		if !summary.Valid {
			r.addDiagnostic("error", "workflow_invalid", summary.Error, summary.Path)
			continue
		}
		doc, err := runner.LoadWorkflowGraphDocument(summary.Name)
		if err != nil {
			r.addDiagnostic("error", "workflow_load_failed", err.Error(), summary.Path)
			continue
		}
		for _, stage := range doc.Stages {
			if isNonExecutableWorkflowNode(stage.NodeType) {
				continue
			}
			if _, ok := agentNames[stage.Agent]; !ok {
				r.addDiagnostic("error", "workflow_agent_missing", "workflow "+doc.Name+" stage "+stage.Name+" references unknown agent "+stage.Agent, summary.Path)
			}
			if _, ok := skillNames[stage.Skill]; !ok {
				r.addDiagnostic("error", "workflow_skill_missing", "workflow "+doc.Name+" stage "+stage.Name+" references unknown skill "+stage.Skill, summary.Path)
			}
			if strings.TrimSpace(stage.Tool) != "" {
				if _, ok := toolNames[stage.Tool]; !ok {
					if workflowToolReferenceLooksLikeCommand(stage.Tool) {
						r.addOptionalNonActionableDiagnosticFor("info", "workflow_tool_command_hint", "workflow "+doc.Name+" stage "+stage.Name+" uses command-like tool metadata "+stage.Tool+"; prefer params.verification_command or a discovered MCP Tool id", summary.Path, "workflow", doc.Name, "tool", "move command examples to params.verification_command, or replace tool with a discovered Tool metadata id when the stage should prefer a real MCP tool")
						continue
					}
					r.addDiagnosticFor("warning", "workflow_tool_unseen", "workflow "+doc.Name+" stage "+stage.Name+" references tool metadata "+stage.Tool+" that is not currently discovered", summary.Path, "workflow", doc.Name, "tool", "use a discovered Tool metadata id in tool, or move command examples to params.verification_command")
				}
			}
		}
	}
}

func workflowToolReferenceLooksLikeCommand(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if len(strings.Fields(value)) > 1 {
		return true
	}
	return strings.ContainsAny(value, "|&;<>()")
}

func isNonExecutableWorkflowNode(nodeType string) bool {
	switch strings.ToLower(strings.TrimSpace(nodeType)) {
	case "start", "end", "condition", "switch", "router", "policy_guard", "guard", "quality_gate", "quality_guard", "quality-guard", "parallel", "fan_out", "fork", "join", "merge", "barrier", "checkpoint", "manual_approval", "approval_gate", "input_gate", "manual_input", "for_each", "foreach", "map", "loop", "until", "while", "sub_workflow", "subworkflow", "workflow", "team", "agent_team", "team_template":
		return true
	default:
		return false
	}
}

func configRestartRequired(runtimeRef *agent.Runtime, cfg *config.Config) bool {
	return len(configRestartAffectedKinds(runtimeRef, cfg)) > 0
}

func configApplySummaryForLoadError(configPath, workspaceRoot, message string) configApplySummary {
	return configApplySummary{
		State:              "error",
		RestartRequired:    false,
		RestartCommandHint: configRestartCommandHint(configPath, workspaceRoot),
		Message:            strings.TrimSpace(message),
	}
}

func configApplySummaryForRuntime(runtimeRef *agent.Runtime, cfg *config.Config, configPath, workspaceRoot string) configApplySummary {
	affectedKinds := configRestartAffectedKinds(runtimeRef, cfg)
	if len(affectedKinds) == 0 {
		return configApplySummary{
			State:                       "active",
			RestartRequired:             false,
			RuntimeRebootstrapSupported: false,
			HotReloadSupported:          false,
			Message:                     "saved config matches the active runtime",
		}
	}
	return configApplySummary{
		State:                       "restart_required",
		RestartRequired:             true,
		RuntimeRebootstrapSupported: false,
		HotReloadSupported:          false,
		RestartCommandHint:          configRestartCommandHint(configPath, workspaceRoot),
		RebootstrapBlockers:         configRebootstrapBlockers(affectedKinds),
		AffectedKinds:               affectedKinds,
		Message:                     "saved config differs from the active runtime; restart GoFlow before relying on saved provider, agent, or MCP server edits",
	}
}

func configRestartAffectedKinds(runtimeRef *agent.Runtime, cfg *config.Config) []string {
	if runtimeRef == nil || cfg == nil {
		return nil
	}
	affected := make([]string, 0, 3)
	if !sameProviderConfig(runtimeRef, cfg.Providers) {
		affected = append(affected, "provider")
	}
	if !sameAgentConfig(runtimeRef, cfg.Agents) {
		affected = append(affected, "agent")
	}
	if !sameMCPServerConfig(runtimeRef, cfg.MCP) {
		affected = append(affected, "mcp_server")
	}
	return affected
}

func configRestartCommandHint(configPath, workspaceRoot string) []string {
	args := []string{"goflow"}
	if strings.TrimSpace(configPath) != "" {
		args = append(args, "--config", strings.TrimSpace(configPath))
	}
	if strings.TrimSpace(workspaceRoot) != "" {
		args = append(args, "--workspace", strings.TrimSpace(workspaceRoot))
	}
	if len(args) == 1 {
		return nil
	}
	return args
}

func configRebootstrapBlockers(affectedKinds []string) []string {
	blockers := make([]string, 0, len(affectedKinds)+1)
	for _, kind := range affectedKinds {
		switch kind {
		case "provider":
			blockers = append(blockers, "providers_bound_to_runtime_clients")
		case "agent":
			blockers = append(blockers, "agents_bound_to_runtime_profiles")
		case "mcp_server":
			blockers = append(blockers, "mcp_servers_bound_to_child_processes")
		}
	}
	if len(blockers) > 0 {
		blockers = append(blockers, "session_approvals_and_tool_policy_depend_on_runtime_catalog")
	}
	return blockers
}

func sameProviderConfig(runtimeRef interface {
	ProviderNames() []string
	Provider(string) (config.LLMConfig, bool)
}, providers map[string]config.LLMConfig) bool {
	names := runtimeRef.ProviderNames()
	if len(names) != len(providers) {
		return false
	}
	for _, name := range names {
		current, ok := runtimeRef.Provider(name)
		if !ok {
			return false
		}
		if !reflect.DeepEqual(current, providers[name]) {
			return false
		}
	}
	return true
}

func sameAgentConfig(runtimeRef interface {
	AgentNames() []string
	Profile(string) (config.AgentProfile, bool)
}, agents map[string]config.AgentProfile) bool {
	names := runtimeRef.AgentNames()
	if len(names) != len(agents) {
		return false
	}
	for _, name := range names {
		current, ok := runtimeRef.Profile(name)
		if !ok {
			return false
		}
		if !reflect.DeepEqual(current, agents[name]) {
			return false
		}
	}
	return true
}

func sameMCPServerConfig(runtimeRef interface {
	MCPServerRefs() []config.MCPServerRef
}, servers []config.MCPServerRef) bool {
	current := normalizedMCPServerMap(runtimeRef.MCPServerRefs())
	saved := normalizedMCPServerMap(servers)
	if len(current) != len(saved) {
		return false
	}
	for name, currentServer := range current {
		savedServer, ok := saved[name]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(currentServer, savedServer) {
			return false
		}
	}
	return true
}

func normalizedMCPServerMap(servers []config.MCPServerRef) map[string]config.MCPServerRef {
	out := make(map[string]config.MCPServerRef, len(servers))
	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			continue
		}
		server.Name = name
		server.Command = strings.TrimSpace(server.Command)
		server.WorkDir = strings.TrimSpace(server.WorkDir)
		server.Isolation = strings.TrimSpace(server.Isolation)
		server.Args = append([]string(nil), server.Args...)
		server.EnvAllowlist = sortedTrimmedStrings(server.EnvAllowlist)
		server.AllowedCommandPaths = sortedTrimmedStrings(server.AllowedCommandPaths)
		server.AllowedCommands = sortedTrimmedStrings(server.AllowedCommands)
		if len(server.IsolationOptions) > 0 {
			options := make(map[string]string, len(server.IsolationOptions))
			for key, value := range server.IsolationOptions {
				key = strings.TrimSpace(key)
				if key == "" {
					continue
				}
				options[key] = strings.TrimSpace(value)
			}
			server.IsolationOptions = options
		}
		out[name] = server
	}
	return out
}

func sortedTrimmedStrings(values []string) []string {
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

func (r *configDiagnosticsResponse) addDiagnostic(severity, code, message, path string) {
	r.addDiagnosticFor(severity, code, message, path, "", "", "", "")
}

func (r *configDiagnosticsResponse) addDiagnosticWithRecommendation(severity, code, message, path, recommendation string) {
	r.addDiagnosticFor(severity, code, message, path, "", "", "", recommendation)
}

func (r *configDiagnosticsResponse) addDiagnosticFor(severity, code, message, path, targetKind, targetName, field, recommendation string) {
	r.addDiagnosticForDetails(severity, code, message, path, targetKind, targetName, field, recommendation, nil)
}

func (r *configDiagnosticsResponse) addDiagnosticForDetails(severity, code, message, path, targetKind, targetName, field, recommendation string, details map[string]string) {
	r.Items = append(r.Items, configDiagnosticItem{
		Severity:       severity,
		Code:           code,
		Message:        message,
		Recommendation: strings.TrimSpace(recommendation),
		Path:           path,
		TargetKind:     strings.TrimSpace(targetKind),
		TargetName:     strings.TrimSpace(targetName),
		Field:          strings.TrimSpace(field),
		Details:        cloneStringMap(details),
	})
}

func (r *configDiagnosticsResponse) addOptionalNonActionableDiagnosticFor(severity, code, message, path, targetKind, targetName, field, recommendation string) {
	actionable := false
	r.Items = append(r.Items, configDiagnosticItem{
		Severity:       severity,
		Code:           code,
		Message:        message,
		Recommendation: strings.TrimSpace(recommendation),
		Path:           path,
		TargetKind:     strings.TrimSpace(targetKind),
		TargetName:     strings.TrimSpace(targetName),
		Field:          strings.TrimSpace(field),
		Category:       "optional",
		Optional:       true,
		Actionable:     &actionable,
	})
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r *configDiagnosticsResponse) finalize() {
	status := "ok"
	r.Diagnostics = diagnosticSeverityCount{}
	for _, item := range r.Items {
		r.Diagnostics.Total++
		switch item.Severity {
		case "error":
			r.Diagnostics.Errors++
			r.Status = "error"
			status = "error"
		case "warning":
			r.Diagnostics.Warnings++
			if status != "error" {
				status = "warning"
			}
		default:
			r.Diagnostics.Info++
		}
	}
	if status == "error" {
		r.Status = "error"
		return
	}
	r.Status = status
}
