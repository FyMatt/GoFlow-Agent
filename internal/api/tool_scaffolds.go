package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/version"
	"gopkg.in/yaml.v3"
)

type toolScaffoldPreset struct {
	Name             string                `json:"name"`
	DefaultName      string                `json:"default_name"`
	Title            string                `json:"title"`
	Description      string                `json:"description"`
	Category         string                `json:"category"`
	Tags             []string              `json:"tags,omitempty"`
	Language         string                `json:"language"`
	Isolation        string                `json:"isolation"`
	WorkspaceMount   string                `json:"workspace_mount,omitempty"`
	NetworkMode      string                `json:"network_mode,omitempty"`
	IsolationProfile string                `json:"isolation_profile,omitempty"`
	DefaultImage     string                `json:"default_image,omitempty"`
	DefaultOptions   map[string]string     `json:"default_isolation_options,omitempty"`
	RiskLevel        string                `json:"risk_level,omitempty"`
	RequiresRestart  bool                  `json:"requires_restart"`
	Capabilities     []string              `json:"capabilities,omitempty"`
	SafetyGuards     []string              `json:"safety_guards,omitempty"`
	GeneratedPaths   []string              `json:"generated_paths,omitempty"`
	ActivationSteps  []string              `json:"activation_steps,omitempty"`
	Recommendations  []string              `json:"recommendations,omitempty"`
	Document         *toolResourceDocument `json:"document,omitempty"`
}

type toolScaffoldRequest struct {
	Name             string            `json:"name" yaml:"name"`
	Description      string            `json:"description" yaml:"description"`
	Image            string            `json:"image" yaml:"image"`
	Runtime          string            `json:"runtime" yaml:"runtime"`
	WorkspaceMount   string            `json:"workspace_mount" yaml:"workspace_mount"`
	NetworkMode      string            `json:"network" yaml:"network"`
	IsolationProfile string            `json:"isolation_profile" yaml:"isolation_profile"`
	Overwrite        bool              `json:"overwrite" yaml:"overwrite"`
	Options          map[string]string `json:"isolation_options" yaml:"isolation_options"`
}

type toolScaffoldResponse struct {
	Created bool                 `json:"created"`
	Updated bool                 `json:"updated,omitempty"`
	Preset  toolScaffoldPreset   `json:"preset"`
	Tool    toolResourceDocument `json:"tool"`
}

