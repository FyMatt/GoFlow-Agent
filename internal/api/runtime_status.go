package api

import (
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/version"
	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
)

type runtimeStatusResponse struct {
	Version     string             `json:"version"`
	ActiveAgent string             `json:"active_agent"`
	Mode        string             `json:"mode"`
	Trace       bool               `json:"trace"`
	RuntimeHome string             `json:"runtime_home,omitempty"`
	Workspace   workspace.Snapshot `json:"workspace"`
	Session     session.Snapshot   `json:"session"`
	StatusLines []string           `json:"status_lines"`
	Agents      []agentSummary     `json:"agents"`
	Skills      []skillSummary     `json:"skills"`
	Tools       []string           `json:"tools"`
	MCPHealth   map[string]string  `json:"mcp_health"`
	Setup       setupStatus        `json:"setup"`
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

type skillSummary struct {
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	PreferredAgent   string   `json:"preferred_agent,omitempty"`
	AllowedToolKinds []string `json:"allowed_tool_kinds,omitempty"`
	NextSkills       []string `json:"next_skills,omitempty"`
}

type setupStatus struct {
	Env []envStatus `json:"env"`
}

type envStatus struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Set         bool   `json:"set"`
	Description string `json:"description"`
}

type updatePolicyResponse struct {
	CurrentVersion string       `json:"current_version"`
	Repository     string       `json:"repository"`
	ReleaseFeed    string       `json:"release_feed"`
	Strategies     []updateMode `json:"strategies"`
	Notes          []string     `json:"notes"`
}

type updateMode struct {
	InstallType string   `json:"install_type"`
	PromptFlow  []string `json:"prompt_flow"`
	AutoUpdate  []string `json:"auto_update"`
}

func (s *Server) handleRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.runtimeStatus(r))
}

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, updatePolicyResponse{
		CurrentVersion: version.Version,
		Repository:     "https://github.com/FyMatt/GoFlow-Agent",
		ReleaseFeed:    "https://api.github.com/repos/FyMatt/GoFlow-Agent/releases/latest",
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

func (s *Server) runtimeStatus(r *http.Request) runtimeStatusResponse {
	snapshot := session.Snapshot{}
	workspaceSnapshot := workspace.Snapshot{Status: "confirmed", Source: "none", Display: "(none)"}
	var (
		active      string
		mode        string
		trace       bool
		runtimeHome string
		statusLines []string
		agents      []agentSummary
		skills      []skillSummary
		tools       []string
		mcpHealth   map[string]string
	)
	if s != nil && s.workspace != nil {
		workspaceSnapshot = s.workspace.Snapshot()
	}
	if s != nil && s.runtime != nil {
		ctx := r.Context()
		active = s.runtime.ActiveAgent()
		mode = s.runtime.Mode()
		trace = s.runtime.TraceEnabled()
		runtimeHome = s.runtime.RuntimeHome()
		snapshot = s.runtime.SessionSnapshot()
		statusLines = s.runtime.StatusLines(ctx)
		agents = s.agentSummaries()
		skills = s.skillSummaries()
		tools = s.runtime.ToolNames()
		sort.Strings(tools)
		mcpHealth = s.runtime.MCPHealthStatus(ctx)
	}
	return runtimeStatusResponse{
		Version:     version.Version,
		ActiveAgent: active,
		Mode:        mode,
		Trace:       trace,
		RuntimeHome: runtimeHome,
		Workspace:   workspaceSnapshot,
		Session:     snapshot,
		StatusLines: statusLines,
		Agents:      agents,
		Skills:      skills,
		Tools:       tools,
		MCPHealth:   mcpHealth,
		Setup:       setupStatus{Env: environmentStatus()},
	}
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

func environmentStatus() []envStatus {
	return []envStatus{
		{Name: "GOFLOW_BASE_URL", Required: true, Set: strings.TrimSpace(os.Getenv("GOFLOW_BASE_URL")) != "", Description: "OpenAI-compatible model API base URL"},
		{Name: "GOFLOW_API_KEY", Required: true, Set: strings.TrimSpace(os.Getenv("GOFLOW_API_KEY")) != "", Description: "primary model API key"},
		{Name: "GOFLOW_MODEL", Required: false, Set: strings.TrimSpace(os.Getenv("GOFLOW_MODEL")) != "", Description: "primary model override"},
		{Name: "GOFLOW_BACKUP_BASE_URL", Required: false, Set: strings.TrimSpace(os.Getenv("GOFLOW_BACKUP_BASE_URL")) != "", Description: "fallback model API base URL"},
		{Name: "GOFLOW_BACKUP_API_KEY", Required: false, Set: strings.TrimSpace(os.Getenv("GOFLOW_BACKUP_API_KEY")) != "", Description: "fallback model API key"},
		{Name: "GOFLOW_BACKUP_MODEL", Required: false, Set: strings.TrimSpace(os.Getenv("GOFLOW_BACKUP_MODEL")) != "", Description: "fallback model override"},
	}
}
