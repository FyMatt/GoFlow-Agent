package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/commandpolicy"
	"github.com/FyMatt/GoFlow-Agent/internal/executionfingerprint"
)

const (
	defaultCommandTimeout    = 120 * time.Second
	maxCommandTimeout        = 240 * time.Second
	inlineOutputLimit        = 64 * 1024
	artifactOutputLimit      = 8 * 1024 * 1024
	defaultArtifactRetention = 7 * 24 * time.Hour
	maxArtifactRetention     = 30 * 24 * time.Hour
	defaultArtifactMaxFiles  = 200
	maxArtifactMaxFiles      = 2000
)

var workspaceRoot = mustWorkspaceRoot()

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Kind        string          `json:"kind,omitempty"`
}

type commandInput struct {
	Program          string   `json:"program"`
	Args             []string `json:"args"`
	CWD              string   `json:"cwd"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	EnvRefs          []string `json:"env_refs"`
	ReuseIfUnchanged bool     `json:"reuse_if_unchanged"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, in io.Reader, out io.Writer) error {
	defer stopAllManagedProcesses()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}
		switch req.Method {
		case "tools/list":
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": builtinTools()}})
		case "tools/call":
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Result: callTool(ctx, req.Params)})
		default:
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	return scanner.Err()
}

func builtinTools() []tool {
	return []tool{{
		Name:        "run_command",
		Description: "在当前工作区内直接运行一个结构化本地程序，不经过 Shell；program 只填可执行程序，子命令和选项逐项放入 args，例如 program=go、args=[test,./...]，不能把 go test 整体填入 program。返回受限输出、退出信息和大日志产物路径。",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"program":{"type":"string","minLength":1,"maxLength":4096,"description":"PATH 中的程序名，或当前任务工作区内的相对/绝对可执行文件路径，例如 .venv/Scripts/python.exe。"},"args":{"type":"array","maxItems":128,"items":{"type":"string","maxLength":8192}},"cwd":{"type":"string","description":"当前工作区内的运行目录。"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":240},"env_refs":{"type":"array","maxItems":32,"items":{"type":"string","pattern":"^[A-Za-z_][A-Za-z0-9_]*$"},"description":"明确传递给子进程的非敏感环境变量名称。"},"reuse_if_unchanged":{"type":"boolean","description":"工作区完全未变化时允许复用此前成功的检查、测试、构建或静态分析结果。"}},"required":["program"],"additionalProperties":false}`),
		Kind:        "exec",
	}, {
		Name:        "start_process",
		Description: "在当前工作区内启动受管的长驻开发进程，例如 Web 开发服务器；返回进程编号、状态和有界日志。进程随 GoFlow 或当前工作区工具进程退出而停止。",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"program":{"type":"string","minLength":1,"maxLength":4096},"args":{"type":"array","maxItems":128,"items":{"type":"string","maxLength":8192}},"cwd":{"type":"string","description":"当前工作区内的运行目录。"},"env_refs":{"type":"array","maxItems":32,"items":{"type":"string","pattern":"^[A-Za-z_][A-Za-z0-9_]*$"}},"startup_wait_seconds":{"type":"integer","minimum":1,"maximum":15}},"required":["program"],"additionalProperties":false}`),
		Kind:        "exec",
	}, {
		Name:        "process_status",
		Description: "读取受管开发进程的运行状态、退出码以及最近的标准输出和错误输出。",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"process_id":{"type":"string","pattern":"^[a-f0-9]{24}$"}},"required":["process_id"],"additionalProperties":false}`),
		Kind:        "read",
	}, {
		Name:        "stop_process",
		Description: "停止当前工作区中的受管开发进程及其子进程树。",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"process_id":{"type":"string","pattern":"^[a-f0-9]{24}$"}},"required":["process_id"],"additionalProperties":false}`),
		Kind:        "exec",
	}}
}

func callTool(ctx context.Context, params json.RawMessage) map[string]any {
	var input struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return toolError(err)
	}
	encoded, err := json.Marshal(input.Arguments)
	if err != nil {
		return toolError(err)
	}
	var payload map[string]any
	switch input.Name {
	case "run_command":
		var command commandInput
		if err := json.Unmarshal(encoded, &command); err != nil {
			return toolError(err)
		}
		payload, err = runCommand(ctx, command)
	case "start_process":
		var process managedProcessInput
		if err := json.Unmarshal(encoded, &process); err != nil {
			return toolError(err)
		}
		payload, err = startManagedProcess(process)
	case "process_status":
		payload, err = managedProcessStatus(stringArgument(input.Arguments, "process_id"))
	case "stop_process":
		payload, err = stopManagedProcess(stringArgument(input.Arguments, "process_id"))
	default:
		return toolError(fmt.Errorf("unknown tool"))
	}
	if err != nil {
		return toolError(err)
	}
	result, _ := json.Marshal(payload)
	isError := false
	if input.Name == "run_command" {
		isError = commandPayloadIsError(payload)
	} else if input.Name == "start_process" {
		if runningValue, ok := payload["running"].(bool); ok && !runningValue {
			if exitCode, ok := payload["exit_code"].(int); ok && exitCode != 0 {
				isError = true
			}
		}
	}
	return map[string]any{"content": string(result), "is_error": isError}
}

func stringArgument(arguments map[string]interface{}, name string) string {
	value, _ := arguments[name].(string)
	return strings.TrimSpace(value)
}

func commandPayloadIsError(payload map[string]any) bool {
	exitCode, _ := payload["exit_code"].(int)
	timedOut, _ := payload["timed_out"].(bool)
	cancelled, _ := payload["cancelled"].(bool)
	return exitCode != 0 || timedOut || cancelled
}

func runCommand(parent context.Context, input commandInput) (map[string]any, error) {
	if len(input.Args) > 128 {
		return nil, fmt.Errorf("too many arguments: maximum 128")
	}
	for index, arg := range input.Args {
		if len(arg) > 8192 {
			return nil, fmt.Errorf("argument %d is too long", index+1)
		}
		if strings.ContainsRune(arg, '\x00') {
			return nil, fmt.Errorf("argument %d contains a NUL byte", index+1)
		}
	}
	cwd, err := resolveWorkspaceDirectory(input.CWD, workspaceRoot)
	if err != nil {
		return nil, err
	}
	program, executable, executableScope, err := resolveCommandProgram(input.Program, cwd, workspaceRoot)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSuffix(filepath.Base(program), ".exe"), "git") {
		if err := requireGitRepositoryInsideWorkspace(cwd, workspaceRoot); err != nil {
			return nil, err
		}
	}
	timeout := defaultCommandTimeout
	if input.TimeoutSeconds > 0 {
		timeout = time.Duration(input.TimeoutSeconds) * time.Second
	}
	if timeout > maxCommandTimeout {
		return nil, fmt.Errorf("timeout_seconds exceeds maximum of %d", int(maxCommandTimeout/time.Second))
	}
	env, envRefs, err := commandEnvironment(input.EnvRefs)
	if err != nil {
		return nil, err
	}
	classification := commandpolicy.Classify(program, input.Args)
	workspaceBefore := workspaceFingerprintPayload(workspaceRoot)
	retention := commandArtifactPolicy()
	// Cleanup is opportunistic so command logs cannot grow without bounds.
	// Retention failures must not block the command itself.
	_ = cleanupCommandArtifacts(workspaceRoot, retention)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	stdout := newOutputCapture(workspaceRoot, filepath.Base(program), "stdout", retention)
	stderr := newOutputCapture(workspaceRoot, filepath.Base(program), "stderr", retention)
	cmd := exec.Command(executable, input.Args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	prepareCommand(cmd)

	started := time.Now()
	if err := cmd.Start(); err != nil {
		stdout.cleanup()
		stderr.cleanup()
		return nil, fmt.Errorf("start %s: %w", program, err)
	}
	processTree, _ := attachProcessTree(cmd)
	defer closeProcessTreeGuard(processTree)
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var waitErr error
	timedOut := false
	cancelled := false
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		timedOut = ctx.Err() == context.DeadlineExceeded
		cancelled = !timedOut
		_ = terminateProcessTree(cmd, processTree)
		select {
		case waitErr = <-waitCh:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			waitErr = <-waitCh
		}
	}
	duration := time.Since(started)
	stdoutResult := stdout.finalize()
	stderrResult := stderr.finalize()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if waitErr != nil {
		exitCode = -1
	}

	payload := map[string]any{
		"program":                    program,
		"executable_scope":           executableScope,
		"args":                       input.Args,
		"env_refs":                   envRefs,
		"classification":             classification.Class,
		"risk_level":                 classification.RiskLevel,
		"requires_explicit_approval": classification.RequiresExplicitApproval,
		"policy_reason":              classification.Reason,
		"reuse_if_unchanged":         input.ReuseIfUnchanged,
		"cwd":                        cwd,
		"relative_cwd":               relativeWorkspacePath(cwd),
		"exit_code":                  exitCode,
		"duration_ms":                duration.Milliseconds(),
		"timed_out":                  timedOut,
		"cancelled":                  cancelled,
		"stdout":                     stdoutResult.Inline,
		"stderr":                     stderrResult.Inline,
		"stdout_bytes":               stdoutResult.TotalBytes,
		"stderr_bytes":               stderrResult.TotalBytes,
		"stdout_truncated":           stdoutResult.InlineTruncated,
		"stderr_truncated":           stderrResult.InlineTruncated,
		"stdout_artifact":            stdoutResult.ArtifactPath,
		"stderr_artifact":            stderrResult.ArtifactPath,
		"stdout_artifact_sha256":     stdoutResult.ArtifactSHA256,
		"stderr_artifact_sha256":     stderrResult.ArtifactSHA256,
		"stdout_artifact_bytes":      stdoutResult.ArtifactBytes,
		"stderr_artifact_bytes":      stderrResult.ArtifactBytes,
		"stdout_artifact_retention":  stdoutResult.ArtifactRetention,
		"stderr_artifact_retention":  stderrResult.ArtifactRetention,
		"stdout_artifact_expires_at": stdoutResult.ArtifactExpiresAt,
		"stderr_artifact_expires_at": stderrResult.ArtifactExpiresAt,
		"stdout_artifact_truncated":  stdoutResult.ArtifactTruncated,
		"stderr_artifact_truncated":  stderrResult.ArtifactTruncated,
	}
	for key, value := range workspaceBefore {
		payload["workspace_fingerprint_before_"+key] = value
	}
	workspaceAfter := workspaceFingerprintPayload(workspaceRoot)
	for key, value := range workspaceAfter {
		payload["workspace_fingerprint_after_"+key] = value
	}
	if before, ok := workspaceBefore["hash"].(string); ok {
		if after, ok := workspaceAfter["hash"].(string); ok {
			payload["workspace_changed"] = before != after
		}
	}
	if waitErr != nil && !timedOut && !cancelled {
		payload["process_error"] = waitErr.Error()
	}
	if recommendation := commandFailureRecommendation(exitCode, stderrResult.Inline); recommendation != "" {
		payload["recommendation"] = recommendation
	}
	return payload, nil
}

func commandFailureRecommendation(exitCode int, stderr string) string {
	if exitCode != 0 && strings.Contains(strings.ToLower(stderr), "no module named pytest") {
		return "pytest is unavailable; do not retry the same command unless dependencies change. Use Python unittest or the framework test client, or request approval before installing pytest."
	}
	return ""
}

func workspaceFingerprintPayload(root string) map[string]any {
	snapshot, err := executionfingerprint.Workspace(root)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"hash": snapshot.Hash, "file_count": snapshot.FileCount, "byte_count": snapshot.ByteCount}
}

func resolveCommandProgram(raw, cwd, root string) (string, string, string, error) {
	program := strings.TrimSpace(raw)
	if program == "" {
		return "", "", "", fmt.Errorf("program is required")
	}
	if strings.ContainsRune(program, '\x00') {
		return "", "", "", fmt.Errorf("program contains a NUL byte")
	}
	normalized := strings.ToLower(strings.TrimSuffix(filepath.Base(program), ".exe"))
	switch normalized {
	case "sh", "bash", "zsh", "fish", "cmd", "powershell", "pwsh", "wsl":
		return "", "", "", fmt.Errorf("shell program %s is not allowed by run_command", program)
	}
	if !filepath.IsAbs(program) && !strings.ContainsAny(program, `/\\`) {
		if normalized == "python" || normalized == "python3" {
			directories := []string{cwd}
			if filepath.Clean(cwd) != filepath.Clean(root) {
				directories = append(directories, root)
			}
			for _, directory := range directories {
				for _, environment := range []string{".venv", "venv"} {
					rel := filepath.Join(environment, "bin", "python")
					if runtime.GOOS == "windows" {
						rel = filepath.Join(environment, "Scripts", "python.exe")
					}
					candidate := filepath.Join(directory, rel)
					if _, err := os.Stat(candidate); os.IsNotExist(err) {
						continue
					}
					executable, err := resolveWorkspaceExecutable(candidate, root)
					if err != nil {
						return "", "", "", err
					}
					relative, _ := filepath.Rel(root, candidate)
					return filepath.ToSlash(relative), executable, "workspace", nil
				}
			}
		}
		executable, err := exec.LookPath(program)
		if err != nil {
			if len(strings.Fields(program)) > 1 {
				return "", "", "", fmt.Errorf("程序参数格式错误：program 只接受可执行程序名，子命令和选项请放入 args（例如 program=go，args=[test,./...]）；本次未执行任何程序：%s", program)
			}
			return "", "", "", fmt.Errorf("program not found on PATH: %s", program)
		}
		return program, executable, "path", nil
	}
	candidates := []string{program}
	if !filepath.IsAbs(program) {
		candidates = []string{filepath.Join(cwd, filepath.FromSlash(program))}
		if !strings.EqualFold(filepath.Clean(cwd), filepath.Clean(root)) {
			candidates = append(candidates, filepath.Join(root, filepath.FromSlash(program)))
		}
	}
	var lastErr error
	for _, candidate := range candidates {
		executable, err := resolveWorkspaceExecutable(candidate, root)
		if err == nil {
			return filepath.ToSlash(program), executable, "workspace", nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("program path is invalid")
	}
	return "", "", "", lastErr
}

func resolveWorkspaceExecutable(candidate, root string) (string, error) {
	cleanRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	candidate, err = filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve program path: %w", err)
	}
	relative, err := filepath.Rel(cleanRoot, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("program path escapes workspace root")
	}
	if resolvedRoot, rootErr := evalSymlinks(cleanRoot); rootErr == nil {
		cleanRoot = resolvedRoot
	}
	resolved, err := evalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve program path: %w", err)
	}
	resolved, err = filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return "", fmt.Errorf("resolve program path: %w", err)
	}
	relative, err = filepath.Rel(cleanRoot, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("program path resolves outside workspace root")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect program path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("program path is not a regular file")
	}
	return resolved, nil
}

func requireGitRepositoryInsideWorkspace(cwd, root string) error {
	candidate, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return err
	}
	cleanRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect workspace git metadata: %w", err)
		}
		if strings.EqualFold(candidate, cleanRoot) {
			break
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
		candidate = parent
	}
	return fmt.Errorf("git repository was not found inside the task workspace; refusing parent-repository discovery")
}

var commandBaseEnvironment = map[string]struct{}{
	"PATH":                  {},
	"Path":                  {},
	"PATHEXT":               {},
	"SystemRoot":            {},
	"WINDIR":                {},
	"COMSPEC":               {},
	"TEMP":                  {},
	"TMP":                   {},
	"USERPROFILE":           {},
	"HOME":                  {},
	"LOCALAPPDATA":          {},
	"APPDATA":               {},
	"GOPATH":                {},
	"GOROOT":                {},
	"GO111MODULE":           {},
	"GOFLAGS":               {},
	"GOCACHE":               {},
	"GOMODCACHE":            {},
	"GOTMPDIR":              {},
	"GOENV":                 {},
	"GOFLOW_WORKSPACE_ROOT": {},
}

func commandEnvironment(refs []string) ([]string, []string, error) {
	environ := make(map[string]string)
	for _, raw := range os.Environ() {
		key, value, ok := strings.Cut(raw, "=")
		if !ok || key == "" {
			continue
		}
		environ[key] = value
	}
	selected := make(map[string]string)
	for key := range commandBaseEnvironment {
		if value, ok := lookupEnvironment(environ, key); ok {
			selected[key] = value
		}
	}
	cleanRefs := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, raw := range refs {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, nil, fmt.Errorf("env_refs cannot contain an empty name")
		}
		if !isEnvironmentName(name) {
			return nil, nil, fmt.Errorf("env_refs contains invalid name %q", name)
		}
		if isSensitiveEnvironmentName(name) {
			return nil, nil, fmt.Errorf("env_refs cannot pass sensitive environment variable %q", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		value, ok := lookupEnvironment(environ, name)
		if !ok {
			return nil, nil, fmt.Errorf("env_refs variable %q is not set", name)
		}
		selected[name] = value
		cleanRefs = append(cleanRefs, name)
	}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+selected[key])
	}
	sort.Strings(cleanRefs)
	return result, cleanRefs, nil
}

func lookupEnvironment(environ map[string]string, name string) (string, bool) {
	if value, ok := environ[name]; ok {
		return value, true
	}
	if runtime.GOOS != "windows" {
		return "", false
	}
	for key, value := range environ {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}

func isEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for index, char := range name {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || char == '_' || (index > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func isSensitiveEnvironmentName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "AUTH"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func truncatedWorkspaceAncestor(candidate, workspace string) bool {
	cand := strings.TrimRight(strings.ToLower(filepath.Clean(candidate)), `\/`)
	root := strings.ToLower(filepath.Clean(workspace))
	if cand == "" || len(cand) >= len(root) || !strings.HasPrefix(root, cand) {
		return false
	}
	rest := root[len(cand):]
	return rest != "" && rest[0] != filepath.Separator && rest[0] != '/'
}

func resolveWorkspaceDirectory(raw, root string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "."
	}
	cleanRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if realRoot, evalErr := filepath.EvalSymlinks(cleanRoot); evalErr == nil {
		cleanRoot, _ = filepath.Abs(filepath.Clean(realRoot))
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(cleanRoot, candidate)
	}
	candidate, err = filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	rel, err := filepath.Rel(cleanRoot, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if truncatedWorkspaceAncestor(candidate, cleanRoot) {
			return cleanRoot, nil
		}
		return "", fmt.Errorf("cwd %q escapes workspace root", raw)
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd is not a directory")
	}
	hasSymlink, err := pathContainsSymlink(cleanRoot, rel)
	if err != nil {
		return "", fmt.Errorf("inspect cwd: %w", err)
	}
	if !hasSymlink {
		return candidate, nil
	}
	realCandidate, err := evalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	realCandidate, err = filepath.Abs(filepath.Clean(realCandidate))
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	realRel, err := filepath.Rel(cleanRoot, realCandidate)
	if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("cwd %q resolves outside workspace root", raw)
	}
	return realCandidate, nil
}

func pathContainsSymlink(root, relative string) (bool, error) {
	current := root
	paths := []string{root}
	if relative != "." && relative != "" {
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			paths = append(paths, current)
		}
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}

func evalSymlinks(path string) (string, error) {
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		var resolved string
		resolved, err = filepath.EvalSymlinks(path)
		if err == nil {
			return resolved, nil
		}
		if runtime.GOOS != "windows" || !os.IsPermission(err) {
			return "", err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return "", err
}

type captureResult struct {
	Inline            string
	ArtifactPath      string
	ArtifactSHA256    string
	ArtifactBytes     int64
	ArtifactRetention string
	ArtifactExpiresAt string
	TotalBytes        int64
	InlineTruncated   bool
	ArtifactTruncated bool
}

type outputCapture struct {
	mu                sync.Mutex
	root              string
	program           string
	stream            string
	inline            bytes.Buffer
	total             int64
	artifact          *os.File
	artifactPath      string
	artifactBytes     int64
	artifactTruncated bool
	artifactHash      hash.Hash
	retention         artifactRetentionPolicy
}

func newOutputCapture(root, program, stream string, retention artifactRetentionPolicy) *outputCapture {
	return &outputCapture{root: root, program: sanitizeArtifactName(program), stream: stream, retention: retention}
}

func (c *outputCapture) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total += int64(len(data))
	if c.inline.Len() < inlineOutputLimit {
		remaining := inlineOutputLimit - c.inline.Len()
		_, _ = c.inline.Write(data[:minInt(len(data), remaining)])
	}
	if c.artifact == nil {
		if err := c.openArtifact(); err != nil {
			return 0, err
		}
	}
	remaining := int64(artifactOutputLimit) - c.artifactBytes
	if remaining > 0 {
		writeLength := minInt(len(data), int(remaining))
		written, err := c.artifact.Write(data[:writeLength])
		c.artifactBytes += int64(written)
		if c.artifactHash != nil && written > 0 {
			_, _ = c.artifactHash.Write(data[:written])
		}
		if err != nil {
			return written, err
		}
	}
	if c.artifactBytes < c.total {
		c.artifactTruncated = true
	}
	return len(data), nil
}

func (c *outputCapture) openArtifact() error {
	dir := filepath.Join(c.root, ".goflow", "artifacts", "commands")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, c.program+"-"+c.stream+"-*.log")
	if err != nil {
		return err
	}
	c.artifact = file
	c.artifactPath = file.Name()
	c.artifactHash = sha256.New()
	return nil
}

func (c *outputCapture) finalize() captureResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.artifact != nil {
		_ = c.artifact.Sync()
		_ = c.artifact.Close()
		c.artifact = nil
	}
	artifactPath := ""
	artifactHash := ""
	artifactBytes := int64(0)
	artifactRetention := ""
	artifactExpiresAt := ""
	if c.total > inlineOutputLimit && c.artifactPath != "" {
		artifactPath = relativeWorkspacePath(c.artifactPath)
		artifactBytes = c.artifactBytes
		if c.artifactHash != nil {
			artifactHash = hex.EncodeToString(c.artifactHash.Sum(nil))
		}
		artifactRetention = c.retention.label()
		if info, err := os.Stat(c.artifactPath); err == nil {
			artifactExpiresAt = info.ModTime().Add(c.retention.duration).UTC().Format(time.RFC3339)
		}
	} else if c.artifactPath != "" {
		_ = os.Remove(c.artifactPath)
	}
	return captureResult{
		Inline:            strings.ToValidUTF8(c.inline.String(), "�"),
		ArtifactPath:      artifactPath,
		ArtifactSHA256:    artifactHash,
		ArtifactBytes:     artifactBytes,
		ArtifactRetention: artifactRetention,
		ArtifactExpiresAt: artifactExpiresAt,
		TotalBytes:        c.total,
		InlineTruncated:   c.total > inlineOutputLimit,
		ArtifactTruncated: c.artifactTruncated,
	}
}

func (c *outputCapture) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.artifact != nil {
		_ = c.artifact.Close()
		c.artifact = nil
	}
	if c.artifactPath != "" {
		_ = os.Remove(c.artifactPath)
	}
}

type artifactRetentionPolicy struct {
	duration time.Duration
	maxFiles int
}

func commandArtifactPolicy() artifactRetentionPolicy {
	policy := artifactRetentionPolicy{duration: defaultArtifactRetention, maxFiles: defaultArtifactMaxFiles}
	if raw := strings.TrimSpace(os.Getenv("GOFLOW_COMMAND_ARTIFACT_RETENTION_HOURS")); raw != "" {
		if hours, err := strconv.Atoi(raw); err == nil && hours > 0 {
			policy.duration = time.Duration(hours) * time.Hour
			if policy.duration > maxArtifactRetention {
				policy.duration = maxArtifactRetention
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("GOFLOW_COMMAND_ARTIFACT_MAX_FILES")); raw != "" {
		if count, err := strconv.Atoi(raw); err == nil && count > 0 {
			policy.maxFiles = count
			if policy.maxFiles > maxArtifactMaxFiles {
				policy.maxFiles = maxArtifactMaxFiles
			}
		}
	}
	return policy
}

func (p artifactRetentionPolicy) label() string {
	return fmt.Sprintf("workspace:%dh", int(p.duration/time.Hour))
}

func cleanupCommandArtifacts(root string, policy artifactRetentionPolicy) error {
	dir := filepath.Join(root, ".goflow", "artifacts", "commands")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	type artifactFile struct {
		path    string
		modTime time.Time
	}
	files := make([]artifactFile, 0, len(entries))
	cutoff := time.Now().Add(-policy.duration)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
			continue
		}
		files = append(files, artifactFile{path: path, modTime: info.ModTime()})
	}
	if len(files) <= policy.maxFiles {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, file := range files[:len(files)-policy.maxFiles] {
		_ = os.Remove(file.path)
	}
	return nil
}

func sanitizeArtifactName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "command"
	}
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func relativeWorkspacePath(path string) string {
	rel, err := filepath.Rel(workspaceRoot, path)
	if err != nil || rel == "" {
		return path
	}
	return filepath.ToSlash(rel)
}

func mustWorkspaceRoot() string {
	if root := strings.TrimSpace(os.Getenv("GOFLOW_WORKSPACE_ROOT")); root != "" {
		return root
	}
	root, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("resolve workspace root: %v", err))
	}
	return root
}

func toolError(err error) map[string]any {
	return map[string]any{"content": err.Error(), "is_error": true}
}

func writeResponse(writer *bufio.Writer, resp response) {
	data, _ := json.Marshal(resp)
	_, _ = writer.Write(append(data, '\n'))
	_ = writer.Flush()
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
