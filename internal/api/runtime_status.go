package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/version"
	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type runtimeStatusResponse struct {
	Version               string                `json:"version"`
	ActiveAgent           string                `json:"active_agent"`
	Mode                  string                `json:"mode"`
	Trace                 bool                  `json:"trace"`
	RuntimeHome           string                `json:"runtime_home,omitempty"`
	Workspace             workspace.Snapshot    `json:"workspace"`
	WorkspaceCapabilities workspaceCapabilities `json:"workspace_capabilities"`
	WorkspaceActions      []workspaceActionHint `json:"workspace_actions,omitempty"`
	Session               session.Snapshot      `json:"session"`
	StatusLines           []string              `json:"status_lines"`
	Providers             []providerSummary     `json:"providers"`
	Agents                []agentSummary        `json:"agents"`
	Skills                []skillSummary        `json:"skills"`
	Tools                 []string              `json:"tools"`
	MCPHealth             map[string]string     `json:"mcp_health"`
	MCPServers            []mcpServerSummary    `json:"mcp_servers,omitempty"`
	MCPIsolationModes     []mcpIsolationMode    `json:"mcp_isolation_modes,omitempty"`
	Setup                 setupStatus           `json:"setup"`
	Cost                  costDiagnostics       `json:"cost"`
	Verifier              verifierSummary       `json:"verifier"`
	Auxiliary             []auxiliarySummary    `json:"auxiliary_models,omitempty"`
	ToolRiskPolicy        toolRiskPolicySummary `json:"tool_risk_policy"`
}

type costDiagnostics struct {
	Latest                         *schema.PromptBudget      `json:"latest,omitempty"`
	History                        []schema.PromptBudget     `json:"history,omitempty"`
	TokenUsageHistory              []schema.TokenUsageSample `json:"token_usage_history,omitempty"`
	Features                       []costControlFeature      `json:"features,omitempty"`
	AuxiliaryRoutes                []auxiliarySummary        `json:"auxiliary_routes,omitempty"`
	Tuning                         []costRouteTuning         `json:"tuning,omitempty"`
	Samples                        int                       `json:"samples"`
	TokenUsageSamples              int                       `json:"token_usage_samples,omitempty"`
	TotalPromptTokens              int                       `json:"total_prompt_tokens,omitempty"`
	TotalOutputTokens              int                       `json:"total_output_tokens,omitempty"`
	TotalCachedTokens              int                       `json:"total_cached_tokens,omitempty"`
	TotalTokens                    int                       `json:"total_tokens,omitempty"`
	AverageEstimatedPromptTokens   int                       `json:"average_estimated_prompt_tokens,omitempty"`
	MaxEstimatedPromptTokens       int                       `json:"max_estimated_prompt_tokens,omitempty"`
	AverageCacheablePrefixTokens   int                       `json:"average_cacheable_prefix_tokens,omitempty"`
	AverageNonCacheableTokens      int                       `json:"average_non_cacheable_tokens,omitempty"`
	HistoryEstimatedSavedTokens    int                       `json:"history_estimated_saved_tokens,omitempty"`
	HistoryDeduplicatedItems       int                       `json:"history_deduplicated_items,omitempty"`
	HistoryCompactedOlderItems     int                       `json:"history_compacted_older_items,omitempty"`
	ToolSchemaDiagnosticSamples    int                       `json:"tool_schema_diagnostic_samples,omitempty"`
	ToolSchemaDiagnosticOmitted    int                       `json:"tool_schema_diagnostic_omitted,omitempty"`
	ToolSchemaEstimatedSavedTokens int                       `json:"tool_schema_estimated_saved_tokens,omitempty"`
	MemoryBlockSamples             int                       `json:"memory_block_samples,omitempty"`
	MemoryOmittedCount             int                       `json:"memory_omitted_count,omitempty"`
	MemoryEstimatedSavedTokens     int                       `json:"memory_estimated_saved_tokens,omitempty"`
	ArtifactRefSamples             int                       `json:"artifact_ref_samples,omitempty"`
	CompactedToolResultCount       int                       `json:"compacted_tool_result_count,omitempty"`
	ArtifactOmittedTokens          int                       `json:"artifact_omitted_tokens,omitempty"`
	SkillOmittedTokens             int                       `json:"skill_omitted_tokens,omitempty"`
	OmittedContextCount            int                       `json:"omitted_context_count,omitempty"`
	UniquePromptPrefixes           int                       `json:"unique_prompt_prefixes,omitempty"`
	PromptPrefixReuseSamples       int                       `json:"prompt_prefix_reuse_samples,omitempty"`
	PromptPrefixReuseRate          float64                   `json:"prompt_prefix_reuse_rate,omitempty"`
	ProviderCacheHitRate           float64                   `json:"provider_cache_hit_rate,omitempty"`
	LatestPromptPrefixHash         string                    `json:"latest_prompt_prefix_hash,omitempty"`
	ByAgent                        []costTrend               `json:"by_agent,omitempty"`
	ByMode                         []costTrend               `json:"by_mode,omitempty"`
	ByStage                        []costTrend               `json:"by_stage,omitempty"`
	Recommendations                []costRecommendation      `json:"recommendations,omitempty"`
}

type costControlFeature struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	Category        string `json:"category"`
	State           string `json:"state"`
	Enabled         bool   `json:"enabled"`
	Observed        bool   `json:"observed"`
	RequiresConfig  bool   `json:"requires_config,omitempty"`
	ExtraModelCall  bool   `json:"extra_model_call,omitempty"`
	Samples         int    `json:"samples,omitempty"`
	EstimatedTokens int    `json:"estimated_tokens,omitempty"`
	SavedTokens     int    `json:"saved_tokens,omitempty"`
	FilteredTools   int    `json:"filtered_tools,omitempty"`
	InjectedTools   int    `json:"injected_tools,omitempty"`
	MemoryBlocks    int    `json:"memory_blocks,omitempty"`
	ArtifactRefs    int    `json:"artifact_refs,omitempty"`
	OmittedItems    int    `json:"omitted_items,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	Description     string `json:"description,omitempty"`
	Measurement     string `json:"measurement,omitempty"`
	Recommendation  string `json:"recommendation,omitempty"`
}

type costTrend struct {
	Key                            string  `json:"key"`
	AgentID                        string  `json:"agent_id,omitempty"`
	Mode                           string  `json:"mode,omitempty"`
	WorkflowName                   string  `json:"workflow_name,omitempty"`
	TaskStage                      string  `json:"task_stage,omitempty"`
	PromptBudgetSamples            int     `json:"prompt_budget_samples,omitempty"`
	TokenUsageSamples              int     `json:"token_usage_samples,omitempty"`
	AverageEstimatedPromptTokens   int     `json:"average_estimated_prompt_tokens,omitempty"`
	MaxEstimatedPromptTokens       int     `json:"max_estimated_prompt_tokens,omitempty"`
	AverageCacheablePrefixTokens   int     `json:"average_cacheable_prefix_tokens,omitempty"`
	AverageNonCacheableTokens      int     `json:"average_non_cacheable_tokens,omitempty"`
	HistoryEstimatedSavedTokens    int     `json:"history_estimated_saved_tokens,omitempty"`
	HistoryDeduplicatedItems       int     `json:"history_deduplicated_items,omitempty"`
	HistoryCompactedOlderItems     int     `json:"history_compacted_older_items,omitempty"`
	ToolSchemaDiagnosticSamples    int     `json:"tool_schema_diagnostic_samples,omitempty"`
	ToolSchemaDiagnosticOmitted    int     `json:"tool_schema_diagnostic_omitted,omitempty"`
	ToolSchemaEstimatedSavedTokens int     `json:"tool_schema_estimated_saved_tokens,omitempty"`
	MemoryBlockSamples             int     `json:"memory_block_samples,omitempty"`
	MemoryEstimatedSavedTokens     int     `json:"memory_estimated_saved_tokens,omitempty"`
	ArtifactRefSamples             int     `json:"artifact_ref_samples,omitempty"`
	ArtifactOmittedTokens          int     `json:"artifact_omitted_tokens,omitempty"`
	SkillOmittedTokens             int     `json:"skill_omitted_tokens,omitempty"`
	OmittedContextCount            int     `json:"omitted_context_count,omitempty"`
	TotalPromptTokens              int     `json:"total_prompt_tokens,omitempty"`
	TotalOutputTokens              int     `json:"total_output_tokens,omitempty"`
	TotalCachedTokens              int     `json:"total_cached_tokens,omitempty"`
	TotalTokens                    int     `json:"total_tokens,omitempty"`
	UniquePromptPrefixes           int     `json:"unique_prompt_prefixes,omitempty"`
	PromptPrefixReuseSamples       int     `json:"prompt_prefix_reuse_samples,omitempty"`
	PromptPrefixReuseRate          float64 `json:"prompt_prefix_reuse_rate,omitempty"`
	ProviderCacheHitRate           float64 `json:"provider_cache_hit_rate,omitempty"`
}

type costRecommendation struct {
	Level               string `json:"level"`
	Code                string `json:"code"`
	Message             string `json:"message"`
	AgentID             string `json:"agent_id,omitempty"`
	Mode                string `json:"mode,omitempty"`
	WorkflowName        string `json:"workflow_name,omitempty"`
	TaskStage           string `json:"task_stage,omitempty"`
	EstimatedTokens     int    `json:"estimated_tokens,omitempty"`
	Measurement         string `json:"measurement,omitempty"`
	Action              string `json:"action,omitempty"`
	RequiresConfig      bool   `json:"requires_config,omitempty"`
	ExpectedSavingsKind string `json:"expected_savings_kind,omitempty"`
}

type costRouteTuning struct {
	Kind                         string   `json:"kind"`
	State                        string   `json:"state"`
	Enabled                      bool     `json:"enabled"`
	Configured                   bool     `json:"configured"`
	Provider                     string   `json:"provider,omitempty"`
	Model                        string   `json:"model,omitempty"`
	ObservedSamples              int      `json:"observed_samples,omitempty"`
	PromptBudgetSamples          int      `json:"prompt_budget_samples,omitempty"`
	TokenUsageSamples            int      `json:"token_usage_samples,omitempty"`
	ObservedPromptTokens         int      `json:"observed_prompt_tokens,omitempty"`
	ObservedOutputTokens         int      `json:"observed_output_tokens,omitempty"`
	ObservedCachedTokens         int      `json:"observed_cached_tokens,omitempty"`
	ObservedTotalTokens          int      `json:"observed_total_tokens,omitempty"`
	CandidateSamples             int      `json:"candidate_samples,omitempty"`
	CandidateAveragePromptTokens int      `json:"candidate_average_prompt_tokens,omitempty"`
	EstimatedExtraCallTokens     int      `json:"estimated_extra_call_tokens,omitempty"`
	EstimatedMainPromptTokens    int      `json:"estimated_main_prompt_tokens,omitempty"`
	NetSavingsSignal             int      `json:"net_savings_signal,omitempty"`
	QualityChecklist             []string `json:"quality_checklist,omitempty"`
	Recommendation               string   `json:"recommendation,omitempty"`
	Action                       string   `json:"action,omitempty"`
}

type verifierSummary struct {
	Enabled          bool     `json:"enabled"`
	Agent            string   `json:"agent,omitempty"`
	Provider         string   `json:"provider,omitempty"`
	Model            string   `json:"model,omitempty"`
	Modes            []string `json:"modes,omitempty"`
	MaxTokens        int      `json:"max_tokens,omitempty"`
	ProviderOverride bool     `json:"provider_override"`
	ModelOverride    bool     `json:"model_override"`
}

type auxiliarySummary struct {
	Enabled          bool    `json:"enabled"`
	Kind             string  `json:"kind"`
	Provider         string  `json:"provider,omitempty"`
	Model            string  `json:"model,omitempty"`
	MaxTokens        int     `json:"max_tokens,omitempty"`
	Temperature      float64 `json:"temperature,omitempty"`
	ProviderOverride bool    `json:"provider_override"`
	ModelOverride    bool    `json:"model_override"`
}

type toolRiskPolicySummary struct {
	Enabled                                 bool                            `json:"enabled"`
	RequireApprovalForUnsandboxedRiskyTools bool                            `json:"require_approval_for_unsandboxed_risky_tools"`
	DisableRememberForUnsandboxedRiskyTools bool                            `json:"disable_remember_for_unsandboxed_risky_tools"`
	RejectUnsandboxedRiskyTools             bool                            `json:"reject_unsandboxed_risky_tools"`
	AppliesToKinds                          []string                        `json:"applies_to_kinds,omitempty"`
	RecognizedSandboxBoundaries             []toolRiskPolicySandboxBoundary `json:"recognized_sandbox_boundaries,omitempty"`
	UnsandboxedDefinition                   string                          `json:"unsandboxed_definition,omitempty"`
	ApprovalBehavior                        string                          `json:"approval_behavior,omitempty"`
	RememberBehavior                        string                          `json:"remember_behavior,omitempty"`
	RejectionBehavior                       string                          `json:"rejection_behavior,omitempty"`
	Recommendation                          string                          `json:"recommendation,omitempty"`
}

type toolRiskPolicySandboxBoundary struct {
	Isolation      string   `json:"isolation"`
	Scope          string   `json:"scope"`
	AppliesToKinds []string `json:"applies_to_kinds,omitempty"`
	Description    string   `json:"description,omitempty"`
}

type agentSummary struct {
	ID               string            `json:"id"`
	Name             string            `json:"name,omitempty"`
	Description      string            `json:"description,omitempty"`
	Mode             string            `json:"mode,omitempty"`
	Provider         string            `json:"provider,omitempty"`
	Model            string            `json:"model,omitempty"`
	ToolPolicy       config.ToolPolicy `json:"tool_policy,omitempty"`
	AllowedToolKinds []config.ToolKind `json:"allowed_tool_kinds,omitempty"`
}

type providerSummary struct {
	ID                    string   `json:"id"`
	Provider              string   `json:"provider,omitempty"`
	BaseURL               string   `json:"base_url,omitempty"`
	Model                 string   `json:"model,omitempty"`
	FallbackProvider      string   `json:"fallback_provider,omitempty"`
	ProviderMessageFields []string `json:"provider_message_fields,omitempty"`
	Timeout               string   `json:"timeout,omitempty"`
	Temperature           float64  `json:"temperature,omitempty"`
	MaxTokens             int      `json:"max_tokens,omitempty"`
	RetryCount            int      `json:"retry_count,omitempty"`
	RetryBackoff          string   `json:"retry_backoff,omitempty"`
	APIKeySet             bool     `json:"api_key_set"`
}

type skillSummary struct {
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	PreferredAgent   string   `json:"preferred_agent,omitempty"`
	AllowedToolKinds []string `json:"allowed_tool_kinds,omitempty"`
	NextSkills       []string `json:"next_skills,omitempty"`
}

type mcpServerSummary struct {
	Name                          string                          `json:"name"`
	Enabled                       bool                            `json:"enabled"`
	Health                        string                          `json:"health,omitempty"`
	Command                       string                          `json:"command,omitempty"`
	Args                          []string                        `json:"args,omitempty"`
	WorkDir                       string                          `json:"workdir,omitempty"`
	Isolation                     string                          `json:"isolation,omitempty"`
	IsolationProfile              string                          `json:"isolation_profile,omitempty"`
	IsolationLevel                string                          `json:"isolation_level"`
	RiskLevel                     string                          `json:"risk_level"`
	ContainerImage                string                          `json:"container_image,omitempty"`
	ContainerImageReferenceType   string                          `json:"container_image_reference_type,omitempty"`
	ContainerImageDigestPinned    *bool                           `json:"container_image_digest_pinned,omitempty"`
	ContainerImageProductionReady *bool                           `json:"container_image_production_ready,omitempty"`
	ContainerPullPolicy           string                          `json:"container_pull_policy,omitempty"`
	Sandboxed                     bool                            `json:"sandboxed"`
	NetworkDisabled               bool                            `json:"network_disabled,omitempty"`
	NetworkEnforced               bool                            `json:"network_enforced"`
	FilesystemSandboxed           bool                            `json:"filesystem_sandboxed"`
	PrivilegeSandboxed            bool                            `json:"privilege_sandboxed"`
	ResourceLimited               bool                            `json:"resource_limited"`
	SandboxFeatures               []string                        `json:"sandbox_features,omitempty"`
	MissingSandboxFeatures        []string                        `json:"missing_sandbox_features,omitempty"`
	WindowsIsolation              *schema.WindowsIsolationProfile `json:"windows_isolation,omitempty"`
	EnvAllowlistSet               bool                            `json:"env_allowlist_set"`
	EnvAllowlist                  []string                        `json:"env_allowlist,omitempty"`
	SensitiveEnv                  []string                        `json:"sensitive_env,omitempty"`
	AllowedCommandsSet            bool                            `json:"allowed_commands_set"`
	AllowedCommands               []string                        `json:"allowed_commands,omitempty"`
	AllowedCommandPaths           []string                        `json:"allowed_command_paths,omitempty"`
	MaxRequestBytes               int                             `json:"max_request_bytes,omitempty"`
	MaxResponseBytes              int                             `json:"max_response_bytes,omitempty"`
	MaxConcurrentCalls            int                             `json:"max_concurrent_calls,omitempty"`
	ActiveCalls                   int                             `json:"active_calls,omitempty"`
	QueuedCalls                   int                             `json:"queued_calls,omitempty"`
	AvailableCallSlots            int                             `json:"available_call_slots,omitempty"`
	RestartLimit                  int                             `json:"restart_limit,omitempty"`
	Cooldown                      string                          `json:"cooldown,omitempty"`
	SecurityBoundary              string                          `json:"security_boundary"`
	Warnings                      []string                        `json:"warnings,omitempty"`
	Recommendations               []string                        `json:"recommendations,omitempty"`
	RequiresRealSandbox           bool                            `json:"requires_real_sandbox,omitempty"`
	CanAccessHostOutside          bool                            `json:"can_access_host_outside_workspace"`
}

type mcpIsolationMode struct {
	Name                   string                   `json:"name"`
	Label                  string                   `json:"label"`
	Platforms              []string                 `json:"platforms,omitempty"`
	ConfigSupported        bool                     `json:"config_supported"`
	Implemented            bool                     `json:"implemented"`
	Recommended            bool                     `json:"recommended,omitempty"`
	Fallback               bool                     `json:"fallback,omitempty"`
	FallbackKind           string                   `json:"fallback_kind,omitempty"`
	RecommendedFor         []string                 `json:"recommended_for,omitempty"`
	IsolationLevel         string                   `json:"isolation_level"`
	Sandboxed              bool                     `json:"sandboxed"`
	NetworkEnforced        bool                     `json:"network_enforced"`
	FilesystemSandboxed    bool                     `json:"filesystem_sandboxed"`
	PrivilegeSandboxed     bool                     `json:"privilege_sandboxed"`
	ResourceLimited        bool                     `json:"resource_limited"`
	RequiresOptions        []string                 `json:"requires_options,omitempty"`
	Options                []mcpIsolationModeOption `json:"options,omitempty"`
	Profiles               []mcpIsolationProfile    `json:"profiles,omitempty"`
	SandboxFeatures        []string                 `json:"sandbox_features,omitempty"`
	MissingSandboxFeatures []string                 `json:"missing_sandbox_features,omitempty"`
	Description            string                   `json:"description"`
	Recommendation         string                   `json:"recommendation,omitempty"`
	DisabledReason         string                   `json:"disabled_reason,omitempty"`
}

type mcpIsolationProfile struct {
	Name           string            `json:"name"`
	Label          string            `json:"label"`
	Description    string            `json:"description"`
	RecommendedFor []string          `json:"recommended_for,omitempty"`
	DefaultOptions map[string]string `json:"default_options,omitempty"`
	RiskLevel      string            `json:"risk_level,omitempty"`
	RequiresDigest bool              `json:"requires_digest,omitempty"`
	AllowsNetwork  bool              `json:"allows_network,omitempty"`
	WorkspaceMount string            `json:"workspace_mount,omitempty"`
	Recommendation string            `json:"recommendation,omitempty"`
}

type mcpIsolationModeOption struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Required       bool     `json:"required,omitempty"`
	Default        string   `json:"default,omitempty"`
	AllowedValues  []string `json:"allowed_values,omitempty"`
	Description    string   `json:"description,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
}

