package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	skillparser "github.com/FyMatt/GoFlow-Agent/internal/skill"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	defaultScriptTimeout = 30 * time.Second
	maxScriptTimeout     = 10 * time.Minute
	maxCapturedOutput    = 512 * 1024
)

var skillsDir = strings.TrimSpace(os.Getenv("GOFLOW_SKILLS_DIR"))

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

type skillScriptCall struct {
	Skill  string         `json:"skill"`
	Script string         `json:"script"`
	Args   map[string]any `json:"args"`
}

type loadedSkillScript struct {
	Skill     *schema.Skill
	Script    schema.SkillScript
	SkillDir  string
	ScriptAbs string
}

func main() {
	flag.StringVar(&skillsDir, "skills-dir", skillsDir, "directory containing skill folders")
	flag.Parse()
	if strings.TrimSpace(skillsDir) == "" {
		skillsDir = "./skills"
	}
	if err := serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxCapturedOutput)
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}

		switch req.Method {
		case "tools/list":
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": builtinTools()}})
		case "tools/call":
			result := callTool(ctx, req.Params)
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Result: result})
		default:
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	return scanner.Err()
}

func builtinTools() []tool {
	return []tool{{
		Name:        "run_script",
		Description: "Run a deterministic helper script declared in a skill's SKILL.md scripts manifest. Execution is an exec tool and must pass normal agent policy, approval, audit, and MCP isolation.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"skill":{"type":"string","minLength":1,"description":"Skill name or skill folder name."},"script":{"type":"string","minLength":1,"description":"Declared script name from the skill scripts manifest."},"args":{"type":"object","description":"JSON arguments passed to the script on stdin and in GOFLOW_SKILL_ARGS_JSON."}},"required":["skill","script"],"additionalProperties":false}`),
		Kind:        "exec",
	}}
}

func callTool(ctx context.Context, params json.RawMessage) map[string]any {
	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return toolError(err)
	}
	if input.Name != "run_script" {
		return toolError(fmt.Errorf("unknown tool: %s", input.Name))
	}
	var call skillScriptCall
	decoder := json.NewDecoder(bytes.NewReader(input.Arguments))
	decoder.UseNumber()
	if err := decoder.Decode(&call); err != nil {
		return toolError(fmt.Errorf("decode arguments: %w", err))
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return toolError(fmt.Errorf("decode arguments: unexpected trailing data"))
		}
		return toolError(fmt.Errorf("decode arguments: %w", err))
	}
	if call.Args == nil {
		call.Args = map[string]any{}
	}
	result, err := runDeclaredScript(ctx, call)
	if err != nil {
		payload := result
		if payload == nil {
			payload = map[string]any{}
		}
		payload["error"] = err.Error()
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": true}
	}
	encoded, _ := json.Marshal(result)
	return map[string]any{"content": string(encoded), "is_error": false}
}

