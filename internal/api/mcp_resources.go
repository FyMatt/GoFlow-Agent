package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const maxMCPImportBytes = 1 << 20

var builtinMCPResourceNames = map[string]struct{}{
	"command_tools":      {},
	"file_tools":         {},
	"host_tools":         {},
	"network_tools":      {},
	"notification_tools": {},
	"office_tools":       {},
	"python_notes":       {},
	"skill_runner":       {},
	"ssh_tools":          {},
	"web_tools":          {},
}

type mcpResourceDocument struct {
	Name            string   `json:"name"`
	TargetScope     string   `json:"target_scope,omitempty"`
	Transport       string   `json:"transport"`
	Command         string   `json:"command,omitempty"`
	Args            []string `json:"args,omitempty"`
	WorkDir         string   `json:"workdir,omitempty"`
	EnvAllowlist    []string `json:"env_allowlist,omitempty"`
	Enabled         bool     `json:"enabled"`
	Optional        bool     `json:"optional,omitempty"`
	Timeout         string   `json:"timeout,omitempty"`
	Isolation       string   `json:"isolation,omitempty"`
	Path            string   `json:"path,omitempty"`
	RestartRequired bool     `json:"restart_required,omitempty"`
	ApplyState      string   `json:"apply_state,omitempty"`
	ApplyMessage    string   `json:"apply_message,omitempty"`
	Builtin         bool     `json:"builtin,omitempty"`
	ReadOnly        bool     `json:"read_only,omitempty"`
	ToolCount       int      `json:"tool_count,omitempty"`
}

type mcpResourceTestResult struct {
	Ready           bool   `json:"ready"`
	Status          string `json:"status"`
	Message         string `json:"message"`
	ResolvedCommand string `json:"resolved_command,omitempty"`
	WorkDir         string `json:"workdir,omitempty"`
}

type mcpImportResult struct {
	Format   string                `json:"format"`
	Saved    []mcpResourceDocument `json:"saved"`
	Warnings []string              `json:"warnings,omitempty"`
}

type codexMCPConfigDocument struct {
	MCPServers map[string]codexMCPServerDocument `toml:"mcp_servers"`
}

