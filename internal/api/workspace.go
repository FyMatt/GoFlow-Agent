package api

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type workspaceActionRequest struct {
	Path string `json:"path"`
}

type workspaceRequirementRequest struct {
	Input     string `json:"input,omitempty"`
	Operation string `json:"operation,omitempty"`
}

type workspaceResponse struct {
	workspace.Snapshot
	Message                string                   `json:"message,omitempty"`
	Capabilities           workspaceCapabilities    `json:"capabilities"`
	Actions                []workspaceActionHint    `json:"actions,omitempty"`
	SwitchPlan             workspaceSwitchPlan      `json:"switch_plan"`
	RestartRequired        bool                     `json:"restart_required,omitempty"`
	RequiresRuntimeRestart bool                     `json:"requires_runtime_restart,omitempty"`
	SelectedPath           string                   `json:"selected_path,omitempty"`
	NormalizedSelectedPath string                   `json:"normalized_selected_path,omitempty"`
	RestartReason          string                   `json:"restart_reason,omitempty"`
	SwitchBlockers         []string                 `json:"switch_blockers,omitempty"`
	SwitchBlockerDetails   []workspaceSwitchBlocker `json:"switch_blocker_details,omitempty"`
	SuggestedArgs          []string                 `json:"suggested_args,omitempty"`
	RestartCommandHint     []string                 `json:"restart_command_hint,omitempty"`
}

type workspaceFolderPickerResponse struct {
	Available             bool                  `json:"available"`
	Cancelled             bool                  `json:"cancelled,omitempty"`
	Path                  string                `json:"path,omitempty"`
	NormalizedPath        string                `json:"normalized_path,omitempty"`
	Message               string                `json:"message,omitempty"`
	Capabilities          workspaceCapabilities `json:"capabilities"`
	Actions               []workspaceActionHint `json:"actions,omitempty"`
	SwitchPlan            workspaceSwitchPlan   `json:"switch_plan"`
	RequiresLocalRequest  bool                  `json:"requires_local_request"`
	RequiresExplicitOptIn bool                  `json:"requires_explicit_opt_in"`
	OptInEnv              string                `json:"opt_in_env,omitempty"`
	FolderPickerMode      string                `json:"folder_picker_mode,omitempty"`
	FolderPickerPlatform  string                `json:"folder_picker_platform,omitempty"`
	FolderPickerReason    string                `json:"folder_picker_reason,omitempty"`
}

type workspaceRequirementResponse struct {
	Workspace            workspace.Snapshot `json:"workspace"`
	Required             bool               `json:"required"`
	Confirmed            bool               `json:"confirmed"`
	Blocked              bool               `json:"blocked"`
	Reason               string             `json:"reason,omitempty"`
	Message              string             `json:"message,omitempty"`
	Action               string             `json:"action,omitempty"`
	ConfirmationEndpoint string             `json:"confirmation_endpoint,omitempty"`
}

type workspaceCapabilities struct {
	PureChatWithoutWorkspace         bool                     `json:"pure_chat_without_workspace"`
	WorkspaceRequiredForFileTasks    bool                     `json:"workspace_required_for_file_tasks"`
	CanConfirmCurrentWorkspace       bool                     `json:"can_confirm_current_workspace"`
	CanClearConfirmation             bool                     `json:"can_clear_confirmation"`
	CanSelectSameWorkspace           bool                     `json:"can_select_same_workspace"`
	CanSwitchWorkspaceWithoutRestart bool                     `json:"can_switch_workspace_without_restart"`
	RuntimeRebindSupported           bool                     `json:"runtime_rebind_supported"`
	SwitchRequiresRestart            bool                     `json:"switch_requires_restart"`
	RequiresRuntimeRestart           bool                     `json:"requires_runtime_restart"`
	SwitchMode                       string                   `json:"switch_mode"`
	SwitchBlockers                   []string                 `json:"switch_blockers,omitempty"`
	SwitchBlockerDetails             []workspaceSwitchBlocker `json:"switch_blocker_details,omitempty"`
	RestartCommandHint               []string                 `json:"restart_command_hint,omitempty"`
	SwitchPlan                       workspaceSwitchPlan      `json:"switch_plan"`
	CanOpenHostFolderPicker          bool                     `json:"can_open_host_folder_picker"`
	FolderPickerMode                 string                   `json:"folder_picker_mode"`
	FolderPickerReason               string                   `json:"folder_picker_reason,omitempty"`
	FolderPickerPlatform             string                   `json:"folder_picker_platform,omitempty"`
	FolderPickerOptInEnv             string                   `json:"folder_picker_opt_in_env,omitempty"`
	FolderPickerEndpoint             string                   `json:"folder_picker_endpoint,omitempty"`
	Guidance                         workspaceGuidance        `json:"guidance"`
	Reason                           string                   `json:"reason,omitempty"`
}

type workspaceGuidance struct {
	Schema              string                         `json:"schema"`
	Mode                string                         `json:"mode"`
	PreflightPath       string                         `json:"preflight_path,omitempty"`
	StatusPath          string                         `json:"status_path,omitempty"`
	SelectPath          string                         `json:"select_path,omitempty"`
	FolderPickerPath    string                         `json:"folder_picker_path,omitempty"`
	RefreshPaths        []string                       `json:"refresh_paths,omitempty"`
	ConflictFields      []string                       `json:"conflict_fields,omitempty"`
	OperatorSteps       []string                       `json:"operator_steps,omitempty"`
	Steps               []workspaceGuidanceStep        `json:"steps,omitempty"`
	PreflightOperations []workspacePreflightOperation  `json:"preflight_operations,omitempty"`
	ResultStates        []workspaceGuidanceResultState `json:"result_states,omitempty"`
}

