package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const maxSubagentImportBytes = 1 << 20

type subagentImportResult struct {
	Format   string                `json:"format"`
	Saved    agentResourceDocument `json:"saved"`
	Warnings []string              `json:"warnings,omitempty"`
}

type codexSubagentDocument struct {
	Name                  string         `toml:"name"`
	Description           string         `toml:"description"`
	DeveloperInstructions string         `toml:"developer_instructions"`
	Model                 string         `toml:"model,omitempty"`
	ModelReasoningEffort  string         `toml:"model_reasoning_effort,omitempty"`
	SandboxMode           string         `toml:"sandbox_mode,omitempty"`
	MCPServers            map[string]any `toml:"mcp_servers,omitempty"`
	Skills                any            `toml:"skills,omitempty"`
}

type flexibleSubagentStringList []string

func (l *flexibleSubagentStringList) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	switch value.Kind {
	case yaml.ScalarNode:
		for _, part := range strings.Split(value.Value, ",") {
			if item := strings.TrimSpace(part); item != "" {
				*l = append(*l, item)
			}
		}
		return nil
	case yaml.SequenceNode:
		var items []string
		if err := value.Decode(&items); err != nil {
			return err
		}
		*l = append(*l, items...)
		return nil
	default:
		return fmt.Errorf("expected a string or list")
	}
}

type claudeSubagentFrontmatter struct {
	Name            string                     `yaml:"name"`
	Description     string                     `yaml:"description"`
	Tools           flexibleSubagentStringList `yaml:"tools"`
	DisallowedTools flexibleSubagentStringList `yaml:"disallowedTools"`
	Model           string                     `yaml:"model"`
	PermissionMode  string                     `yaml:"permissionMode"`
	MaxTurns        int                        `yaml:"maxTurns"`
	Skills          flexibleSubagentStringList `yaml:"skills"`
	MCPServers      []any                      `yaml:"mcpServers"`
	Background      bool                       `yaml:"background"`
	Isolation       string                     `yaml:"isolation"`
}