type setupStatus struct {
	Env                   []envStatus           `json:"env"`
	Providers             []providerSetupStatus `json:"providers,omitempty"`
	ModelReady            bool                  `json:"model_ready"`
	MissingProviderFields []string              `json:"missing_provider_fields,omitempty"`
}

type envStatus struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Set         bool   `json:"set"`
	Description string `json:"description"`
}

type providerSetupStatus struct {
	ID       string   `json:"id"`
	Provider string   `json:"provider,omitempty"`
	Ready    bool     `json:"ready"`
	Missing  []string `json:"missing,omitempty"`
}

type updatePolicyResponse struct {
	CurrentVersion string       `json:"current_version"`
	Repository     string       `json:"repository"`
	ReleaseFeed    string       `json:"release_feed"`
	CheckEndpoint  string       `json:"check_endpoint"`
	NetworkOptIn   bool         `json:"network_opt_in"`
	CheckEnabled   bool         `json:"check_enabled"`
	DisabledReason string       `json:"disabled_reason,omitempty"`
	Strategies     []updateMode `json:"strategies"`
	Notes          []string     `json:"notes"`
}

type updateMode struct {
	InstallType string   `json:"install_type"`
	PromptFlow  []string `json:"prompt_flow"`
	AutoUpdate  []string `json:"auto_update"`
}

type updateCheckResponse struct {
	CheckedAt       string               `json:"checked_at"`
	CurrentVersion  string               `json:"current_version"`
	LatestVersion   string               `json:"latest_version,omitempty"`
	UpdateAvailable bool                 `json:"update_available"`
	Prerelease      bool                 `json:"prerelease,omitempty"`
	Draft           bool                 `json:"draft,omitempty"`
	ReleaseURL      string               `json:"release_url,omitempty"`
	ReleaseFeed     string               `json:"release_feed"`
	Message         string               `json:"message,omitempty"`
	NotesPreview    string               `json:"notes_preview,omitempty"`
	Assets          []updateReleaseAsset `json:"assets,omitempty"`
	AssetSummary    updateAssetSummary   `json:"asset_summary"`
}

type updateReleaseAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size,omitempty"`
	URL  string `json:"url,omitempty"`
}

type updateAssetSummary struct {
	AssetCount             int      `json:"asset_count"`
	CurrentPlatform        string   `json:"current_platform"`
	ExpectedArchive        string   `json:"expected_archive,omitempty"`
	MatchingArchive        string   `json:"matching_archive,omitempty"`
	HasCurrentPlatform     bool     `json:"has_current_platform"`
	HasChecksums           bool     `json:"has_checksums"`
	HasSBOM                bool     `json:"has_sbom"`
	SigstoreBundleCount    int      `json:"sigstore_bundle_count"`
	HasArchiveSignature    bool     `json:"has_archive_signature"`
	HasChecksumSignature   bool     `json:"has_checksum_signature"`
	HasSBOMSignature       bool     `json:"has_sbom_signature"`
	VerificationReady      bool     `json:"verification_ready"`
	RecommendedInstallType string   `json:"recommended_install_type,omitempty"`
	Missing                []string `json:"missing,omitempty"`
	VerifySteps            []string `json:"verify_steps,omitempty"`
}

