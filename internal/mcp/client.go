package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const defaultKillTimeout = 2 * time.Second

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Client manages a stdio MCP server connection.
type Client struct {
	name             string
	command          string
	args             []string
	timeout          time.Duration
	workDir          string
	env              []string
	envAllowlist     []string
	workspaceRoot    string
	networkDisabled  bool
	isolation        string
	isolationOptions map[string]string
	restartLimit     int
	cooldown         time.Duration
	maxRequestBytes  int
	maxResponseBytes int
	killTimeout      time.Duration
	isolationHandle  *processIsolation

	mu                 sync.Mutex
	nextID             int
	cmd                *exec.Cmd
	stdin              *bufio.Writer
	stdout             *bufio.Reader
	restartCount       int
	lastStart          time.Time
	lastError          string
	lastErrorAt        time.Time
	lastHealth         string
	lastHealthAt       time.Time
	cooldownUntil      time.Time
	consecutiveFailure int
}

// NewClient builds a stdio MCP client.
func NewClient(cfg config.MCPServerRef) *Client {
	killTimeout := defaultKillTimeout
	if cfg.Timeout > 0 && cfg.Timeout < killTimeout {
		killTimeout = cfg.Timeout
	}
	return &Client{
		name:             cfg.Name,
		command:          cfg.Command,
		args:             append([]string(nil), cfg.Args...),
		timeout:          cfg.Timeout,
		workDir:          cfg.WorkDir,
		env:              buildClientEnv(cfg.EnvAllowlist, cfg.WorkspaceRoot),
		envAllowlist:     append([]string(nil), cfg.EnvAllowlist...),
		workspaceRoot:    cfg.WorkspaceRoot,
		networkDisabled:  cfg.NetworkDisabled,
		isolation:        normalizeIsolationMode(cfg.Isolation),
		isolationOptions: cloneStringMap(cfg.IsolationOptions),
		restartLimit:     cfg.RestartLimit,
		cooldown:         cfg.Cooldown,
		maxRequestBytes:  cfg.MaxRequestBytes,
		maxResponseBytes: cfg.MaxResponseBytes,
		killTimeout:      killTimeout,
		nextID:           1,
		lastHealth:       "idle",
	}
}

func (c *Client) ensureStarted() error {
	if c.inCooldownLocked() {
		return c.cooldownErrorLocked()
	}
	if c.cmd != nil && c.cmd.ProcessState == nil {
		return nil
	}
	return c.restartLocked()
}

func (c *Client) restartLocked() error {
	if c.inCooldownLocked() {
		return c.cooldownErrorLocked()
	}
	if c.restartLimit > 0 && c.consecutiveFailure >= c.restartLimit {
		c.cooldownUntil = time.Now().Add(c.cooldown)
		c.lastHealth = "cooldown"
		c.lastHealthAt = time.Now()
		return c.cooldownErrorLocked()
	}
	if err := c.stopProcessLocked(); err != nil {
		c.recordFailureLocked(err)
		return err
	}

	cmd, err := c.newServerCommand()
	if err != nil {
		wrapped := fmt.Errorf("prepare mcp server %s command: %w", c.name, err)
		c.recordFailureLocked(wrapped)
		return wrapped
	}
	processAttr, attrCleanup, err := newSysProcAttr(c.processIsolationMode())
	if err != nil {
		wrapped := fmt.Errorf("prepare mcp server %s isolation %s: %w", c.name, c.isolation, err)
		c.recordFailureLocked(wrapped)
		return wrapped
	}
	attrCleanupTransferred := false
	defer func() {
		if attrCleanup != nil && !attrCleanupTransferred {
			_ = attrCleanup()
		}
	}()
	cmd.SysProcAttr = processAttr
	if c.isolation != "container" && strings.TrimSpace(c.workDir) != "" {
		cmd.Dir = c.workDir
	} else if c.isolation != "container" && filepath.IsAbs(c.command) {
		cmd.Dir = filepath.Dir(c.command)
	}
	cmd.Stderr = os.Stderr

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		wrapped := fmt.Errorf("open stdin pipe: %w", err)
		c.recordFailureLocked(wrapped)
		return wrapped
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		wrapped := fmt.Errorf("open stdout pipe: %w", err)
		c.recordFailureLocked(wrapped)
		return wrapped
	}
	if err := cmd.Start(); err != nil {
		wrapped := fmt.Errorf("start mcp server %s: %w", c.name, err)
		c.recordFailureLocked(wrapped)
		return wrapped
	}
	attrCleanupTransferred = true
	isolationHandle, err := attachProcessIsolation(c.isolation, c.isolationOptions, cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if attrCleanup != nil {
			_ = attrCleanup()
		}
		wrapped := fmt.Errorf("apply mcp server %s isolation %s: %w", c.name, c.isolation, err)
		c.recordFailureLocked(wrapped)
		return wrapped
	}
	if isolationHandle == nil && attrCleanup != nil {
		isolationHandle = &processIsolation{}
	}
	if isolationHandle != nil && attrCleanup != nil {
		isolationHandle.cleanup = attrCleanup
	}
	c.cmd = cmd
	c.isolationHandle = isolationHandle
	c.stdin = bufio.NewWriter(stdinPipe)
	c.stdout = bufio.NewReaderSize(stdoutPipe, c.maxResponseBytes)
	c.restartCount++
	c.lastStart = time.Now()
	c.lastHealth = c.readyHealthLabel()
	c.lastHealthAt = time.Now()
	return nil
}