const defaultPythonMCPContainerImageRepository = "ghcr.io/fymatt/goflow-agent-mcp-python"

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
	presets := []toolScaffoldPreset{
		{
			Name:            "python-local",
			DefaultName:     "workspace-helper",
			Title:           "Python MCP Tool",
			Description:     "Create a local Python stdio MCP server with read/write examples and workspace-root path checks.",
			Category:        "local",
			Tags:            []string{"python", "workspace", "starter"},
			Language:        "python",
			Isolation:       "process_group",
			RiskLevel:       "medium",
			RequiresRestart: true,
			Capabilities:    []string{"stdio-json-rpc", "read_text example", "write_text example", "workspace file access"},
			SafetyGuards:    commonPythonMCPScaffoldSafetyGuards(),
			GeneratedPaths:  commonPythonMCPScaffoldGeneratedPaths(),
			ActivationSteps: commonPythonMCPScaffoldActivationSteps(),
			Recommendations: []string{"keep allowed_tools narrow on agents that can call this server", "review generated write tools before enabling them for broad agents"},
		},
		{
			Name:             "python-container-readonly",
			DefaultName:      "sandboxed-reader",
			Title:            "Containerized Read-Only Python MCP Tool",
			Description:      "Create a Python MCP server that runs in Docker/Podman with the generated tool file mounted read-only and the workspace mounted read-only.",
			Category:         "container",
			Tags:             []string{"python", "container", "readonly", "sandbox"},
			Language:         "python",
			Isolation:        "container",
			WorkspaceMount:   "ro",
			NetworkMode:      "disabled",
			IsolationProfile: "readonly",
			DefaultImage:     defaultPythonMCPContainerImage(),
			DefaultOptions:   config.ContainerIsolationProfileDefaults("readonly"),
			RiskLevel:        "low",
			RequiresRestart:  true,
			Capabilities:     []string{"stdio-json-rpc", "read_text example", "containerized execution", "read-only workspace mount"},
			SafetyGuards:     append(commonPythonMCPScaffoldSafetyGuards(), "read-only workspace mount", "single generated tool file mounted read-only", "network disabled", "resource limits", "read-only container root filesystem", "dedicated tmpfs scratch mounts", "isolated IPC namespace", "user namespace where supported", "non-root container user by default", "dropped Linux capabilities where supported", "no-new-privileges where supported", "container init for child-process cleanup"),
			GeneratedPaths:   commonPythonMCPScaffoldGeneratedPaths(),
			ActivationSteps:  commonPythonMCPScaffoldActivationSteps(),
			Recommendations:  []string{"use this preset for untrusted read-only helpers", "switch to a write-capable preset only when the workflow needs workspace mutation"},
		},
		{
			Name:             "python-container-writer",
			DefaultName:      "sandboxed-writer",
			Title:            "Containerized Write-Capable Python MCP Tool",
			Description:      "Create a Python MCP server that runs in Docker/Podman with network disabled, resource limits, and read-write workspace access.",
			Category:         "container",
			Tags:             []string{"python", "container", "write", "sandbox"},
			Language:         "python",
			Isolation:        "container",
			WorkspaceMount:   "rw",
			NetworkMode:      "disabled",
			IsolationProfile: "writer",
			DefaultImage:     defaultPythonMCPContainerImage(),
			DefaultOptions:   config.ContainerIsolationProfileDefaults("writer"),
			RiskLevel:        "medium",
			RequiresRestart:  true,
			Capabilities:     []string{"stdio-json-rpc", "read_text example", "write_text example", "containerized execution", "read-write workspace mount"},
			SafetyGuards:     append(commonPythonMCPScaffoldSafetyGuards(), "single generated tool file mounted read-only", "network disabled", "resource limits", "read-only container root filesystem", "dedicated tmpfs scratch mounts", "isolated IPC namespace", "user namespace where supported", "dropped Linux capabilities where supported", "no-new-privileges where supported", "container init for child-process cleanup"),
			GeneratedPaths:   commonPythonMCPScaffoldGeneratedPaths(),
			ActivationSteps:  commonPythonMCPScaffoldActivationSteps(),
			Recommendations:  []string{"route this tool through confirm policy", "prefer exact allowed_tools for agents using write-capable container tools"},
		},
		{
			Name:             "python-container-network",
			DefaultName:      "sandboxed-fetcher",
			Title:            "Containerized Network Python MCP Tool",
			Description:      "Create a Python MCP server that runs in Docker/Podman with workspace read-only access and explicit network egress enabled.",
			Category:         "container",
			Tags:             []string{"python", "container", "network", "fetch"},
			Language:         "python",
			Isolation:        "container",
			WorkspaceMount:   "ro",
			NetworkMode:      "bridge",
			IsolationProfile: "network",
			DefaultImage:     defaultPythonMCPContainerImage(),
			DefaultOptions:   config.ContainerIsolationProfileDefaults("network"),
			RiskLevel:        "medium",
			RequiresRestart:  true,
			Capabilities:     []string{"stdio-json-rpc", "read_text example", "containerized execution", "network egress", "read-only workspace mount"},
			SafetyGuards:     append(commonPythonMCPScaffoldSafetyGuards(), "read-only workspace mount", "single generated tool file mounted read-only", "resource limits", "read-only container root filesystem", "dedicated tmpfs scratch mounts", "isolated IPC namespace", "user namespace where supported", "non-root container user by default", "dropped Linux capabilities where supported", "no-new-privileges where supported", "container init for child-process cleanup", "network egress explicitly declared"),
			GeneratedPaths:   commonPythonMCPScaffoldGeneratedPaths(),
			ActivationSteps:  commonPythonMCPScaffoldActivationSteps(),
			Recommendations:  []string{"declare network tools as kind=network", "keep approvals enabled for network-capable agents"},
		},
		{
			Name:             "python-container-production",
			DefaultName:      "production-reader",
			Title:            "Production Python MCP Tool",
			Description:      "Create a locked-down read-only Python MCP server for production deployments that pre-pull trusted digest-pinned images.",
			Category:         "container",
			Tags:             []string{"python", "container", "production", "digest", "sandbox"},
			Language:         "python",
			Isolation:        "container",
			WorkspaceMount:   "ro",
			NetworkMode:      "disabled",
			IsolationProfile: "production",
			DefaultImage:     defaultPythonMCPContainerImage(),
			DefaultOptions:   config.ContainerIsolationProfileDefaults("production"),
			RiskLevel:        "medium",
			RequiresRestart:  true,
			Capabilities:     []string{"stdio-json-rpc", "read_text example", "containerized execution", "read-only workspace mount", "production profile defaults", "pre-pulled image mode"},
			SafetyGuards:     append(commonPythonMCPScaffoldSafetyGuards(), "read-only workspace mount", "single generated tool file mounted read-only", "network disabled", "pull_policy=never", "resource limits", "read-only container root filesystem", "dedicated tmpfs scratch mounts", "isolated IPC namespace", "user namespace where supported", "non-root container user by default", "dropped Linux capabilities where supported", "no-new-privileges where supported", "container init for child-process cleanup"),
			GeneratedPaths:   commonPythonMCPScaffoldGeneratedPaths(),
			ActivationSteps:  append(commonPythonMCPScaffoldActivationSteps(), "replace the scaffold image tag with a trusted image@sha256:... reference", "pre-pull the trusted image on each deployment host before enabling the tool"),
			Recommendations:  []string{"use this preset for production read-only helpers", "replace the default image tag with an immutable digest before deployment", "keep pull_policy=never after pre-pulling trusted images"},
		},
	}
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
	doc := defaultToolResource(name)
	doc.Description = firstWorkflowRunQueryValue(req.Description, preset.Description, doc.Description)
	switch preset.Isolation {
	case "container":
		codePath, _, err := s.toolResourcePaths(name)
		if err != nil {
			return toolResourceDocument{}, err
		}
		target := "/goflow-tools/" + name + ".py"
		image := firstWorkflowRunQueryValue(req.Image, defaultPythonMCPContainerImage())
		runtime := firstWorkflowRunQueryValue(req.Runtime, "docker")
		workspaceMount := firstWorkflowRunQueryValue(req.WorkspaceMount, preset.WorkspaceMount, "ro")
		network := firstWorkflowRunQueryValue(req.NetworkMode, preset.NetworkMode, "disabled")
		profile := firstWorkflowRunQueryValue(req.IsolationProfile, preset.IsolationProfile, containerIsolationProfileForScaffold(workspaceMount, network))
		doc.Command = "python"
		doc.Args = []string{target}
		doc.WorkDir = "."
		doc.NetworkDisabled = network == "disabled" || network == "none"
		doc.Isolation = "container"
		doc.IsolationProfile = profile
		doc.IsolationOptions = config.ContainerIsolationProfileDefaults(profile)
		if len(doc.IsolationOptions) == 0 {
			doc.IsolationOptions = defaultPythonMCPContainerIsolationOptions(workspaceMount, network, target)
		}
		doc.IsolationOptions["image"] = image
		doc.IsolationOptions["runtime"] = runtime
		doc.IsolationOptions["tool_source"] = filepath.Clean(codePath)
		doc.IsolationOptions["tool_target"] = target
		for key, value := range req.Options {
			key = strings.ToLower(strings.TrimSpace(key))
			if key == "" {
				continue
			}
			doc.IsolationOptions[key] = strings.TrimSpace(value)
		}
		doc.AllowedCommands = []string{"python"}
	default:
		doc.Isolation = "process_group"
	}
	doc.Code = renderPythonMCPToolTemplate(name)
	return doc, nil
}

