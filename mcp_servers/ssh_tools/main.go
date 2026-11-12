package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/executiontarget"
	"github.com/FyMatt/GoFlow-Agent/internal/sshhostkey"
)

const (
	defaultTimeoutSeconds = 30
	maxTimeoutSeconds     = 120
	maxOutputBytes        = 1024 * 1024
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Kind        string          `json:"kind,omitempty"`
}

type toolInput struct {
	TargetID        string `json:"target_id,omitempty"`
	Host            string `json:"host,omitempty"`
	Port            int    `json:"port,omitempty"`
	AuthorizedScope bool   `json:"authorized_scope,omitempty"`
	Command         string `json:"command,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
}

type commandOutput struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

type hostKey = sshhostkey.Key

type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.truncated = true
	}
	return written, nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }

var (
	hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	runCommand      = runBoundedCommand
)

func main() {
	if err := serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx = withHostKeyCache(ctx)
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
	return []tool{
		{
			Name:        "ssh_host_key_scan",
			Description: "扫描已授权 SSH 主机的公开主机密钥并计算 SHA256 指纹，不登录主机、不使用凭据。配置新目标时可直接传 host；检查既有目标时传 target_id。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"target_id":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,63}$"},"host":{"type":"string","maxLength":253},"port":{"type":"integer","minimum":1,"maximum":65535},"authorized_scope":{"type":"boolean"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":120}},"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "ssh_run_command",
			Description: "在已保存且启用的 SSH Target 上执行一条精确白名单命令。强制固定主机指纹、密钥认证、BatchMode、超时、取消和输出上限，不支持密码、交互式 sudo 或任意 Shell。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"target_id":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,63}$"},"command":{"type":"string","minLength":1,"maxLength":500},"timeout_seconds":{"type":"integer","minimum":1,"maximum":120}},"required":["target_id","command"],"additionalProperties":false}`),
			Kind:        "exec",
		},
	}
}

func callTool(ctx context.Context, params json.RawMessage) map[string]any {
	var input struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return toolError(err)
	}
	encoded, err := json.Marshal(input.Arguments)
	if err != nil {
		return toolError(err)
	}
	var args toolInput
	if err := json.Unmarshal(encoded, &args); err != nil {
		return toolError(err)
	}
	var payload map[string]any
	switch input.Name {
	case "ssh_host_key_scan":
		payload, err = scanTool(ctx, args)
	case "ssh_run_command":
		payload, err = runTool(ctx, args)
	default:
		err = fmt.Errorf("unknown tool %q", input.Name)
	}
	if err != nil {
		return toolError(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return toolError(err)
	}
	isError, _ := payload["is_error"].(bool)
	delete(payload, "is_error")
	if isError {
		data, _ = json.Marshal(payload)
	}
	return map[string]any{"content": string(data), "is_error": isError}
}