func (c *Client) newServerCommand() (*exec.Cmd, error) {
	if c.isolation == "linux_netns" {
		runtimeCommand, runtimeArgs, runtimeEnv, err := buildLinuxNetworkNamespaceCommand(linuxNetworkNamespaceConfig{
			Command: c.command,
			Args:    c.args,
			Options: c.isolationOptions,
			Env:     c.env,
		})
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(runtimeCommand, runtimeArgs...)
		cmd.Env = runtimeEnv
		return cmd, nil
	}
	if c.isolation != "container" {
		cmd := exec.Command(c.command, c.args...)
		cmd.Env = c.env
		return cmd, nil
	}
	runtimeCommand, runtimeArgs, runtimeEnv, err := buildContainerRunCommand(containerRunConfig{
		ServerName:      c.name,
		Command:         c.command,
		Args:            c.args,
		Options:         c.isolationOptions,
		EnvAllowlist:    c.envAllowlist,
		WorkspaceRoot:   c.workspaceRoot,
		NetworkDisabled: c.networkDisabled,
	})
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(runtimeCommand, runtimeArgs...)
	cmd.Env = runtimeEnv
	return cmd, nil
}

func (c *Client) processIsolationMode() string {
	if c.isolation == "container" || c.isolation == "linux_netns" {
		return "process_group"
	}
	return c.isolation
}

// Close stops the MCP server process if it is running.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopProcessLocked()
}