type githubReleaseResponse struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	HTMLURL    string `json:"html_url"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func (s *Server) handleRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.runtimeStatus(r))
}

func (s *Server) handleRuntimeCost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snapshot := session.Snapshot{}
	if s != nil && s.runtime != nil {
		snapshot = s.sessionSnapshotWithApprovalRisk()
	}
	writeJSON(w, s.runtimeCostDiagnostics(snapshot))
}

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, updatePolicyResponse{
		CurrentVersion: version.Version,
		Repository:     "https://github.com/FyMatt/GoFlow-Agent",
		ReleaseFeed:    updateReleaseFeedURL(),
		CheckEndpoint:  "/api/update-policy/check",
		NetworkOptIn:   true,
		CheckEnabled:   !updateChecksDisabled(),
		DisabledReason: updateChecksDisabledReason(),
		Strategies: []updateMode{
			{
				InstallType: "release-archive",
				PromptFlow: []string{
					"check GitHub Releases for the newest semver tag",
					"compare with the embedded build version",
					"show release notes and asset checksums before download",
				},
				AutoUpdate: []string{
					"download the matching Windows/Linux archive only after user approval",
					"verify SHA256SUMS and Sigstore bundle before replacing files",
					"stage the update beside the current install and restart through the launcher",
				},
			},
			{
				InstallType: "docker",
				PromptFlow: []string{
					"compare the running image tag with the latest release tag",
					"show docker pull and compose upgrade commands",
				},
				AutoUpdate: []string{
					"do not self-update inside the container by default",
					"let the host orchestrator pull and restart the container",
				},
			},
			{
				InstallType: "source-checkout",
				PromptFlow: []string{
					"check the upstream tag or branch status",
					"show git fetch, checkout, and build commands",
				},
				AutoUpdate: []string{
					"require explicit operator approval before mutating the repository",
				},
			},
		},
		Notes: []string{
			"update checks should be opt-in or clearly visible because they contact GitHub",
			"offline deployments can disable checks and rely on manual release import",
			"automatic replacement is safest for release archives; Docker and source installs should prefer prompted instructions",
		},
	})
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if updateChecksDisabled() {
		writeJSONStatus(w, http.StatusForbidden, updateCheckResponse{
			CheckedAt:      time.Now().UTC().Format(time.RFC3339),
			CurrentVersion: version.Version,
			ReleaseFeed:    updateReleaseFeedURL(),
			Message:        updateChecksDisabledReason(),
		})
		return
	}
	ctx := r.Context()
	feed := updateReleaseFeedURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "goflow-agent-update-check/1")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, updateCheckResponse{
			CheckedAt:      time.Now().UTC().Format(time.RFC3339),
			CurrentVersion: version.Version,
			ReleaseFeed:    feed,
			Message:        "update check failed: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		http.Error(w, "read release feed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeJSONStatus(w, http.StatusBadGateway, updateCheckResponse{
			CheckedAt:      time.Now().UTC().Format(time.RFC3339),
			CurrentVersion: version.Version,
			ReleaseFeed:    feed,
			Message:        fmt.Sprintf("release feed returned HTTP %d", resp.StatusCode),
		})
		return
	}
	var release githubReleaseResponse
	if err := json.Unmarshal(body, &release); err != nil {
		http.Error(w, "parse release feed: "+err.Error(), http.StatusBadGateway)
		return
	}
	result := buildUpdateCheckResponse(feed, release)
	writeJSON(w, result)
}

func updateReleaseFeedURL() string {
	if value := strings.TrimSpace(os.Getenv("GOFLOW_RELEASE_FEED")); value != "" {
		return value
	}
	return "https://api.github.com/repos/FyMatt/GoFlow-Agent/releases/latest"
}

func updateChecksDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GOFLOW_DISABLE_UPDATE_CHECKS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func updateChecksDisabledReason() string {
	if !updateChecksDisabled() {
		return ""
	}
	return "update checks are disabled by GOFLOW_DISABLE_UPDATE_CHECKS"
}

func buildUpdateCheckResponse(feed string, release githubReleaseResponse) updateCheckResponse {
	assets := make([]updateReleaseAsset, 0, len(release.Assets))
	for _, asset := range release.Assets {
		name := strings.TrimSpace(asset.Name)
		if name == "" {
			continue
		}
		assets = append(assets, updateReleaseAsset{
			Name: name,
			Size: asset.Size,
			URL:  strings.TrimSpace(asset.BrowserDownloadURL),
		})
	}
	latest := strings.TrimSpace(release.TagName)
	if latest == "" {
		latest = strings.TrimSpace(release.Name)
	}
	current := version.Version
	summary := summarizeUpdateAssets(latest, assets)
	return updateCheckResponse{
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
		CurrentVersion:  current,
		LatestVersion:   latest,
		UpdateAvailable: updateVersionAvailable(current, latest),
		Prerelease:      release.Prerelease,
		Draft:           release.Draft,
		ReleaseURL:      strings.TrimSpace(release.HTMLURL),
		ReleaseFeed:     feed,
		NotesPreview:    updateNotesPreview(release.Body, 600),
		Assets:          assets,
		AssetSummary:    summary,
	}
}

func summarizeUpdateAssets(latest string, assets []updateReleaseAsset) updateAssetSummary {
	platform := goruntime.GOOS + "/" + goruntime.GOARCH
	expected := expectedReleaseArchiveName(latest, goruntime.GOOS, goruntime.GOARCH)
	names := make(map[string]struct{}, len(assets))
	lowerNames := make(map[string]string, len(assets))
	for _, asset := range assets {
		name := strings.TrimSpace(asset.Name)
		if name == "" {
			continue
		}
		names[name] = struct{}{}
		lowerNames[strings.ToLower(name)] = name
	}
	_, hasChecksums := names["SHA256SUMS"]
	_, hasSBOM := names["SBOM.spdx.json"]
	matchingArchive := ""
	if expected != "" {
		if actual, ok := lowerNames[strings.ToLower(expected)]; ok {
			matchingArchive = actual
		}
	}
	sigstoreCount := 0
	for name := range names {
		if strings.HasSuffix(strings.ToLower(name), ".sigstore.json") {
			sigstoreCount++
		}
	}
	hasArchiveSignature := false
	if matchingArchive != "" {
		_, hasArchiveSignature = names[matchingArchive+".sigstore.json"]
	}
	_, hasChecksumSignature := names["SHA256SUMS.sigstore.json"]
	_, hasSBOMSignature := names["SBOM.spdx.json.sigstore.json"]
	summary := updateAssetSummary{
		AssetCount:             len(assets),
		CurrentPlatform:        platform,
		ExpectedArchive:        expected,
		MatchingArchive:        matchingArchive,
		HasCurrentPlatform:     matchingArchive != "",
		HasChecksums:           hasChecksums,
		HasSBOM:                hasSBOM,
		SigstoreBundleCount:    sigstoreCount,
		HasArchiveSignature:    hasArchiveSignature,
		HasChecksumSignature:   hasChecksumSignature,
		HasSBOMSignature:       hasSBOMSignature,
		RecommendedInstallType: "release-archive",
	}
	required := []struct {
		ok   bool
		name string
	}{
		{summary.HasCurrentPlatform, "current platform archive"},
		{summary.HasChecksums, "SHA256SUMS"},
		{summary.HasSBOM, "SBOM.spdx.json"},
		{summary.HasArchiveSignature, "archive Sigstore bundle"},
		{summary.HasChecksumSignature, "SHA256SUMS Sigstore bundle"},
		{summary.HasSBOMSignature, "SBOM Sigstore bundle"},
	}
	for _, item := range required {
		if !item.ok {
			summary.Missing = append(summary.Missing, item.name)
		}
	}
	summary.VerificationReady = len(summary.Missing) == 0
	summary.VerifySteps = updateVerifySteps(summary)
	return summary
}

func updateVerifySteps(summary updateAssetSummary) []string {
	if summary.MatchingArchive == "" {
		return nil
	}
	steps := []string{}
	if summary.HasChecksums {
		steps = append(steps, checksumVerifyCommand(summary.MatchingArchive))
	}
	if summary.HasChecksums && summary.HasSBOM {
		steps = append(steps, checksumVerifyCommand("SBOM.spdx.json"))
	}
	if summary.HasArchiveSignature {
		steps = append(steps, cosignVerifyCommand(summary.MatchingArchive))
	}
	if summary.HasChecksumSignature {
		steps = append(steps, cosignVerifyCommand("SHA256SUMS"))
	}
	if summary.HasSBOMSignature {
		steps = append(steps, cosignVerifyCommand("SBOM.spdx.json"))
	}
	return steps
}

func checksumVerifyCommand(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return ""
	}
	if goruntime.GOOS == "windows" {
		return fmt.Sprintf("Select-String -Path SHA256SUMS -SimpleMatch %s; Get-FileHash %s -Algorithm SHA256", powershellSingleQuoteSafe("  "+filename), powershellSingleQuoteSafe(".\\"+filename))
	}
	return fmt.Sprintf("grep -- %s SHA256SUMS | sha256sum -c -", shellSingleQuoteSafe("  "+filename+"$"))
}

func cosignVerifyCommand(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return ""
	}
	if goruntime.GOOS == "windows" {
		return fmt.Sprintf("cosign verify-blob %s --bundle %s --certificate-identity-regexp 'https://github.com/FyMatt/GoFlow-Agent/.github/workflows/.*' --certificate-oidc-issuer https://token.actions.githubusercontent.com", powershellSingleQuoteSafe(".\\"+filename), powershellSingleQuoteSafe(".\\"+filename+".sigstore.json"))
	}
	return fmt.Sprintf("cosign verify-blob %s --bundle %s --certificate-identity-regexp 'https://github.com/FyMatt/GoFlow-Agent/.github/workflows/.*' --certificate-oidc-issuer https://token.actions.githubusercontent.com", shellSingleQuoteSafe(filename), shellSingleQuoteSafe(filename+".sigstore.json"))
}

func shellSingleQuoteSafe(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func powershellSingleQuoteSafe(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func expectedReleaseArchiveName(latest, goos, goarch string) string {
	versionTag := strings.TrimSpace(latest)
	if versionTag == "" {
		return ""
	}
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("goflow-agent_%s_%s_%s%s", versionTag, goos, goarch, extension)
}

func updateVersionAvailable(current, latest string) bool {
	current = normalizeVersionTag(current)
	latest = normalizeVersionTag(latest)
	if latest == "" || current == "" || current == "dev" {
		return false
	}
	if current == latest {
		return false
	}
	return compareSemverish(latest, current) > 0
}

func normalizeVersionTag(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "refs/tags/")
	value = strings.TrimPrefix(value, "v")
	return value
}

func compareSemverish(left, right string) int {
	leftParts := versionNumericParts(left)
	rightParts := versionNumericParts(right)
	for i := 0; i < 3; i++ {
		if leftParts[i] > rightParts[i] {
			return 1
		}
		if leftParts[i] < rightParts[i] {
			return -1
		}
	}
	return strings.Compare(left, right)
}

func versionNumericParts(value string) [3]int {
	var parts [3]int
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == '-' || r == '+'
	})
	for i := 0; i < len(fields) && i < len(parts); i++ {
		part := 0
		for _, r := range fields[i] {
			if r < '0' || r > '9' {
				break
			}
			part = part*10 + int(r-'0')
		}
		parts[i] = part
	}
	return parts
}

func updateNotesPreview(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "..."
}

func (s *Server) runtimeStatus(r *http.Request) runtimeStatusResponse {
	fullSnapshot := session.Snapshot{}
	apiSnapshot := session.Snapshot{}
	workspaceSnapshot := workspace.Snapshot{Status: "confirmed", Source: "none", Display: "(none)"}
	var (
		active      string
		mode        string
		trace       bool
		runtimeHome string
		statusLines []string
		providers   []providerSummary
		agents      []agentSummary
		skills      []skillSummary
		tools       []string
		mcpHealth   map[string]string
		mcpServers  []mcpServerSummary
		verifier    verifierSummary
		auxiliary   []auxiliarySummary
	)
	if s != nil && s.workspace != nil {
		workspaceSnapshot = s.workspace.Snapshot()
	}
	if s != nil && s.runtime != nil {
		ctx := r.Context()
		fullDetails := requestWantsFullSession(r)
		active = s.runtime.ActiveAgent()
		mode = s.runtime.Mode()
		trace = s.runtime.TraceEnabled()
		runtimeHome = s.runtime.RuntimeHome()
		fullSnapshot = s.sessionSnapshotWithApprovalRisk()
		if fullDetails {
			fullSnapshot = s.runtime.HydrateSnapshot(fullSnapshot)
		}
		apiSnapshot = sessionSnapshotForAPI(fullSnapshot, fullDetails)
		statusLines = s.runtime.StatusLines(ctx)
		providers = s.providerSummaries()
		agents = s.agentSummaries()
		skills = s.skillSummaries()
		tools = s.runtime.ToolNames()
		sort.Strings(tools)
		mcpHealth = s.runtime.MCPHealthStatus(ctx)
		mcpServers = s.mcpServerSummaries(mcpHealth)
		verifier = verifierRouteSummary(s.runtime.VerifierRoute())
		auxiliary = auxiliaryRouteSummaries(s.runtime.AuxiliaryModelRoutes())
	}
	return runtimeStatusResponse{
		Version:               version.Version,
		ActiveAgent:           active,
		Mode:                  mode,
		Trace:                 trace,
		RuntimeHome:           runtimeHome,
		Workspace:             workspaceSnapshot,
		WorkspaceCapabilities: workspaceCapabilitySummary(workspaceSnapshot, s.workspaceRebindSupported()),
		WorkspaceActions:      workspaceActionHints(workspaceSnapshot, "", s.workspaceRebindSupported()),
		Session:               apiSnapshot,
		StatusLines:           statusLines,
		Providers:             providers,
		Agents:                agents,
		Skills:                skills,
		Tools:                 tools,
		MCPHealth:             mcpHealth,
		MCPServers:            mcpServers,
		MCPIsolationModes:     mcpIsolationModes(),
		Setup:                 buildSetupStatus(providers),
		Cost:                  s.runtimeCostDiagnostics(fullSnapshot),
		Verifier:              verifier,
		Auxiliary:             auxiliary,
		ToolRiskPolicy:        s.toolRiskPolicySummary(),
	}
}

func (s *Server) toolRiskPolicySummary() toolRiskPolicySummary {
	policy := config.ToolRiskPolicyConfig{}
	if s != nil && s.runtime != nil {
		policy = s.runtime.ToolRiskPolicy()
	}
	enabled := policy.RequireApprovalForUnsandboxedRiskyTools ||
		policy.DisableRememberForUnsandboxedRiskyTools ||
		policy.RejectUnsandboxedRiskyTools
	summary := toolRiskPolicySummary{
		Enabled:                                 enabled,
		RequireApprovalForUnsandboxedRiskyTools: policy.RequireApprovalForUnsandboxedRiskyTools,
		DisableRememberForUnsandboxedRiskyTools: policy.DisableRememberForUnsandboxedRiskyTools,
		RejectUnsandboxedRiskyTools:             policy.RejectUnsandboxedRiskyTools,
		AppliesToKinds:                          []string{string(config.ToolKindWrite), string(config.ToolKindExec), string(config.ToolKindNetwork), string(config.ToolKindUnknown)},
		RecognizedSandboxBoundaries: []toolRiskPolicySandboxBoundary{
			{
				Isolation:      "container",
				Scope:          "broad",
				AppliesToKinds: []string{string(config.ToolKindWrite), string(config.ToolKindExec), string(config.ToolKindNetwork), string(config.ToolKindUnknown)},
				Description:    "Container isolation is treated as the broad sandbox boundary for risky MCP tools when configured with an explicit runtime profile.",
			},
			{
				Isolation:      "linux_netns",
				Scope:          "capability_specific",
				AppliesToKinds: []string{string(config.ToolKindNetwork)},
				Description:    "Linux network namespaces satisfy this policy for network-kind tools only; they do not sandbox filesystem access, exec behavior, or privileges.",
			},
		},
		UnsandboxedDefinition: "write, exec, network, or unknown tools that are not running through a real sandbox boundary; isolation: container covers risky tools broadly, while capability-specific enforced sandboxes such as isolation: linux_netns only cover matching network tools",
		Recommendation:        "Enable these switches when untrusted MCP tools may be configured or when allow/remember policies should not bypass sandbox review.",
	}
	if policy.RequireApprovalForUnsandboxedRiskyTools {
		summary.ApprovalBehavior = "unsandboxed risky tools require explicit approval even when the active agent tool_policy is allow"
	} else {
		summary.ApprovalBehavior = "agent tool_policy controls whether unsandboxed risky tools require approval"
	}
	if policy.DisableRememberForUnsandboxedRiskyTools {
		summary.RememberBehavior = "approve-and-remember and approve-all-tools are disabled for unsandboxed risky tools; one-time approval remains available unless rejection is enabled"
	} else {
		summary.RememberBehavior = "remember/approve-all behavior follows the normal approval scope"
	}
	if policy.RejectUnsandboxedRiskyTools {
		summary.RejectionBehavior = "unsandboxed risky tools are rejected before execution; run them with isolation: container or a capability-specific enforced sandbox such as isolation: linux_netns for network-only tools"
	} else {
		summary.RejectionBehavior = "unsandboxed risky tools are not rejected solely by tool_risk_policy"
	}
	return summary
}

func mcpIsolationModes() []mcpIsolationMode {
	return []mcpIsolationMode{
		{
			Name:            "none",
			Label:           "None",
			ConfigSupported: true,
			Implemented:     true,
			Fallback:        true,
			FallbackKind:    "trusted_local_only",
			IsolationLevel:  "none",
			Sandboxed:       false,
			Description:     "Starts the MCP server as a normal child process. Workspace-scoped tools and policy gates still apply, but there is no process sandbox.",
			Recommendation:  "Use only for trusted local tools, or switch untrusted tools to container isolation.",
		},
		{
			Name:                   "process_group",
			Label:                  "Process Group",
			ConfigSupported:        true,
			Implemented:            true,
			Fallback:               true,
			FallbackKind:           "lifecycle_cleanup",
			IsolationLevel:         "lifecycle",
			SandboxFeatures:        []string{"process_group_lifecycle"},
			MissingSandboxFeatures: []string{"filesystem_policy", "network_policy", "privilege_reduction"},
			Description:            "Starts the MCP server in a separate process group where supported, improving lifecycle cleanup without changing filesystem, network, or privilege access.",
			Recommendation:         "Use as a lifecycle baseline for trusted local tools; use container isolation for a real security boundary.",
		},
		{
			Name:                   "windows_job",
			Label:                  "Windows Job Object",
			Platforms:              []string{"windows"},
			ConfigSupported:        goruntime.GOOS == "windows",
			Implemented:            true,
			Fallback:               true,
			FallbackKind:           "windows_lifecycle_cleanup",
			IsolationLevel:         "lifecycle",
			SandboxFeatures:        []string{"windows_job_object_lifecycle"},
			MissingSandboxFeatures: []string{"windows_restricted_token", "filesystem_policy", "network_policy", "privilege_reduction"},
			Description:            "Windows-only lifecycle adapter that attaches the MCP process to a kill-on-close Job Object. It does not restrict filesystem, network, or privileges.",
			Recommendation:         "Use windows_restricted_token for privilege reduction, or container isolation when a filesystem or network boundary is required.",
			DisabledReason:         mcpIsolationModePlatformDisabledReason("Windows-only isolation mode"),
		},
		{
			Name:                   "windows_restricted_token",
			Label:                  "Windows Restricted Token",
			Platforms:              []string{"windows"},
			ConfigSupported:        goruntime.GOOS == "windows",
			Implemented:            true,
			Fallback:               true,
			FallbackKind:           "windows_privilege_reduction",
			IsolationLevel:         "windows_restricted_token",
			PrivilegeSandboxed:     goruntime.GOOS == "windows",
			SandboxFeatures:        []string{"windows_restricted_token", "windows_low_integrity", "windows_job_object_lifecycle", "process_group_lifecycle"},
			MissingSandboxFeatures: []string{"filesystem_policy", "network_policy"},
			Description:            "Windows-only privilege-reduction adapter using a constrained restricted token, low integrity, and Job Object lifecycle cleanup.",
			Recommendation:         "Treat this as privilege reduction only; use container isolation for filesystem and network policy.",
			DisabledReason:         mcpIsolationModePlatformDisabledReason("Windows-only isolation mode"),
		},
		{
			Name:                   "linux_cgroup",
			Label:                  "Linux Cgroup",
			Platforms:              []string{"linux"},
			ConfigSupported:        goruntime.GOOS == "linux",
			Implemented:            true,
			Fallback:               true,
			FallbackKind:           "linux_resource_control",
			IsolationLevel:         "resource_control",
			ResourceLimited:        goruntime.GOOS == "linux",
			Options:                linuxCgroupIsolationModeOptions(),
			SandboxFeatures:        []string{"linux_cgroup_resource_limits"},
			MissingSandboxFeatures: []string{"filesystem_policy", "network_policy", "privilege_reduction"},
			Description:            "Linux-only resource-control adapter for cgroup v2 CPU, memory, and process limits. It does not restrict filesystem, network, or privileges.",
			Recommendation:         "Combine with container isolation or another filesystem/network boundary for untrusted tools.",
			DisabledReason:         mcpIsolationModePlatformDisabledReason("Linux-only isolation mode"),
		},
		{
			Name:                   "linux_netns",
			Label:                  "Linux Network Namespace",
			Platforms:              []string{"linux"},
			ConfigSupported:        goruntime.GOOS == "linux",
			Implemented:            true,
			Fallback:               true,
			FallbackKind:           "linux_network_only",
			IsolationLevel:         "network_namespace",
			NetworkEnforced:        goruntime.GOOS == "linux",
			Options:                linuxNetworkNamespaceIsolationModeOptions(),
			SandboxFeatures:        []string{"linux_network_namespace"},
			MissingSandboxFeatures: []string{"filesystem_policy", "privilege_reduction"},
			Description:            "Linux-only network namespace adapter that starts the MCP process through unshare --net. It is network isolation only.",
			Recommendation:         "Combine with container isolation or host sandboxing when filesystem or privilege boundaries are required.",
			DisabledReason:         mcpIsolationModePlatformDisabledReason("Linux-only isolation mode"),
		},
		{
			Name:                   "container",
			Label:                  "Container",
			Platforms:              []string{"linux", "windows", "darwin"},
			ConfigSupported:        goruntime.GOOS == "linux" || goruntime.GOOS == "windows" || goruntime.GOOS == "darwin",
			Implemented:            true,
			Recommended:            true,
			RecommendedFor:         []string{"generated_tools", "third_party_tools", "write_tools", "exec_tools", "network_tools", "vulnerability_research_tools", "binary_analysis_tools", "untrusted_code"},
			IsolationLevel:         "container_configured",
			Sandboxed:              true,
			NetworkEnforced:        true,
			FilesystemSandboxed:    true,
			PrivilegeSandboxed:     true,
			ResourceLimited:        true,
			RequiresOptions:        []string{"image"},
			Options:                containerIsolationModeOptions(),
			Profiles:               containerIsolationProfiles(),
			SandboxFeatures:        []string{"container_runtime", "filesystem_mount_policy", "network_policy", "resource_limits", "privilege_controls"},
			MissingSandboxFeatures: []string{},
			Description:            "Runs the MCP server through Docker or Podman with explicit image, mounts, network mode, environment allowlist, and optional resource and privilege hardening.",
			Recommendation:         "Use for untrusted third-party MCP servers and pin images, mounts, network, resource limits, and privilege hardening options.",
			DisabledReason:         mcpIsolationModePlatformDisabledReason("Container isolation is supported on Linux, Windows, and macOS hosts with Docker or Podman"),
		},
	}
}

func linuxCgroupIsolationModeOptions() []mcpIsolationModeOption {
	return []mcpIsolationModeOption{
		{Name: "cgroup_parent", Type: "absolute_path", Default: "/sys/fs/cgroup/goflow", Description: "Writable cgroup v2 parent directory used for MCP child resource limits."},
		{Name: "cgroup_name", Type: "safe_name", Default: "mcp-<pid>", Description: "Safe cgroup leaf name. Letters, numbers, dash, underscore, and dot are accepted."},
		{Name: "memory_max", Type: "size_or_max", Description: "Value for memory.max. Accepts max, bytes, or K/M/G/T suffixes such as 256M."},
		{Name: "pids_max", Type: "integer_or_max", Description: "Value for pids.max. Accepts max or a positive integer."},
		{Name: "cpu_max", Type: "cpu_quota_period_or_max", Description: "Value for cpu.max. Accepts max or '<quota> <period>'."},
	}
}

func linuxNetworkNamespaceIsolationModeOptions() []mcpIsolationModeOption {
	return []mcpIsolationModeOption{
		{Name: "unshare_command", Type: "command", Default: "unshare", AllowedValues: []string{"unshare", "absolute_path"}, Description: "Command used to create the Linux network namespace."},
		{Name: "map_root_user", Type: "boolean_string", Default: "false", Description: "Pass --map-root-user to unshare when the host supports it."},
	}
}

func containerIsolationModeOptions() []mcpIsolationModeOption {
	return []mcpIsolationModeOption{
		{Name: "image", Type: "string", Required: true, Description: "Container image used to run the MCP server.", Recommendation: "Use a trusted version tag or digest instead of a floating latest tag."},
		{Name: "runtime", Type: "command", Default: "docker", AllowedValues: []string{"docker", "podman", "absolute_path"}, Description: "Container runtime command."},
		{Name: "workspace_mount", Type: "enum", Default: "rw", AllowedValues: []string{"rw", "ro", "none"}, Description: "How the selected workspace is mounted into the container.", Recommendation: "Use ro for read-only tools and none when workspace access is unnecessary."},
		{Name: "workspace_target", Type: "container_absolute_path", Default: "/workspace", Description: "Path exposed to the MCP server through GOFLOW_WORKSPACE_ROOT."},
		{Name: "container_workdir", Type: "container_absolute_path", Description: "Optional container working directory."},
		{Name: "network", Type: "enum_or_network_name", Default: "disabled when network_disabled is true", AllowedValues: []string{"default", "disabled", "none", "bridge", "host", "safe_network_name"}, Description: "Container network mode.", Recommendation: "Use disabled/none unless this MCP tool explicitly needs network access."},
		{Name: "ipc", Type: "enum", Default: "private", AllowedValues: []string{"private", "none"}, Description: "Container IPC namespace mode.", Recommendation: "Use none for ordinary MCP tools that do not need shared memory or host IPC."},
		{Name: "userns", Type: "enum", Default: "private", AllowedValues: []string{"private", "auto", "nomap", "keep-id"}, Description: "Container user namespace mode when supported by the runtime.", Recommendation: "Use auto or nomap on compatible runtimes to reduce host UID/GID exposure."},
		{Name: "memory", Type: "size", Description: "Container memory limit, such as 256m."},
		{Name: "memory_swap", Type: "size", Description: "Container memory plus swap limit. Set equal to memory to prevent extra swap allowance when supported."},
		{Name: "cpus", Type: "positive_float", Description: "Container CPU limit, such as 0.5."},
		{Name: "pids_limit", Type: "positive_integer", Description: "Maximum number of processes allowed in the container."},
		{Name: "pull_policy", Type: "enum", Default: "runtime default", AllowedValues: []string{"always", "missing", "never"}, Description: "Container image pull policy passed to Docker/Podman.", Recommendation: "Use missing for normal use or never for production environments that pre-pull trusted images."},
		{Name: "readonly_rootfs", Type: "boolean_string", Default: "false", Description: "Run the container with a read-only root filesystem.", Recommendation: "Enable when the image only needs declared mounts and tmpfs scratch paths."},
		{Name: "no_new_privileges", Type: "boolean_string", Default: "false", Description: "Pass no-new-privileges to the container runtime.", Recommendation: "Enable unless the MCP server intentionally needs privilege transitions."},
		{Name: "cap_drop", Type: "csv", AllowedValues: []string{"all", "capability_names"}, Description: "Linux capabilities to drop.", Recommendation: "Use all for ordinary MCP tools."},
		{Name: "user", Type: "user_or_uid", Description: "Container user or UID/GID token.", Recommendation: "Use a non-root user when the image supports it."},
		{Name: "security_opt", Type: "list", Description: "Semicolon, comma, or newline-separated container security options such as seccomp or AppArmor profiles."},
		{Name: "tmpfs", Type: "list", Description: "Semicolon or newline-separated tmpfs mounts for intentional scratch paths, preserving commas inside each mount option."},
		{Name: "init", Type: "boolean_string", Default: "false", Description: "Run a container init process for child-process reaping."},
		{Name: "tool_source", Type: "host_absolute_path", Description: "Optional single generated tool file to mount into the container."},
		{Name: "tool_target", Type: "container_absolute_path", Description: "Container path for the optional single generated tool file."},
		{Name: "tool_mount", Type: "enum", Default: "ro", AllowedValues: []string{"ro", "rw", "none"}, Description: "Mount mode for the optional generated tool file.", Recommendation: "Keep ro unless the container must edit that tool file."},
	}
}

func containerIsolationProfiles() []mcpIsolationProfile {
	return []mcpIsolationProfile{
		{
			Name:           "readonly",
			Label:          "Read-only helper",
			Description:    "Hardened Docker/Podman defaults for tools that only need to inspect workspace files.",
			RecommendedFor: []string{"read_tools", "static_analysis", "workspace_indexing", "untrusted_helpers"},
			DefaultOptions: config.ContainerIsolationProfileDefaults("readonly"),
			RiskLevel:      "low",
			WorkspaceMount: "ro",
			Recommendation: "Use this as the default for generated or third-party read-only MCP servers.",
		},
		{
			Name:           "writer",
			Label:          "Workspace writer",
			Description:    "Hardened Docker/Podman defaults with read-write workspace access for tools that must create or edit files.",
			RecommendedFor: []string{"write_tools", "code_generators", "formatters", "project_mutation"},
			DefaultOptions: config.ContainerIsolationProfileDefaults("writer"),
			RiskLevel:      "medium",
			WorkspaceMount: "rw",
			Recommendation: "Keep confirmation policy enabled and prefer exact allowed_tools for write-capable container MCP servers.",
		},
		{
			Name:           "network",
			Label:          "Network helper",
			Description:    "Hardened Docker/Podman defaults with explicit bridge network egress and read-only workspace access.",
			RecommendedFor: []string{"network_tools", "fetchers", "web_research", "asset_collection"},
			DefaultOptions: config.ContainerIsolationProfileDefaults("network"),
			RiskLevel:      "medium",
			AllowsNetwork:  true,
			WorkspaceMount: "ro",
			Recommendation: "Use only for tools that need outbound network access and keep approval visible for network-capable agents.",
		},
		{
			Name:           "production",
			Label:          "Production locked-down",
			Description:    "Read-only, network-disabled defaults with pull_policy=never for pre-pulled and digest-pinned production images.",
			RecommendedFor: []string{"production", "pre_pulled_images", "immutable_image_references", "high_trust_deployments"},
			DefaultOptions: config.ContainerIsolationProfileDefaults("production"),
			RiskLevel:      "low",
			RequiresDigest: true,
			WorkspaceMount: "ro",
			Recommendation: "Use image@sha256:... references and pre-pull trusted images before starting GoFlow in production.",
		},
	}
}

func mcpIsolationModePlatformDisabledReason(reason string) string {
	if reason == "" {
		return ""
	}
	switch reason {
	case "Windows-only isolation mode":
		if goruntime.GOOS == "windows" {
			return ""
		}
	case "Linux-only isolation mode":
		if goruntime.GOOS == "linux" {
			return ""
		}
	default:
		if goruntime.GOOS == "linux" || goruntime.GOOS == "windows" || goruntime.GOOS == "darwin" {
			if strings.HasPrefix(reason, "Container isolation") {
				return ""
			}
		}
	}
	return reason + "; current platform is " + goruntime.GOOS + "."
}

func auxiliaryRouteSummaries(routes []agent.AuxiliaryModelRoute) []auxiliarySummary {
	out := make([]auxiliarySummary, 0, len(routes))
	for _, route := range routes {
		out = append(out, auxiliarySummary{
			Enabled:          route.Enabled,
			Kind:             route.Kind,
			Provider:         route.Provider,
			Model:            route.Model,
			MaxTokens:        route.MaxTokens,
			Temperature:      route.Temperature,
			ProviderOverride: route.ProviderOverride,
			ModelOverride:    route.ModelOverride,
		})
	}
	return out
}

func verifierRouteSummary(route agent.VerifierRoute) verifierSummary {
	return verifierSummary{
		Enabled:          route.Enabled,
		Agent:            route.Agent,
		Provider:         route.Provider,
		Model:            route.Model,
		Modes:            append([]string(nil), route.Modes...),
		MaxTokens:        route.MaxTokens,
		ProviderOverride: route.ProviderOverride,
		ModelOverride:    route.ModelOverride,
	}
}

func stripSessionArtifactContent(snapshot *session.Snapshot) {
	if snapshot == nil {
		return
	}
	for i := range snapshot.Artifacts {
		snapshot.Artifacts[i].Content = ""
	}
}

func (s *Server) runtimeCostDiagnostics(snapshot session.Snapshot) costDiagnostics {
	diagnostics := promptCostDiagnostics(snapshot)
	if s != nil && s.runtime != nil {
		diagnostics.AuxiliaryRoutes = auxiliaryRouteSummaries(s.runtime.AuxiliaryModelRoutes())
	}
	diagnostics.Tuning = buildCostRouteTuning(diagnostics, snapshot, diagnostics.AuxiliaryRoutes)
	diagnostics.Recommendations = buildCostRecommendations(diagnostics)
	diagnostics.Features = buildCostControlFeatures(diagnostics, snapshot, diagnostics.AuxiliaryRoutes)
	return diagnostics
}

func promptCostDiagnostics(snapshot session.Snapshot) costDiagnostics {
	history := append([]schema.PromptBudget(nil), snapshot.PromptBudgets...)
	tokenUsages := append([]schema.TokenUsageSample(nil), snapshot.TokenUsages...)
	latest := snapshot.PromptBudget
	if latest == nil && len(history) > 0 {
		copied := history[len(history)-1]
		latest = &copied
	}
	diagnostics := summarizePromptCostSamples(history, tokenUsages, latest)
	diagnostics.Recommendations = buildCostRecommendations(diagnostics)
	diagnostics.Features = buildCostControlFeatures(diagnostics, snapshot, nil)
	return diagnostics
}

func summarizePromptCostSamples(history []schema.PromptBudget, tokenUsages []schema.TokenUsageSample, latest *schema.PromptBudget) costDiagnostics {
	diagnostics := costDiagnostics{
		Latest:            latest,
		History:           history,
		TokenUsageHistory: tokenUsages,
		Samples:           len(history),
		TokenUsageSamples: len(tokenUsages),
	}
	prefixes := make(map[string]struct{})
	totalEstimated := 0
	totalCacheable := 0
	totalNonCacheable := 0
	for _, budget := range history {
		totalEstimated += budget.EstimatedPromptTokens
		totalCacheable += budget.CacheablePrefixTokens
		totalNonCacheable += nonCacheablePromptTokens(budget)
		diagnostics.HistoryEstimatedSavedTokens += budget.HistoryEstimatedSavedTokens
		diagnostics.HistoryDeduplicatedItems += budget.HistoryPromptDeduplicatedItems + budget.HistoryToolDeduplicatedItems
		diagnostics.HistoryCompactedOlderItems += budget.HistoryPromptCompactedOlderItems + budget.HistoryToolCompactedOlderItems
		diagnostics.ToolSchemaDiagnosticSamples += budget.ToolSchemaDiagnosticCount
		diagnostics.ToolSchemaDiagnosticOmitted += budget.ToolSchemaDiagnosticOmitted
		diagnostics.ToolSchemaEstimatedSavedTokens += budget.ToolSchemaEstimatedSavedTokens
		diagnostics.MemoryBlockSamples += budget.MemoryBlockCount
		diagnostics.MemoryOmittedCount += budget.MemoryOmittedCount
		diagnostics.MemoryEstimatedSavedTokens += budget.MemoryEstimatedSavedTokens
		diagnostics.ArtifactRefSamples += budget.ArtifactRefCount
		diagnostics.CompactedToolResultCount += budget.CompactedToolResultCount
		diagnostics.ArtifactOmittedTokens += budget.ArtifactOmittedTokens
		diagnostics.SkillOmittedTokens += budget.SkillOmittedTokens
		diagnostics.OmittedContextCount += len(budget.OmittedContext)
		if budget.EstimatedPromptTokens > diagnostics.MaxEstimatedPromptTokens {
			diagnostics.MaxEstimatedPromptTokens = budget.EstimatedPromptTokens
		}
		if strings.TrimSpace(budget.PromptPrefixHash) != "" {
			prefixes[budget.PromptPrefixHash] = struct{}{}
		}
	}
	for _, usage := range tokenUsages {
		diagnostics.TotalPromptTokens += usage.PromptTokens
		diagnostics.TotalOutputTokens += usage.OutputTokens
		diagnostics.TotalCachedTokens += usage.CachedTokens
		if usage.TotalTokens > 0 {
			diagnostics.TotalTokens += usage.TotalTokens
		} else {
			diagnostics.TotalTokens += usage.PromptTokens + usage.OutputTokens
		}
	}
	if len(history) > 0 {
		diagnostics.AverageEstimatedPromptTokens = totalEstimated / len(history)
		diagnostics.AverageCacheablePrefixTokens = totalCacheable / len(history)
		diagnostics.AverageNonCacheableTokens = totalNonCacheable / len(history)
	}
	diagnostics.UniquePromptPrefixes = len(prefixes)
	if diagnostics.Samples > 0 && diagnostics.UniquePromptPrefixes > 0 {
		diagnostics.PromptPrefixReuseSamples = diagnostics.Samples - diagnostics.UniquePromptPrefixes
		if diagnostics.PromptPrefixReuseSamples < 0 {
			diagnostics.PromptPrefixReuseSamples = 0
		}
		diagnostics.PromptPrefixReuseRate = ratio(diagnostics.PromptPrefixReuseSamples, diagnostics.Samples)
	}
	if diagnostics.TotalPromptTokens > 0 {
		diagnostics.ProviderCacheHitRate = ratio(diagnostics.TotalCachedTokens, diagnostics.TotalPromptTokens)
	}
	if latest != nil {
		diagnostics.LatestPromptPrefixHash = latest.PromptPrefixHash
	}
	diagnostics.ByAgent = buildCostTrends(history, tokenUsages, "agent")
	diagnostics.ByMode = buildCostTrends(history, tokenUsages, "mode")
	diagnostics.ByStage = buildCostTrends(history, tokenUsages, "stage")
	return diagnostics
}

func buildCostControlFeatures(diagnostics costDiagnostics, snapshot session.Snapshot, routes []auxiliarySummary) []costControlFeature {
	features := []costControlFeature{
		{
			Code:            "prompt_budget_events",
			Name:            "Prompt budget telemetry",
			Category:        "measurement",
			State:           observedState(diagnostics.Samples > 0),
			Enabled:         true,
			Observed:        diagnostics.Samples > 0,
			Samples:         diagnostics.Samples,
			EstimatedTokens: diagnostics.AverageEstimatedPromptTokens,
			Description:     "Emits pre-call prompt token estimates, cacheable-prefix hashes, and visible-tool counts before provider calls.",
			Measurement:     "samples is the number of prompt_budget snapshots retained in this session.",
			Recommendation:  "Keep this enabled and use recommendations to identify avoidable context before adding extra helper calls.",
		},
		{
			Code:            "tool_schema_minimization",
			Name:            "Tool schema minimization",
			Category:        "prompt_reduction",
			State:           toolSchemaMinimizationState(diagnostics),
			Enabled:         true,
			Observed:        latestFilteredToolCount(diagnostics) > 0,
			Samples:         diagnostics.Samples,
			EstimatedTokens: latestToolSchemaTokens(diagnostics),
			FilteredTools:   latestFilteredToolCount(diagnostics),
			InjectedTools:   latestInjectedToolCount(diagnostics),
			OmittedItems:    diagnostics.ToolSchemaDiagnosticOmitted,
			SavedTokens:     diagnostics.ToolSchemaEstimatedSavedTokens,
			Description:     "Only exposes tools allowed by the active agent and matched skill while executor policy still enforces hidden tools.",
			Measurement:     "injected_tools, filtered_tools, and saved_tokens summarize the latest tool-surface budget.",
			Recommendation:  "Use narrower agent allowed_tools or skill tool declarations when tool_schema_high appears.",
		},
		{
			Code:           "session_history_compaction",
			Name:           "Session history compaction",
			Category:       "prompt_reduction",
			State:          observedState(diagnostics.HistoryEstimatedSavedTokens > 0 || diagnostics.HistoryDeduplicatedItems > 0 || diagnostics.HistoryCompactedOlderItems > 0),
			Enabled:        true,
			Observed:       diagnostics.HistoryEstimatedSavedTokens > 0 || diagnostics.HistoryDeduplicatedItems > 0 || diagnostics.HistoryCompactedOlderItems > 0,
			Samples:        diagnostics.Samples,
			SavedTokens:    diagnostics.HistoryEstimatedSavedTokens,
			Description:    "Deduplicates and compacts bounded prompt/tool history before it is injected into system prompts.",
			Measurement:    "saved_tokens is the estimated prompt-history savings accumulated in retained budget samples.",
			Recommendation: "For very long tasks, split work into focused workflow stages and pass compact artifacts between stages.",
		},
		{
			Code:           "session_artifact_refs",
			Name:           "Session artifact references",
			Category:       "prompt_reduction",
			State:          observedState(len(snapshot.Artifacts) > 0 || diagnostics.ArtifactRefSamples > 0 || diagnostics.CompactedToolResultCount > 0),
			Enabled:        true,
			Observed:       len(snapshot.Artifacts) > 0 || diagnostics.ArtifactRefSamples > 0 || diagnostics.CompactedToolResultCount > 0,
			Samples:        maxInt(len(snapshot.Artifacts), diagnostics.CompactedToolResultCount),
			SavedTokens:    diagnostics.ArtifactOmittedTokens,
			ArtifactRefs:   diagnostics.ArtifactRefSamples,
			OmittedItems:   diagnostics.CompactedToolResultCount,
			Description:    "Stores oversized observations as rehydratable goflow://session-artifacts/<id> refs instead of replaying full content every turn.",
			Measurement:    "artifact_refs counts refs visible to model requests; saved_tokens estimates omitted compacted tool-result content.",
			Recommendation: "Use artifact refs when exact large tool output is needed again; otherwise keep downstream inputs compact.",
		},
		{
			Code:            "retrieval_memory_blocks",
			Name:            "Retrieval memory blocks",
			Category:        "prompt_reduction",
			State:           retrievalMemoryState(diagnostics),
			Enabled:         true,
			Observed:        diagnostics.MemoryBlockSamples > 0,
			Samples:         diagnostics.Samples,
			EstimatedTokens: latestMemoryTokens(diagnostics),
			SavedTokens:     diagnostics.MemoryEstimatedSavedTokens,
			MemoryBlocks:    diagnostics.MemoryBlockSamples,
			OmittedItems:    diagnostics.MemoryOmittedCount,
			Description:     "Injects selected project/task/file/error summaries and refs instead of loading full memory or session history.",
			Measurement:     "memory_blocks counts summary refs injected into retained prompt-budget samples.",
			Recommendation:  "Keep project and file summaries fresh so model requests can stay summary-first and retrieve detail only on demand.",
		},
		{
			Code:            "skill_schema_slimming",
			Name:            "Skill schema slimming",
			Category:        "prompt_reduction",
			State:           skillSchemaSlimmingState(diagnostics),
			Enabled:         true,
			Observed:        diagnostics.SkillOmittedTokens > 0,
			Samples:         diagnostics.Samples,
			EstimatedTokens: latestSkillTokens(diagnostics),
			SavedTokens:     diagnostics.SkillOmittedTokens,
			OmittedItems:    diagnostics.OmittedContextCount,
			Description:     "Injects matched skill identity, description, scripts, resources, and bounded instruction summaries while tracking omitted full instructions by ref.",
			Measurement:     "saved_tokens is the estimated full skill instruction text avoided by summary-first prompt injection.",
			Recommendation:  "Keep large skills structured with concise headings and examples so the summary remains useful.",
		},
		{
			Code:            "provider_prompt_cache_signals",
			Name:            "Provider prompt-cache signals",
			Category:        "cache_observability",
			State:           providerPromptCacheState(diagnostics),
			Enabled:         true,
			Observed:        diagnostics.TotalCachedTokens > 0,
			Samples:         diagnostics.TokenUsageSamples,
			EstimatedTokens: diagnostics.AverageCacheablePrefixTokens,
			SavedTokens:     diagnostics.TotalCachedTokens,
			Description:     "Records stable prompt-prefix hashes and provider-reported cached tokens when the selected provider/model reports them.",
			Measurement:     "saved_tokens is provider-reported cached input tokens, not a local KV-cache stored by GoFlow.",
			Recommendation:  "If reusable prefixes show zero cached tokens, verify provider/model prompt-cache support and keep tool visibility stable.",
		},
	}
	for _, route := range routes {
		features = append(features, auxiliaryCostFeature(route))
	}
	return features
}

func observedState(observed bool) string {
	if observed {
		return "observed"
	}
	return "ready"
}

func toolSchemaMinimizationState(diagnostics costDiagnostics) string {
	if diagnostics.Samples == 0 {
		return "ready"
	}
	if latestFilteredToolCount(diagnostics) > 0 {
		return "observed"
	}
	return "active_no_filtering_needed"
}

func retrievalMemoryState(diagnostics costDiagnostics) string {
	if diagnostics.Samples == 0 {
		return "ready"
	}
	if diagnostics.MemoryBlockSamples > 0 {
		return "observed"
	}
	return "ready_no_matches"
}

func skillSchemaSlimmingState(diagnostics costDiagnostics) string {
	if diagnostics.Samples == 0 {
		return "ready"
	}
	if diagnostics.SkillOmittedTokens > 0 {
		return "observed"
	}
	if diagnostics.Latest != nil && strings.TrimSpace(diagnostics.Latest.SkillName) != "" {
		return "active_no_slimming_needed"
	}
	return "ready"
}

func providerPromptCacheState(diagnostics costDiagnostics) string {
	if diagnostics.TotalCachedTokens > 0 {
		return "observed"
	}
	if diagnostics.TokenUsageSamples == 0 {
		return "waiting_for_provider_usage"
	}
	if diagnostics.PromptPrefixReuseSamples > 0 {
		return "not_observed"
	}
	return "ready"
}

func auxiliaryCostFeature(route auxiliarySummary) costControlFeature {
	enabled := route.Enabled
	state := "disabled"
	if enabled {
		state = "configured"
	}
	description := "Routes low-value helper calls to an optional cheaper model."
	recommendation := "Leave disabled unless this helper saves more cost than the extra model call adds."
	switch route.Kind {
	case "router":
		description = "Optionally uses a cheap model only when local intent routing cannot classify an ordinary request."
		recommendation = "Enable only with a cheap, low-token model and confirm routing quality in cost diagnostics."
	case "summarizer":
		description = "Optionally uses a cheap model for final no-tool summaries after an iteration budget is reached."
		recommendation = "Enable only if budget-summary calls are common and cheaper than using the main agent model."
	}
	return costControlFeature{
		Code:           "auxiliary_" + route.Kind,
		Name:           "Auxiliary " + route.Kind + " model",
		Category:       "auxiliary_model",
		State:          state,
		Enabled:        enabled,
		Observed:       false,
		RequiresConfig: true,
		ExtraModelCall: true,
		Provider:       route.Provider,
		Model:          route.Model,
		Description:    description,
		Measurement:    "Emits its own prompt_budget and token_usage events when the helper route is invoked.",
		Recommendation: recommendation,
	}
}

func latestFilteredToolCount(diagnostics costDiagnostics) int {
	if diagnostics.Latest == nil {
		return 0
	}
	return diagnostics.Latest.FilteredToolCount
}

func latestInjectedToolCount(diagnostics costDiagnostics) int {
	if diagnostics.Latest == nil {
		return 0
	}
	return diagnostics.Latest.ExposedToolCount
}

func latestToolSchemaTokens(diagnostics costDiagnostics) int {
	if diagnostics.Latest == nil {
		return 0
	}
	return diagnostics.Latest.ToolSchemaTokens
}

func latestMemoryTokens(diagnostics costDiagnostics) int {
	if diagnostics.Latest == nil {
		return 0
	}
	return diagnostics.Latest.MemoryTokens
}

func latestSkillTokens(diagnostics costDiagnostics) int {
	if diagnostics.Latest == nil {
		return 0
	}
	if diagnostics.Latest.SkillInjectedTokens > 0 {
		return diagnostics.Latest.SkillInjectedTokens
	}
	return diagnostics.Latest.SkillTokens
}

func buildCostRouteTuning(diagnostics costDiagnostics, snapshot session.Snapshot, routes []auxiliarySummary) []costRouteTuning {
	kinds := []string{"router", "summarizer"}
	out := make([]costRouteTuning, 0, len(kinds))
	for _, kind := range kinds {
		route := auxiliaryRouteByKind(routes, kind)
		tuning := costRouteTuning{
			Kind:             kind,
			Enabled:          route.Enabled,
			Configured:       route.Enabled || strings.TrimSpace(route.Provider) != "" || strings.TrimSpace(route.Model) != "",
			Provider:         route.Provider,
			Model:            route.Model,
			QualityChecklist: costRouteQualityChecklist(kind),
		}
		observedBudgets := observedRoutePromptBudgets(snapshot.PromptBudgets, kind)
		observedUsages := observedRouteTokenUsages(snapshot.TokenUsages, kind)
		tuning.PromptBudgetSamples = len(observedBudgets)
		tuning.TokenUsageSamples = len(observedUsages)
		tuning.ObservedSamples = maxInt(tuning.PromptBudgetSamples, tuning.TokenUsageSamples)
		for _, usage := range observedUsages {
			tuning.ObservedPromptTokens += usage.PromptTokens
			tuning.ObservedOutputTokens += usage.OutputTokens
			tuning.ObservedCachedTokens += usage.CachedTokens
			if usage.TotalTokens > 0 {
				tuning.ObservedTotalTokens += usage.TotalTokens
			} else {
				tuning.ObservedTotalTokens += usage.PromptTokens + usage.OutputTokens
			}
		}
		if tuning.ObservedTotalTokens == 0 {
			for _, budget := range observedBudgets {
				tuning.ObservedTotalTokens += budget.EstimatedPromptTokens
			}
		}
		candidateSamples, candidateAverage := costRouteCandidateSignal(diagnostics, snapshot, kind)
		tuning.CandidateSamples = candidateSamples
		tuning.CandidateAveragePromptTokens = candidateAverage
		tuning.EstimatedExtraCallTokens = estimatedAuxiliaryRouteTokens(kind, route)
		if tuning.ObservedSamples > 0 {
			tuning.EstimatedMainPromptTokens = candidateAverage * tuning.ObservedSamples
			if tuning.EstimatedMainPromptTokens == 0 {
				tuning.EstimatedMainPromptTokens = averagePromptBudgetTokens(observedBudgets) * tuning.ObservedSamples
			}
			tuning.NetSavingsSignal = tuning.EstimatedMainPromptTokens - tuning.ObservedTotalTokens
		} else if candidateSamples > 0 {
			tuning.EstimatedMainPromptTokens = candidateAverage * candidateSamples
			tuning.NetSavingsSignal = tuning.EstimatedMainPromptTokens - tuning.EstimatedExtraCallTokens*candidateSamples
		}
		tuning.State, tuning.Recommendation, tuning.Action = costRouteTuningMessage(kind, tuning)
		out = append(out, tuning)
	}
	return out
}

func auxiliaryRouteByKind(routes []auxiliarySummary, kind string) auxiliarySummary {
	for _, route := range routes {
		if strings.EqualFold(route.Kind, kind) {
			return route
		}
	}
	return auxiliarySummary{Kind: kind}
}

func observedRoutePromptBudgets(items []schema.PromptBudget, kind string) []schema.PromptBudget {
	out := make([]schema.PromptBudget, 0)
	for _, item := range items {
		if costRouteSampleMatches(kind, item.AgentID, item.Mode, item.WorkflowName, item.TaskStage) {
			out = append(out, item)
		}
	}
	return out
}

func observedRouteTokenUsages(items []schema.TokenUsageSample, kind string) []schema.TokenUsageSample {
	out := make([]schema.TokenUsageSample, 0)
	for _, item := range items {
		if costRouteSampleMatches(kind, item.AgentID, item.Mode, item.WorkflowName, item.TaskStage) {
			out = append(out, item)
		}
	}
	return out
}

func costRouteSampleMatches(kind, agentID, mode, workflowName, taskStage string) bool {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	mode = strings.ToLower(strings.TrimSpace(mode))
	workflowName = strings.ToLower(strings.TrimSpace(workflowName))
	taskStage = strings.ToLower(strings.TrimSpace(taskStage))
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "router":
		return agentID == "router" || mode == "route" || strings.Contains(taskStage, "router") || strings.Contains(taskStage, "route")
	case "summarizer":
		return agentID == "summarizer" || mode == "summarize" || strings.Contains(taskStage, "summar")
	default:
		return strings.Contains(agentID, kind) || strings.Contains(mode, kind) || strings.Contains(workflowName, kind) || strings.Contains(taskStage, kind)
	}
}

func costRouteCandidateSignal(diagnostics costDiagnostics, snapshot session.Snapshot, kind string) (int, int) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "router":
		return diagnostics.Samples, diagnostics.AverageEstimatedPromptTokens
	case "summarizer":
		summaryBudgets := observedRoutePromptBudgets(snapshot.PromptBudgets, "summarizer")
		if len(summaryBudgets) > 0 {
			return len(summaryBudgets), averagePromptBudgetTokens(summaryBudgets)
		}
		if summary := costTrendByStageSubstring(diagnostics.ByStage, "summar"); summary != nil {
			return summary.PromptBudgetSamples, summary.AverageEstimatedPromptTokens
		}
		return 0, 0
	default:
		return diagnostics.Samples, diagnostics.AverageEstimatedPromptTokens
	}
}

func averagePromptBudgetTokens(items []schema.PromptBudget) int {
	if len(items) == 0 {
		return 0
	}
	total := 0
	for _, item := range items {
		total += item.EstimatedPromptTokens
	}
	return total / len(items)
}

func estimatedAuxiliaryRouteTokens(kind string, route auxiliarySummary) int {
	maxTokens := route.MaxTokens
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "router":
		if maxTokens <= 0 {
			maxTokens = 64
		}
		return 160 + maxTokens
	case "summarizer":
		if maxTokens <= 0 {
			maxTokens = 512
		}
		return 420 + maxTokens
	default:
		if maxTokens <= 0 {
			maxTokens = 256
		}
		return 240 + maxTokens
	}
}

func costRouteQualityChecklist(kind string) []string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "router":
		return []string{
			"compare router decisions with the final selected agent/mode",
			"track false fix/audit routes because they can trigger unnecessary tool approvals",
			"keep router max_tokens low and require deterministic one-word outputs",
		}
	case "summarizer":
		return []string{
			"compare summaries against completed tool observations before using them as final answers",
			"verify no repeated plan text or fabricated verification claims",
			"keep summarizer max_tokens bounded and disable tools for summary calls",
		}
	default:
		return []string{"compare quality against the main model before enabling this auxiliary route by default"}
	}
}

func costRouteTuningMessage(kind string, tuning costRouteTuning) (string, string, string) {
	if tuning.Enabled && tuning.ObservedSamples > 0 {
		if tuning.NetSavingsSignal > 0 {
			return "observed_saving", "Observed samples suggest this auxiliary route may reduce token spend; keep checking quality before making it the default path.", "keep enabled for the current workload and compare route quality against main-model outputs"
		}
		return "observed_costly", "Observed samples do not show a positive token-savings signal yet; the helper may be adding extra model calls without enough offset.", "disable or narrow this route unless quality improves enough to justify the extra call"
	}
	if tuning.Enabled {
		return "configured_waiting_for_samples", "The route is configured but has not produced enough observable samples in this session.", "run representative tasks and review observed_samples, token_usage_samples, and quality checklist before deciding"
	}
	if tuning.CandidateSamples >= 3 && tuning.CandidateAveragePromptTokens >= auxiliaryCandidatePromptThreshold(kind) {
		return "candidate", "Measured prompt samples are large enough to evaluate a cheaper auxiliary route with an A/B run.", "configure a cheap provider/model for this route, run the same workflow, then compare token_usage and quality"
	}
	return "not_enough_data", "There is not enough measured prompt data to justify adding an extra auxiliary model call.", "leave disabled until representative workflow samples are available"
}

func auxiliaryCandidatePromptThreshold(kind string) int {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "summarizer":
		return 800
	default:
		return 1500
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func buildCostRecommendations(diagnostics costDiagnostics) []costRecommendation {
	if diagnostics.Latest == nil && len(diagnostics.ByAgent) == 0 {
		return nil
	}
	window := recentCostRecommendationWindow(diagnostics, 8)
	if window.Samples == 0 && window.TokenUsageSamples == 0 {
		window = diagnostics
	}
	out := make([]costRecommendation, 0, 4)
	if latest := diagnostics.Latest; latest != nil {
		nonCacheable := nonCacheablePromptTokens(*latest)
		if latest.MessageTokens >= 2000 {
			out = append(out, costRecommendation{
				Level:               "warning",
				Code:                "message_context_high",
				Message:             "Recent session/tool context is a large part of this prompt. Prefer artifact refs, shorter stage inputs, or a fresh focused workflow stage.",
				AgentID:             latest.AgentID,
				Mode:                latest.Mode,
				WorkflowName:        latest.WorkflowName,
				TaskStage:           latest.TaskStage,
				EstimatedTokens:     latest.MessageTokens,
				Measurement:         fmt.Sprintf("latest message/tool context estimate is %d tokens", latest.MessageTokens),
				Action:              "summarize or store large observations as artifacts, then pass compact refs into downstream stages",
				ExpectedSavingsKind: "prompt_reduction",
			})
		}
		if latest.ToolSchemaTokens >= 1000 && latest.FilteredToolCount > 0 {
			out = append(out, costRecommendation{
				Level:               "info",
				Code:                "tool_schema_high",
				Message:             "Visible tool schemas are costly. Narrow agent allowed_tools or skill tool declarations for this task.",
				AgentID:             latest.AgentID,
				Mode:                latest.Mode,
				WorkflowName:        latest.WorkflowName,
				TaskStage:           latest.TaskStage,
				EstimatedTokens:     latest.ToolSchemaTokens,
				Measurement:         fmt.Sprintf("latest exposed tool schema estimate is %d tokens after filtering %d tools and saving about %d tokens", latest.ToolSchemaTokens, latest.FilteredToolCount, latest.ToolSchemaEstimatedSavedTokens),
				Action:              "move broad tool access into narrower agents or skills and expose exact allowed_tools for this workflow",
				RequiresConfig:      true,
				ExpectedSavingsKind: "tool_schema_reduction",
			})
		}
		if latest.EstimatedPromptTokens > 0 && nonCacheable >= 1000 && nonCacheable*2 >= latest.EstimatedPromptTokens {
			out = append(out, costRecommendation{
				Level:               "info",
				Code:                "non_cacheable_context_high",
				Message:             "Most estimated prompt tokens are outside the stable cacheable prefix. Keep repeated instructions and tool schemas stable, and pass volatile data as compact stage inputs.",
				AgentID:             latest.AgentID,
				Mode:                latest.Mode,
				WorkflowName:        latest.WorkflowName,
				TaskStage:           latest.TaskStage,
				EstimatedTokens:     nonCacheable,
				Measurement:         fmt.Sprintf("latest non-cacheable prompt estimate is %d of %d tokens", nonCacheable, latest.EstimatedPromptTokens),
				Action:              "keep prompts/tool visibility stable and move volatile evidence into compact stage inputs",
				ExpectedSavingsKind: "cacheability",
			})
		}
		if latest.MemoryBlockCount == 0 && latest.EstimatedPromptTokens >= 1500 {
			out = append(out, costRecommendation{
				Level:               "info",
				Code:                "memory_not_injected",
				Message:             "No memory blocks were injected for this prompt. Rebuild or update project/file memory so future runs can use summary refs instead of fresh context.",
				AgentID:             latest.AgentID,
				Mode:                latest.Mode,
				WorkflowName:        latest.WorkflowName,
				TaskStage:           latest.TaskStage,
				EstimatedTokens:     latest.EstimatedPromptTokens,
				Measurement:         fmt.Sprintf("latest prompt estimate is %d tokens with zero memory blocks", latest.EstimatedPromptTokens),
				Action:              "run memory rebuild and keep project memory current for recurring work",
				ExpectedSavingsKind: "retrieval_memory",
			})
		}
		if latest.SkillOmittedTokens >= 500 {
			out = append(out, costRecommendation{
				Level:               "info",
				Code:                "skill_summary_saving",
				Message:             "Large skill instructions were summarized before prompt injection. Keep the full skill available by ref and make headings/examples concise.",
				AgentID:             latest.AgentID,
				Mode:                latest.Mode,
				WorkflowName:        latest.WorkflowName,
				TaskStage:           latest.TaskStage,
				EstimatedTokens:     latest.SkillOmittedTokens,
				Measurement:         fmt.Sprintf("latest skill summary omitted an estimated %d instruction tokens", latest.SkillOmittedTokens),
				Action:              "keep skill instructions structured so summary-first injection preserves the useful contract",
				ExpectedSavingsKind: "skill_schema_reduction",
			})
		}
	}
	if window.Samples >= 4 && window.UniquePromptPrefixes > window.Samples/2 {
		out = append(out, costRecommendation{
			Level:               "info",
			Code:                "prompt_prefix_churn",
			Message:             "Prompt prefixes are changing frequently. Stable agent prompts, skill context, and tool visibility improve provider prompt-cache hit rates.",
			EstimatedTokens:     window.AverageCacheablePrefixTokens,
			Measurement:         fmt.Sprintf("recent %d prompt samples contain %d unique prompt prefixes", window.Samples, window.UniquePromptPrefixes),
			Action:              "avoid changing base prompts and tool visibility between similar workflow stages",
			ExpectedSavingsKind: "provider_prompt_cache",
		})
	}
	if window.TokenUsageSamples >= 3 &&
		window.TotalCachedTokens == 0 &&
		window.AverageCacheablePrefixTokens >= 1000 &&
		window.PromptPrefixReuseSamples > 0 {
		out = append(out, costRecommendation{
			Level:               "info",
			Code:                "provider_cache_not_observed",
			Message:             "Prompt prefixes appear reusable, but provider-reported cached tokens are zero. Confirm the selected provider/model supports prompt caching and that stable-prefix hashes remain consistent.",
			EstimatedTokens:     window.AverageCacheablePrefixTokens,
			Measurement:         fmt.Sprintf("recent %d samples show %d reusable prefixes with zero provider-reported cached tokens", window.Samples, window.PromptPrefixReuseSamples),
			Action:              "verify provider/model prompt-cache support or use a model route that reports cached_tokens",
			RequiresConfig:      true,
			ExpectedSavingsKind: "provider_prompt_cache",
		})
	}
	if len(window.ByAgent) > 0 {
		top := window.ByAgent[0]
		if top.AverageEstimatedPromptTokens >= 6000 {
			out = append(out, costRecommendation{
				Level:               "warning",
				Code:                "agent_prompt_high",
				Message:             "This agent has a high average estimated prompt size. Consider a narrower agent profile, fewer visible tools, or splitting the workflow into smaller stages.",
				AgentID:             top.AgentID,
				EstimatedTokens:     top.AverageEstimatedPromptTokens,
				Measurement:         fmt.Sprintf("agent %s averages %d estimated prompt tokens across the recent %d samples", top.AgentID, top.AverageEstimatedPromptTokens, top.PromptBudgetSamples),
				Action:              "split this role into narrower agents or move repeated work into a workflow with compact handoffs",
				RequiresConfig:      true,
				ExpectedSavingsKind: "prompt_reduction",
			})
		}
	}
	if !auxiliaryRouteEnabled(diagnostics.AuxiliaryRoutes, "router") && window.Samples >= 3 && window.AverageEstimatedPromptTokens >= 1500 {
		out = append(out, costRecommendation{
			Level:               "info",
			Code:                "router_auxiliary_candidate",
			Message:             "Routing samples are frequent enough to evaluate an optional low-cost router model. Enable only if it reduces main-model classification calls without hurting routing quality.",
			EstimatedTokens:     window.AverageEstimatedPromptTokens,
			Measurement:         fmt.Sprintf("recent %d prompt samples average %d estimated prompt tokens and router route is disabled", window.Samples, window.AverageEstimatedPromptTokens),
			Action:              "configure cost_control.router with a cheaper model, compare route accuracy and token_usage before leaving it enabled",
			RequiresConfig:      true,
			ExpectedSavingsKind: "auxiliary_model_route",
		})
	}
	if !auxiliaryRouteEnabled(diagnostics.AuxiliaryRoutes, "summarizer") {
		if summary := costTrendByStageSubstring(window.ByStage, "summar"); summary != nil && summary.AverageEstimatedPromptTokens >= 800 {
			out = append(out, costRecommendation{
				Level:               "info",
				Code:                "summarizer_auxiliary_candidate",
				Message:             "Summary stages have measurable prompt cost. A cheaper summarizer model may reduce cost when iteration-budget summaries happen often.",
				TaskStage:           summary.TaskStage,
				EstimatedTokens:     summary.AverageEstimatedPromptTokens,
				Measurement:         fmt.Sprintf("stage %s averages %d estimated prompt tokens across the recent %d samples", summary.Key, summary.AverageEstimatedPromptTokens, summary.PromptBudgetSamples),
				Action:              "configure cost_control.summarizer with a cheap model and compare final-summary quality before enabling by default",
				RequiresConfig:      true,
				ExpectedSavingsKind: "auxiliary_model_route",
			})
		}
	}
	return out
}

func recentCostRecommendationWindow(diagnostics costDiagnostics, limit int) costDiagnostics {
	if limit <= 0 {
		limit = 8
	}
	history := diagnostics.History
	if len(history) > limit {
		history = append([]schema.PromptBudget(nil), history[len(history)-limit:]...)
	} else {
		history = append([]schema.PromptBudget(nil), history...)
	}
	usages := diagnostics.TokenUsageHistory
	if len(usages) > limit {
		usages = append([]schema.TokenUsageSample(nil), usages[len(usages)-limit:]...)
	} else {
		usages = append([]schema.TokenUsageSample(nil), usages...)
	}
	latest := diagnostics.Latest
	if latest == nil && len(history) > 0 {
		copied := history[len(history)-1]
		latest = &copied
	}
	window := summarizePromptCostSamples(history, usages, latest)
	window.AuxiliaryRoutes = append([]auxiliarySummary(nil), diagnostics.AuxiliaryRoutes...)
	return window
}

func auxiliaryRouteEnabled(routes []auxiliarySummary, kind string) bool {
	for _, route := range routes {
		if strings.EqualFold(route.Kind, kind) && route.Enabled {
			return true
		}
	}
	return false
}

func costTrendByStageSubstring(items []costTrend, needle string) *costTrend {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return nil
	}
	for i := range items {
		if strings.Contains(strings.ToLower(items[i].Key), needle) || strings.Contains(strings.ToLower(items[i].TaskStage), needle) {
			return &items[i]
		}
	}
	return nil
}

func buildCostTrends(budgets []schema.PromptBudget, usages []schema.TokenUsageSample, dimension string) []costTrend {
	items := make(map[string]*costTrend)
	prefixes := make(map[string]map[string]struct{})
	for _, budget := range budgets {
		key, agentID, mode, workflowName, taskStage := costTrendKey(dimension, budget.AgentID, budget.Mode, budget.WorkflowName, budget.TaskStage)
		if key == "" {
			continue
		}
		item := ensureCostTrend(items, key, agentID, mode, workflowName, taskStage)
		item.PromptBudgetSamples++
		item.AverageEstimatedPromptTokens += budget.EstimatedPromptTokens
		item.AverageCacheablePrefixTokens += budget.CacheablePrefixTokens
		item.AverageNonCacheableTokens += nonCacheablePromptTokens(budget)
		item.HistoryEstimatedSavedTokens += budget.HistoryEstimatedSavedTokens
		item.HistoryDeduplicatedItems += budget.HistoryPromptDeduplicatedItems + budget.HistoryToolDeduplicatedItems
		item.HistoryCompactedOlderItems += budget.HistoryPromptCompactedOlderItems + budget.HistoryToolCompactedOlderItems
		item.ToolSchemaDiagnosticSamples += budget.ToolSchemaDiagnosticCount
		item.ToolSchemaDiagnosticOmitted += budget.ToolSchemaDiagnosticOmitted
		item.ToolSchemaEstimatedSavedTokens += budget.ToolSchemaEstimatedSavedTokens
		item.MemoryBlockSamples += budget.MemoryBlockCount
		item.MemoryEstimatedSavedTokens += budget.MemoryEstimatedSavedTokens
		item.ArtifactRefSamples += budget.ArtifactRefCount
		item.ArtifactOmittedTokens += budget.ArtifactOmittedTokens
		item.SkillOmittedTokens += budget.SkillOmittedTokens
		item.OmittedContextCount += len(budget.OmittedContext)
		if budget.EstimatedPromptTokens > item.MaxEstimatedPromptTokens {
			item.MaxEstimatedPromptTokens = budget.EstimatedPromptTokens
		}
		if strings.TrimSpace(budget.PromptPrefixHash) != "" {
			if prefixes[key] == nil {
				prefixes[key] = make(map[string]struct{})
			}
			prefixes[key][budget.PromptPrefixHash] = struct{}{}
		}
	}
	for _, usage := range usages {
		key, agentID, mode, workflowName, taskStage := costTrendKey(dimension, usage.AgentID, usage.Mode, usage.WorkflowName, usage.TaskStage)
		if key == "" {
			continue
		}
		item := ensureCostTrend(items, key, agentID, mode, workflowName, taskStage)
		item.TokenUsageSamples++
		item.TotalPromptTokens += usage.PromptTokens
		item.TotalOutputTokens += usage.OutputTokens
		item.TotalCachedTokens += usage.CachedTokens
		if usage.TotalTokens > 0 {
			item.TotalTokens += usage.TotalTokens
		} else {
			item.TotalTokens += usage.PromptTokens + usage.OutputTokens
		}
	}
	out := make([]costTrend, 0, len(items))
	for key, item := range items {
		if item.PromptBudgetSamples > 0 {
			item.AverageEstimatedPromptTokens /= item.PromptBudgetSamples
			item.AverageCacheablePrefixTokens /= item.PromptBudgetSamples
			item.AverageNonCacheableTokens /= item.PromptBudgetSamples
		}
		item.UniquePromptPrefixes = len(prefixes[key])
		if item.PromptBudgetSamples > 0 && item.UniquePromptPrefixes > 0 {
			item.PromptPrefixReuseSamples = item.PromptBudgetSamples - item.UniquePromptPrefixes
			if item.PromptPrefixReuseSamples < 0 {
				item.PromptPrefixReuseSamples = 0
			}
			item.PromptPrefixReuseRate = ratio(item.PromptPrefixReuseSamples, item.PromptBudgetSamples)
		}
		if item.TotalPromptTokens > 0 {
			item.ProviderCacheHitRate = ratio(item.TotalCachedTokens, item.TotalPromptTokens)
		}
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalTokens == out[j].TotalTokens {
			if out[i].AverageEstimatedPromptTokens == out[j].AverageEstimatedPromptTokens {
				return out[i].Key < out[j].Key
			}
			return out[i].AverageEstimatedPromptTokens > out[j].AverageEstimatedPromptTokens
		}
		return out[i].TotalTokens > out[j].TotalTokens
	})
	return out
}

func ensureCostTrend(items map[string]*costTrend, key, agentID, mode, workflowName, taskStage string) *costTrend {
	if item, ok := items[key]; ok {
		return item
	}
	item := &costTrend{Key: key, AgentID: agentID, Mode: mode, WorkflowName: workflowName, TaskStage: taskStage}
	items[key] = item
	return item
}

func costTrendKey(dimension, agentID, mode, workflowName, taskStage string) (string, string, string, string, string) {
	agentID = strings.TrimSpace(agentID)
	mode = strings.TrimSpace(mode)
	workflowName = strings.TrimSpace(workflowName)
	taskStage = strings.TrimSpace(taskStage)
	switch dimension {
	case "mode":
		if mode == "" {
			return "", "", "", "", ""
		}
		return mode, "", mode, "", ""
	case "stage":
		if taskStage == "" {
			return "", "", "", "", ""
		}
		key := taskStage
		if workflowName != "" {
			key = workflowName + "/" + taskStage
		}
		return key, "", "", workflowName, taskStage
	default:
		if agentID == "" {
			return "", "", "", "", ""
		}
		return agentID, agentID, "", "", ""
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func nonCacheablePromptTokens(budget schema.PromptBudget) int {
	value := budget.EstimatedPromptTokens - budget.CacheablePrefixTokens
	if value < 0 {
		return 0
	}
	return value
}

func (s *Server) providerSummaries() []providerSummary {
	names := s.runtime.ProviderNames()
	out := make([]providerSummary, 0, len(names))
	for _, id := range names {
		provider, ok := s.runtime.Provider(id)
		if !ok {
			continue
		}
		out = append(out, providerSummary{
			ID:                    id,
			Provider:              provider.Provider,
			BaseURL:               provider.BaseURL,
			Model:                 provider.Model,
			FallbackProvider:      provider.FallbackProvider,
			ProviderMessageFields: append([]string(nil), provider.ProviderMessageFields...),
			Timeout:               provider.Timeout.String(),
			Temperature:           provider.Temperature,
			MaxTokens:             provider.MaxTokens,
			RetryCount:            provider.RetryCount,
			RetryBackoff:          provider.RetryBackoff.String(),
			APIKeySet:             strings.TrimSpace(provider.APIKey) != "",
		})
	}
	return out
}

func (s *Server) agentSummaries() []agentSummary {
	names := s.runtime.AgentNames()
	out := make([]agentSummary, 0, len(names))
	for _, id := range names {
		profile, ok := s.runtime.Profile(id)
		if !ok {
			continue
		}
		out = append(out, agentSummary{
			ID:               id,
			Name:             profile.Name,
			Description:      profile.Description,
			Mode:             profile.Mode,
			Provider:         profile.Provider,
			Model:            profile.Model,
			ToolPolicy:       profile.ToolPolicy,
			AllowedToolKinds: append([]config.ToolKind(nil), profile.AllowedToolKinds...),
		})
	}
	return out
}

func (s *Server) skillSummaries() []skillSummary {
	items := s.runtime.SkillList()
	out := make([]skillSummary, 0, len(items))
	for _, item := range items {
		out = append(out, skillSummary{
			Name:             item.Name,
			Description:      item.Description,
			Mode:             item.Mode,
			PreferredAgent:   item.PreferredAgent,
			AllowedToolKinds: append([]string(nil), item.AllowedToolKinds...),
			NextSkills:       append([]string(nil), item.NextSkills...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (s *Server) mcpServerSummaries(health map[string]string) []mcpServerSummary {
	if s == nil || s.runtime == nil {
		return nil
	}
	servers := s.runtime.MCPServerRefs()
	metrics := s.runtime.MCPCallMetrics()
	out := make([]mcpServerSummary, 0, len(servers))
	seen := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			name = "unnamed"
		}
		seen[name] = struct{}{}
		isolation := strings.ToLower(strings.TrimSpace(server.Isolation))
		if isolation == "" {
			isolation = "none"
		}
		summary := mcpServerSummary{
			Name:                 name,
			Enabled:              server.Enabled,
			Health:               health[name],
			Command:              server.Command,
			Args:                 append([]string(nil), server.Args...),
			WorkDir:              server.WorkDir,
			Isolation:            isolation,
			IsolationProfile:     strings.TrimSpace(server.IsolationProfile),
			NetworkDisabled:      server.NetworkDisabled,
			NetworkEnforced:      false,
			FilesystemSandboxed:  false,
			PrivilegeSandboxed:   false,
			EnvAllowlistSet:      len(server.EnvAllowlist) > 0,
			EnvAllowlist:         sanitizedEnvAllowlist(server.EnvAllowlist),
			SensitiveEnv:         sensitiveEnvAllowlistEntries(server.EnvAllowlist),
			AllowedCommandsSet:   len(server.AllowedCommands) > 0 || len(server.AllowedCommandPaths) > 0,
			AllowedCommands:      append([]string(nil), server.AllowedCommands...),
			AllowedCommandPaths:  append([]string(nil), server.AllowedCommandPaths...),
			MaxRequestBytes:      server.MaxRequestBytes,
			MaxResponseBytes:     server.MaxResponseBytes,
			MaxConcurrentCalls:   server.MaxConcurrentCalls,
			RestartLimit:         server.RestartLimit,
			Cooldown:             durationString(server.Cooldown),
			RequiresRealSandbox:  true,
			CanAccessHostOutside: true,
		}
		if metric, ok := metrics[name]; ok {
			summary.MaxConcurrentCalls = firstPositive(metric.MaxConcurrentCalls, summary.MaxConcurrentCalls)
			summary.ActiveCalls = metric.ActiveCalls
			summary.QueuedCalls = metric.QueuedCalls
			summary.AvailableCallSlots = metric.AvailableCallSlots
		}
		switch isolation {
		case "linux_cgroup":
			summary.IsolationLevel = "resource_control"
			summary.ResourceLimited = true
			summary.SandboxFeatures = []string{"cgroup_resource_limits"}
			summary.MissingSandboxFeatures = []string{"filesystem_policy", "network_policy", "privilege_reduction"}
			summary.RiskLevel = "medium"
			summary.SecurityBoundary = "Linux cgroup resource limits only; filesystem, network, and privilege boundaries are not enforced."
			summary.Warnings = append(summary.Warnings, "linux_cgroup limits CPU, memory, or process counts when configured, but it is not a filesystem or network sandbox")
		case "linux_netns":
			summary.IsolationLevel = "network_namespace"
			summary.Sandboxed = true
			summary.NetworkEnforced = true
			summary.FilesystemSandboxed = false
			summary.PrivilegeSandboxed = false
			summary.ResourceLimited = false
			summary.SandboxFeatures = []string{"network_namespace"}
			summary.MissingSandboxFeatures = []string{"filesystem_policy", "privilege_reduction"}
			summary.RiskLevel = "medium"
			summary.SecurityBoundary = "Linux network namespace isolation is configured through unshare; network egress is isolated, but filesystem and privilege boundaries are not enforced."
			summary.Warnings = append(summary.Warnings, "linux_netns requires host support and sufficient privileges for unshare --net; startup fails instead of falling back if unavailable")
			summary.Warnings = append(summary.Warnings, "linux_netns is not a filesystem, mount, seccomp, or privilege sandbox")
			summary.Recommendations = append(summary.Recommendations, "combine linux_netns with linux_cgroup, containers, or external sandboxing for broader isolation when running untrusted tools")
		case "process_group":
			summary.IsolationLevel = "lifecycle"
			summary.SandboxFeatures = []string{"process_group_lifecycle"}
			summary.MissingSandboxFeatures = []string{"filesystem_policy", "network_policy", "privilege_reduction"}
			summary.RiskLevel = "high"
			summary.SecurityBoundary = "Process-group lifecycle isolation only; no filesystem, network, or privilege sandbox is enforced."
			summary.Warnings = append(summary.Warnings, "process_group helps cleanup child processes but does not constrain host access")
		case "windows_job":
			summary.IsolationLevel = "lifecycle"
			summary.SandboxFeatures = []string{"windows_job_object_lifecycle"}
			summary.MissingSandboxFeatures = []string{"windows_restricted_token", "filesystem_policy", "network_policy", "privilege_reduction"}
			summary.WindowsIsolation = &schema.WindowsIsolationProfile{
				JobObject:       true,
				RestrictedToken: false,
				AppContainer:    false,
				LifecycleOnly:   true,
			}
			summary.RiskLevel = "high"
			summary.SecurityBoundary = "Windows Job Object lifecycle isolation only; no filesystem, network, or privilege sandbox is enforced."
			summary.Warnings = append(summary.Warnings, "windows_job improves process-tree cleanup but is not a restricted-token sandbox")
		case "windows_restricted_token":
			summary.IsolationLevel = "windows_restricted_token"
			summary.Sandboxed = true
			summary.PrivilegeSandboxed = true
			summary.SandboxFeatures = []string{"windows_restricted_token", "windows_low_integrity", "windows_job_object_lifecycle", "process_group_lifecycle"}
			summary.MissingSandboxFeatures = []string{"filesystem_policy", "network_policy"}
			summary.WindowsIsolation = &schema.WindowsIsolationProfile{
				JobObject:       true,
				RestrictedToken: true,
				AppContainer:    false,
				LifecycleOnly:   false,
			}
			summary.RiskLevel = "medium"
			summary.SecurityBoundary = "Windows restricted-token isolation lowers MCP process privileges and integrity level and attaches the process to a kill-on-close Job Object, but does not provide AppContainer, filesystem, or network policy."
			summary.Warnings = append(summary.Warnings, "windows_restricted_token does not restrict filesystem paths or network egress by itself")
			summary.Recommendations = append(summary.Recommendations, "use container isolation when untrusted tools need filesystem or network boundaries")
		case "container":
			options := normalizedIsolationOptions(server.IsolationOptions)
			networkMode := containerSummaryNetworkMode(options["network"], server.NetworkDisabled)
			workspaceMount := strings.ToLower(strings.TrimSpace(options["workspace_mount"]))
			if workspaceMount == "" {
				workspaceMount = "rw"
			}
			readOnlyRootFS := optionEnabled(options["readonly_rootfs"])
			noNewPrivileges := optionEnabled(options["no_new_privileges"])
			capDropAll := containerCapDropIncludesAll(options["cap_drop"])
			tmpfsConfigured := strings.TrimSpace(options["tmpfs"]) != ""
			securityOptConfigured := strings.TrimSpace(options["security_opt"]) != ""
			initConfigured := optionEnabled(options["init"])
			ipcIsolated := strings.EqualFold(strings.TrimSpace(options["ipc"]), "none")
			userNamespaceConfigured := containerUserNamespaceConfigured(options["userns"])
			containerUser := strings.TrimSpace(options["user"])
			nonRootUser := containerUserLooksNonRoot(containerUser)
			resourceLimitsComplete := containerResourceLimitsComplete(options)
			summary.IsolationLevel = "container_configured"
			summary.Sandboxed = true
			summary.NetworkEnforced = networkMode == "none"
			summary.FilesystemSandboxed = true
			summary.PrivilegeSandboxed = true
			summary.ResourceLimited = resourceLimitsComplete
			summary.SandboxFeatures = []string{"container_runtime", "filesystem_mount_policy", "privilege_reduction"}
			if summary.NetworkEnforced {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "network_policy")
			} else {
				summary.MissingSandboxFeatures = append(summary.MissingSandboxFeatures, "network_policy")
			}
			if summary.ResourceLimited {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "resource_limits")
			} else {
				summary.MissingSandboxFeatures = append(summary.MissingSandboxFeatures, "resource_limits")
			}
			if readOnlyRootFS {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "readonly_rootfs")
			}
			if noNewPrivileges {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "no_new_privileges")
			}
			if capDropAll {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "cap_drop_all")
			}
			if tmpfsConfigured {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "tmpfs_mounts")
			}
			if securityOptConfigured {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "custom_security_opts")
			}
			if ipcIsolated {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "ipc_namespace_none")
			}
			if userNamespaceConfigured {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "user_namespace")
			}
			if initConfigured {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "container_init")
			}
			if nonRootUser {
				summary.SandboxFeatures = append(summary.SandboxFeatures, "non_root_user")
			}
			summary.RiskLevel = "medium"
			image := strings.TrimSpace(options["image"])
			imageReferenceType := containerImageReferenceType(image)
			imageDigestPinned := imageReferenceType == "digest"
			pullPolicy := strings.ToLower(strings.TrimSpace(options["pull_policy"]))
			productionReadyImage := imageDigestPinned && pullPolicy == "never"
			summary.ContainerImage = image
			summary.ContainerImageReferenceType = imageReferenceType
			summary.ContainerImageDigestPinned = boolPtr(imageDigestPinned)
			summary.ContainerImageProductionReady = boolPtr(productionReadyImage)
			summary.ContainerPullPolicy = pullPolicy
			if summary.NetworkEnforced && summary.ResourceLimited && nonRootUser && !containerHasWritableMounts(options) && imageDigestPinned {
				summary.RiskLevel = "low"
			}
			summary.SecurityBoundary = "Container isolation is configured; host access is limited to declared mounts, network mode, and container runtime policy."
			summary.CanAccessHostOutside = false
			summary.RequiresRealSandbox = false
			if !summary.NetworkEnforced {
				summary.Warnings = append(summary.Warnings, "container network is not disabled; configure isolation_options.network=disabled for network egress isolation")
			}
			if image != "" && !imageDigestPinned {
				summary.Warnings = append(summary.Warnings, "container image is not pinned by digest; production deployments should prefer an immutable image@sha256:... reference")
				summary.Recommendations = append(summary.Recommendations, "pin the container image to an immutable digest for production environments")
			}
			if strings.EqualFold(strings.TrimSpace(server.IsolationProfile), "production") {
				if image != "" && !imageDigestPinned {
					summary.Warnings = append(summary.Warnings, "production container profile requires an immutable image@sha256:... reference")
				}
				if pullPolicy != "never" {
					summary.Warnings = append(summary.Warnings, "production container profile should use pull_policy=never with pre-pulled trusted images")
					summary.Recommendations = append(summary.Recommendations, "set isolation_options.pull_policy=never after pre-pulling trusted digest-pinned MCP images")
				}
			}
			if !summary.ResourceLimited {
				summary.Warnings = append(summary.Warnings, "container CPU, memory, and process-count resource limits are incomplete")
			}
			if workspaceMount == "rw" || workspaceMount == "readwrite" {
				summary.Warnings = append(summary.Warnings, "container can modify the mounted workspace")
			}
			if containerUser != "" && !nonRootUser {
				summary.Warnings = append(summary.Warnings, "container explicitly runs as root; configure a non-root isolation_options.user unless writable bind mounts require host-compatible permissions")
			}
			if !noNewPrivileges {
				summary.Recommendations = append(summary.Recommendations, "set isolation_options.no_new_privileges=true for stronger container privilege hardening")
			}
			if !capDropAll {
				summary.Recommendations = append(summary.Recommendations, "set isolation_options.cap_drop=all when the MCP server does not need Linux capabilities")
			}
			if !readOnlyRootFS {
				summary.Recommendations = append(summary.Recommendations, "set isolation_options.readonly_rootfs=true when the MCP server can run without writing to the image filesystem")
			} else if !tmpfsConfigured {
				summary.Recommendations = append(summary.Recommendations, "add isolation_options.tmpfs for intentional scratch paths such as /tmp when readonly_rootfs is enabled")
			}
			if !ipcIsolated {
				summary.Recommendations = append(summary.Recommendations, "set isolation_options.ipc=none for ordinary MCP tools that do not need shared memory or host IPC")
			}
			if !userNamespaceConfigured {
				summary.Recommendations = append(summary.Recommendations, "set isolation_options.userns=auto or nomap on compatible runtimes to reduce host UID/GID exposure")
			}
			if !initConfigured {
				summary.Recommendations = append(summary.Recommendations, "set isolation_options.init=true for MCP servers that may spawn child processes")
			}
			if !nonRootUser && !containerHasWritableMounts(options) {
				summary.Recommendations = append(summary.Recommendations, "set isolation_options.user to a non-root UID:GID when the MCP server does not need root")
			}
		default:
			summary.IsolationLevel = "none"
			summary.MissingSandboxFeatures = []string{"process_lifecycle_isolation", "filesystem_policy", "network_policy", "privilege_reduction"}
			summary.RiskLevel = "high"
			summary.SecurityBoundary = "No OS/container sandbox is configured; only GoFlow tool policy, command allowlists, and workspace-aware tool behavior apply."
			summary.Warnings = append(summary.Warnings, "no process, filesystem, network, or privilege sandbox is enforced for this MCP server")
		}
		if server.NetworkDisabled && !summary.NetworkEnforced {
			summary.Warnings = append(summary.Warnings, "network_disabled is advisory until a real network sandbox adapter is configured")
		}
		if !summary.AllowedCommandsSet {
			summary.Warnings = append(summary.Warnings, "startup command allowlist is missing")
		}
		if !summary.EnvAllowlistSet {
			summary.Recommendations = append(summary.Recommendations, "set env_allowlist explicitly to the smallest required environment")
		} else if len(summary.SensitiveEnv) > 0 {
			summary.Warnings = append(summary.Warnings, "env_allowlist includes sensitive variable names; MCP tools can read those secret values")
			summary.Recommendations = append(summary.Recommendations, "remove API keys, tokens, passwords, and cloud credentials from env_allowlist unless the MCP server absolutely requires them")
		}
		if !summary.Sandboxed {
			summary.Recommendations = append(summary.Recommendations, "use container isolation before running untrusted MCP servers")
		}
		out = append(out, summary)
	}
	for name, status := range health {
		name = strings.TrimSpace(name)
		if name == "" || name == "none" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		summary := mcpServerSummary{
			Name:                   name,
			Enabled:                true,
			Health:                 status,
			Isolation:              "unknown",
			IsolationLevel:         "unknown",
			RiskLevel:              "high",
			Sandboxed:              false,
			NetworkEnforced:        false,
			FilesystemSandboxed:    false,
			PrivilegeSandboxed:     false,
			ResourceLimited:        false,
			MissingSandboxFeatures: []string{"process_lifecycle_isolation", "filesystem_policy", "network_policy", "privilege_reduction"},
			RequiresRealSandbox:    true,
			CanAccessHostOutside:   true,
			SecurityBoundary:       "MCP server is visible through runtime health but has no runtime config profile; sandboxing and command/env boundaries cannot be verified.",
			Warnings:               []string{"MCP server isolation is unknown because no runtime MCP config entry was found"},
			Recommendations:        []string{"define this MCP server in config with command allowlists, env_allowlist, and explicit isolation settings"},
		}
		if metric, ok := metrics[name]; ok {
			summary.MaxConcurrentCalls = metric.MaxConcurrentCalls
			summary.ActiveCalls = metric.ActiveCalls
			summary.QueuedCalls = metric.QueuedCalls
			summary.AvailableCallSlots = metric.AvailableCallSlots
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func sanitizedEnvAllowlist(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" {
			continue
		}
		key := strings.ToUpper(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToUpper(out[i]) < strings.ToUpper(out[j]) })
	return out
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func boolPtr(value bool) *bool {
	return &value
}

func sensitiveEnvAllowlistEntries(values []string) []string {
	var sensitive []string
	for _, name := range sanitizedEnvAllowlist(values) {
		if isSensitiveEnvName(name) {
			sensitive = append(sensitive, name)
		}
	}
	return sensitive
}

func isSensitiveEnvName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return false
	}
	for _, marker := range []string{
		"API_KEY",
		"APP_KEY",
		"AUTH_TOKEN",
		"ACCESS_TOKEN",
		"REFRESH_TOKEN",
		"BEARER_TOKEN",
		"SECRET",
		"PASSWORD",
		"PASSWD",
		"PRIVATE_KEY",
		"CREDENTIAL",
		"CREDENTIALS",
		"SESSION_TOKEN",
		"CLIENT_SECRET",
	} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return strings.HasSuffix(upper, "_TOKEN") ||
		strings.HasSuffix(upper, "_KEY") ||
		strings.HasSuffix(upper, "_SECRET") ||
		strings.HasSuffix(upper, "_PASSWORD")
}

func normalizedIsolationOptions(options map[string]string) map[string]string {
	out := make(map[string]string, len(options))
	for key, value := range options {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func containerSummaryNetworkMode(value string, networkDisabled bool) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "default" {
		if networkDisabled {
			return "none"
		}
		return ""
	}
	if value == "disabled" {
		return "none"
	}
	return value
}

func containerUserNamespaceConfigured(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto", "nomap", "keep-id":
		return true
	default:
		return false
	}
}

func optionEnabled(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

func buildSetupStatus(providers []providerSummary) setupStatus {
	env := providerEnvStatus(providers)
	providerStatuses := providerSetupStatuses(providers)
	missing := make([]string, 0)
	for _, provider := range providerStatuses {
		for _, field := range provider.Missing {
			missing = append(missing, provider.ID+"."+field)
		}
	}
	return setupStatus{
		Env:                   env,
		Providers:             providerStatuses,
		ModelReady:            len(providers) > 0 && len(missing) == 0,
		MissingProviderFields: missing,
	}
}

func providerSetupStatuses(providers []providerSummary) []providerSetupStatus {
	out := make([]providerSetupStatus, 0, len(providers))
	for _, provider := range providers {
		id := strings.TrimSpace(provider.ID)
		if id == "" {
			id = "provider"
		}
		missing := make([]string, 0, 3)
		if strings.TrimSpace(provider.BaseURL) == "" {
			missing = append(missing, "base_url")
		}
		if !provider.APIKeySet {
			missing = append(missing, "api_key")
		}
		if strings.TrimSpace(provider.Model) == "" {
			missing = append(missing, "model")
		}
		out = append(out, providerSetupStatus{
			ID:       id,
			Provider: provider.Provider,
			Ready:    len(missing) == 0,
			Missing:  missing,
		})
	}
	return out
}

func providerEnvStatus(providers []providerSummary) []envStatus {
	out := make([]envStatus, 0, len(providers)*3)
	for _, provider := range providers {
		id := strings.TrimSpace(provider.ID)
		if id == "" {
			id = "provider"
		}
		out = append(out,
			envStatus{Name: id + ".base_url", Required: true, Set: strings.TrimSpace(provider.BaseURL) != "", Description: "Provider " + id + " OpenAI-compatible API base URL"},
			envStatus{Name: id + ".api_key", Required: true, Set: provider.APIKeySet, Description: "Provider " + id + " API Key"},
			envStatus{Name: id + ".model", Required: true, Set: strings.TrimSpace(provider.Model) != "", Description: "Provider " + id + " model id"},
		)
	}
	return out
}