func scanTool(ctx context.Context, input toolInput) (map[string]any, error) {
	host := strings.Trim(strings.TrimSpace(input.Host), "[]")
	port := input.Port
	pinned := ""
	targetID := strings.ToLower(strings.TrimSpace(input.TargetID))
	if targetID != "" {
		target, err := loadTarget(targetID)
		if err != nil {
			return nil, err
		}
		host, port, pinned = target.Host, target.Port, target.HostKeySHA256
	} else {
		if !input.AuthorizedScope {
			return nil, fmt.Errorf("authorized_scope must be true before scanning a host")
		}
		if !validHost(host) {
			return nil, fmt.Errorf("host must be an IP address or DNS name without a scheme or path")
		}
		if port == 0 {
			port = 22
		}
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("port must be between 1 and 65535")
		}
	}
	deadlineCtx, cancel := timeoutContext(ctx, input.TimeoutSeconds)
	defer cancel()
	keys, output, err := scanHostKeys(deadlineCtx, host, port)
	if err != nil {
		return nil, err
	}
	matched := pinned == ""
	for _, key := range keys {
		if key.Fingerprint == pinned {
			matched = true
		}
	}
	if pinned != "" && !matched {
		return nil, fmt.Errorf("SSH host key mismatch for Target %q: pinned fingerprint was not presented", targetID)
	}
	if pinned != "" {
		for _, key := range keys {
			if key.Fingerprint == pinned {
				target := executiontarget.Target{Host: host, Port: port, HostKeySHA256: pinned}
				cachedPinnedHostKey(ctx, target, knownHostName(host, port)+" "+key.KeyType+" "+key.KeyData+"\n")
				break
			}
		}
	}
	return map[string]any{
		"kind": "ssh_host_key_scan", "status": "ok", "target_id": targetID, "host": host, "port": port,
		"keys": keys, "count": len(keys), "pinned_fingerprint": pinned, "pinned_match": matched,
		"stderr": strings.TrimSpace(output.Stderr), "truncated": output.Truncated, "collected_at": time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func runTool(ctx context.Context, input toolInput) (map[string]any, error) {
	target, err := loadTarget(input.TargetID)
	if err != nil {
		return nil, err
	}
	command := normalizeCommand(input.Command)
	if !allowedCommand(target.AllowedCommands, command) {
		return nil, fmt.Errorf("command is outside the exact allowlist for Target %q", target.ID)
	}
	deadlineCtx, cancel := timeoutContext(ctx, input.TimeoutSeconds)
	defer cancel()
	knownHostLine := cachedPinnedHostKey(ctx, target, "")
	hostKeyCached := knownHostLine != ""
	if knownHostLine == "" {
		keys, _, scanErr := scanHostKeys(deadlineCtx, target.Host, target.Port)
		if scanErr != nil {
			return nil, scanErr
		}
		for _, key := range keys {
			if key.Fingerprint == target.HostKeySHA256 {
				knownHostLine = knownHostName(target.Host, target.Port) + " " + key.KeyType + " " + key.KeyData + "\n"
				cachedPinnedHostKey(ctx, target, knownHostLine)
				break
			}
		}
	}
	if knownHostLine == "" {
		return nil, fmt.Errorf("SSH host key mismatch for Target %q", target.ID)
	}
	privateKey, err := resolvePrivateKey(target.CredentialRef)
	if err != nil {
		return nil, err
	}
	tempRoot, err := os.MkdirTemp("", "goflow-ssh-")
	if err != nil {
		return nil, fmt.Errorf("create SSH temporary directory: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	if err := secureSSHDirectory(tempRoot); err != nil {
		return nil, fmt.Errorf("restrict SSH temporary directory: %w", err)
	}
	keyPath := filepath.Join(tempRoot, "identity")
	knownHostsPath := filepath.Join(tempRoot, "known_hosts")
	if err := os.WriteFile(keyPath, append(bytes.TrimSpace(privateKey), '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("write temporary SSH identity: %w", err)
	}
	if err := os.WriteFile(knownHostsPath, []byte(knownHostLine), 0o600); err != nil {
		return nil, fmt.Errorf("write temporary known_hosts: %w", err)
	}
	sshCommand := firstEnv("GOFLOW_SSH_CMD", "ssh")
	connectTimeout := boundedTimeout(input.TimeoutSeconds)
	args := []string{
		"-F", os.DevNull,
		"-o", "BatchMode=yes", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no",
		"-o", "PubkeyAuthentication=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + knownHostsPath, "-o", "GlobalKnownHostsFile=" + os.DevNull,
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout), "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=1",
		"-p", strconv.Itoa(target.Port), "-i", keyPath, target.User + "@" + target.Host, command,
	}
	output, runErr := runCommand(deadlineCtx, sshCommand, args...)
	status := "ok"
	if runErr != nil || output.ExitCode != 0 {
		status = "failed"
	}
	payload := map[string]any{
		"kind": "ssh_command", "status": status, "target_id": target.ID, "host": target.Host, "port": target.Port,
		"command": command, "stdout": output.Stdout, "stderr": output.Stderr, "exit_code": output.ExitCode,
		"truncated": output.Truncated, "host_key_sha256": target.HostKeySHA256, "completed_at": time.Now().UTC().Format(time.RFC3339Nano),
		"host_key_cached": hostKeyCached,
	}
	if runErr != nil {
		payload["error"] = runErr.Error()
		payload["is_error"] = true
	}
	if output.ExitCode != 0 {
		payload["is_error"] = true
	}
	return payload, nil
}

func loadTarget(id string) (executiontarget.Target, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" || id == executiontarget.LocalID {
		return executiontarget.Target{}, fmt.Errorf("ssh tool requires a saved non-local target_id")
	}
	root := strings.TrimSpace(os.Getenv("GOFLOW_DATA_ROOT"))
	if root == "" {
		return executiontarget.Target{}, fmt.Errorf("GOFLOW_DATA_ROOT is required")
	}
	target, ok, err := executiontarget.NewStore(root).Get(id)
	if err != nil {
		return executiontarget.Target{}, err
	}
	if !ok {
		return executiontarget.Target{}, fmt.Errorf("SSH Target %q was not found", id)
	}
	if !target.Enabled {
		return executiontarget.Target{}, fmt.Errorf("SSH Target %q is disabled", id)
	}
	if target.Kind != executiontarget.KindSSH {
		return executiontarget.Target{}, fmt.Errorf("Target %q is not an SSH target", id)
	}
	return target, nil
}

func scanHostKeys(ctx context.Context, host string, port int) ([]hostKey, commandOutput, error) {
	keyscan := firstEnv("GOFLOW_SSH_KEYSCAN_CMD", "ssh-keyscan")
	keys, output, err := sshhostkey.Scan(ctx, keyscan, host, port, func(ctx context.Context, name string, args ...string) (sshhostkey.CommandOutput, error) {
		result, runErr := runCommand(ctx, name, args...)
		return sshhostkey.CommandOutput{
			Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode, Truncated: result.Truncated,
		}, runErr
	})
	return keys, commandOutput{
		Stdout: output.Stdout, Stderr: output.Stderr, ExitCode: output.ExitCode, Truncated: output.Truncated,
	}, err
}

func parseHostKeys(raw string) ([]hostKey, error) {
	return sshhostkey.Parse(raw)
}

func resolvePrivateKey(reference string) ([]byte, error) {
	reference = strings.TrimSpace(reference)
	var value string
	var err error
	if strings.HasPrefix(reference, "secret://") {
		value, err = config.ResolveSecretReference(os.Getenv("GOFLOW_DATA_ROOT"), reference)
	} else if strings.HasPrefix(reference, "env:") {
		name := strings.TrimPrefix(reference, "env:")
		name = strings.TrimPrefix(name, "//")
		value = os.Getenv(name)
		if strings.TrimSpace(value) == "" {
			err = fmt.Errorf("credential environment variable %s is empty", name)
		}
	} else {
		err = fmt.Errorf("unsupported SSH credential reference")
	}
	if err != nil {
		return nil, err
	}
	value = strings.TrimSpace(value)
	if strings.Contains(value, "PRIVATE KEY") {
		return []byte(value), nil
	}
	info, statErr := os.Stat(value)
	if statErr != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("SSH credential reference must resolve to private-key content or a regular key file")
	}
	data, readErr := os.ReadFile(value)
	if readErr != nil {
		return nil, fmt.Errorf("read SSH private key: %w", readErr)
	}
	if !strings.Contains(string(data), "PRIVATE KEY") {
		return nil, fmt.Errorf("SSH credential does not contain a private key")
	}
	return data, nil
}