func (c *Client) stopProcessLocked() error {
	if c.cmd == nil || c.cmd.Process == nil {
		c.resetLocked()
		return nil
	}

	_ = interruptProcess(c.cmd)
	waitCtx, cancel := context.WithTimeout(context.Background(), c.killTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func(cmd *exec.Cmd) {
		done <- cmd.Wait()
	}(c.cmd)

	select {
	case err := <-done:
		_ = closeProcessIsolation(c.isolationHandle)
		c.resetLocked()
		if err != nil && !isAlreadyExited(err) {
			wrapped := fmt.Errorf("wait mcp server %s: %w", c.name, err)
			c.recordFailureLocked(wrapped)
			return wrapped
		}
		return nil
	case <-waitCtx.Done():
		if err := terminateProcessIsolation(c.isolationHandle, c.cmd); err != nil && !isAlreadyExited(err) {
			_ = closeProcessIsolation(c.isolationHandle)
			c.resetLocked()
			wrapped := fmt.Errorf("kill mcp server %s: %w", c.name, err)
			c.recordFailureLocked(wrapped)
			return wrapped
		}
		err := <-done
		_ = closeProcessIsolation(c.isolationHandle)
		c.resetLocked()
		if err != nil && !isAlreadyExited(err) {
			wrapped := fmt.Errorf("wait mcp server %s after kill: %w", c.name, err)
			c.recordFailureLocked(wrapped)
			return wrapped
		}
		return nil
	}
}

func (c *Client) resetLocked() {
	c.cmd = nil
	c.isolationHandle = nil
	c.stdin = nil
	c.stdout = nil
}

func (c *Client) recordFailureLocked(err error) {
	if err == nil {
		return
	}
	c.lastError = err.Error()
	c.lastErrorAt = time.Now()
	c.lastHealth = "error"
	c.lastHealthAt = c.lastErrorAt
	c.consecutiveFailure++
	if c.restartLimit > 0 && c.consecutiveFailure >= c.restartLimit {
		c.cooldownUntil = time.Now().Add(c.cooldown)
		c.lastHealth = "cooldown"
		c.lastHealthAt = time.Now()
	}
}

func (c *Client) markHealthyLocked(detail string) {
	c.lastHealth = detail
	c.lastHealthAt = time.Now()
	c.consecutiveFailure = 0
	c.cooldownUntil = time.Time{}
}

func (c *Client) inCooldownLocked() bool {
	return !c.cooldownUntil.IsZero() && time.Now().Before(c.cooldownUntil)
}

func (c *Client) cooldownErrorLocked() error {
	if !c.inCooldownLocked() {
		return nil
	}
	remaining := time.Until(c.cooldownUntil).Round(time.Second)
	if remaining < time.Second {
		remaining = time.Second
	}
	return fmt.Errorf("mcp server %s is cooling down for %s", c.name, remaining)
}

func (c *Client) readyHealthLabel() string {
	if c.networkDisabled {
		return "ready (network=disabled)"
	}
	return "ready"
}

// HealthStatus reports current status for operator-facing views.
func (c *Client) HealthStatus(ctx context.Context) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.inCooldownLocked() {
		return fmt.Sprintf("cooldown (restarts=%d, until=%s)", c.restartCount, c.cooldownUntil.Format(time.RFC3339))
	}
	if c.cmd == nil || c.cmd.ProcessState != nil {
		if c.lastError != "" {
			return fmt.Sprintf("error (restarts=%d, last_error=%s)", c.restartCount, c.lastError)
		}
		return fmt.Sprintf("idle (restarts=%d)", c.restartCount)
	}

	var out struct {
		Tools []schema.Tool `json:"tools"`
	}
	if err := c.callLocked(ctx, "tools/list", map[string]any{}, &out); err != nil {
		c.recordFailureLocked(err)
		if c.inCooldownLocked() {
			return fmt.Sprintf("cooldown (restarts=%d, until=%s)", c.restartCount, c.cooldownUntil.Format(time.RFC3339))
		}
		return fmt.Sprintf("error (restarts=%d, last_error=%s)", c.restartCount, c.lastError)
	}
	c.markHealthyLocked(fmt.Sprintf("%s (%d tools)", c.readyHealthLabel(), len(out.Tools)))
	return fmt.Sprintf("%s, restarts=%d", c.lastHealth, c.restartCount)
}

// ListTools requests the tool list from the remote MCP server.
func (c *Client) ListTools(ctx context.Context) ([]schema.Tool, error) {
	var out struct {
		Tools []schema.Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	for i := range out.Tools {
		out.Tools[i].Server = c.name
	}
	sort.Slice(out.Tools, func(i, j int) bool {
		return out.Tools[i].Name < out.Tools[j].Name
	})
	return out.Tools, nil
}

// CallTool invokes a named tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, arguments []byte) (schema.ToolResult, error) {
	args, err := decodeArguments(arguments, c.maxRequestBytes)
	if err != nil {
		return schema.ToolResult{}, err
	}

	var out struct {
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	if err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &out); err != nil {
		return schema.ToolResult{}, err
	}
	return schema.ToolResult{ToolName: name, Content: out.Content, IsError: out.IsError}, nil
}

func (c *Client) call(ctx context.Context, method string, params interface{}, out interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.callLocked(ctx, method, params, out)
}