type codexMCPServerDocument struct {
	Command           string            `toml:"command,omitempty"`
	Args              []string          `toml:"args,omitempty"`
	Env               map[string]string `toml:"env,omitempty"`
	EnvVars           []string          `toml:"env_vars,omitempty"`
	CWD               string            `toml:"cwd,omitempty"`
	URL               string            `toml:"url,omitempty"`
	Enabled           *bool             `toml:"enabled,omitempty"`
	StartupTimeoutSec int               `toml:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec    int               `toml:"tool_timeout_sec,omitempty"`
}

type claudeMCPConfigDocument struct {
	MCPServers map[string]claudeMCPServerDocument `json:"mcpServers"`
}

type claudeMCPServerDocument struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

func (s *Server) handleMCPResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.mcpResourceList())
}

func (s *Server) handleMCPResourceItem(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/mcp/"), "/")
	parts := strings.Split(name, "/")
	if len(parts) == 1 && parts[0] == "import" {
		s.handleMCPImport(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "export" {
		s.handleMCPExport(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "test" {
		s.handleMCPTest(w, r, parts[0])
		return
	}
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, ok := s.mcpResourceByName(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, doc)
	case http.MethodPut:
		if s.builtinMCPModuleExists(name) {
			http.Error(w, "built-in MCP resources are read-only", http.StatusForbidden)
			return
		}
		var doc mcpResourceDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		doc.Name = name
		saved, err := s.saveMCPResource(doc)
		if err != nil {
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if s.builtinMCPModuleExists(name) {
			http.Error(w, "built-in MCP resources are read-only", http.StatusForbidden)
			return
		}
		if err := s.deleteMCPResource(name); err != nil {
			http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) mcpResourceList() []mcpResourceDocument {
	seen := make(map[string]struct{})
	out := make([]mcpResourceDocument, 0)
	if s != nil && s.runtime != nil {
		for _, ref := range s.runtime.MCPServerRefs() {
			doc := mcpResourceFromRef(ref)
			if saved, ok := s.loadMCPResourceModule(doc.Name); ok {
				doc = saved
			}
			s.annotateMCPResourceApplyState(&doc)
			s.annotateMCPResourceMetadata(&doc)
			out = append(out, doc)
			seen[doc.Name] = struct{}{}
		}
	}
	root, err := s.mcpResourceRoot()
	if err == nil {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if entry.IsDir() || !isYAMLFile(entry.Name()) {
				continue
			}
			name := normalizeResourceName(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
			if _, ok := seen[name]; ok {
				continue
			}
			if doc, ok := s.loadMCPResourceModule(name); ok {
				s.annotateMCPResourceApplyState(&doc)
				s.annotateMCPResourceMetadata(&doc)
				out = append(out, doc)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) mcpResourceByName(name string) (mcpResourceDocument, bool) {
	name = normalizeResourceName(name)
	if doc, ok := s.loadMCPResourceModule(name); ok {
		s.annotateMCPResourceApplyState(&doc)
		s.annotateMCPResourceMetadata(&doc)
		return doc, true
	}
	if s == nil || s.runtime == nil {
		return mcpResourceDocument{}, false
	}
	for _, ref := range s.runtime.MCPServerRefs() {
		if normalizeResourceName(ref.Name) == name {
			doc := mcpResourceFromRef(ref)
			s.annotateMCPResourceApplyState(&doc)
			s.annotateMCPResourceMetadata(&doc)
			return doc, true
		}
	}
	return mcpResourceDocument{}, false
}

func (s *Server) handleMCPTest(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var doc mcpResourceDocument
	if err := json.NewDecoder(io.LimitReader(r.Body, maxMCPImportBytes)).Decode(&doc); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	doc.Name = name
	doc = normalizeMCPResource(doc)
	if err := validateMCPResource(doc); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, mcpResourceTestResult{Status: "invalid", Message: err.Error()})
		return
	}
	result, err := s.testMCPResource(doc)
	if err != nil {
		writeJSONStatus(w, http.StatusUnprocessableEntity, result)
		return
	}
	writeJSON(w, result)
}

func (s *Server) testMCPResource(doc mcpResourceDocument) (mcpResourceTestResult, error) {
	result := mcpResourceTestResult{Status: "unavailable", WorkDir: doc.WorkDir}
	if s == nil || s.runtime == nil {
		result.Message = "平台运行时尚未就绪。"
		return result, fmt.Errorf("runtime is unavailable")
	}
	workdir := strings.TrimSpace(doc.WorkDir)
	if workdir == "" || workdir == "." {
		workdir = s.runtime.RuntimeHome()
	} else if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(s.runtime.RuntimeHome(), workdir)
	}
	workdir = filepath.Clean(workdir)
	info, err := os.Stat(workdir)
	if err != nil || !info.IsDir() {
		result.Message = "工作目录不存在或不可访问。"
		return result, fmt.Errorf("MCP workdir is unavailable")
	}
	result.WorkDir = workdir
	command := doc.Command
	resolved := ""
	if filepath.IsAbs(command) || strings.ContainsAny(command, `/\\`) {
		resolved = command
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(workdir, resolved)
		}
		resolved = filepath.Clean(resolved)
		info, err = os.Stat(resolved)
		if err != nil || info.IsDir() {
			result.Message = "命令文件不存在或不可执行。"
			return result, fmt.Errorf("MCP command is unavailable")
		}
	} else {
		resolved, err = exec.LookPath(command)
		if err != nil {
			result.Message = "系统 PATH 中找不到该命令。"
			return result, fmt.Errorf("MCP command is unavailable")
		}
	}
	result.Ready = true
	result.Status = "ready"
	result.ResolvedCommand = resolved
	result.Message = "命令和工作目录检查通过，保存后重启平台即可加载。"
	return result, nil
}

func (s *Server) annotateMCPResourceMetadata(doc *mcpResourceDocument) {
	if doc == nil {
		return
	}
	doc.Builtin = s.builtinMCPModuleExists(doc.Name)
	doc.ReadOnly = doc.Builtin
	doc.ToolCount = 0
	if s == nil || s.runtime == nil {
		return
	}
	prefix := normalizeResourceName(doc.Name) + "/"
	for _, name := range s.runtime.ToolNames() {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), prefix) {
			doc.ToolCount++
		}
	}
}

func (s *Server) saveMCPResource(doc mcpResourceDocument) (mcpResourceDocument, error) {
	if err := s.ensureResourceNotInUse("mcp", doc.Name); err != nil {
		return mcpResourceDocument{}, err
	}
	doc = normalizeMCPResource(doc)
	if err := validateMCPResource(doc); err != nil {
		return mcpResourceDocument{}, err
	}
	path, err := s.mcpResourcePath(doc.Name)
	if err != nil {
		return mcpResourceDocument{}, err
	}
	content, err := renderMCPResource(doc)
	if err != nil {
		return mcpResourceDocument{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return mcpResourceDocument{}, fmt.Errorf("create MCP config directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return mcpResourceDocument{}, fmt.Errorf("write MCP config: %w", err)
	}
	if _, ok := parseMCPResourceModule([]byte(content), doc.Name); !ok {
		_ = os.Remove(tmp)
		return mcpResourceDocument{}, fmt.Errorf("validate MCP config module")
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return mcpResourceDocument{}, fmt.Errorf("replace MCP config: %w", err)
	}
	if err := s.runtime.SetResourceTombstone("mcp", doc.Name, false); err != nil {
		return mcpResourceDocument{}, fmt.Errorf("restore MCP visibility: %w", err)
	}
	doc.Path = path
	if s.runtime != nil {
		var parsed struct {
			MCPServers []config.MCPServerRef `yaml:"mcp_servers"`
		}
		if err := yaml.Unmarshal([]byte(content), &parsed); err == nil && len(parsed.MCPServers) == 1 {
			config.ApplyMCPServerDefaults(&parsed.MCPServers[0])
			if err := s.runtime.UpsertMCPServer(parsed.MCPServers[0]); err != nil {
				doc.RestartRequired = true
				doc.ApplyState = "restart_required"
				doc.ApplyMessage = err.Error()
				return doc, nil
			}
		}
	}
	s.annotateMCPResourceApplyState(&doc)
	return doc, nil
}

func (s *Server) deleteMCPResource(name string) error {
	if err := s.ensureResourceNotInUse("mcp", name); err != nil {
		return err
	}
	path, err := s.mcpResourcePath(name)
	if err != nil {
		return err
	}
	if _, ok := s.loadMCPResourceModule(name); !ok {
		if s.builtinMCPModuleExists(name) {
			return s.runtime.SetResourceTombstone("mcp", normalizeResourceName(name), true)
		}
		return fmt.Errorf("MCP server %q is not a file-backed module", name)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete MCP config: %w", err)
	}
	if s.runtime != nil {
		_ = s.runtime.RemoveMCPServer(normalizeResourceName(name))
	}
	return nil
}

func (s *Server) builtinMCPModuleExists(name string) bool {
	name = normalizeResourceName(name)
	if _, ok := builtinMCPResourceNames[name]; ok {
		return true
	}
	if s == nil || s.runtime == nil || sameAPIPath(s.runtime.DataRoot(), s.runtime.RuntimeHome()) {
		return false
	}
	root := filepath.Join(s.runtime.RuntimeHome(), "configs", "mcp_servers")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		if _, ok := parseMCPResourceModule(data, name); ok {
			return true
		}
	}
	return false
}

func (s *Server) loadMCPResourceModule(name string) (mcpResourceDocument, bool) {
	path, err := s.mcpResourcePath(name)
	if err != nil {
		return mcpResourceDocument{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return mcpResourceDocument{}, false
	}
	doc, ok := parseMCPResourceModule(data, name)
	if !ok {
		return mcpResourceDocument{}, false
	}
	doc.Path = path
	return doc, true
}

func parseMCPResourceModule(data []byte, name string) (mcpResourceDocument, bool) {
	var wrapped struct {
		MCPServers []config.MCPServerRef `yaml:"mcp_servers"`
	}
	if err := yaml.Unmarshal(data, &wrapped); err != nil {
		return mcpResourceDocument{}, false
	}
	name = normalizeResourceName(name)
	for _, ref := range wrapped.MCPServers {
		if normalizeResourceName(ref.Name) == name {
			return mcpResourceFromRef(ref), true
		}
	}
	return mcpResourceDocument{}, false
}

func normalizeMCPResource(doc mcpResourceDocument) mcpResourceDocument {
	doc.Name = normalizeResourceName(doc.Name)
	doc.TargetScope = strings.ToLower(strings.TrimSpace(doc.TargetScope))
	doc.Transport = strings.ToLower(strings.TrimSpace(doc.Transport))
	if doc.Transport == "" {
		doc.Transport = "stdio"
	}
	doc.Command = strings.TrimSpace(doc.Command)
	doc.WorkDir = strings.TrimSpace(doc.WorkDir)
	if doc.WorkDir == "" {
		doc.WorkDir = "."
	}
	doc.Timeout = strings.TrimSpace(doc.Timeout)
	if doc.Timeout == "" {
		doc.Timeout = "30s"
	}
	doc.Args = normalizedOrderedMCPStrings(doc.Args)
	doc.EnvAllowlist = normalizedComparableStrings(doc.EnvAllowlist)
	return doc
}

func validateMCPResource(doc mcpResourceDocument) error {
	if !resourceNamePattern.MatchString(doc.Name) {
		return fmt.Errorf("invalid MCP name %q", doc.Name)
	}
	if doc.Transport != "stdio" {
		return fmt.Errorf("MCP transport %q is not available yet; use stdio", doc.Transport)
	}
	if doc.Command == "" {
		return fmt.Errorf("MCP command is required")
	}
	if doc.TargetScope != "" && doc.TargetScope != "host" {
		return fmt.Errorf("MCP target_scope must be empty or host")
	}
	if _, err := time.ParseDuration(doc.Timeout); err != nil {
		return fmt.Errorf("invalid MCP timeout %q", doc.Timeout)
	}
	return nil
}

func renderMCPResource(doc mcpResourceDocument) (string, error) {
	timeout, _ := time.ParseDuration(doc.Timeout)
	ref := config.MCPServerRef{
		Name:               doc.Name,
		TargetScope:        doc.TargetScope,
		Command:            doc.Command,
		Args:               append([]string(nil), doc.Args...),
		Enabled:            doc.Enabled,
		Optional:           doc.Optional,
		Timeout:            timeout,
		WorkDir:            doc.WorkDir,
		EnvAllowlist:       append([]string(nil), doc.EnvAllowlist...),
		Isolation:          "auto",
		RestartLimit:       3,
		Cooldown:           10 * time.Second,
		MaxConcurrentCalls: 1,
		MaxRequestBytes:    65536,
		MaxResponseBytes:   2097152,
	}
	docWrapper := struct {
		MCPServers []config.MCPServerRef `yaml:"mcp_servers"`
	}{MCPServers: []config.MCPServerRef{ref}}
	data, err := yaml.Marshal(docWrapper)
	if err != nil {
		return "", fmt.Errorf("render MCP config: %w", err)
	}
	return "# Loaded automatically from configs/mcp_servers/*.yaml on next startup.\n" + string(data), nil
}

func mcpResourceFromRef(ref config.MCPServerRef) mcpResourceDocument {
	return mcpResourceDocument{
		Name:         normalizeResourceName(ref.Name),
		TargetScope:  normalizeTargetScope(ref.TargetScope),
		Transport:    "stdio",
		Command:      ref.Command,
		Args:         append([]string(nil), ref.Args...),
		WorkDir:      ref.WorkDir,
		EnvAllowlist: append([]string(nil), ref.EnvAllowlist...),
		Isolation:    ref.Isolation,
		Enabled:      ref.Enabled,
		Optional:     ref.Optional,
		Timeout:      ref.Timeout.String(),
	}
}

func (s *Server) annotateMCPResourceApplyState(doc *mcpResourceDocument) {
	if doc == nil {
		return
	}
	doc.RestartRequired = true
	doc.ApplyState = "restart_required"
	doc.ApplyMessage = "Saved MCP configuration requires a restart."
	if s == nil || s.runtime == nil {
		return
	}
	for _, ref := range s.runtime.MCPServerRefs() {
		active := normalizeMCPResource(mcpResourceFromRef(ref))
		candidate := normalizeMCPResource(*doc)
		active.Path, candidate.Path = "", ""
		active.RestartRequired, candidate.RestartRequired = false, false
		active.ApplyState, candidate.ApplyState = "", ""
		active.ApplyMessage, candidate.ApplyMessage = "", ""
		if active.Name == candidate.Name && reflect.DeepEqual(active, candidate) {
			doc.RestartRequired = false
			doc.ApplyState = "active"
			doc.ApplyMessage = "MCP configuration is active."
			return
		}
	}
}

func (s *Server) mcpResourceRoot() (string, error) {
	return s.coreResourceRoot("mcp", "configs", "mcp_servers")
}

func (s *Server) mcpResourcePath(name string) (string, error) {
	root, err := s.mcpResourceRoot()
	if err != nil {
		return "", err
	}
	name = normalizeResourceName(name)
	path := filepath.Clean(filepath.Join(root, name+".yaml"))
	if !referenceWithinBase(root, path) {
		return "", fmt.Errorf("MCP config path escapes runtime config directory")
	}
	return path, nil
}

func (s *Server) handleMCPImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	data, err := io.ReadAll(io.LimitReader(r.Body, maxMCPImportBytes+1))
	if err != nil || len(data) > maxMCPImportBytes {
		http.Error(w, "invalid or oversized MCP import", http.StatusBadRequest)
		return
	}
	var docs []mcpResourceDocument
	var warnings []string
	switch format {
	case "codex":
		docs, warnings, err = parseCodexMCPConfig(data)
	case "claude":
		docs, warnings, err = parseClaudeMCPConfig(data)
	default:
		err = fmt.Errorf("MCP import format must be codex or claude")
	}
	if err != nil {
		http.Error(w, err.Error(), resourceErrorStatus(err, http.StatusBadRequest))
		return
	}
	saved := make([]mcpResourceDocument, 0, len(docs))
	for _, doc := range docs {
		item, saveErr := s.saveMCPResource(doc)
		if saveErr != nil {
			http.Error(w, saveErr.Error(), http.StatusBadRequest)
			return
		}
		saved = append(saved, item)
	}
	writeJSON(w, mcpImportResult{Format: format, Saved: saved, Warnings: warnings})
}

func parseCodexMCPConfig(data []byte) ([]mcpResourceDocument, []string, error) {
	var source codexMCPConfigDocument
	if err := toml.Unmarshal(data, &source); err != nil {
		return nil, nil, fmt.Errorf("parse Codex MCP TOML: %w", err)
	}
	if len(source.MCPServers) == 0 {
		return nil, nil, fmt.Errorf("Codex MCP config contains no mcp_servers")
	}
	names := sortedMapKeys(source.MCPServers)
	docs := make([]mcpResourceDocument, 0, len(names))
	warnings := make([]string, 0)
	for _, name := range names {
		item := source.MCPServers[name]
		if strings.TrimSpace(item.URL) != "" {
			return nil, nil, fmt.Errorf("Codex MCP server %q uses HTTP; GoFlow currently imports stdio servers only", name)
		}
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		timeout := 30
		if item.ToolTimeoutSec > 0 {
			timeout = item.ToolTimeoutSec
		} else if item.StartupTimeoutSec > 0 {
			timeout = item.StartupTimeoutSec
		}
		env := append([]string(nil), item.EnvVars...)
		for key := range item.Env {
			env = append(env, key)
		}
		if len(item.Env) > 0 {
			warnings = append(warnings, fmt.Sprintf("Codex MCP server %s environment values were replaced by environment references", name))
		}
		docs = append(docs, mcpResourceDocument{Name: name, Transport: "stdio", Command: item.Command, Args: item.Args, WorkDir: item.CWD, EnvAllowlist: env, Enabled: enabled, Timeout: fmt.Sprintf("%ds", timeout)})
	}
	return docs, warnings, nil
}

func parseClaudeMCPConfig(data []byte) ([]mcpResourceDocument, []string, error) {
	var source claudeMCPConfigDocument
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, nil, fmt.Errorf("parse Claude MCP JSON: %w", err)
	}
	if len(source.MCPServers) == 0 {
		return nil, nil, fmt.Errorf("Claude MCP config contains no mcpServers")
	}
	names := sortedMapKeys(source.MCPServers)
	docs := make([]mcpResourceDocument, 0, len(names))
	warnings := make([]string, 0)
	for _, name := range names {
		item := source.MCPServers[name]
		if strings.TrimSpace(item.URL) != "" || (item.Type != "" && !strings.EqualFold(item.Type, "stdio")) {
			return nil, nil, fmt.Errorf("Claude MCP server %q is not stdio; GoFlow currently imports stdio servers only", name)
		}
		env := make([]string, 0, len(item.Env))
		for key := range item.Env {
			env = append(env, key)
		}
		if len(item.Env) > 0 {
			warnings = append(warnings, fmt.Sprintf("Claude MCP server %s environment values were replaced by environment references", name))
		}
		docs = append(docs, mcpResourceDocument{Name: name, Transport: "stdio", Command: item.Command, Args: item.Args, EnvAllowlist: env, Enabled: true, Timeout: "30s"})
	}
	return docs, warnings, nil
}

func (s *Server) handleMCPExport(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	doc, ok := s.mcpResourceByName(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	var data []byte
	var err error
	switch format {
	case "codex":
		data, err = renderCodexMCPConfig(doc)
		w.Header().Set("Content-Type", "application/toml; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-mcp.toml"`, doc.Name))
	case "claude":
		data, err = renderClaudeMCPConfig(doc)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-mcp.json"`, doc.Name))
	default:
		http.Error(w, "MCP export format must be codex or claude", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func renderCodexMCPConfig(doc mcpResourceDocument) ([]byte, error) {
	enabled := doc.Enabled
	server := codexMCPServerDocument{Command: doc.Command, Args: doc.Args, EnvVars: doc.EnvAllowlist, CWD: doc.WorkDir, Enabled: &enabled}
	timeout, _ := time.ParseDuration(doc.Timeout)
	server.ToolTimeoutSec = int(timeout.Seconds())
	return toml.Marshal(codexMCPConfigDocument{MCPServers: map[string]codexMCPServerDocument{doc.Name: server}})
}

func renderClaudeMCPConfig(doc mcpResourceDocument) ([]byte, error) {
	env := make(map[string]string, len(doc.EnvAllowlist))
	for _, key := range doc.EnvAllowlist {
		env[key] = "${" + key + "}"
	}
	return json.MarshalIndent(claudeMCPConfigDocument{MCPServers: map[string]claudeMCPServerDocument{doc.Name: {Type: "stdio", Command: doc.Command, Args: doc.Args, Env: env}}}, "", "  ")
}

func sortedMapKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizedOrderedMCPStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
