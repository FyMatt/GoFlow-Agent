package api

import (
	"fmt"
	"net/http"

	"github.com/FyMatt/GoFlow-Agent/internal/version"
)

const (
	apiDiscoverySchema        = "goflow.api.discovery"
	apiDiscoverySchemaVersion = 1
)

type helpResponse struct {
	Meta                 apiDiscoveryMetadata     `json:"meta"`
	Commands             []helpCommand            `json:"commands"`
	Capabilities         map[string]bool          `json:"capabilities"`
	ResourceCatalog      helpResourceCatalog      `json:"resource_catalog,omitempty"`
	ClientContracts      []helpClientContract     `json:"client_contracts,omitempty"`
	ResourceCapabilities []helpResourceCapability `json:"resource_capabilities,omitempty"`
	MCPIsolationModes    []mcpIsolationMode       `json:"mcp_isolation_modes,omitempty"`
	Parity               []helpParityItem         `json:"parity,omitempty"`
	Coverage             helpCoverage             `json:"coverage"`
}

type capabilityResponse struct {
	Meta                 apiDiscoveryMetadata     `json:"meta"`
	Capabilities         map[string]bool          `json:"capabilities"`
	ResourceCatalog      helpResourceCatalog      `json:"resource_catalog,omitempty"`
	ClientContracts      []helpClientContract     `json:"client_contracts,omitempty"`
	ResourceCapabilities []helpResourceCapability `json:"resource_capabilities,omitempty"`
	MCPIsolationModes    []mcpIsolationMode       `json:"mcp_isolation_modes,omitempty"`
}

type apiDiscoveryMetadata struct {
	Schema                    string `json:"schema"`
	SchemaVersion             int    `json:"schema_version"`
	MinSupportedSchemaVersion int    `json:"min_supported_schema_version"`
	RuntimeVersion            string `json:"runtime_version"`
	CacheKey                  string `json:"cache_key"`
}

type helpCommand struct {
	Category    string `json:"category"`
	Command     string `json:"command"`
	Description string `json:"description"`
	Method      string `json:"method,omitempty"`
	Path        string `json:"path,omitempty"`
	StreamPath  string `json:"stream_path,omitempty"`
}

type helpParityEndpoint struct {
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
	StreamPath string `json:"stream_path,omitempty"`
}

type helpParityItem struct {
	Category          string               `json:"category"`
	Capability        string               `json:"capability"`
	CLI               []string             `json:"cli,omitempty"`
	HTTP              []helpParityEndpoint `json:"http,omitempty"`
	Status            string               `json:"status"`
	Notes             string               `json:"notes,omitempty"`
	RequiresWorkspace bool                 `json:"requires_workspace,omitempty"`
	Durable           bool                 `json:"durable,omitempty"`
	RestartRequired   bool                 `json:"restart_required,omitempty"`
}

type helpCoverage struct {
	Total       int `json:"total"`
	Implemented int `json:"implemented"`
	Partial     int `json:"partial"`
	Planned     int `json:"planned"`
}