func (c *Client) callLocked(ctx context.Context, method string, params interface{}, out interface{}) error {
	if err := c.ensureStarted(); err != nil {
		return err
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	request := rpcRequest{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params}
	c.nextID++
	data, err := json.Marshal(request)
	if err != nil {
		wrapped := fmt.Errorf("marshal rpc request: %w", err)
		c.recordFailureLocked(wrapped)
		return wrapped
	}
	if c.maxRequestBytes > 0 && len(data) > c.maxRequestBytes {
		wrapped := fmt.Errorf("rpc request too large: %d bytes exceeds %d", len(data), c.maxRequestBytes)
		c.recordFailureLocked(wrapped)
		return wrapped
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		_ = c.stopProcessLocked()
		wrapped := fmt.Errorf("write rpc request: %w", err)
		c.recordFailureLocked(wrapped)
		return wrapped
	}
	if err := c.stdin.Flush(); err != nil {
		_ = c.stopProcessLocked()
		wrapped := fmt.Errorf("flush rpc request: %w", err)
		c.recordFailureLocked(wrapped)
		return wrapped
	}

	lineCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := readLimitedLine(c.stdout, c.maxResponseBytes)
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- line
	}()

	select {
	case <-ctx.Done():
		wrapped := fmt.Errorf("mcp call timeout: %w", ctx.Err())
		c.recordFailureLocked(wrapped)
		return wrapped
	case err := <-errCh:
		if err == io.EOF {
			_ = c.stopProcessLocked()
		}
		wrapped := fmt.Errorf("read rpc response: %w", err)
		c.recordFailureLocked(wrapped)
		return wrapped
	case line := <-lineCh:
		var response rpcResponse
		if err := json.Unmarshal(line, &response); err != nil {
			wrapped := fmt.Errorf("decode rpc response: %w", err)
			c.recordFailureLocked(wrapped)
			return wrapped
		}
		if response.ID != request.ID {
			wrapped := fmt.Errorf("unexpected rpc response id %d for request %d", response.ID, request.ID)
			c.recordFailureLocked(wrapped)
			return wrapped
		}
		if response.Error != nil {
			wrapped := fmt.Errorf("rpc error %d: %s", response.Error.Code, response.Error.Message)
			c.recordFailureLocked(wrapped)
			return wrapped
		}
		if out == nil {
			c.markHealthyLocked(c.readyHealthLabel())
			return nil
		}
		if err := json.Unmarshal(response.Result, out); err != nil {
			wrapped := fmt.Errorf("decode rpc result: %w", err)
			c.recordFailureLocked(wrapped)
			return wrapped
		}
		c.markHealthyLocked(c.readyHealthLabel())
		return nil
	}
}

func decodeArguments(arguments []byte, maxBytes int) (map[string]any, error) {
	if len(arguments) == 0 {
		return map[string]any{}, nil
	}
	if maxBytes > 0 && len(arguments) > maxBytes {
		return nil, fmt.Errorf("tool arguments too large: %d bytes exceeds %d", len(arguments), maxBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode tool arguments: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode tool arguments: unexpected trailing data")
		}
		return nil, fmt.Errorf("decode tool arguments: %w", err)
	}
	args, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("decode tool arguments: expected JSON object")
	}
	return args, nil
}

func readLimitedLine(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return reader.ReadBytes('\n')
	}
	line, err := reader.ReadSlice('\n')
	if err == nil {
		if len(line) > maxBytes {
			return nil, fmt.Errorf("rpc response too large: %d bytes exceeds %d", len(line), maxBytes)
		}
		return append([]byte(nil), line...), nil
	}
	collected := append([]byte(nil), line...)
	if len(collected) > maxBytes {
		return nil, fmt.Errorf("rpc response too large: exceeds %d bytes", maxBytes)
	}
	if err != bufio.ErrBufferFull {
		return nil, err
	}

	for {
		fragment, readErr := reader.ReadSlice('\n')
		collected = append(collected, fragment...)
		if len(collected) > maxBytes {
			return nil, fmt.Errorf("rpc response too large: exceeds %d bytes", maxBytes)
		}
		if readErr == nil {
			return collected, nil
		}
		if readErr != bufio.ErrBufferFull {
			return nil, readErr
		}
	}
}

func buildAllowedEnv(allowlist []string) []string {
	return buildClientEnv(allowlist, "")
}

func buildClientEnv(allowlist []string, workspaceRoot string) []string {
	var env []string
	if len(allowlist) == 0 {
		env = mergeEssentialEnv(nil, make(map[string]struct{}))
	} else {
		allowed := make([]string, 0, len(allowlist))
		seen := make(map[string]struct{}, len(allowlist))
		for _, name := range allowlist {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			value, ok := os.LookupEnv(name)
			if !ok {
				continue
			}
			allowed = append(allowed, fmt.Sprintf("%s=%s", name, value))
			seen[name] = struct{}{}
		}
		env = mergeEssentialEnv(allowed, seen)
	}
	if strings.TrimSpace(workspaceRoot) != "" {
		env = append(env, fmt.Sprintf("GOFLOW_WORKSPACE_ROOT=%s", workspaceRoot))
	}
	return env
}

func mergeEssentialEnv(env []string, seen map[string]struct{}) []string {
	for _, name := range []string{"SYSTEMROOT", "COMSPEC", "PATHEXT"} {
		if _, ok := seen[name]; ok {
			continue
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", name, value))
		seen[name] = struct{}{}
	}
	return env
}

func isAlreadyExited(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return true
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err == os.ErrProcessDone
	}
	return errors.Is(err, os.ErrProcessDone)
}

var interruptProcess = func(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}

var killProcess = func(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func normalizeIsolationMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "none"
	}
	return mode
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