func defaultPythonMCPContainerIsolationOptions(workspaceMount, network, toolTarget string) map[string]string {
	options := map[string]string{
		"runtime":           "docker",
		"workspace_mount":   workspaceMount,
		"workspace_target":  "/workspace",
		"container_workdir": "/workspace",
		"network":           network,
		"ipc":               "none",
		"userns":            "auto",
		"memory":            "256m",
		"memory_swap":       "256m",
		"cpus":              "0.5",
		"pids_limit":        "64",
		"pull_policy":       "missing",
		"readonly_rootfs":   "true",
		"no_new_privileges": "true",
		"cap_drop":          "all",
		"tmpfs":             "/tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m",
		"init":              "true",
		"tool_mount":        "ro",
	}
	if strings.TrimSpace(toolTarget) != "" {
		options["tool_target"] = toolTarget
	}
	if !isWriteCapableContainerWorkspaceMount(workspaceMount) {
		options["user"] = "65532:65532"
	}
	return options
}

func containerIsolationProfileForScaffold(workspaceMount, network string) string {
	if !strings.EqualFold(strings.TrimSpace(network), "disabled") && !strings.EqualFold(strings.TrimSpace(network), "none") {
		return "network"
	}
	if isWriteCapableContainerWorkspaceMount(workspaceMount) {
		return "writer"
	}
	return "readonly"
}

func defaultPythonMCPContainerImage() string {
	tag := strings.TrimSpace(version.Version)
	if tag == "" || strings.EqualFold(tag, "dev") {
		tag = "latest"
	}
	return defaultPythonMCPContainerImageRepository + ":" + tag
}

func isWriteCapableContainerWorkspaceMount(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "rw", "readwrite":
		return true
	default:
		return false
	}
}