type helpResourceCatalog struct {
	Schema      string `json:"schema"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	CacheKey    string `json:"cache_key,omitempty"`
}

type helpClientContract struct {
	Area              string   `json:"area"`
	Summary           string   `json:"summary,omitempty"`
	State             string   `json:"state,omitempty"`
	PrimaryEndpoints  []string `json:"primary_endpoints,omitempty"`
	StreamEndpoints   []string `json:"stream_endpoints,omitempty"`
	ActionEndpoints   []string `json:"action_endpoints,omitempty"`
	RequiredBehavior  []string `json:"required_behavior,omitempty"`
	ReconnectStrategy string   `json:"reconnect_strategy,omitempty"`
	RestartBehavior   string   `json:"restart_behavior,omitempty"`
	WorkspaceBehavior string   `json:"workspace_behavior,omitempty"`
	Notes             []string `json:"notes,omitempty"`
}

type helpResourceCapability struct {
	Kind                  string               `json:"kind"`
	DisplayName           string               `json:"display_name,omitempty"`
	Description           string               `json:"description,omitempty"`
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
	Notes                 []string             `json:"notes,omitempty"`
	Actions               []helpResourceAction `json:"actions,omitempty"`
}

type helpResourceAction struct {
	Name                  string `json:"name"`
	Label                 string `json:"label,omitempty"`
	Description           string `json:"description,omitempty"`
	Method                string `json:"method,omitempty"`
	Path                  string `json:"path,omitempty"`
	RequiresSavedResource bool   `json:"requires_saved_resource,omitempty"`
	Destructive           bool   `json:"destructive,omitempty"`
	Returns               string `json:"returns,omitempty"`
}

func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parity := helpParity()
	writeJSON(w, helpResponse{
		Meta:                 apiDiscoveryMeta(),
		Commands:             helpCommands(),
		Capabilities:         helpCapabilities(),
		ResourceCatalog:      helpResourceCatalogInfo(),
		ClientContracts:      helpClientContracts(),
		ResourceCapabilities: helpResourceCapabilities(),
		MCPIsolationModes:    mcpIsolationModes(),
		Parity:               parity,
		Coverage:             helpCoverageFor(parity),
	})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, capabilityResponse{
		Meta:                 apiDiscoveryMeta(),
		Capabilities:         helpCapabilities(),
		ResourceCatalog:      helpResourceCatalogInfo(),
		ClientContracts:      helpClientContracts(),
		ResourceCapabilities: helpResourceCapabilities(),
		MCPIsolationModes:    mcpIsolationModes(),
	})
}

func apiDiscoveryMeta() apiDiscoveryMetadata {
	return apiDiscoveryMetadata{
		Schema:                    apiDiscoverySchema,
		SchemaVersion:             apiDiscoverySchemaVersion,
		MinSupportedSchemaVersion: 1,
		RuntimeVersion:            version.Version,
		CacheKey:                  fmt.Sprintf("%s:v%d:%s", apiDiscoverySchema, apiDiscoverySchemaVersion, version.Version),
	}
}

func helpResourceCatalogInfo() helpResourceCatalog {
	return helpResourceCatalog{
		Schema:      resourceCatalogSchema,
		Path:        "/api/resources",
		Description: "Read-only resource-family catalog with counts, capabilities, apply-state, and Studio entry paths.",
		CacheKey:    fmt.Sprintf("%s:v%d:%s", resourceCatalogSchema, resourceCatalogSchemaVersion, version.Version),
	}
}

func helpCommands() []helpCommand {
	return []helpCommand{
		{Category: "chat", Command: "message", Description: "Run the active agent once.", Method: http.MethodPost, Path: "/api/run", StreamPath: "/api/run/stream"},
		{Category: "runtime", Command: "/status", Description: "Inspect runtime, session, tools, agents, skills, setup, and cost diagnostics.", Method: http.MethodGet, Path: "/api/runtime"},
		{Category: "runtime", Command: "/cost", Description: "Inspect only prompt budget and token cost diagnostics without fetching the full runtime snapshot.", Method: http.MethodGet, Path: "/api/runtime/cost"},
		{Category: "runtime", Command: "/config-diagnostics [--json]", Description: "Reload-validate saved config modules and show setup, restart, and MCP hardening diagnostics.", Method: http.MethodGet, Path: "/api/config/diagnostics"},
		{Category: "runtime", Command: "/agent <id>", Description: "Switch the active agent.", Method: http.MethodPost, Path: "/api/runtime/agent"},
		{Category: "runtime", Command: "/trace on|off", Description: "Toggle trace/status verbosity for runtime streams.", Method: http.MethodPost, Path: "/api/runtime/trace"},
		{Category: "workspace", Command: "/workspace status", Description: "Inspect the active workspace and confirmation state.", Method: http.MethodGet, Path: "/api/workspace"},
		{Category: "workspace", Command: "/workspace confirm", Description: "Confirm the active workspace for file-affecting work.", Method: http.MethodPost, Path: "/api/workspace/confirm"},
		{Category: "workspace", Command: "/workspace clear", Description: "Clear workspace confirmation so file-affecting work asks again.", Method: http.MethodPost, Path: "/api/workspace/clear"},
		{Category: "workspace", Command: "/workspace use <path>", Description: "Confirm the same workspace, dynamically rebind when supported, or return restart guidance for a different workspace.", Method: http.MethodPost, Path: "/api/workspace/select"},
		{Category: "workspace", Command: "/workspace choose", Description: "Open an explicit opt-in local host folder picker and return the selected workspace path.", Method: http.MethodPost, Path: "/api/workspace/pick-folder"},
		{Category: "workspace", Command: "workspace requirement", Description: "Preflight whether an input or operation needs confirmed workspace access.", Method: http.MethodPost, Path: "/api/workspace/requirement"},
		{Category: "workspace", Command: "@file", Description: "Resolve workspace-scoped file references and suggestions.", Method: http.MethodGet, Path: "/api/workspace-files"},
		{Category: "chat", Command: "runs", Description: "List ordinary Agent and workflow run history in one filtered envelope for Studio history views; supports type/status/agent/tool/action/query filters.", Method: http.MethodGet, Path: "/api/runs"},
		{Category: "chat", Command: "run context", Description: "Lazy-load compact prompt budget, memory block, artifact ref, and tool-schema visibility diagnostics for one Agent or workflow run.", Method: http.MethodGet, Path: "/api/runs/{id}/context"},
		{Category: "chat", Command: "agent runs", Description: "List, replay, cancel, or reconnect to durable ordinary Agent runs started with background=true.", Method: http.MethodGet, Path: "/api/agent-runs", StreamPath: "/api/agent-runs/{id}/events/stream?since=0"},
		{Category: "chat", Command: "agent context", Description: "Lazy-load compact prompt budget, memory block, artifact ref, and tool-schema visibility diagnostics for one ordinary Agent run.", Method: http.MethodGet, Path: "/api/agent-runs/{id}/context"},
		{Category: "chat", Command: "agent replay", Description: "Replay or export one durable ordinary Agent run with events, timeline, filters, and actions.", Method: http.MethodGet, Path: "/api/agent-runs/{id}/replay"},
		{Category: "chat", Command: "agent run diffs", Description: "List parsed write/delete diffs from one durable ordinary Agent run.", Method: http.MethodGet, Path: "/api/agent-runs/{id}/diffs"},
		{Category: "chat", Command: "agent run export", Description: "Export one durable ordinary Agent run as JSON or Markdown.", Method: http.MethodGet, Path: "/api/agent-runs/{id}/export?format=json|md"},
		{Category: "workflow", Command: "/workflow <name>", Description: "Run a workflow synchronously, as a stream, or as a durable background run.", Method: http.MethodPost, Path: "/api/workflows/{name}", StreamPath: "/api/workflows/{name}/stream"},
		{Category: "workflow", Command: "workflow expression validation", Description: "Validate workflow references and expressions for Studio node forms, optionally using run_id output hints.", Method: http.MethodPost, Path: "/api/workflow-graphs/validate-expression"},
		{Category: "workflow", Command: "/expression-helpers [name]", Description: "List supported workflow expression helpers with signatures, argument metadata, and examples.", Method: http.MethodGet, Path: "/api/workflow-expression-functions"},
		{Category: "workflow", Command: "/workflow-schemas [name] [--rebuild|--json|--export|--import <path>|--clear]", Description: "Inspect, export, import, rebuild, or clear observed workflow output schemas used by Studio completions.", Method: http.MethodGet, Path: "/api/workflow-schemas"},
		{Category: "workflow", Command: "workflow runs", Description: "List persisted workflow runs.", Method: http.MethodGet, Path: "/api/workflow-runs"},
		{Category: "workflow", Command: "workflow replay", Description: "Replay one run with stages, events, artifacts, actions, navigation, counts, collaboration, blackboard, and team state.", Method: http.MethodGet, Path: "/api/workflow-runs/{id}/replay"},
		{Category: "workflow", Command: "workflow context", Description: "Lazy-load compact prompt budget, memory block, artifact ref, and tool-schema visibility diagnostics for one workflow run.", Method: http.MethodGet, Path: "/api/workflow-runs/{id}/context"},
		{Category: "workflow", Command: "workflow evidence", Description: "Load an evidence/provenance graph for one workflow run with quality checks, artifacts, files, refs, and edges.", Method: http.MethodGet, Path: "/api/workflow-runs/{id}/evidence"},
		{Category: "workflow", Command: "workflow navigation", Description: "Load frontend-ready stage navigation, anchors, paths, and per-stage badges for one workflow run.", Method: http.MethodGet, Path: "/api/workflow-runs/{id}/navigation"},
		{Category: "workflow", Command: "workflow run diffs", Description: "List parsed write/delete diffs from one workflow run.", Method: http.MethodGet, Path: "/api/workflow-runs/{id}/diffs"},
		{Category: "workflow", Command: "workflow run export", Description: "Export one workflow run as JSON or Markdown.", Method: http.MethodGet, Path: "/api/workflow-runs/{id}/export?format=json|md"},
		{Category: "workflow", Command: "workflow events", Description: "Reconnectable workflow event stream with since or Last-Event-ID cursors.", Method: http.MethodGet, StreamPath: "/api/workflow-runs/{id}/events/stream?since=0"},
		{Category: "approval", Command: "/approve <call-id>", Description: "Approve a pending ordinary or workflow tool call when live context is available.", Method: http.MethodPost, Path: "/api/approvals/{callID}/approve", StreamPath: "/api/approvals/{callID}/approve/stream"},
		{Category: "approval", Command: "/deny <call-id>", Description: "Deny a pending ordinary or workflow tool call.", Method: http.MethodPost, Path: "/api/approvals/{callID}/deny"},
		{Category: "resources", Command: "resource catalog", Description: "List resource families, counts, capabilities, apply-state, diagnostics, and Studio entry paths.", Method: http.MethodGet, Path: "/api/resources"},
		{Category: "resources", Command: "/agents", Description: "List, create, update, or delete modular agent definitions.", Method: http.MethodGet, Path: "/api/resources/agents"},
		{Category: "resources", Command: "agent validation", Description: "Validate and normalize a modular agent definition without saving it.", Method: http.MethodPost, Path: "/api/resources/agents/{id}/validate"},
		{Category: "resources", Command: "/skills", Description: "List, create, update, or delete skills and bundled skill files.", Method: http.MethodGet, Path: "/api/resources/skills"},
		{Category: "resources", Command: "skill validation", Description: "Validate and normalize a skill document without saving SKILL.md.", Method: http.MethodPost, Path: "/api/resources/skills/{name}/validate"},
		{Category: "resources", Command: "/tools", Description: "List MCP tools and manage modular MCP server configs.", Method: http.MethodGet, Path: "/api/resources/tools"},
		{Category: "resources", Command: "tool validation", Description: "Validate and normalize a modular MCP tool server without saving code or config.", Method: http.MethodPost, Path: "/api/resources/tools/{name}/validate"},
		{Category: "resources", Command: "tool scaffolds", Description: "List or create local and containerized Python MCP tool presets.", Method: http.MethodGet, Path: "/api/resources/tools/scaffolds"},
		{Category: "resources", Command: "providers", Description: "List, create, update, or delete modular model provider configs.", Method: http.MethodGet, Path: "/api/resources/providers"},
		{Category: "resources", Command: "provider validation", Description: "Validate and normalize a modular model provider definition without saving it.", Method: http.MethodPost, Path: "/api/resources/providers/{id}/validate"},
		{Category: "resources", Command: "/policy-rules [name]", Description: "List built-in and file-backed workflow policy rules, or inspect one rule.", Method: http.MethodGet, Path: "/api/resources/policy-rules"},
		{Category: "resources", Command: "/new-policy-rule <preset> <name>", Description: "Create a validated workflow policy rule from a reusable guard preset.", Method: http.MethodPost, Path: "/api/resources/policy-rules/scaffolds/{preset}"},
		{Category: "resources", Command: "workflow schema resources", Description: "List, save, validate, activate, capture, or delete reusable workflow schema resources.", Method: http.MethodGet, Path: "/api/resources/workflow-schemas"},
		{Category: "resources", Command: "workflow schema resource validation", Description: "Validate and normalize a reusable workflow schema resource without saving or activating it.", Method: http.MethodPost, Path: "/api/resources/workflow-schemas/{name}/validate"},
		{Category: "resources", Command: "/workflow-templates [name]", Description: "List built-in and file-backed workflow templates, or inspect one template.", Method: http.MethodGet, Path: "/api/resources/workflow-templates"},
		{Category: "resources", Command: "/new-workflow-template <source> <name>", Description: "Fork a built-in or loaded workflow template into a reusable file-backed template.", Method: http.MethodPost, Path: "/api/resources/workflow-templates/{name}/fork"},
		{Category: "resources", Command: "workflow template resources", Description: "List, save, validate, capture, fork, or delete reusable workflow template resources.", Method: http.MethodGet, Path: "/api/resources/workflow-templates"},
		{Category: "resources", Command: "workflow template resource validation", Description: "Validate and normalize a reusable workflow template resource without saving it.", Method: http.MethodPost, Path: "/api/resources/workflow-templates/{name}/validate"},
		{Category: "resources", Command: "/workflow-node-metadata [type]", Description: "List, save, validate, or delete Studio metadata for existing workflow node types.", Method: http.MethodGet, Path: "/api/resources/workflow-node-metadata"},
		{Category: "resources", Command: "workflow node metadata validation", Description: "Validate and normalize workflow node Studio metadata without saving it.", Method: http.MethodPost, Path: "/api/resources/workflow-node-metadata/{type}/validate"},
		{Category: "resources", Command: "expression helper metadata", Description: "List, save, validate, or delete Studio metadata for existing workflow expression helpers.", Method: http.MethodGet, Path: "/api/resources/expression-helpers"},
		{Category: "resources", Command: "expression helper metadata validation", Description: "Validate and normalize expression helper Studio metadata without saving it.", Method: http.MethodPost, Path: "/api/resources/expression-helpers/{name}/validate"},
		{Category: "resources", Command: "policy rule scaffolds", Description: "List or create reusable workflow policy rule presets.", Method: http.MethodGet, Path: "/api/resources/policy-rules/scaffolds"},
		{Category: "resources", Command: "policy rule validation", Description: "Validate and normalize a custom workflow policy rule without saving it.", Method: http.MethodPost, Path: "/api/resources/policy-rules/{name}/validate"},
		{Category: "resources", Command: "/teams [name]", Description: "List reusable multi-agent team templates, or inspect one template.", Method: http.MethodGet, Path: "/api/resources/team-templates"},
		{Category: "resources", Command: "/new-team <preset> <name>", Description: "Create a validated multi-agent team template from a reusable collaboration preset.", Method: http.MethodPost, Path: "/api/resources/team-templates/scaffolds/{preset}"},
		{Category: "resources", Command: "team template resources", Description: "List, save, or delete reusable multi-agent team template resources.", Method: http.MethodGet, Path: "/api/resources/team-templates"},
		{Category: "resources", Command: "team template validation", Description: "Validate and normalize a reusable multi-agent team template without saving it.", Method: http.MethodPost, Path: "/api/resources/team-templates/{name}/validate"},
		{Category: "resources", Command: "team template scaffolds", Description: "List or create reusable multi-agent team template presets.", Method: http.MethodGet, Path: "/api/resources/team-templates/scaffolds"},
		{Category: "resources", Command: "/kits [name] [--export] | /kits --import <path>", Description: "List, inspect, export, or import packaged vertical Agent kits.", Method: http.MethodGet, Path: "/api/kits"},
		{Category: "resources", Command: "vertical kit resources", Description: "List, save, validate, export, import, or delete packaged vertical Agent kits.", Method: http.MethodGet, Path: "/api/resources/kits"},
		{Category: "resources", Command: "/new-kit <preset> <name>", Description: "Create a vertical Agent kit manifest from a reusable preset.", Method: http.MethodPost, Path: "/api/resources/kits/scaffolds/{preset}"},
		{Category: "resources", Command: "vertical kit validation", Description: "Validate and normalize a vertical kit draft without saving it.", Method: http.MethodPost, Path: "/api/resources/kits/{name}/validate"},
		{Category: "resources", Command: "vertical kit bundle export", Description: "Export a kit plus referenced resource documents as JSON or YAML.", Method: http.MethodGet, Path: "/api/resources/kits/{name}/export"},
		{Category: "resources", Command: "vertical kit bundle import", Description: "Import a kit bundle into the file-backed resource catalog.", Method: http.MethodPost, Path: "/api/resources/kits/import"},
		{Category: "collaboration", Command: "team state", Description: "Inspect derived multi-agent team state for a run.", Method: http.MethodGet, Path: "/api/team-state"},
		{Category: "collaboration", Command: "blackboard", Description: "List and update durable shared blackboard entries.", Method: http.MethodGet, Path: "/api/collaboration/blackboard"},
	}
}

func helpCapabilities() map[string]bool {
	return map[string]bool{
		"background_agent_runs":                              true,
		"agent_run_event_reconnect":                          true,
		"agent_run_action_background":                        true,
		"agent_run_scoped_tool_approvals":                    true,
		"agent_run_durable_tool_approval":                    true,
		"approval_risk_metadata":                             true,
		"agent_run_diffs":                                    true,
		"agent_run_export":                                   true,
		"run_action_protocol_metadata":                       true,
		"unified_run_history":                                true,
		"unified_run_action_filter":                          true,
		"unified_run_action_facets":                          true,
		"background_workflow_runs":                           true,
		"workflow_event_reconnect":                           true,
		"workflow_action_background":                         true,
		"durable_run_sse_heartbeats":                         true,
		"durable_run_sse_retry_directive":                    true,
		"workflow_run_diffs":                                 true,
		"workflow_run_export":                                true,
		"workflow_run_evidence_graph":                        true,
		"workflow_run_replay_navigation":                     true,
		"workspace_requirement_preflight":                    true,
		"runtime_workspace_action_metadata":                  true,
		"workspace_switch_blocker_details":                   true,
		"workspace_switch_guidance":                          true,
		"workspace_scoped_at_refs":                           true,
		"runtime_trace_toggle":                               true,
		"resource_crud":                                      true,
		"resource_validation":                                true,
		"skill_resource_validation":                          true,
		"collaboration_blackboard":                           true,
		"team_state":                                         true,
		"prompt_cost_diagnostics":                            true,
		"runtime_cost_endpoint":                              true,
		"cost_control_feature_catalog":                       true,
		"config_diagnostics_field_targets":                   true,
		"config_diagnostics_severity_counts":                 true,
		"config_diagnostics_recommendations":                 true,
		"config_diagnostics_mcp_hardening":                   true,
		"config_diagnostics_tool_risk_policy":                true,
		"config_diagnostics_tool_risk_policy_mcp_boundaries": true,
		"config_diagnostics_apply_summary":                   true,
		"config_load_failure_field_targets":                  true,
		"mcp_isolation_mode_catalog":                         true,
		"mcp_isolation_mode_recommendations":                 true,
		"workflow_import_export":                             true,
		"workflow_validation_overlay":                        true,
		"workflow_parallel_validation":                       true,
		"workflow_expression_validation":                     true,
		"workflow_expression_functions":                      true,
		"workflow_schema_catalog":                            true,
		"workflow_schema_resources":                          true,
		"workflow_schema_resource_validation":                true,
		"workflow_template_resources":                        true,
		"workflow_template_fork":                             true,
		"workflow_template_resource_validation":              true,
		"workflow_node_metadata_resources":                   true,
		"workflow_node_metadata_validation":                  true,
		"expression_helper_metadata_resources":               true,
		"expression_helper_metadata_validation":              true,
		"policy_rule_validation":                             true,
		"policy_rule_scaffold_presets":                       true,
		"team_template_resources":                            true,
		"team_template_validation":                           true,
		"team_template_scaffold_presets":                     true,
		"kit_resources":                                      true,
		"kit_resource_validation":                            true,
		"kit_scaffold_presets":                               true,
		"kit_bundle_import_export":                           true,
		"tool_scaffold_presets":                              true,
		"container_tool_scaffold_presets":                    true,
		"container_tool_scaffold_hardened_defaults":          true,
		"container_isolation_profiles":                       true,
		"container_tool_scaffold_versioned_image_default":    true,
		"tool_scaffold_default_option_metadata":              true,
		"runtime_tool_risk_policy_status":                    true,
		"tool_risk_policy_sandbox_boundaries":                true,
		"skill_bundled_file_editing":                         true,
		"modular_provider_agent_config":                      true,
		"resource_catalog_endpoint":                          true,
		"resource_capability_metadata":                       true,
		"client_contract_metadata":                           true,
		"http_cli_parity_matrix":                             true,
	}
}

func helpClientContracts() []helpClientContract {
	return []helpClientContract{
		{
			Area:             "ordinary_agent_runs",
			State:            "implemented",
			Summary:          "Browser clients should start long ordinary Agent work as durable background runs instead of binding execution to a tab request.",
			PrimaryEndpoints: []string{"POST /api/run", "GET /api/agent-runs", "GET /api/agent-runs/{id}", "GET /api/agent-runs/{id}/context", "GET /api/agent-runs/{id}/replay", "GET /api/agent-runs/{id}/timeline", "GET /api/agent-runs/{id}/diffs", "GET /api/agent-runs/{id}/export"},
			StreamEndpoints:  []string{"GET /api/agent-runs/{id}/events/stream?since=<seq>"},
			ActionEndpoints:  []string{"GET /api/agent-runs/{id}/actions", "POST /api/agent-runs/{id}/retry", "POST /api/agent-runs/{id}/cancel", "POST /api/agent-runs/{id}/approve-tool", "POST /api/agent-runs/{id}/approve-remember-tool", "POST /api/agent-runs/{id}/deny-tool", "POST /api/agent-runs/{id}/approve-tools", "POST /api/agent-runs/{id}/approve-remember-tools", "POST /api/agent-runs/{id}/deny-tools"},
			RequiredBehavior: []string{
				"send {background:true} for browser-safe long-running work",
				"store run_id and last received sequence number on the client",
				"use accepted response URLs for events, timeline, replay, actions, diffs, and cancel navigation instead of reconstructing paths",
				"lazy-load /context for prompt budget, memory block, artifact ref, and tool-schema visibility diagnostics instead of embedding diagnostics in list rows",
				"use /api/runs actions_summary.available_items for lightweight list-row buttons; fetch /actions for full risk/body_schema metadata before rendering detailed action forms",
				"use /api/runs?action=<name> or action_name=<name> to filter history rows by currently available list-row action",
				"use /api/runs facets.actions to render available-action filter chips and counts",
				"do not treat browser navigation or EventSource disconnect as cancellation",
				"use explicit cancel action when the user wants to stop a run",
				"render actions from the backend action discovery response instead of hard-coding availability",
				"use action kind, supports_background, supports_stream, accepts_body, requires_body, body_schema, and risk metadata when rendering buttons and forms",
				"ignore SSE comment heartbeat frames; they are only keep-alive signals, not business events",
				"honor SSE retry directives for EventSource reconnect timing when the client stack exposes them",
			},
			ReconnectStrategy: "reconnect to events/stream with since=<last_seq> or Last-Event-ID, honor retry directives, ignore idle heartbeat comments, then refresh replay or timeline for a complete snapshot",
		},
		{
			Area:             "workflow_runs",
			State:            "implemented",
			Summary:          "Workflow runs support durable background execution, replayable events, stage navigation, evidence, diffs, exports, retries, input gates, and approval resume.",
			PrimaryEndpoints: []string{"POST /api/workflows/{name}", "GET /api/workflow-runs", "GET /api/workflow-runs/{id}", "GET /api/workflow-runs/{id}/context", "GET /api/workflow-runs/{id}/replay", "GET /api/workflow-runs/{id}/navigation", "GET /api/workflow-runs/{id}/evidence", "GET /api/workflow-runs/{id}/diffs", "GET /api/workflow-runs/{id}/export"},
			StreamEndpoints:  []string{"GET /api/workflow-runs/{id}/events/stream?since=<seq>"},
			ActionEndpoints:  []string{"GET /api/workflow-runs/{id}/actions", "POST /api/workflow-runs/{id}/retry", "POST /api/workflow-runs/{id}/cancel", "POST /api/workflow-runs/{id}/input", "POST /api/workflow-runs/{id}/approve", "POST /api/workflow-runs/{id}/approve-tool", "POST /api/workflow-runs/{id}/deny-tool", "POST /api/workflow-runs/{id}/approve-tools"},
			RequiredBehavior: []string{
				"send {background:true} for browser-safe workflow execution",
				"preserve run_id and stage focus in the browser so users can return after navigation",
				"lazy-load /context for prompt budget, memory block, artifact ref, and tool-schema visibility diagnostics instead of embedding diagnostics in list rows",
				"use /api/runs actions_summary.available_items for lightweight list-row buttons; fetch /actions for full risk/body_schema metadata before rendering detailed action forms",
				"use /api/runs?action=<name> or action_name=<name> to filter history rows by currently available list-row action",
				"use /api/runs facets.actions to render available-action filter chips and counts",
				"consume pending_input and pending_input_fields from snapshots before rendering input forms",
				"consume action discovery for approval/retry/cancel/input availability",
				"use action kind, supports_background, supports_stream, accepts_body, requires_body, body_schema, and risk metadata when rendering buttons and forms",
				"refresh replay/evidence/diffs after terminal states or after an action completes",
				"ignore SSE comment heartbeat frames; they are only keep-alive signals, not workflow events",
				"honor SSE retry directives for EventSource reconnect timing when the client stack exposes them",
			},
			ReconnectStrategy: "reconnect to events/stream with since=<last_seq> or Last-Event-ID, honor retry directives, ignore idle heartbeat comments, then use replay for missed state and navigation for stage anchors",
		},
		{
			Area:             "approvals",
			State:            "implemented",
			Summary:          "Tool approvals are explicit user actions with risk metadata, scoped remember behavior, and durable run-scoped resume for supported ordinary Agent and workflow runs.",
			PrimaryEndpoints: []string{"GET /api/session", "GET /api/runtime", "GET /api/agent-runs/{id}/actions", "GET /api/workflow-runs/{id}/actions"},
			ActionEndpoints:  []string{"POST /api/approvals/{callID}/approve", "POST /api/approvals/{callID}/approve-remember", "POST /api/approvals/{callID}/deny", "POST /api/approvals/approve-all", "POST /api/agent-runs/{id}/approve-tool", "POST /api/agent-runs/{id}/approve-tools", "POST /api/workflow-runs/{id}/approve-tool", "POST /api/workflow-runs/{id}/approve-tools"},
			RequiredBehavior: []string{
				"show tool name, arguments summary, kind, risk, sandbox, destructive flags, and remember availability before approval",
				"read /api/runtime.tool_risk_policy to explain global risk-aware approval, remember, and rejection behavior",
				"use /api/runtime.tool_risk_policy.recognized_sandbox_boundaries to render policy-recognized sandbox modes by tool kind instead of parsing explanation text",
				"disable remember/approve-all UI when backend action discovery marks it unavailable",
				"treat deny/cancel as explicit user decisions and keep replay visible afterward",
				"prefer run-scoped approval endpoints when a durable run_id is present",
			},
			Notes: []string{"risk metadata is duplicated into pending approvals, stream events, runtime/session snapshots, and run snapshots where available", "/api/runtime.tool_risk_policy summarizes global policy switches and recognized sandbox boundaries; action discovery remains authoritative for each pending call"},
		},
		{
			Area:             "workspace",
			State:            "implemented",
			Summary:          "Pure chat can run without confirmed workspace access, but file, tool, command, and workflow operations must use an explicit confirmed workspace.",
			PrimaryEndpoints: []string{"GET /api/workspace", "POST /api/workspace/confirm", "POST /api/workspace/clear", "POST /api/workspace/select", "POST /api/workspace/pick-folder", "POST /api/workspace/requirement", "GET /api/workspace-files"},
			RequiredBehavior: []string{
				"preflight draft inputs with /api/workspace/requirement when the UI can predict workspace-sensitive work",
				"show workspace confirmation before file-affecting tasks",
				"treat selecting a different workspace according to /api/workspace switch_mode: dynamic_rebind can switch in-process, restart_required needs operator restart guidance",
				"prefer /api/workspace capabilities.guidance for preflight, select, conflict, refresh, and restart UI sequencing",
				"prefer switch_blocker_details and switch_plan.blocker_details for user-facing blocker messages; keep switch_blockers as stable code filters",
				"use /api/workspace/pick-folder only for local opt-in host picker flows, then submit the selected path to /api/workspace/select",
				"use /api/workspace-files for @file suggestions instead of traversing from the browser",
			},
			WorkspaceBehavior: "same-workspace confirmation is available; switching to a different root returns structured dynamic_rebind or restart_required metadata, blocker codes, guidance steps, and switch_blocker_details with message/recommendation/actionability fields",
		},
		{
			Area:             "configuration_apply",
			State:            "implemented",
			Summary:          "Provider, agent, and MCP server config edits are saved to file-backed modules but are runtime-bound and require restart before the active runtime trusts them.",
			PrimaryEndpoints: []string{"GET /api/config/diagnostics", "GET /api/help", "GET /api/resources/agents", "GET /api/resources/providers", "GET /api/resources/tools"},
			RequiredBehavior: []string{
				"read /api/help.resource_capabilities for editor badges and save expectations",
				"read /api/config/diagnostics.apply for current saved-vs-runtime apply state",
				"render tool_risk_policy_* diagnostics as global safety-policy guidance, including the warning state where approval is required but approve-and-remember remains allowed",
				"render tool_risk_policy_mcp_* diagnostics on each MCP server row to explain broad sandbox, network-only sandbox, or unsandboxed risk-policy treatment",
				"show restart guidance from restart_command_hint when apply.state is restart_required",
				"do not imply hot rebootstrap support for provider, agent, or MCP server edits",
			},
			RestartBehavior: "runtime_rebootstrap_supported and hot_reload_supported are false for provider, agent, and MCP server changes; restart GoFlow with the reported config/workspace command hint",
		},
		{
			Area:             "cost_control",
			State:            "implemented",
			Summary:          "Cost diagnostics expose measured token usage plus a machine-readable feature catalog for prompt budgeting, schema minimization, compaction, artifact refs, provider cache signals, and auxiliary helper routes.",
			PrimaryEndpoints: []string{"GET /api/runtime/cost", "GET /api/runtime"},
			RequiredBehavior: []string{
				"use cost.features to render enabled/observed/configured cost-control capabilities instead of parsing recommendation text",
				"treat auxiliary helper routes as extra model calls and only promote them when measurement shows net savings",
				"treat provider cached tokens as provider-reported prompt-cache telemetry; GoFlow does not store provider KV-cache tensors",
				"use cost.recommendations for operator guidance and cost.features for stable status badges",
			},
			Notes: []string{"cost.features is available from both /api/runtime.cost and lightweight /api/runtime/cost"},
		},
		{
			Area:             "resource_editing",
			State:            "implemented",
			Summary:          "File-backed resources expose a read-only catalog plus validation and save endpoints; clients should dry-run validate drafts before save and display backend issue codes/recommendations.",
			PrimaryEndpoints: []string{"GET /api/resources", "GET /api/help", "GET /api/resources/skills", "GET /api/resources/agents", "GET /api/resources/providers", "GET /api/resources/tools", "GET /api/resources/workflow-templates", "GET /api/resources/workflow-schemas", "GET /api/resources/policy-rules", "GET /api/resources/team-templates", "GET /api/resources/kits"},
			RequiredBehavior: []string{
				"use /api/resources for resource-family counts, apply-state badges, entry paths, diagnostics links, and scaffold/action availability",
				"discover editor support from /api/help.resource_capabilities",
				"use /api/capabilities.mcp_isolation_modes.options for MCP isolation option forms instead of hard-coding container/cgroup/netns fields",
				"call validate endpoints before save when the user is editing YAML/JSON/form data",
				"render issue severity, field, code, and recommendation without parsing natural-language text",
				"after saving agent/provider/tool resources, refresh /api/config/diagnostics before claiming the active runtime changed",
			},
		},
	}
}

func helpResourceCapabilities() []helpResourceCapability {
	return []helpResourceCapability{
		{
			Kind:                  "agent",
			DisplayName:           "Agents",
			Description:           "Runtime agent profiles used for chat, planning, fixing, audit, security, and custom roles.",
			CollectionPath:        "/api/resources/agents",
			DetailPath:            "/api/resources/agents/{id}",
			ValidatePath:          "/api/resources/agents/{id}/validate",
			StorageRoot:           "configs/agents",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			RestartRequiredOnSave: true,
			ApplyStateOnSave:      "restart_required",
			ApplyMessage:          "saved agent modules are loaded into runtime profiles during startup; restart GoFlow before relying on edits",
			DiagnosticsPath:       "/api/config/diagnostics",
			RelatedCapabilities:   []string{"modular_provider_agent_config", "config_diagnostics_apply_summary"},
		},
		{
			Kind:                  "provider",
			DisplayName:           "Providers",
			Description:           "Model provider routes and defaults used by agents, verifiers, and auxiliary model calls.",
			CollectionPath:        "/api/resources/providers",
			DetailPath:            "/api/resources/providers/{id}",
			ValidatePath:          "/api/resources/providers/{id}/validate",
			StorageRoot:           "configs/providers",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			RestartRequiredOnSave: true,
			ApplyStateOnSave:      "restart_required",
			ApplyMessage:          "saved provider modules are bound to LLM clients during startup; restart GoFlow before relying on edits",
			DiagnosticsPath:       "/api/config/diagnostics",
			RelatedCapabilities:   []string{"modular_provider_agent_config", "config_diagnostics_apply_summary"},
		},
		{
			Kind:                  "tool",
			DisplayName:           "MCP Tools",
			Description:           "File-backed MCP server modules and discovered MCP tool catalog entries.",
			CollectionPath:        "/api/resources/tools",
			DetailPath:            "/api/resources/tools/{name}",
			ValidatePath:          "/api/resources/tools/{name}/validate",
			ScaffoldPath:          "/api/resources/tools/scaffolds",
			StorageRoot:           "configs/mcp_servers + mcp_servers",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			CanScaffold:           true,
			RestartRequiredOnSave: true,
			ApplyStateOnSave:      "restart_required",
			ApplyMessage:          "saved MCP server modules start child processes during runtime bootstrap; restart GoFlow before relying on edits",
			DiagnosticsPath:       "/api/config/diagnostics",
			RelatedCapabilities:   []string{"tool_scaffold_presets", "container_tool_scaffold_presets", "container_tool_scaffold_hardened_defaults", "container_isolation_profiles", "container_tool_scaffold_versioned_image_default", "tool_scaffold_default_option_metadata", "config_diagnostics_mcp_hardening", "config_diagnostics_apply_summary"},
		},
		{
			Kind:                  "skill",
			DisplayName:           "Skills",
			Description:           "Skill instructions plus bundled references, scripts, assets, templates, and helper agents.",
			CollectionPath:        "/api/resources/skills",
			DetailPath:            "/api/resources/skills/{name}",
			ValidatePath:          "/api/resources/skills/{name}/validate",
			StorageRoot:           "skills",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			HotReloadSupported:    true,
			RestartRequiredOnSave: false,
			ApplyStateOnSave:      "hot_reload_when_supported",
			ApplyMessage:          "skill saves reload immediately when the configured skill manager supports reload; otherwise restart applies the file-backed skill",
			RelatedCapabilities:   []string{"skill_resource_validation", "skill_bundled_file_editing"},
		},
		{
			Kind:                  "workflow",
			DisplayName:           "Workflow Graphs",
			Description:           "Executable workflow graphs for agent, skill, tool, control-flow, team, and approval nodes.",
			CollectionPath:        "/api/workflow-graphs",
			DetailPath:            "/api/workflow-graphs/{name}",
			ValidatePath:          "/api/workflow-graphs/validate",
			StorageRoot:           "workflows",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			RestartRequiredOnSave: false,
			ApplyStateOnSave:      "active",
			ApplyMessage:          "workflow graph saves update the active workflow catalog without a runtime restart",
			RelatedCapabilities:   []string{"workflow_import_export", "workflow_validation_overlay"},
			Actions: []helpResourceAction{
				{Name: "import", Label: "Import graph", Description: "Import a workflow graph from JSON or YAML.", Method: http.MethodPost, Path: "/api/workflow-graphs/import", Returns: "workflow_graph"},
				{Name: "export", Label: "Export graph", Description: "Export one workflow graph as JSON or YAML.", Method: http.MethodGet, Path: "/api/workflow-graphs/{name}/export?format=json|yaml", RequiresSavedResource: true, Returns: "workflow_graph"},
			},
		},
		{
			Kind:                  "workflow_template",
			DisplayName:           "Workflow Templates",
			Description:           "Reusable workflow starter templates that can be forked into executable graphs.",
			CollectionPath:        "/api/resources/workflow-templates",
			DetailPath:            "/api/resources/workflow-templates/{name}",
			ValidatePath:          "/api/resources/workflow-templates/{name}/validate",
			ScaffoldPath:          "/api/resources/workflow-templates/{name}/fork",
			StorageRoot:           "templates/workflows",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			CanScaffold:           true,
			RestartRequiredOnSave: false,
			ApplyStateOnSave:      "active",
			ApplyMessage:          "workflow template saves update the template catalog without a runtime restart",
			RelatedCapabilities:   []string{"workflow_template_resources", "workflow_template_resource_validation", "workflow_template_fork"},
			Actions: []helpResourceAction{
				{Name: "capture", Label: "Capture template", Description: "Capture an existing workflow graph as a reusable template resource.", Method: http.MethodPost, Path: "/api/resources/workflow-templates/{name}/capture", Returns: "workflow_template"},
				{Name: "fork", Label: "Fork template", Description: "Fork a built-in or saved workflow template into a user-editable resource.", Method: http.MethodPost, Path: "/api/resources/workflow-templates/{name}/fork", Returns: "workflow_template"},
			},
		},
		{
			Kind:                  "workflow_schema",
			DisplayName:           "Workflow Schemas",
			Description:           "Reusable observed output schemas used by expression validation and Studio completions.",
			CollectionPath:        "/api/resources/workflow-schemas",
			DetailPath:            "/api/resources/workflow-schemas/{name}",
			ValidatePath:          "/api/resources/workflow-schemas/{name}/validate",
			StorageRoot:           "schemas/workflows",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			RestartRequiredOnSave: false,
			ApplyStateOnSave:      "active",
			ApplyMessage:          "workflow schema saves can be activated into the session catalog without a runtime restart",
			RelatedCapabilities:   []string{"workflow_schema_catalog", "workflow_schema_resources", "workflow_schema_resource_validation"},
			Actions: []helpResourceAction{
				{Name: "capture", Label: "Capture schema", Description: "Capture an observed workflow output schema as a reusable resource.", Method: http.MethodPost, Path: "/api/resources/workflow-schemas/{name}/capture", Returns: "workflow_schema_resource"},
				{Name: "activate", Label: "Activate schema", Description: "Activate a saved workflow schema resource into the session schema catalog.", Method: http.MethodPost, Path: "/api/resources/workflow-schemas/{name}/activate", RequiresSavedResource: true, Returns: "workflow_schema"},
				{Name: "export_catalog", Label: "Export schema catalog", Description: "Export observed workflow schemas as a JSON or YAML bundle.", Method: http.MethodGet, Path: "/api/workflow-schemas/export?format=json|yaml", Returns: "workflow_schema_bundle"},
				{Name: "import_catalog", Label: "Import schema catalog", Description: "Import observed workflow schemas into the session schema catalog.", Method: http.MethodPost, Path: "/api/workflow-schemas/import", Returns: "workflow_schema_import_result"},
			},
		},
		{
			Kind:                  "policy_rule",
			DisplayName:           "Policy Rules",
			Description:           "Reusable workflow guard rules for conditions, risk gates, minimum counts, and quorum checks.",
			CollectionPath:        "/api/resources/policy-rules",
			DetailPath:            "/api/resources/policy-rules/{name}",
			ValidatePath:          "/api/resources/policy-rules/{name}/validate",
			ScaffoldPath:          "/api/resources/policy-rules/scaffolds",
			StorageRoot:           "policies/workflow_rules",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			CanScaffold:           true,
			RestartRequiredOnSave: false,
			ApplyStateOnSave:      "active",
			ApplyMessage:          "policy rule saves update workflow option discovery without a runtime restart",
			RelatedCapabilities:   []string{"policy_rule_validation", "policy_rule_scaffold_presets"},
		},
		{
			Kind:                  "team_template",
			DisplayName:           "Team Templates",
			Description:           "Reusable multi-agent collaboration team templates with roles, handoffs, and review gates.",
			CollectionPath:        "/api/resources/team-templates",
			DetailPath:            "/api/resources/team-templates/{name}",
			ValidatePath:          "/api/resources/team-templates/{name}/validate",
			ScaffoldPath:          "/api/resources/team-templates/scaffolds",
			StorageRoot:           "templates/teams",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			CanScaffold:           true,
			RestartRequiredOnSave: false,
			ApplyStateOnSave:      "active",
			ApplyMessage:          "team template saves update team resource discovery without a runtime restart",
			RelatedCapabilities:   []string{"team_template_resources", "team_template_validation", "team_template_scaffold_presets"},
		},
		{
			Kind:                  "kit",
			DisplayName:           "Vertical Kits",
			Description:           "Packaged vertical Agent bundles referencing agents, providers, skills, tools, workflows, teams, policies, and examples.",
			CollectionPath:        "/api/resources/kits",
			DetailPath:            "/api/resources/kits/{name}",
			ValidatePath:          "/api/resources/kits/{name}/validate",
			ScaffoldPath:          "/api/resources/kits/scaffolds",
			StorageRoot:           "kits",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			CanScaffold:           true,
			RestartRequiredOnSave: false,
			ApplyStateOnSave:      "active_metadata",
			ApplyMessage:          "kit manifests are immediately visible, but referenced provider, agent, and MCP modules still follow their own restart-required rules",
			DiagnosticsPath:       "/api/config/diagnostics",
			RelatedCapabilities:   []string{"kit_resources", "kit_resource_validation", "kit_scaffold_presets", "kit_bundle_import_export"},
			Actions: []helpResourceAction{
				{Name: "export_bundle", Label: "Export kit bundle", Description: "Export a kit manifest plus available referenced resource documents as JSON or YAML.", Method: http.MethodGet, Path: "/api/resources/kits/{name}/export?format=json|yaml", RequiresSavedResource: true, Returns: "kit_bundle"},
				{Name: "import_bundle", Label: "Import kit bundle", Description: "Import a kit bundle into the file-backed resource catalog.", Method: http.MethodPost, Path: "/api/resources/kits/import", Returns: "kit_bundle_import_result"},
				{Name: "validate_saved", Label: "Validate saved kit", Description: "Validate whether a saved kit's references and required environment variables are available.", Method: http.MethodGet, Path: "/api/resources/kits/{name}/validate", RequiresSavedResource: true, Returns: "kit_validation"},
			},
		},
		{
			Kind:                  "workflow_node_metadata",
			DisplayName:           "Workflow Node Metadata",
			Description:           "Studio labels, fields, hints, examples, warnings, and outputs for workflow node types.",
			CollectionPath:        "/api/resources/workflow-node-metadata",
			DetailPath:            "/api/resources/workflow-node-metadata/{type}",
			ValidatePath:          "/api/resources/workflow-node-metadata/{type}/validate",
			StorageRoot:           "metadata/workflow_nodes",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			RestartRequiredOnSave: false,
			ApplyStateOnSave:      "active",
			ApplyMessage:          "workflow node metadata saves update Studio metadata discovery without a runtime restart",
			RelatedCapabilities:   []string{"workflow_node_metadata_resources", "workflow_node_metadata_validation"},
		},
		{
			Kind:                  "expression_helper_metadata",
			DisplayName:           "Expression Helper Metadata",
			Description:           "Studio documentation and examples for workflow expression helper functions.",
			CollectionPath:        "/api/resources/expression-helpers",
			DetailPath:            "/api/resources/expression-helpers/{name}",
			ValidatePath:          "/api/resources/expression-helpers/{name}/validate",
			StorageRoot:           "metadata/expression_helpers",
			FileBacked:            true,
			CanList:               true,
			CanCreate:             true,
			CanUpdate:             true,
			CanDelete:             true,
			CanValidate:           true,
			RestartRequiredOnSave: false,
			ApplyStateOnSave:      "active",
			ApplyMessage:          "expression helper metadata saves update Studio metadata discovery without a runtime restart",
			RelatedCapabilities:   []string{"expression_helper_metadata_resources", "expression_helper_metadata_validation"},
		},
	}
}

func helpParity() []helpParityItem {
	return []helpParityItem{
		{
			Category:   "chat",
			Capability: "Run ordinary Agent turns",
			CLI:        []string{"message", "/use <agent>", "/mode <mode>"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodPost, Path: "/api/run"},
				{Method: http.MethodPost, Path: "/api/run/stream", StreamPath: "/api/run/stream"},
				{Method: http.MethodPost, Path: "/api/runtime/agent"},
			},
			Status:  "implemented",
			Durable: true,
			Notes:   "Use POST /api/run or /api/run/stream with background=true for browser-safe durable runs; reconnect through /api/agent-runs/{id}/events/stream.",
		},
		{
			Category:   "chat",
			Capability: "Reconnect, replay, retry, cancel, export, and diff ordinary Agent runs",
			CLI:        []string{"/session", "/status", "Esc Esc", "continue/retry"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodGet, Path: "/api/agent-runs"},
				{Method: http.MethodGet, Path: "/api/agent-runs/{id}/context"},
				{Method: http.MethodGet, Path: "/api/agent-runs/{id}/replay"},
				{Method: http.MethodGet, Path: "/api/agent-runs/{id}/diffs"},
				{Method: http.MethodGet, Path: "/api/agent-runs/{id}/export?format=json|md"},
				{Method: http.MethodGet, StreamPath: "/api/agent-runs/{id}/events/stream?since=0"},
				{Method: http.MethodPost, Path: "/api/agent-runs/{id}/retry"},
				{Method: http.MethodPost, Path: "/api/agent-runs/{id}/cancel"},
			},
			Status:  "implemented",
			Durable: true,
			Notes:   "Session, runtime, Agent-run, and workflow-run snapshots annotate pending approvals with risk metadata when the backend can infer the tool profile.",
		},
		{
			Category:   "history",
			Capability: "Unified Agent and workflow run history",
			CLI:        []string{"/session", "/status"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodGet, Path: "/api/runs"},
				{Method: http.MethodGet, Path: "/api/runs/{id}/context"},
			},
			Status:  "implemented",
			Durable: true,
			Notes:   "The unified endpoint is HTTP-only convenience for Studio; item paths point to the typed run APIs, context_path lazy-loads cost/context diagnostics, and action/action_name filters match currently available backend-discovered actions.",
		},
		{
			Category:   "runtime",
			Capability: "Runtime status, inventory, trace, and cost diagnostics",
			CLI:        []string{"/status", "/agents", "/skills", "/tools", "/trace on|off", "/cost", "/config-diagnostics [--json]"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodGet, Path: "/api/runtime"},
				{Method: http.MethodGet, Path: "/api/runtime/cost"},
				{Method: http.MethodPost, Path: "/api/runtime/trace"},
				{Method: http.MethodGet, Path: "/api/config/diagnostics"},
			},
			Status: "implemented",
			Notes:  "/api/runtime also mirrors workspace_capabilities and workspace_actions for Studio workspace controls.",
		},
		{
			Category:   "workspace",
			Capability: "Workspace selection, confirmation, and @file references",
			CLI:        []string{"/workspace status", "/workspace confirm", "/workspace clear", "/workspace use <path>", "/workspace choose", "@file"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodGet, Path: "/api/workspace"},
				{Method: http.MethodPost, Path: "/api/workspace/confirm"},
				{Method: http.MethodPost, Path: "/api/workspace/clear"},
				{Method: http.MethodPost, Path: "/api/workspace/select"},
				{Method: http.MethodPost, Path: "/api/workspace/pick-folder"},
				{Method: http.MethodPost, Path: "/api/workspace/requirement"},
				{Method: http.MethodGet, Path: "/api/workspace-files"},
			},
			Status:            "implemented",
			RequiresWorkspace: true,
			RestartRequired:   false,
			Notes:             "Workspace switching is explicit. Use /api/workspace switch_mode to choose dynamic rebind or restart-required guidance. Host folder picking is local opt-in only and returns a path for the normal select flow.",
		},
		{
			Category:   "workflow",
			Capability: "Run workflows and reconnect to durable workflow runs",
			CLI:        []string{"/workflow <name> <request>", "/workflow skill-chain <request>"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodPost, Path: "/api/workflows/{name}"},
				{Method: http.MethodPost, Path: "/api/workflows/{name}/stream", StreamPath: "/api/workflows/{name}/stream"},
				{Method: http.MethodGet, Path: "/api/workflow-runs"},
				{Method: http.MethodGet, StreamPath: "/api/workflow-runs/{id}/events/stream?since=0"},
			},
			Status:            "implemented",
			RequiresWorkspace: true,
			Durable:           true,
		},
		{
			Category:   "workflow",
			Capability: "Workflow authoring, validation, templates, metadata, and schemas",
			CLI:        []string{"/new-workflow <name> [--template <template>]", "/workflow-templates [name]", "/workflow-node-metadata [type]", "/expression-helpers [name]", "/workflow-schemas [name] [--rebuild|--json|--export|--import <path>|--clear]", "/new-workflow-template <source> <name>"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodGet, Path: "/api/workflow-graphs"},
				{Method: http.MethodPost, Path: "/api/workflow-graphs"},
				{Method: http.MethodPost, Path: "/api/workflow-graphs/validate"},
				{Method: http.MethodPost, Path: "/api/workflow-graphs/validate-expression"},
				{Method: http.MethodGet, Path: "/api/workflow-templates"},
				{Method: http.MethodGet, Path: "/api/workflow-options"},
				{Method: http.MethodGet, Path: "/api/workflow-expression-functions"},
				{Method: http.MethodGet, Path: "/api/workflow-schemas"},
				{Method: http.MethodGet, Path: "/api/resources/workflow-templates"},
				{Method: http.MethodPost, Path: "/api/resources/workflow-templates/{name}/fork"},
			},
			Status:            "implemented",
			RequiresWorkspace: true,
		},
		{
			Category:   "workflow",
			Capability: "Workflow replay, navigation, artifacts, diffs, actions, and export",
			CLI:        []string{"/session", "/status"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodGet, Path: "/api/workflow-runs/{id}/replay"},
				{Method: http.MethodGet, Path: "/api/workflow-runs/{id}/context"},
				{Method: http.MethodGet, Path: "/api/workflow-runs/{id}/evidence"},
				{Method: http.MethodGet, Path: "/api/workflow-runs/{id}/navigation"},
				{Method: http.MethodGet, Path: "/api/workflow-runs/{id}/timeline"},
				{Method: http.MethodGet, Path: "/api/workflow-runs/{id}/diffs"},
				{Method: http.MethodGet, Path: "/api/workflow-runs/{id}/export?format=json|md"},
				{Method: http.MethodGet, Path: "/api/workflow-runs/{id}/actions"},
			},
			Status:  "implemented",
			Durable: true,
		},
		{
			Category:   "approval",
			Capability: "Approve, approve-and-remember, deny, approve-all, and resume paused work",
			CLI:        []string{"/approve <call-id>", "/deny <call-id>", "approval menu"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodPost, Path: "/api/approvals/{callID}/approve"},
				{Method: http.MethodPost, Path: "/api/approvals/{callID}/approve-remember"},
				{Method: http.MethodPost, Path: "/api/approvals/{callID}/deny"},
				{Method: http.MethodPost, Path: "/api/approvals/approve-all"},
				{Method: http.MethodPost, Path: "/api/agent-runs/{id}/approve-tool"},
				{Method: http.MethodPost, Path: "/api/agent-runs/{id}/approve-tools"},
				{Method: http.MethodPost, Path: "/api/workflow-runs/{id}/approve-tools"},
			},
			Status:  "implemented",
			Durable: true,
		},
		{
			Category:   "resources",
			Capability: "Edit agents, providers, tools, skills, workflow resources, team templates, and kits",
			CLI:        []string{"/new-agent <name>", "/new-provider <name>", "/new-tool python <name>", "/new-skill <template> <name>", "/new-policy-rule <preset> <name>", "/new-team <preset> <name>", "/new-workflow-template <source> <name>", "/kits [name] [--export]", "/kits --import <path>", "/new-kit <preset> <name>"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodGet, Path: "/api/resources"},
				{Method: http.MethodGet, Path: "/api/resources/agents"},
				{Method: http.MethodGet, Path: "/api/resources/providers"},
				{Method: http.MethodGet, Path: "/api/resources/tools"},
				{Method: http.MethodGet, Path: "/api/resources/tools/scaffolds"},
				{Method: http.MethodPost, Path: "/api/resources/tools/scaffolds/{preset}"},
				{Method: http.MethodGet, Path: "/api/resources/skills"},
				{Method: http.MethodGet, Path: "/api/resources/workflow-schemas"},
				{Method: http.MethodPost, Path: "/api/resources/workflow-schemas/{name}/validate"},
				{Method: http.MethodGet, Path: "/api/resources/workflow-templates"},
				{Method: http.MethodPost, Path: "/api/resources/workflow-templates/{name}/fork"},
				{Method: http.MethodGet, Path: "/api/resources/workflow-node-metadata"},
				{Method: http.MethodPost, Path: "/api/resources/workflow-node-metadata/{type}/validate"},
				{Method: http.MethodGet, Path: "/api/resources/expression-helpers"},
				{Method: http.MethodPost, Path: "/api/resources/expression-helpers/{name}/validate"},
				{Method: http.MethodGet, Path: "/api/resources/team-templates"},
				{Method: http.MethodGet, Path: "/api/resources/team-templates/scaffolds"},
				{Method: http.MethodPost, Path: "/api/resources/team-templates/scaffolds/{preset}"},
				{Method: http.MethodGet, Path: "/api/resources/policy-rules"},
				{Method: http.MethodGet, Path: "/api/resources/policy-rules/scaffolds"},
				{Method: http.MethodPost, Path: "/api/resources/policy-rules/scaffolds/{preset}"},
				{Method: http.MethodGet, Path: "/api/kits"},
				{Method: http.MethodGet, Path: "/api/kits/{name}"},
				{Method: http.MethodGet, Path: "/api/resources/kits"},
				{Method: http.MethodGet, Path: "/api/resources/kits/scaffolds"},
				{Method: http.MethodPost, Path: "/api/resources/kits/scaffolds/{preset}"},
				{Method: http.MethodGet, Path: "/api/resources/kits/{name}/export"},
				{Method: http.MethodPost, Path: "/api/resources/kits/import"},
			},
			Status:          "implemented",
			RestartRequired: true,
			Notes:           "Provider, agent, and MCP server changes are file-backed and may require restart/rebootstrap to affect the active runtime.",
		},
		{
			Category:   "collaboration",
			Capability: "Team templates, team state, collaboration messages, and blackboard lifecycle",
			CLI:        []string{"/teams", "/teams <name>", "/new-team <preset> <name>", "/team-state [run-id] [team]"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodGet, Path: "/api/team-templates"},
				{Method: http.MethodGet, Path: "/api/team-templates/{name}"},
				{Method: http.MethodGet, Path: "/api/resources/team-templates"},
				{Method: http.MethodGet, Path: "/api/resources/team-templates/scaffolds"},
				{Method: http.MethodPost, Path: "/api/resources/team-templates/scaffolds/{preset}"},
				{Method: http.MethodGet, Path: "/api/team-state"},
				{Method: http.MethodGet, Path: "/api/collaboration/messages"},
				{Method: http.MethodGet, Path: "/api/collaboration/blackboard"},
			},
			Status:  "implemented",
			Durable: true,
		},
		{
			Category:   "artifacts",
			Capability: "Session artifact references and rehydration",
			CLI:        []string{"goflow://session-artifacts/<id>"},
			HTTP: []helpParityEndpoint{
				{Method: http.MethodGet, Path: "/api/session-artifacts"},
				{Method: http.MethodGet, Path: "/api/session-artifacts/{id}"},
			},
			Status: "implemented",
			Notes:  "Large observations can be compacted into refs and rehydrated only when exact content is needed.",
		},
	}
}

func helpCoverageFor(items []helpParityItem) helpCoverage {
	coverage := helpCoverage{Total: len(items)}
	for _, item := range items {
		switch item.Status {
		case "implemented":
			coverage.Implemented++
		case "partial":
			coverage.Partial++
		case "planned":
			coverage.Planned++
		}
	}
	return coverage
}
