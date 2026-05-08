package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultContainerWorkspaceTarget = "/workspace"

type containerRunConfig struct {
	ServerName      string
	Command         string
	Args            []string
	Options         map[string]string
	EnvAllowlist    []string
	WorkspaceRoot   string
	NetworkDisabled bool
}

func buildContainerRunCommand(cfg containerRunConfig) (string, []string, []string, error) {
	options := normalizeContainerOptions(cfg.Options)
	image := strings.TrimSpace(options["image"])
	if image == "" {
		return "", nil, nil, fmt.Errorf("isolation_options.image is required for container isolation")
	}
	runtimeCommand := strings.TrimSpace(options["runtime"])
	if runtimeCommand == "" {
		runtimeCommand = "docker"
	}
	if !validContainerRuntimeCommand(runtimeCommand) {
		return "", nil, nil, fmt.Errorf("container runtime must be docker, podman, or an absolute path")
	}
	workspaceTarget := strings.TrimSpace(options["workspace_target"])
	if workspaceTarget == "" {
		workspaceTarget = defaultContainerWorkspaceTarget
	}
	if err := validateContainerTargetPath(workspaceTarget); err != nil {
		return "", nil, nil, fmt.Errorf("workspace_target: %w", err)
	}
	containerWorkDir := strings.TrimSpace(options["container_workdir"])
	if err := validateContainerTargetPath(containerWorkDir); err != nil {
		return "", nil, nil, fmt.Errorf("container_workdir: %w", err)
	}

	args := []string{"run", "--rm", "-i", "--name", containerName(cfg.ServerName), "--label", "goflow.mcp.server=" + sanitizeContainerLabel(cfg.ServerName)}
	if networkMode := effectiveContainerNetworkMode(options["network"], cfg.NetworkDisabled); networkMode != "" {
		args = append(args, "--network", networkMode)
	}
	if ipcMode := effectiveContainerIPCMode(options["ipc"]); ipcMode != "" {
		args = append(args, "--ipc", ipcMode)
	}
	if userNamespace := effectiveContainerUserNamespace(options["userns"]); userNamespace != "" {
		args = append(args, "--userns", userNamespace)
	}
	if optionBool(options["readonly_rootfs"]) {
		args = append(args, "--read-only")
	}
	if optionBool(options["no_new_privileges"]) {
		args = append(args, "--security-opt", "no-new-privileges")
	}
	for _, securityOpt := range splitContainerSecurityOptions(options["security_opt"]) {
		args = append(args, "--security-opt", securityOpt)
	}
	if optionBool(options["init"]) {
		args = append(args, "--init")
	}
	if user := strings.TrimSpace(options["user"]); user != "" {
		args = append(args, "--user", user)
	}
	for _, capName := range splitCSVOption(options["cap_drop"]) {
		args = append(args, "--cap-drop", capName)
	}
	if memory := strings.TrimSpace(options["memory"]); memory != "" {
		args = append(args, "--memory", memory)
	}
	if memorySwap := strings.TrimSpace(options["memory_swap"]); memorySwap != "" {
		args = append(args, "--memory-swap", memorySwap)
	}
	if cpus := strings.TrimSpace(options["cpus"]); cpus != "" {
		args = append(args, "--cpus", cpus)
	}
	if pidsLimit := strings.TrimSpace(options["pids_limit"]); pidsLimit != "" {
		args = append(args, "--pids-limit", pidsLimit)
	}
	for _, tmpfs := range splitContainerTmpfsOptions(options["tmpfs"]) {
		args = append(args, "--tmpfs", tmpfs)
	}
	if mountArg, err := containerWorkspaceMountArg(cfg.WorkspaceRoot, workspaceTarget, options["workspace_mount"]); err != nil {
		return "", nil, nil, err
	} else if mountArg != "" {
		args = append(args, "--mount", mountArg)
	}
	if mountArg, err := containerToolMountArg(options["tool_source"], options["tool_target"], options["tool_mount"]); err != nil {
		return "", nil, nil, err
	} else if mountArg != "" {
		args = append(args, "--mount", mountArg)
	}
	if containerWorkDir != "" {
		args = append(args, "--workdir", containerWorkDir)
	}
	for _, env := range buildContainerServerEnv(cfg.EnvAllowlist, workspaceTarget) {
		args = append(args, "--env", env)
	}
	if pullPolicy := effectiveContainerPullPolicy(options["pull_policy"]); pullPolicy != "" {
		args = append(args, "--pull", pullPolicy)
	}
	args = append(args, image)
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return "", nil, nil, fmt.Errorf("containerized MCP command is required")
	}
	args = append(args, command)
	args = append(args, cfg.Args...)
	return runtimeCommand, args, buildContainerRuntimeEnv(), nil
}

func effectiveContainerPullPolicy(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "always", "missing", "never":
		return value
	default:
		return ""
	}
}