type workspaceGuidanceStep struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Method      string   `json:"method,omitempty"`
	Path        string   `json:"path,omitempty"`
	Action      string   `json:"action,omitempty"`
	Next        string   `json:"next,omitempty"`
	Refresh     []string `json:"refresh,omitempty"`
}

type workspacePreflightOperation struct {
	Operation   string `json:"operation"`
	Requires    bool   `json:"requires_workspace"`
	Description string `json:"description,omitempty"`
}

type workspaceGuidanceResultState struct {
	Status         int      `json:"status"`
	State          string   `json:"state"`
	Message        string   `json:"message"`
	Refresh        []string `json:"refresh,omitempty"`
	Fields         []string `json:"fields,omitempty"`
	OperatorAction string   `json:"operator_action,omitempty"`
}

type workspaceSwitchPlan struct {
	Mode                       string                   `json:"mode"`
	Strategy                   string                   `json:"strategy"`
	RuntimeRebindSupported     bool                     `json:"runtime_rebind_supported"`
	RequiresRuntimeRestart     bool                     `json:"requires_runtime_restart"`
	RequiresMCPRebootstrap     bool                     `json:"requires_mcp_rebootstrap"`
	RequiresSessionScopeReset  bool                     `json:"requires_session_scope_reset"`
	RequiresApprovalScopeReset bool                     `json:"requires_approval_scope_reset"`
	RequiresToolPolicyRebuild  bool                     `json:"requires_tool_policy_rebuild"`
	Blockers                   []string                 `json:"blockers,omitempty"`
	BlockerDetails             []workspaceSwitchBlocker `json:"blocker_details,omitempty"`
	Reason                     string                   `json:"reason,omitempty"`
	RestartCommandHint         []string                 `json:"restart_command_hint,omitempty"`
	SuggestedArgs              []string                 `json:"suggested_args,omitempty"`
	TargetPath                 string                   `json:"target_path,omitempty"`
	NormalizedTargetPath       string                   `json:"normalized_target_path,omitempty"`
}