func runBoundedCommand(ctx context.Context, name string, args ...string) (commandOutput, error) {
	var stdout, stderr boundedBuffer
	stdout.limit, stderr.limit = maxOutputBytes, maxOutputBytes
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	output := commandOutput{Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.truncated || stderr.truncated}
	if err == nil {
		return output, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		output.ExitCode = exitErr.ExitCode()
	} else {
		output.ExitCode = -1
	}
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	return output, err
}

func timeoutContext(parent context.Context, seconds int) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, time.Duration(boundedTimeout(seconds))*time.Second)
}

func boundedTimeout(value int) int {
	if value <= 0 {
		return defaultTimeoutSeconds
	}
	if value > maxTimeoutSeconds {
		return maxTimeoutSeconds
	}
	return value
}

func validHost(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), "[]")
	if value == "" || strings.ContainsAny(value, `/\\?#@ \t\r\n`) {
		return false
	}
	return net.ParseIP(value) != nil || hostnamePattern.MatchString(value) && !strings.Contains(value, "..")
}

func normalizeCommand(value string) string { return strings.Join(strings.Fields(value), " ") }

func allowedCommand(allowed []string, command string) bool {
	for _, candidate := range allowed {
		if normalizeCommand(candidate) == command {
			return true
		}
	}
	return false
}

func knownHostName(host string, port int) string {
	host = strings.Trim(host, "[]")
	if port == 22 {
		return host
	}
	return "[" + host + "]:" + strconv.Itoa(port)
}

func firstEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func toolError(err error) map[string]any {
	return map[string]any{"content": err.Error(), "is_error": true}
}

func writeResponse(writer *bufio.Writer, value response) {
	data, _ := json.Marshal(value)
	_, _ = writer.Write(data)
	_ = writer.WriteByte('\n')
	_ = writer.Flush()
}