func normalizeContainerOptions(options map[string]string) map[string]string {
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

func validContainerRuntimeCommand(value string) bool {
	value = strings.TrimSpace(value)
	if value == "docker" || value == "podman" {
		return true
	}
	return filepath.IsAbs(value)
}

func validateContainerTargetPath(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("path contains a null byte")
	}
	if !strings.HasPrefix(value, "/") {
		return fmt.Errorf("path must be absolute inside the container")
	}
	if strings.Contains(value, "\\") {
		return fmt.Errorf("path must use forward slashes")
	}
	return nil
}

func effectiveContainerNetworkMode(value string, networkDisabled bool) string {
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

func effectiveContainerIPCMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "private":
		return ""
	case "none":
		return "none"
	default:
		return value
	}
}

func effectiveContainerUserNamespace(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "private":
		return ""
	default:
		return value
	}
}

func optionBool(value string) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	return err == nil && parsed
}

func splitCSVOption(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key := strings.ToLower(field)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, field)
	}
	return out
}

func splitContainerSecurityOptions(value string) []string {
	return splitContainerDelimitedOption(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
}

func splitContainerTmpfsOptions(value string) []string {
	return splitContainerDelimitedOption(value, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\r'
	})
}

func splitContainerDelimitedOption(value string, separator func(rune) bool) []string {
	fields := strings.FieldsFunc(value, separator)
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key := strings.ToLower(field)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, field)
	}
	return out
}

func containerWorkspaceMountArg(workspaceRoot, target, mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "rw"
	}
	switch mode {
	case "none":
		return "", nil
	case "rw", "readwrite", "ro", "readonly":
	default:
		return "", fmt.Errorf("workspace_mount must be rw, ro, or none")
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return "", fmt.Errorf("workspace root is required for container workspace mount")
	}
	absolute, err := filepath.Abs(filepath.Clean(workspaceRoot))
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if !filepath.IsAbs(absolute) {
		return "", fmt.Errorf("workspace root must be absolute for container workspace mount")
	}
	mount := "type=bind,source=" + absolute + ",target=" + target
	if mode == "ro" || mode == "readonly" {
		mount += ",readonly"
	}
	return mount, nil
}

func containerToolMountArg(source, target, mode string) (string, error) {
	source = strings.TrimSpace(source)
	target = strings.TrimSpace(target)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "ro"
	}
	if mode == "none" {
		return "", nil
	}
	if source == "" && target == "" {
		return "", nil
	}
	if source == "" || target == "" {
		return "", fmt.Errorf("tool_source and tool_target must be set together")
	}
	if mode != "ro" && mode != "readonly" && mode != "rw" && mode != "readwrite" {
		return "", fmt.Errorf("tool_mount must be rw, ro, or none")
	}
	absolute, err := filepath.Abs(filepath.Clean(source))
	if err != nil {
		return "", fmt.Errorf("resolve tool source: %w", err)
	}
	if !filepath.IsAbs(absolute) {
		return "", fmt.Errorf("tool_source must be absolute")
	}
	if err := validateContainerTargetPath(target); err != nil {
		return "", fmt.Errorf("tool_target: %w", err)
	}
	mount := "type=bind,source=" + absolute + ",target=" + target
	if mode == "ro" || mode == "readonly" {
		mount += ",readonly"
	}
	return mount, nil
}

func buildContainerServerEnv(allowlist []string, workspaceTarget string) []string {
	seen := make(map[string]struct{}, len(allowlist)+1)
	out := make([]string, 0, len(allowlist)+1)
	for _, name := range allowlist {
		name = strings.TrimSpace(name)
		if name == "" || name == "GOFLOW_WORKSPACE_ROOT" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		out = append(out, name+"="+value)
		seen[name] = struct{}{}
	}
	out = append(out, "GOFLOW_WORKSPACE_ROOT="+workspaceTarget)
	return out
}

func buildContainerRuntimeEnv() []string {
	names := []string{
		"PATH",
		"HOME",
		"USERPROFILE",
		"LOCALAPPDATA",
		"TMP",
		"TEMP",
		"DOCKER_HOST",
		"DOCKER_CONTEXT",
		"DOCKER_CONFIG",
		"XDG_RUNTIME_DIR",
		"SSH_AUTH_SOCK",
		"SYSTEMROOT",
		"COMSPEC",
		"PATHEXT",
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		out = append(out, name+"="+value)
		seen[name] = struct{}{}
	}
	return out
}

func containerName(server string) string {
	server = sanitizeContainerLabel(server)
	if server == "" || !isContainerNameStart(server[0]) {
		server = "server"
	}
	name := fmt.Sprintf("goflow-mcp-%s-%d", server, time.Now().UnixNano())
	if len(name) > 120 {
		name = name[:120]
	}
	return strings.TrimRight(name, "-")
}

func isContainerNameStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
}

func sanitizeContainerLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, ch := range value {
		ok := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.'
		if ok {
			b.WriteRune(ch)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