type workspaceSwitchBlocker struct {
	Code           string `json:"code"`
	Category       string `json:"category,omitempty"`
	Severity       string `json:"severity,omitempty"`
	Actionable     bool   `json:"actionable"`
	Transient      bool   `json:"transient,omitempty"`
	Message        string `json:"message,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
}

type workspaceActionHint struct {
	Name            string   `json:"name"`
	Method          string   `json:"method,omitempty"`
	Path            string   `json:"path,omitempty"`
	Available       bool     `json:"available"`
	RestartRequired bool     `json:"restart_required,omitempty"`
	ClientOnly      bool     `json:"client_only,omitempty"`
	FollowUpAction  string   `json:"follow_up_action,omitempty"`
	Description     string   `json:"description,omitempty"`
	SuggestedArgs   []string `json:"suggested_args,omitempty"`
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.workspaceResponse(""))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	action := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workspace/"), "/")
	if action == "requirement" {
		s.handleWorkspaceRequirement(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch action {
	case "pick-folder":
		s.handleWorkspacePickFolder(w, r)
	case "confirm":
		if s.workspace != nil {
			s.workspace.Confirm()
		}
		if s.runtime != nil {
			s.runtime.SetWorkspaceConfirmed(true)
		}
		writeJSON(w, s.workspaceResponse("workspace confirmed"))
	case "clear":
		if s.workspace != nil {
			s.workspace.ClearConfirmation()
		}
		if s.runtime != nil {
			s.runtime.SetWorkspaceConfirmed(false)
		}
		writeJSON(w, s.workspaceResponse("workspace confirmation cleared"))
	case "select":
		var req workspaceActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		path := workspace.NormalizePath(req.Path)
		if path == "" {
			http.Error(w, "workspace path is required", http.StatusBadRequest)
			return
		}
		if s.workspace != nil && workspace.SamePath(path, s.workspace.Root()) {
			s.workspace.Confirm()
			if s.runtime != nil {
				s.runtime.SetWorkspaceConfirmed(true)
			}
			response := s.workspaceResponse("workspace confirmed")
			response.Actions = workspaceActionHints(response.Snapshot, path, s.workspaceRebindSupported())
			response.SelectedPath = req.Path
			response.NormalizedSelectedPath = path
			writeJSON(w, response)
			return
		}
		if s.workspaceRebindSupported() {
			unlock, blockers := s.lockWorkspaceRebind()
			if len(blockers) > 0 {
				response := s.workspaceResponse("workspace rebind is currently blocked")
				response.Actions = workspaceActionHints(response.Snapshot, path, true)
				response.SwitchPlan = workspaceSwitchPlanForPath(req.Path, path, true)
				response.SelectedPath = req.Path
				response.NormalizedSelectedPath = path
				response.SwitchBlockers = blockers
				response.SwitchBlockerDetails = workspaceSwitchBlockerDetails(blockers)
				response.SwitchPlan.Blockers = blockers
				response.SwitchPlan.BlockerDetails = workspaceSwitchBlockerDetails(blockers)
				response.RestartReason = "workspace rebind requires idle agent/workflow execution and no pending workspace-scoped approvals"
				writeJSONStatus(w, http.StatusConflict, response)
				return
			}
			defer unlock()
			newRuntime, newWorkspace, err := s.workspaceRebinder(r.Context(), path)
			if err != nil {
				response := s.workspaceResponse("workspace rebind failed: " + err.Error())
				response.Actions = workspaceActionHints(response.Snapshot, path, true)
				response.SwitchPlan = workspaceSwitchPlanForPath(req.Path, path, true)
				response.SelectedPath = req.Path
				response.NormalizedSelectedPath = path
				writeJSONStatus(w, http.StatusInternalServerError, response)
				return
			}
			if newWorkspace == nil {
				newWorkspace = workspace.New(path, true)
			}
			newWorkspace.Confirm()
			if newRuntime != nil {
				newRuntime.SetWorkspaceConfirmed(true)
			}
			s.runtime = newRuntime
			s.workspace = newWorkspace
			response := s.workspaceResponse("workspace rebound")
			response.Actions = workspaceActionHints(response.Snapshot, path, true)
			response.SwitchPlan = workspaceSwitchPlanForPath(req.Path, path, true)
			response.SelectedPath = req.Path
			response.NormalizedSelectedPath = path
			writeJSON(w, response)
			return
		}
		restartReason := workspaceSwitchRestartReason()
		response := s.workspaceResponse(restartReason)
		response.Actions = workspaceActionHints(response.Snapshot, path, false)
		response.SwitchPlan = workspaceSwitchPlanForPath(req.Path, path, false)
		response.RestartRequired = true
		response.RequiresRuntimeRestart = true
		response.SelectedPath = req.Path
		response.NormalizedSelectedPath = path
		response.RestartReason = restartReason
		response.SwitchBlockers = workspaceSwitchBlockers()
		response.SwitchBlockerDetails = workspaceSwitchBlockerDetails(response.SwitchBlockers)
		response.SuggestedArgs = []string{"--workspace", path}
		response.RestartCommandHint = workspaceRestartCommandHint(path)
		writeJSONStatus(w, http.StatusConflict, response)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleWorkspacePickFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	capability := workspace.HostFolderPickerCapability()
	response := s.workspaceFolderPickerResponse("", capability)
	if !capability.Available {
		writeJSONStatus(w, http.StatusConflict, response)
		return
	}
	if !isLocalHTTPRequest(r) {
		response.Message = "host folder picker requires a local HTTP request; use a client-side picker or type a path"
		writeJSONStatus(w, http.StatusForbidden, response)
		return
	}
	var req workspaceActionRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
	}
	initialPath := req.Path
	if strings.TrimSpace(initialPath) == "" && s.workspace != nil {
		initialPath = s.workspace.Root()
	}
	path, cancelled, err := workspace.PickFolder(r.Context(), initialPath)
	if err != nil {
		response.Message = err.Error()
		writeJSONStatus(w, http.StatusInternalServerError, response)
		return
	}
	if cancelled {
		response.Cancelled = true
		response.Message = "folder picker cancelled"
		writeJSON(w, response)
		return
	}
	response.Path = path
	response.NormalizedPath = path
	response.Message = "folder selected"
	response.Actions = workspaceActionHints(s.workspaceSnapshot(), path, s.workspaceRebindSupported())
	response.SwitchPlan = workspaceSwitchPlanForPath(path, path, s.workspaceRebindSupported())
	writeJSON(w, response)
}

func (s *Server) handleWorkspaceRequirement(w http.ResponseWriter, r *http.Request) {
	req := workspaceRequirementRequest{
		Input:     r.URL.Query().Get("input"),
		Operation: r.URL.Query().Get("operation"),
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.workspaceRequirementResponse(req))
}

func (s *Server) workspaceRequirementResponse(req workspaceRequirementRequest) workspaceRequirementResponse {
	snapshot := workspace.Snapshot{Status: "confirmed", Source: "none", Display: "(none)", Confirmed: true}
	if s != nil && s.workspace != nil {
		snapshot = s.workspace.Snapshot()
	}
	requirement := workspaceRequirementForOperation(req)
	confirmed := snapshot.Confirmed
	blocked := requirement.Required && !confirmed
	response := workspaceRequirementResponse{
		Workspace:            snapshot,
		Required:             requirement.Required,
		Confirmed:            confirmed,
		Blocked:              blocked,
		Reason:               requirement.Reason,
		ConfirmationEndpoint: "/api/workspace/confirm",
	}
	if blocked {
		response.Message = workspaceRequiredMessage(requirement.Reason, snapshot)
		response.Action = "confirm_workspace"
	} else if requirement.Required {
		response.Message = "workspace already confirmed for this request"
	} else {
		response.Message = "workspace confirmation is not required for this request"
	}
	return response
}

func workspaceRequirementForOperation(req workspaceRequirementRequest) workspace.Requirement {
	operation := strings.ToLower(strings.TrimSpace(req.Operation))
	switch operation {
	case "workflow", "workflow_run", "run_workflow":
		return workspace.Requirement{Required: true, Reason: "workflow execution runs workspace-scoped stages"}
	case "file", "files", "workspace_file", "at_ref", "@file":
		return workspace.Requirement{Required: true, Reason: "@file reference reads workspace files"}
	case "tool", "tools", "write", "exec", "command":
		return workspace.Requirement{Required: true, Reason: "workspace-scoped tool or command requested"}
	default:
		return workspace.RequirementForInput(req.Input)
	}
}

func (s *Server) workspaceResponse(message string) workspaceResponse {
	snapshot := s.workspaceSnapshot()
	return workspaceResponse{
		Snapshot:     snapshot,
		Message:      message,
		Capabilities: workspaceCapabilitySummary(snapshot, s.workspaceRebindSupported()),
		Actions:      workspaceActionHints(snapshot, "", s.workspaceRebindSupported()),
		SwitchPlan:   workspaceSwitchPlanForPath("", "", s.workspaceRebindSupported()),
	}
}

func (s *Server) workspaceFolderPickerResponse(message string, capability workspace.FolderPickerCapability) workspaceFolderPickerResponse {
	snapshot := s.workspaceSnapshot()
	return workspaceFolderPickerResponse{
		Available:             capability.Available,
		Message:               strings.TrimSpace(message),
		Capabilities:          workspaceCapabilitySummary(snapshot, s.workspaceRebindSupported()),
		Actions:               workspaceActionHints(snapshot, "", s.workspaceRebindSupported()),
		SwitchPlan:            workspaceSwitchPlanForPath("", "", s.workspaceRebindSupported()),
		RequiresLocalRequest:  true,
		RequiresExplicitOptIn: true,
		OptInEnv:              capability.OptInEnv,
		FolderPickerMode:      capability.Mode,
		FolderPickerPlatform:  capability.Platform,
		FolderPickerReason:    capability.Reason,
	}
}

func (s *Server) workspaceSnapshot() workspace.Snapshot {
	snapshot := workspace.Snapshot{Status: "confirmed", Source: "none", Display: "(none)"}
	if s != nil && s.workspace != nil {
		snapshot = s.workspace.Snapshot()
	}
	return snapshot
}

func workspaceCapabilitySummary(snapshot workspace.Snapshot, rebindSupported bool) workspaceCapabilities {
	plan := workspaceSwitchPlanForPath("", "", rebindSupported)
	switchMode := "restart_required"
	reason := workspaceSwitchRestartReason()
	if rebindSupported {
		switchMode = "dynamic_rebind"
		reason = workspaceSwitchRebindReason()
	}
	picker := workspace.HostFolderPickerCapability()
	pickerEndpoint := ""
	if picker.Available {
		pickerEndpoint = "/api/workspace/pick-folder"
	}
	return workspaceCapabilities{
		PureChatWithoutWorkspace:         true,
		WorkspaceRequiredForFileTasks:    true,
		CanConfirmCurrentWorkspace:       strings.TrimSpace(snapshot.Root) != "" && !snapshot.Confirmed,
		CanClearConfirmation:             strings.TrimSpace(snapshot.Root) != "" && snapshot.Confirmed,
		CanSelectSameWorkspace:           strings.TrimSpace(snapshot.Root) != "",
		CanSwitchWorkspaceWithoutRestart: rebindSupported,
		RuntimeRebindSupported:           rebindSupported,
		SwitchRequiresRestart:            !rebindSupported,
		RequiresRuntimeRestart:           !rebindSupported,
		SwitchMode:                       switchMode,
		SwitchBlockers:                   workspaceSwitchBlockers(),
		SwitchBlockerDetails:             workspaceSwitchBlockerDetails(workspaceSwitchBlockers()),
		RestartCommandHint:               workspaceRestartCommandHintIfNeeded("<path>", rebindSupported),
		SwitchPlan:                       plan,
		CanOpenHostFolderPicker:          picker.Available,
		FolderPickerMode:                 picker.Mode,
		FolderPickerReason:               picker.Reason,
		FolderPickerPlatform:             picker.Platform,
		FolderPickerOptInEnv:             picker.OptInEnv,
		FolderPickerEndpoint:             pickerEndpoint,
		Guidance:                         workspaceGuidanceForMode(switchMode, pickerEndpoint),
		Reason:                           reason,
	}
}

func workspaceGuidanceForMode(mode, pickerEndpoint string) workspaceGuidance {
	refreshPaths := []string{
		"/api/runtime",
		"/api/session",
		"/api/resources",
		"/api/config/diagnostics",
		"/api/workspace-files",
	}
	conflictFields := []string{
		"switch_plan",
		"switch_blocker_details",
		"restart_command_hint",
		"suggested_args",
	}
	guidance := workspaceGuidance{
		Schema:              "goflow.workspace.guidance.v1",
		Mode:                mode,
		PreflightPath:       "/api/workspace/requirement",
		StatusPath:          "/api/workspace",
		SelectPath:          "/api/workspace/select",
		RefreshPaths:        refreshPaths,
		ConflictFields:      conflictFields,
		PreflightOperations: workspacePreflightOperations(),
	}
	if pickerEndpoint != "" {
		guidance.FolderPickerPath = pickerEndpoint
	}
	if mode == "dynamic_rebind" {
		guidance.OperatorSteps = []string{
			"preflight workspace-sensitive drafts before starting runs",
			"submit the chosen path to /api/workspace/select",
			"if the response is 409, render switch_plan.blocker_details and wait for active work or approvals to clear",
			"after a successful switch, refresh runtime, session, resource, diagnostics, and workspace-file suggestions",
		}
		guidance.Steps = workspaceGuidanceSteps(mode, pickerEndpoint, refreshPaths)
		guidance.ResultStates = []workspaceGuidanceResultState{
			{Status: http.StatusOK, State: "rebound", Message: "Workspace switched in-process. Refresh runtime-bound views before starting new work.", Refresh: refreshPaths, OperatorAction: "refresh_runtime_state"},
			{Status: http.StatusConflict, State: "blocked", Message: "Workspace switching is blocked by active execution or pending approvals.", Fields: conflictFields, OperatorAction: "render_blockers_and_retry_when_idle"},
		}
		return guidance
	}
	guidance.OperatorSteps = []string{
		"preflight workspace-sensitive drafts before starting runs",
		"submit the chosen path to /api/workspace/select",
		"when the response is 409 restart_required, show restart_command_hint or suggested_args",
		"after restart, refresh runtime, session, resource, diagnostics, and workspace-file suggestions",
	}
	guidance.Steps = workspaceGuidanceSteps(mode, pickerEndpoint, refreshPaths)
	guidance.ResultStates = []workspaceGuidanceResultState{
		{Status: http.StatusOK, State: "confirmed", Message: "The selected path matches the current workspace and was confirmed.", Refresh: refreshPaths, OperatorAction: "refresh_workspace_state"},
		{Status: http.StatusConflict, State: "restart_required", Message: "Workspace switching requires restarting GoFlow with the selected path.", Fields: conflictFields, OperatorAction: "show_restart_command"},
	}
	return guidance
}

func workspaceGuidanceSteps(mode, pickerEndpoint string, refreshPaths []string) []workspaceGuidanceStep {
	choosePath := "/api/workspace/select"
	if pickerEndpoint != "" {
		choosePath = pickerEndpoint
	}
	steps := []workspaceGuidanceStep{
		{
			ID:          "preflight",
			Label:       "Preflight request",
			Description: "Check whether the draft operation needs confirmed workspace access before starting an Agent or workflow run.",
			Method:      http.MethodPost,
			Path:        "/api/workspace/requirement",
			Next:        "choose_workspace",
		},
		{
			ID:          "choose_workspace",
			Label:       "Choose workspace",
			Description: "Use the local opt-in folder picker when available, otherwise let the browser choose a folder or let the user type a path.",
			Method:      http.MethodPost,
			Path:        choosePath,
			Action:      "choose_folder_or_type_path",
			Next:        "select_workspace",
		},
		{
			ID:          "select_workspace",
			Label:       "Select workspace",
			Description: "Submit the normalized path to the backend. Same-path selection confirms the workspace; different-path selection follows the reported switch mode.",
			Method:      http.MethodPost,
			Path:        "/api/workspace/select",
			Action:      mode,
			Next:        "handle_result",
		},
		{
			ID:          "handle_result",
			Label:       "Handle result",
			Description: "On success refresh runtime-bound data. On conflict render backend blocker details or restart guidance.",
			Action:      "refresh_or_render_conflict",
			Refresh:     append([]string(nil), refreshPaths...),
		},
	}
	return steps
}

func workspacePreflightOperations() []workspacePreflightOperation {
	return []workspacePreflightOperation{
		{Operation: "chat", Requires: false, Description: "Plain conversation can run without confirming a workspace."},
		{Operation: "file", Requires: true, Description: "@file references and workspace file browsing read from the selected workspace."},
		{Operation: "tool", Requires: true, Description: "Workspace-scoped tools and command execution require a confirmed workspace."},
		{Operation: "workflow", Requires: true, Description: "Workflow execution runs stages over workspace-scoped resources."},
	}
}

func workspaceSwitchRestartReason() string {
	return "workspace-scoped MCP servers, session approvals, and tool policy are bound at runtime startup; switch workspaces by restarting with --workspace <path>"
}

func workspaceSwitchRebindReason() string {
	return "workspace-scoped MCP servers, session approvals, and tool policy can be rebuilt in-process when no agent or workflow run is active"
}

func workspaceSwitchBlockers() []string {
	return []string{
		"mcp_servers_bound_to_workspace_root",
		"session_state_and_approvals_are_workspace_scoped",
		"tool_policy_is_evaluated_against_startup_workspace",
	}
}

func workspaceSwitchBlockerDetails(codes []string) []workspaceSwitchBlocker {
	if len(codes) == 0 {
		return nil
	}
	out := make([]workspaceSwitchBlocker, 0, len(codes))
	seen := map[string]struct{}{}
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, workspaceSwitchBlockerDetail(code))
	}
	return out
}

func workspaceSwitchBlockerDetail(code string) workspaceSwitchBlocker {
	detail := workspaceSwitchBlocker{
		Code:           strings.TrimSpace(code),
		Category:       "workspace_switch",
		Severity:       "warning",
		Actionable:     true,
		Message:        "Workspace switching is blocked.",
		Recommendation: "Finish or cancel active work, then try selecting the workspace again.",
	}
	switch detail.Code {
	case "mcp_servers_bound_to_workspace_root":
		detail.Category = "runtime_binding"
		detail.Transient = false
		detail.Actionable = false
		detail.Message = "MCP server processes are bound to the current workspace root."
		detail.Recommendation = "Restart GoFlow with --workspace <path>, or use an HTTP runtime with dynamic rebind support."
	case "session_state_and_approvals_are_workspace_scoped":
		detail.Category = "session_scope"
		detail.Transient = false
		detail.Actionable = false
		detail.Message = "Session state and remembered approvals are scoped to the startup workspace."
		detail.Recommendation = "Restart with the target workspace so the session and approval scope are rebuilt safely."
	case "tool_policy_is_evaluated_against_startup_workspace":
		detail.Category = "tool_policy"
		detail.Transient = false
		detail.Actionable = false
		detail.Message = "Workspace-scoped tool policy was built for the startup workspace."
		detail.Recommendation = "Restart or dynamically rebind while idle so tool policy is rebuilt for the target workspace."
	case "active_agent_or_workflow_runs_must_finish":
		detail.Category = "active_execution"
		detail.Transient = true
		detail.Message = "An Agent or workflow run is active."
		detail.Recommendation = "Wait for active execution to finish, or cancel it explicitly before switching workspaces."
	case "pending_workspace_approvals_must_be_resolved":
		detail.Category = "pending_approval"
		detail.Transient = true
		detail.Message = "A workspace-scoped approval may be pending."
		detail.Recommendation = "Approve, deny, or cancel pending workspace-scoped actions before switching workspaces."
	case "active_agent_run":
		detail.Category = "active_execution"
		detail.Transient = true
		detail.Message = "An ordinary Agent run is currently executing."
		detail.Recommendation = "Wait for the Agent run to finish, or cancel it before switching workspaces."
	case "active_workflow_run":
		detail.Category = "active_execution"
		detail.Transient = true
		detail.Message = "A workflow run is currently executing."
		detail.Recommendation = "Wait for the workflow run to finish, or cancel it before switching workspaces."
	case "pending_approvals":
		detail.Category = "pending_approval"
		detail.Transient = true
		detail.Message = "There are pending tool approvals."
		detail.Recommendation = "Approve or deny pending tool calls before switching workspaces."
	case "pending_agent_handoff":
		detail.Category = "pending_handoff"
		detail.Transient = true
		detail.Message = "An Agent handoff is pending."
		detail.Recommendation = "Confirm, deny, or clear the pending handoff before switching workspaces."
	case "active_or_paused_workflow", "active_or_paused_workflow_run":
		detail.Category = "workflow_state"
		detail.Transient = true
		detail.Message = "A workflow is active or paused."
		detail.Recommendation = "Resume, cancel, or wait for the workflow before switching workspaces."
	case "active_or_paused_agent_run":
		detail.Category = "agent_run_state"
		detail.Transient = true
		detail.Message = "An ordinary Agent run is active or paused."
		detail.Recommendation = "Resume, cancel, or wait for the Agent run before switching workspaces."
	}
	return detail
}

func workspaceRestartCommandHint(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "<path>"
	}
	return []string{"goflow", "--workspace", path}
}

func workspaceRestartCommandHintIfNeeded(path string, rebindSupported bool) []string {
	if rebindSupported {
		return nil
	}
	return workspaceRestartCommandHint(path)
}

func workspaceSwitchPlanForPath(selectedPath, normalizedPath string, rebindSupported bool) workspaceSwitchPlan {
	selectedPath = strings.TrimSpace(selectedPath)
	normalizedPath = strings.TrimSpace(normalizedPath)
	if normalizedPath == "" {
		normalizedPath = "<path>"
	}
	if rebindSupported {
		return workspaceSwitchPlan{
			Mode:                       "dynamic_rebind",
			Strategy:                   "runtime_rebind",
			RuntimeRebindSupported:     true,
			RequiresRuntimeRestart:     false,
			RequiresMCPRebootstrap:     true,
			RequiresSessionScopeReset:  true,
			RequiresApprovalScopeReset: true,
			RequiresToolPolicyRebuild:  true,
			Blockers:                   []string{"active_agent_or_workflow_runs_must_finish", "pending_workspace_approvals_must_be_resolved"},
			BlockerDetails:             workspaceSwitchBlockerDetails([]string{"active_agent_or_workflow_runs_must_finish", "pending_workspace_approvals_must_be_resolved"}),
			Reason:                     workspaceSwitchRebindReason(),
			SuggestedArgs:              []string{"--workspace", normalizedPath},
			TargetPath:                 selectedPath,
			NormalizedTargetPath:       normalizedPath,
		}
	}
	return workspaceSwitchPlan{
		Mode:                       "restart_required",
		Strategy:                   "restart",
		RuntimeRebindSupported:     false,
		RequiresRuntimeRestart:     true,
		RequiresMCPRebootstrap:     true,
		RequiresSessionScopeReset:  true,
		RequiresApprovalScopeReset: true,
		RequiresToolPolicyRebuild:  true,
		Blockers:                   workspaceSwitchBlockers(),
		BlockerDetails:             workspaceSwitchBlockerDetails(workspaceSwitchBlockers()),
		Reason:                     workspaceSwitchRestartReason(),
		RestartCommandHint:         workspaceRestartCommandHint(normalizedPath),
		SuggestedArgs:              []string{"--workspace", normalizedPath},
		TargetPath:                 selectedPath,
		NormalizedTargetPath:       normalizedPath,
	}
}

func workspaceActionHints(snapshot workspace.Snapshot, selectedPath string, rebindSupported bool) []workspaceActionHint {
	selectedPath = workspace.NormalizePath(selectedPath)
	picker := workspace.HostFolderPickerCapability()
	switchDescription := "Switching to a different workspace requires restarting GoFlow so MCP servers rebind safely."
	if rebindSupported {
		switchDescription = "Switch to a different workspace by rebuilding runtime state, MCP servers, session scope, and tool policy in-process."
	}
	actions := []workspaceActionHint{
		{
			Name:           "choose_folder",
			Method:         http.MethodPost,
			Path:           "/api/workspace/pick-folder",
			Available:      picker.Available,
			ClientOnly:     !picker.Available,
			FollowUpAction: "select",
			Description:    workspaceChooseFolderDescription(picker),
		},
		{
			Name:        "confirm",
			Method:      http.MethodPost,
			Path:        "/api/workspace/confirm",
			Available:   strings.TrimSpace(snapshot.Root) != "" && !snapshot.Confirmed,
			Description: "Confirm the current workspace for file, tool, and workflow operations.",
		},
		{
			Name:        "clear",
			Method:      http.MethodPost,
			Path:        "/api/workspace/clear",
			Available:   strings.TrimSpace(snapshot.Root) != "" && snapshot.Confirmed,
			Description: "Clear workspace confirmation. Pure chat can continue; workspace-scoped operations will ask again.",
		},
		{
			Name:        "select_same",
			Method:      http.MethodPost,
			Path:        "/api/workspace/select",
			Available:   selectedPath != "" && workspace.SamePath(selectedPath, snapshot.Root),
			Description: "Selecting the current workspace confirms it without restart.",
		},
		{
			Name:            "switch",
			Method:          http.MethodPost,
			Path:            "/api/workspace/select",
			Available:       selectedPath != "" && !workspace.SamePath(selectedPath, snapshot.Root),
			RestartRequired: !rebindSupported,
			Description:     switchDescription,
		},
	}
	if selectedPath != "" && !workspace.SamePath(selectedPath, snapshot.Root) && !rebindSupported {
		actions[len(actions)-1].SuggestedArgs = []string{"--workspace", selectedPath}
	}
	return actions
}

func workspaceChooseFolderDescription(capability workspace.FolderPickerCapability) string {
	if capability.Available {
		return "Open the local host folder picker, then submit the selected path to /api/workspace/select."
	}
	return "Use a browser/client folder picker when available, or type a workspace path and submit it to /api/workspace/select."
}

func isLocalHTTPRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return strings.EqualFold(host, "localhost")
	}
	return ip.IsLoopback()
}

func (s *Server) workspaceRebindSupported() bool {
	return s != nil && s.workspaceRebinder != nil
}

func (s *Server) lockWorkspaceRebind() (func(), []string) {
	if s == nil {
		return func() {}, nil
	}
	if !s.agentExecMu.TryLock() {
		return func() {}, []string{"active_agent_run"}
	}
	agentLocked := true
	if !s.workflowExecMu.TryLock() {
		s.agentExecMu.Unlock()
		agentLocked = false
		return func() {}, []string{"active_workflow_run"}
	}
	unlock := func() {
		s.workflowExecMu.Unlock()
		if agentLocked {
			s.agentExecMu.Unlock()
		}
	}
	snapshot := s.sessionSnapshotWithApprovalRisk()
	blockers := workspaceRebindSessionBlockers(snapshot)
	if len(blockers) > 0 {
		unlock()
		return func() {}, blockers
	}
	return unlock, nil
}

func workspaceRebindSessionBlockers(snapshot session.Snapshot) []string {
	var blockers []string
	if len(snapshot.PendingApprovals) > 0 {
		blockers = append(blockers, "pending_approvals")
	}
	if strings.TrimSpace(snapshot.PendingHandoff.TargetAgent) != "" || strings.TrimSpace(snapshot.PendingHandoff.ExpectedAction) != "" {
		blockers = append(blockers, "pending_agent_handoff")
	}
	switch strings.ToLower(strings.TrimSpace(snapshot.Workflow.Status)) {
	case "running", "awaiting_approval", "awaiting_tool_approval", "awaiting_input", "awaiting_sub_workflow":
		blockers = append(blockers, "active_or_paused_workflow")
	}
	for _, run := range snapshot.AgentRuns {
		switch strings.ToLower(strings.TrimSpace(run.Status)) {
		case "running", "awaiting_approval", "awaiting_tool_approval":
			blockers = append(blockers, "active_or_paused_agent_run")
		}
	}
	for _, run := range snapshot.WorkflowRuns {
		switch strings.ToLower(strings.TrimSpace(run.Status)) {
		case "running", "awaiting_approval", "awaiting_tool_approval", "awaiting_input", "awaiting_sub_workflow":
			blockers = append(blockers, "active_or_paused_workflow_run")
		}
	}
	return blockers
}

func (s *Server) ensureWorkspaceConfirmedForRun(w http.ResponseWriter, input string) bool {
	if s == nil || s.workspace == nil || s.workspace.Confirmed() {
		return true
	}
	req := workspace.RequirementForInput(input)
	if !req.Required {
		return true
	}
	writeWorkspaceRequired(w, req.Reason, s.workspace.Snapshot())
	return false
}

func (s *Server) ensureWorkspaceConfirmedForJSON(w http.ResponseWriter, reason string) bool {
	if s == nil || s.workspace == nil || s.workspace.Confirmed() {
		return true
	}
	writeWorkspaceRequired(w, reason, s.workspace.Snapshot())
	return false
}

func (s *Server) ensureWorkspaceConfirmedForStream(w http.ResponseWriter, input string) bool {
	if s == nil || s.workspace == nil || s.workspace.Confirmed() {
		return true
	}
	req := workspace.RequirementForInput(input)
	if !req.Required {
		return true
	}
	writer, ok := newSSEWriter(w)
	if !ok {
		return false
	}
	_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: workspaceRequiredMessage(req.Reason, s.workspace.Snapshot()), IsError: true, NeedsAction: true})
	return false
}

func (s *Server) ensureWorkspaceConfirmedForWorkflowStream(w http.ResponseWriter) bool {
	if s == nil || s.workspace == nil || s.workspace.Confirmed() {
		return true
	}
	writer, ok := newSSEWriter(w)
	if !ok {
		return false
	}
	_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: workspaceRequiredMessage("workflow execution runs workspace-scoped stages", s.workspace.Snapshot()), IsError: true, NeedsAction: true})
	return false
}

func writeWorkspaceRequired(w http.ResponseWriter, reason string, snapshot workspace.Snapshot) {
	writeJSONStatus(w, http.StatusConflict, map[string]any{
		"error":     "workspace_required",
		"message":   workspaceRequiredMessage(reason, snapshot),
		"reason":    strings.TrimSpace(reason),
		"workspace": snapshot,
		"action":    "confirm the current workspace or restart with --workspace <path>",
	})
}

func workspaceRequiredMessage(reason string, snapshot workspace.Snapshot) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "workspace-scoped action requested"
	}
	root := strings.TrimSpace(snapshot.Root)
	if root == "" {
		root = "(none)"
	}
	return "workspace confirmation required: " + reason + "; current workspace: " + root
}

func (s *Server) handleWorkspacePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = workspacePageTemplate.Execute(w, s.workspaceResponse(""))
}

var workspacePageTemplate = template.Must(template.New("workspace").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>GoFlow Workspace</title>
  <style>
    body { margin: 0; font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif; background: #f5f7fb; color: #18202f; }
    main { max-width: 860px; margin: 0 auto; padding: 32px 20px; }
    h1 { font-size: 24px; margin: 0 0 18px; }
    .panel { background: white; border: 1px solid #d8deea; border-radius: 8px; padding: 20px; box-shadow: 0 8px 24px rgba(27,39,67,.08); }
    dl { display: grid; grid-template-columns: 150px 1fr; gap: 12px 18px; margin: 0 0 20px; }
    dt { color: #647086; font-weight: 600; }
    dd { margin: 0; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; overflow-wrap: anywhere; }
    button { border: 1px solid #2f6fed; background: #2f6fed; color: white; border-radius: 6px; padding: 9px 14px; font-weight: 700; cursor: pointer; }
    button.secondary { background: white; color: #2f405f; border-color: #b7c0d1; }
    input { border: 1px solid #b7c0d1; border-radius: 6px; padding: 9px 10px; min-width: 320px; }
    .actions { display: flex; flex-wrap: wrap; gap: 10px; }
    .note { color: #647086; margin-top: 16px; line-height: 1.45; }
    #message { margin-top: 14px; font-weight: 700; color: #2f6fed; }
  </style>
</head>
<body>
<main>
  <h1>GoFlow Workspace</h1>
  <section class="panel">
    <dl>
      <dt>Root</dt><dd id="root">{{.Root}}</dd>
      <dt>Status</dt><dd id="status">{{.Status}}</dd>
      <dt>Source</dt><dd id="source">{{.Source}}</dd>
    </dl>
    <div class="actions">
      <button id="confirm">Confirm Workspace</button>
      <button class="secondary" id="clear">Clear Confirmation</button>
      <input id="path" placeholder="Path for restart suggestion">
      <button class="secondary" id="select">Use Path</button>
    </div>
    <p class="note">Changing to a different workspace requires restarting GoFlow so MCP servers are rebound under the new root.</p>
    <div id="message"></div>
  </section>
</main>
<script>
async function api(path, body) {
  const options = body ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : { method: "POST" };
  const res = await fetch(path, options);
  const data = await res.json();
  render(data);
  if (!res.ok && data.message) throw new Error(data.message);
  return data;
}
function render(data) {
  if (!data) return;
  document.getElementById("root").textContent = data.root || "(none)";
  document.getElementById("status").textContent = data.status || "";
  document.getElementById("source").textContent = data.source || "";
  document.getElementById("message").textContent = data.message || (data.restart_required ? "Restart required: goflow --workspace " + (data.suggested_args || []).slice(-1)[0] : "");
}
document.getElementById("confirm").onclick = () => api("/api/workspace/confirm").catch(err => document.getElementById("message").textContent = err.message);
document.getElementById("clear").onclick = () => api("/api/workspace/clear").catch(err => document.getElementById("message").textContent = err.message);
document.getElementById("select").onclick = () => api("/api/workspace/select", { path: document.getElementById("path").value }).catch(err => document.getElementById("message").textContent = err.message);
</script>
</body>
</html>`))