func (s *Server) handleSubagentImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format != "codex" && format != "claude" {
		http.Error(w, "subagent import format must be codex or claude", http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSubagentImportBytes+1))
	if err != nil {
		http.Error(w, "read subagent import: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(data) > maxSubagentImportBytes {
		http.Error(w, "subagent import exceeds 1 MiB", http.StatusRequestEntityTooLarge)
		return
	}

	var doc agentResourceDocument
	var warnings []string
	switch format {
	case "codex":
		doc, warnings, err = parseCodexSubagent(data)
	case "claude":
		doc, warnings, err = parseClaudeSubagent(data)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if override := normalizeResourceName(r.URL.Query().Get("name")); override != "" {
		doc.ID = override
	}
	saved, err := s.saveAgentResource(doc.ID, doc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, subagentImportResult{Format: format, Saved: saved, Warnings: warnings})
}

func (s *Server) handleSubagentExport(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	doc, ok := s.agentResourceByName(name)
	if !ok || doc.Kind != config.AgentKindSubagent {
		http.NotFound(w, r)
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "claude"
	}
	var data []byte
	var err error
	var extension string
	switch format {
	case "codex":
		data, err = renderCodexSubagent(doc)
		extension = "toml"
	case "claude":
		data, err = renderClaudeSubagent(doc)
		extension = "md"
	default:
		http.Error(w, "subagent export format must be codex or claude", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	contentType := "text/markdown; charset=utf-8"
	if format == "codex" {
		contentType = "application/toml; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, doc.ID, extension))
	_, _ = w.Write(data)
}

func parseCodexSubagent(data []byte) (agentResourceDocument, []string, error) {
	var source codexSubagentDocument
	decoder := toml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&source); err != nil {
		return agentResourceDocument{}, nil, fmt.Errorf("parse Codex subagent TOML: %w", err)
	}
	if strings.TrimSpace(source.Name) == "" || strings.TrimSpace(source.Description) == "" || strings.TrimSpace(source.DeveloperInstructions) == "" {
		return agentResourceDocument{}, nil, fmt.Errorf("Codex subagent requires name, description, and developer_instructions")
	}
	doc := baseImportedSubagent(source.Name, source.Description, source.DeveloperInstructions)
	doc.Model = strings.TrimSpace(source.Model)
	warnings := make([]string, 0, 3)
	switch strings.ToLower(strings.TrimSpace(source.SandboxMode)) {
	case "", "read-only":
		doc.AllowedToolKinds = []string{string(config.ToolKindRead)}
		doc.Mode = "audit"
	case "workspace-write":
		doc.AllowedToolKinds = []string{string(config.ToolKindRead), string(config.ToolKindWrite), string(config.ToolKindExec)}
		doc.Mode = "fix"
	default:
		doc.AllowedToolKinds = []string{string(config.ToolKindRead)}
		warnings = append(warnings, "Codex sandbox_mode was not mapped; imported with read-only tools")
	}
	if strings.TrimSpace(source.ModelReasoningEffort) != "" {
		warnings = append(warnings, "Codex model_reasoning_effort is preserved only by Codex and is not active in GoFlow")
	}
	if len(source.MCPServers) > 0 || source.Skills != nil {
		warnings = append(warnings, "Codex agent-scoped MCP or skills configuration requires manual selection in GoFlow")
	}
	return doc, warnings, nil
}

func parseClaudeSubagent(data []byte) (agentResourceDocument, []string, error) {
	frontmatter, body, err := splitMarkdownFrontmatter(data)
	if err != nil {
		return agentResourceDocument{}, nil, fmt.Errorf("parse Claude subagent: %w", err)
	}
	var source claudeSubagentFrontmatter
	if err := yaml.Unmarshal(frontmatter, &source); err != nil {
		return agentResourceDocument{}, nil, fmt.Errorf("parse Claude subagent frontmatter: %w", err)
	}
	if strings.TrimSpace(source.Name) == "" || strings.TrimSpace(source.Description) == "" {
		return agentResourceDocument{}, nil, fmt.Errorf("Claude subagent requires name and description")
	}
	if strings.TrimSpace(body) == "" {
		return agentResourceDocument{}, nil, fmt.Errorf("Claude subagent requires a Markdown instruction body")
	}
	doc := baseImportedSubagent(source.Name, source.Description, body)
	doc.Model = strings.TrimSpace(source.Model)
	if source.MaxTurns > 0 {
		doc.MaxIterations = source.MaxTurns
	}
	doc.AllowedToolKinds = claudeToolsToGoFlowKinds(source.Tools)
	warnings := make([]string, 0, 4)
	if len(doc.AllowedToolKinds) == 0 {
		doc.AllowedToolKinds = []string{string(config.ToolKindRead)}
	}
	if len(source.DisallowedTools) > 0 {
		warnings = append(warnings, "Claude disallowedTools cannot be represented exactly and requires review")
	}
	if len(source.Skills) > 0 || len(source.MCPServers) > 0 {
		warnings = append(warnings, "Claude agent-scoped skills or MCP servers require manual selection in GoFlow")
	}
	if source.Background {
		warnings = append(warnings, "Claude background execution is chosen per GoFlow task and was not stored on the resource")
	}
	if strings.TrimSpace(source.Isolation) != "" {
		warnings = append(warnings, "Claude isolation is not mapped; GoFlow keeps its current workspace permission boundary")
	}
	if mode := strings.ToLower(strings.TrimSpace(source.PermissionMode)); mode == "plan" {
		doc.Mode = "plan"
		doc.AllowedToolKinds = []string{string(config.ToolKindRead)}
	} else if mode == "bypasspermissions" {
		warnings = append(warnings, "Claude bypassPermissions was reduced to GoFlow confirmation policy")
	}
	return doc, warnings, nil
}

func baseImportedSubagent(name, description, instructions string) agentResourceDocument {
	return agentResourceDocument{
		ID:               normalizeResourceName(name),
		Kind:             config.AgentKindSubagent,
		Name:             strings.TrimSpace(name),
		Description:      strings.TrimSpace(description),
		SystemPrompt:     strings.TrimSpace(instructions),
		Provider:         "primary",
		MaxIterations:    6,
		AllowedToolKinds: []string{string(config.ToolKindRead)},
		ToolPolicy:       string(config.ToolPolicyConfirm),
		Mode:             "audit",
	}
}

func splitMarkdownFrontmatter(data []byte) ([]byte, string, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", fmt.Errorf("missing YAML frontmatter")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return []byte(strings.Join(lines[1:i], "\n")), strings.TrimSpace(strings.Join(lines[i+1:], "\n")), nil
		}
	}
	return nil, "", fmt.Errorf("missing closing YAML frontmatter delimiter")
}

func claudeToolsToGoFlowKinds(tools []string) []string {
	set := make(map[string]struct{})
	for _, tool := range tools {
		lower := strings.ToLower(strings.TrimSpace(tool))
		switch {
		case lower == "read" || lower == "glob" || lower == "grep":
			set[string(config.ToolKindRead)] = struct{}{}
		case lower == "write" || lower == "edit" || lower == "notebookedit":
			set[string(config.ToolKindWrite)] = struct{}{}
		case lower == "bash" || lower == "powershell":
			set[string(config.ToolKindExec)] = struct{}{}
		case lower == "webfetch" || lower == "websearch":
			set[string(config.ToolKindNetwork)] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for kind := range set {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

func renderCodexSubagent(doc agentResourceDocument) ([]byte, error) {
	source := codexSubagentDocument{
		Name:                  doc.ID,
		Description:           strings.TrimSpace(doc.Description),
		DeveloperInstructions: strings.TrimSpace(doc.SystemPrompt),
		Model:                 strings.TrimSpace(doc.Model),
		SandboxMode:           codexSandboxMode(doc.AllowedToolKinds),
	}
	data, err := toml.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("render Codex subagent TOML: %w", err)
	}
	return data, nil
}

func renderClaudeSubagent(doc agentResourceDocument) ([]byte, error) {
	frontmatter := map[string]any{
		"name":        doc.ID,
		"description": strings.TrimSpace(doc.Description),
		"tools":       goFlowKindsToClaudeTools(doc.AllowedToolKinds),
		"model":       firstNonEmptySubagentValue(strings.TrimSpace(doc.Model), "inherit"),
		"maxTurns":    doc.MaxIterations,
	}
	data, err := yaml.Marshal(frontmatter)
	if err != nil {
		return nil, fmt.Errorf("render Claude subagent frontmatter: %w", err)
	}
	return []byte("---\n" + string(data) + "---\n\n" + strings.TrimSpace(doc.SystemPrompt) + "\n"), nil
}

func codexSandboxMode(kinds []string) string {
	for _, kind := range kinds {
		if kind == string(config.ToolKindWrite) || kind == string(config.ToolKindExec) {
			return "workspace-write"
		}
	}
	return "read-only"
}

func goFlowKindsToClaudeTools(kinds []string) []string {
	set := make(map[string]struct{})
	for _, kind := range kinds {
		switch kind {
		case string(config.ToolKindRead):
			set["Read"] = struct{}{}
			set["Glob"] = struct{}{}
			set["Grep"] = struct{}{}
		case string(config.ToolKindWrite):
			set["Edit"] = struct{}{}
			set["Write"] = struct{}{}
		case string(config.ToolKindExec):
			set["Bash"] = struct{}{}
		case string(config.ToolKindNetwork):
			set["WebFetch"] = struct{}{}
			set["WebSearch"] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for tool := range set {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

func firstNonEmptySubagentValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