func runDeclaredScript(ctx context.Context, call skillScriptCall) (map[string]any, error) {
	loaded, err := loadDeclaredScript(call.Skill, call.Script)
	if err != nil {
		return nil, err
	}
	if err := validateScriptArgs(loaded.Script, call.Args); err != nil {
		return baseScriptPayload(loaded, call.Args), err
	}
	timeout := scriptTimeout(loaded.Script.Timeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command, args, err := commandForScript(loaded.Script.Runtime, loaded.ScriptAbs)
	if err != nil {
		return baseScriptPayload(loaded, call.Args), err
	}
	argsJSON, _ := json.Marshal(call.Args)
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = loaded.SkillDir
	cmd.Env = append(os.Environ(),
		"GOFLOW_SKILL_NAME="+loaded.Skill.Name,
		"GOFLOW_SKILL_SCRIPT_NAME="+loaded.Script.Name,
		"GOFLOW_SKILL_SCRIPT_PATH="+filepath.ToSlash(loaded.Script.Path),
		"GOFLOW_SKILL_ARGS_JSON="+string(argsJSON),
	)
	if strings.TrimSpace(loaded.Script.WorkspaceMount) == "none" {
		cmd.Env = withoutEnvName(cmd.Env, "GOFLOW_WORKSPACE_ROOT")
	}
	cmd.Stdin = bytes.NewReader(argsJSON)

	started := time.Now()
	output, execErr := cmd.CombinedOutput()
	duration := time.Since(started)
	stdout := string(output)
	truncated := false
	if len(stdout) > maxCapturedOutput {
		stdout = stdout[:maxCapturedOutput] + "\n[truncated]"
		truncated = true
	}
	payload := baseScriptPayload(loaded, call.Args)
	payload["duration_ms"] = duration.Milliseconds()
	payload["timeout"] = timeout.String()
	payload["stdout"] = stdout
	payload["output"] = stdout
	payload["truncated"] = truncated
	payload["exit_code"] = exitCode(execErr)
	payload["timed_out"] = errors.Is(runCtx.Err(), context.DeadlineExceeded)
	if loaded.Script.Output == "json" {
		payload["output_json_valid"] = strings.TrimSpace(stdout) == "" || json.Valid([]byte(strings.TrimSpace(stdout)))
	}
	if execErr != nil {
		return payload, execErr
	}
	if loaded.Script.Output == "json" && !payload["output_json_valid"].(bool) {
		return payload, fmt.Errorf("script declared json output but stdout is not valid JSON")
	}
	return payload, nil
}

func baseScriptPayload(loaded loadedSkillScript, args map[string]any) map[string]any {
	return map[string]any{
		"skill":            loaded.Skill.Name,
		"script":           loaded.Script.Name,
		"path":             filepath.ToSlash(loaded.Script.Path),
		"runtime":          effectiveScriptRuntime(loaded.Script.Runtime),
		"declared_runtime": loaded.Script.Runtime,
		"declared_output":  loaded.Script.Output,
		"declared_network": loaded.Script.Network,
		"workspace_mount":  loaded.Script.WorkspaceMount,
		"approval":         loaded.Script.Approval,
		"args":             args,
		"sandbox_note":     "execution uses the MCP server's configured isolation; use isolation: container for a strong Docker/Podman boundary",
	}
}

func loadDeclaredScript(skillName, scriptName string) (loadedSkillScript, error) {
	skillName = strings.TrimSpace(skillName)
	scriptName = strings.TrimSpace(scriptName)
	if skillName == "" {
		return loadedSkillScript{}, fmt.Errorf("skill is required")
	}
	if scriptName == "" {
		return loadedSkillScript{}, fmt.Errorf("script is required")
	}
	if strings.ContainsAny(skillName, `/\`) || strings.Contains(skillName, "..") {
		return loadedSkillScript{}, fmt.Errorf("skill must be a skill name or folder name, not a path")
	}
	if strings.ContainsAny(scriptName, `/\`) || strings.Contains(scriptName, "..") {
		return loadedSkillScript{}, fmt.Errorf("script must be a declared script name, not a path")
	}
	root, err := canonicalRoot(skillsDir)
	if err != nil {
		return loadedSkillScript{}, err
	}
	var lastErr error
	if direct, err := loadSkillFromDir(root, filepath.Join(root, skillName), skillName, scriptName); err == nil {
		return direct, nil
	} else if !os.IsNotExist(err) {
		// Continue scanning below, because skillName may be the frontmatter name
		// rather than the folder name.
		lastErr = err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return loadedSkillScript{}, fmt.Errorf("list skills directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		loaded, err := loadSkillFromDir(root, filepath.Join(root, entry.Name()), skillName, scriptName)
		if err == nil {
			return loaded, nil
		}
		if !os.IsNotExist(err) {
			lastErr = err
		}
	}
	if lastErr != nil {
		return loadedSkillScript{}, fmt.Errorf("declared script %q not found for skill %q: %w", scriptName, skillName, lastErr)
	}
	return loadedSkillScript{}, fmt.Errorf("declared script %q not found for skill %q", scriptName, skillName)
}

func loadSkillFromDir(root, dir, requestedSkill, requestedScript string) (loadedSkillScript, error) {
	cleanDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return loadedSkillScript{}, err
	}
	if !isWithinBase(root, cleanDir) {
		return loadedSkillScript{}, fmt.Errorf("skill path escapes skills directory")
	}
	skillPath := filepath.Join(cleanDir, "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		return loadedSkillScript{}, err
	}
	parsed, err := skillparser.ParseFile(skillPath)
	if err != nil {
		return loadedSkillScript{}, err
	}
	if !sameName(requestedSkill, parsed.Name) && !sameName(requestedSkill, filepath.Base(cleanDir)) {
		return loadedSkillScript{}, fmt.Errorf("skill mismatch")
	}
	for _, script := range parsed.Scripts {
		if !sameName(requestedScript, script.Name) {
			continue
		}
		scriptAbs, err := resolveSkillScriptPath(root, cleanDir, script.Path)
		if err != nil {
			return loadedSkillScript{}, err
		}
		if info, err := os.Stat(scriptAbs); err != nil {
			return loadedSkillScript{}, err
		} else if info.IsDir() {
			return loadedSkillScript{}, fmt.Errorf("declared script path is a directory: %s", script.Path)
		}
		return loadedSkillScript{Skill: parsed, Script: script, SkillDir: cleanDir, ScriptAbs: scriptAbs}, nil
	}
	return loadedSkillScript{}, fmt.Errorf("script mismatch")
}

func resolveSkillScriptPath(root, skillDir, relative string) (string, error) {
	if strings.TrimSpace(relative) == "" {
		return "", fmt.Errorf("script path is required")
	}
	candidate := filepath.Join(skillDir, filepath.FromSlash(relative))
	candidate, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve script path: %w", err)
	}
	if !isWithinBase(root, candidate) || !isWithinBase(filepath.Join(skillDir, "scripts"), candidate) {
		return "", fmt.Errorf("script path escapes the skill scripts directory")
	}
	if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink != 0 {
		realPath, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve script symlink: %w", err)
		}
		realPath, err = filepath.Abs(filepath.Clean(realPath))
		if err != nil {
			return "", fmt.Errorf("resolve script real path: %w", err)
		}
		if !isWithinBase(root, realPath) || !isWithinBase(filepath.Join(skillDir, "scripts"), realPath) {
			return "", fmt.Errorf("script symlink escapes the skill scripts directory")
		}
		return realPath, nil
	}
	return candidate, nil
}

func validateScriptArgs(script schema.SkillScript, args map[string]any) error {
	if script.ArgsSchema == nil {
		return nil
	}
	rawSchema, err := json.Marshal(script.ArgsSchema)
	if err != nil {
		return fmt.Errorf("encode args_schema: %w", err)
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("encode args: %w", err)
	}
	return policy.ValidateToolCall(schema.ToolCall{Name: "skill_runner/run_script", Arguments: rawArgs}, schema.Tool{Name: "skill_runner/run_script", InputSchema: rawSchema})
}

func commandForScript(runtimeName, scriptPath string) (string, []string, error) {
	runtimeName = effectiveScriptRuntime(runtimeName)
	switch runtimeName {
	case "python":
		command := firstAvailableCommand(envOrDefault("GOFLOW_SKILL_PYTHON_CMD", ""), "python3", "python")
		if command == "" {
			return "", nil, fmt.Errorf("python runtime not found; set GOFLOW_SKILL_PYTHON_CMD or install python/python3")
		}
		return command, []string{scriptPath}, nil
	case "node":
		command := firstAvailableCommand(envOrDefault("GOFLOW_SKILL_NODE_CMD", ""), "node")
		if command == "" {
			return "", nil, fmt.Errorf("node runtime not found; set GOFLOW_SKILL_NODE_CMD or install node")
		}
		return command, []string{scriptPath}, nil
	case "bash":
		command := firstAvailableCommand(envOrDefault("GOFLOW_SKILL_BASH_CMD", ""), "bash")
		if command == "" {
			return "", nil, fmt.Errorf("bash runtime not found; set GOFLOW_SKILL_BASH_CMD or install bash")
		}
		return command, []string{scriptPath}, nil
	case "powershell":
		command := firstAvailableCommand(envOrDefault("GOFLOW_SKILL_POWERSHELL_CMD", ""), "pwsh", "powershell")
		if command == "" {
			return "", nil, fmt.Errorf("powershell runtime not found; set GOFLOW_SKILL_POWERSHELL_CMD or install PowerShell")
		}
		return command, []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath}, nil
	case "go":
		command := firstAvailableCommand(envOrDefault("GOFLOW_SKILL_GO_CMD", ""), "go")
		if command == "" {
			return "", nil, fmt.Errorf("go runtime not found; set GOFLOW_SKILL_GO_CMD or install go")
		}
		return command, []string{"run", scriptPath}, nil
	case "binary":
		return scriptPath, nil, nil
	default:
		return "", nil, fmt.Errorf("unsupported script runtime %q", runtimeName)
	}
}

func effectiveScriptRuntime(runtimeName string) string {
	runtimeName = strings.ToLower(strings.TrimSpace(runtimeName))
	if runtimeName == "" {
		return "python"
	}
	return runtimeName
}

func scriptTimeout(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultScriptTimeout
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return defaultScriptTimeout
	}
	if timeout > maxScriptTimeout {
		return maxScriptTimeout
	}
	return timeout
}

func firstAvailableCommand(candidates ...string) string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func canonicalRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("skills directory is required")
	}
	clean, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve skills directory: %w", err)
	}
	real, err := filepath.EvalSymlinks(clean)
	if err == nil {
		clean, err = filepath.Abs(filepath.Clean(real))
		if err != nil {
			return "", fmt.Errorf("resolve skills directory: %w", err)
		}
	}
	return clean, nil
}

func isWithinBase(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sameName(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func withoutEnvName(env []string, name string) []string {
	prefix := name + "="
	out := env[:0]
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func toolError(err error) map[string]any {
	if err == nil {
		return map[string]any{"content": "", "is_error": true}
	}
	return map[string]any{"content": err.Error(), "is_error": true}
}

func write(writer *bufio.Writer, resp response) {
	data, _ := json.Marshal(resp)
	_, _ = writer.Write(append(data, '\n'))
	_ = writer.Flush()
}

func init() {
	if goruntime.GOOS == "windows" {
		// Keep PowerShell child processes non-interactive when scripts are run
		// from release archives or HTTP mode.
		_ = os.Setenv("POWERSHELL_TELEMETRY_OPTOUT", "1")
	}
}
