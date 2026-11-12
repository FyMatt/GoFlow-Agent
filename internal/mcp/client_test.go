package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

func TestMCPDelayHelperProcess(t *testing.T) {
	if os.Getenv("GOFLOW_MCP_DELAY_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &request)
		time.Sleep(10 * time.Second)
		encoded, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  map[string]any{"tools": []any{}},
		})
		fmt.Fprintln(os.Stdout, string(encoded))
	}
	os.Exit(0)
}

func TestMCPResyncHelperProcess(t *testing.T) {
	if os.Getenv("GOFLOW_MCP_RESYNC_HELPER") != "1" {
		return
	}
	marker := os.Getenv("GOFLOW_MCP_RESYNC_MARKER")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &request)
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			_ = os.WriteFile(marker, []byte("first request received"), 0o600)
			time.Sleep(10 * time.Second)
		}
		encoded, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{"tools": []map[string]any{{
				"name":        "resynced_tool",
				"description": "available after restart",
				"kind":        "read",
				"input_schema": map[string]any{
					"type": "object",
				},
			}}},
		})
		fmt.Fprintln(os.Stdout, string(encoded))
	}
	os.Exit(0)
}

func TestNewClientAppliesIsolationConfig(t *testing.T) {
	t.Setenv("PATH", "/usr/local/bin")
	t.Setenv("HOME", "/tmp/home")

	client := NewClient(config.MCPServerRef{
		Name:               "file_tools",
		Command:            "go",
		Args:               []string{"run", "./mcp_servers/file_tools"},
		WorkDir:            "./sandbox",
		EnvAllowlist:       []string{"PATH", "HOME", "PATH", "MISSING"},
		NetworkDisabled:    true,
		Isolation:          "process_group",
		RestartLimit:       4,
		Cooldown:           15 * time.Second,
		MaxConcurrentCalls: 2,
		Timeout:            5 * time.Second,
		MaxRequestBytes:    1024,
		MaxResponseBytes:   2048,
	})

	if client.workDir != "./sandbox" {
		t.Fatalf("unexpected workdir: %q", client.workDir)
	}
	if !client.networkDisabled {
		t.Fatal("expected networkDisabled to be true")
	}
	if client.isolation != "process_group" {
		t.Fatalf("expected process_group isolation, got %q", client.isolation)
	}
	if client.restartLimit != 4 {
		t.Fatalf("unexpected restart limit: %d", client.restartLimit)
	}
	if client.cooldown != 15*time.Second {
		t.Fatalf("unexpected cooldown: %s", client.cooldown)
	}
	if client.maxConcurrent != 2 || cap(client.callGate) != 2 {
		t.Fatalf("unexpected max concurrent call gate: max=%d cap=%d", client.maxConcurrent, cap(client.callGate))
	}
	expected := []string{"PATH=/usr/local/bin", "HOME=/tmp/home"}
	if runtime.GOOS == "windows" {
		expected = append(expected,
			"SYSTEMROOT="+os.Getenv("SYSTEMROOT"),
			"COMSPEC="+os.Getenv("COMSPEC"),
			"PATHEXT="+os.Getenv("PATHEXT"),
		)
	}
	if !reflect.DeepEqual(client.env, expected) {
		t.Fatalf("unexpected env: %#v", client.env)
	}
}

func TestNewClientDefaultsMaxConcurrentCalls(t *testing.T) {
	client := NewClient(config.MCPServerRef{Name: "file_tools"})
	if client.maxConcurrent != 1 || cap(client.callGate) != 1 {
		t.Fatalf("expected default max concurrent call gate, got max=%d cap=%d", client.maxConcurrent, cap(client.callGate))
	}
}

func TestAcquireCallSlotHonorsContextCancellation(t *testing.T) {
	client := NewClient(config.MCPServerRef{Name: "file_tools", MaxConcurrentCalls: 1})
	if err := client.acquireCallSlot(context.Background()); err != nil {
		t.Fatalf("acquire first slot: %v", err)
	}
	metrics := client.CallMetrics()
	if metrics.ActiveCalls != 1 || metrics.QueuedCalls != 0 || metrics.AvailableCallSlots != 0 {
		t.Fatalf("unexpected metrics after first acquire: %#v", metrics)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := client.acquireCallSlot(ctx)
	client.releaseCallSlot()
	if err == nil || !strings.Contains(err.Error(), "mcp call queue wait cancelled") {
		t.Fatalf("expected queue wait cancellation error, got %v", err)
	}
	metrics = client.CallMetrics()
	if metrics.ActiveCalls != 0 || metrics.QueuedCalls != 0 || metrics.AvailableCallSlots != 1 {
		t.Fatalf("unexpected metrics after cancelled wait and release: %#v", metrics)
	}
}

func TestAcquireCallSlotReportsQueuedAndActiveMetrics(t *testing.T) {
	client := NewClient(config.MCPServerRef{Name: "file_tools", MaxConcurrentCalls: 1})
	if err := client.acquireCallSlot(context.Background()); err != nil {
		t.Fatalf("acquire first slot: %v", err)
	}
	acquired := make(chan error, 1)
	go func() {
		acquired <- client.acquireCallSlot(context.Background())
	}()

	deadline := time.Now().Add(time.Second)
	for {
		metrics := client.CallMetrics()
		if metrics.ActiveCalls == 1 && metrics.QueuedCalls == 1 && metrics.AvailableCallSlots == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for queued metrics, got %#v", metrics)
		}
		time.Sleep(time.Millisecond)
	}

	client.releaseCallSlot()
	if err := <-acquired; err != nil {
		t.Fatalf("acquire queued slot: %v", err)
	}
	metrics := client.CallMetrics()
	if metrics.ActiveCalls != 1 || metrics.QueuedCalls != 0 || metrics.AvailableCallSlots != 0 {
		t.Fatalf("unexpected metrics after queued acquire: %#v", metrics)
	}
	client.releaseCallSlot()
	metrics = client.CallMetrics()
	if metrics.ActiveCalls != 0 || metrics.QueuedCalls != 0 || metrics.AvailableCallSlots != 1 {
		t.Fatalf("unexpected metrics after final release: %#v", metrics)
	}
}

func TestCallTimeoutStopsServerBeforeReturning(t *testing.T) {
	t.Setenv("GOFLOW_MCP_DELAY_HELPER", "1")
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	client := NewClient(config.MCPServerRef{
		Name:               "delay_helper",
		Command:            binary,
		Args:               []string{"-test.run=TestMCPDelayHelperProcess"},
		EnvAllowlist:       []string{"GOFLOW_MCP_DELAY_HELPER", "PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "TMP", "TEMP"},
		Isolation:          "process_group",
		Timeout:            50 * time.Millisecond,
		MaxRequestBytes:    64 * 1024,
		MaxResponseBytes:   64 * 1024,
		MaxConcurrentCalls: 1,
	})
	client.killTimeout = 100 * time.Millisecond
	_, err = client.ListTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "mcp call timeout") {
		t.Fatalf("expected timeout, got %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.cmd != nil || client.stdin != nil || client.stdout != nil {
		t.Fatalf("expected timed-out server process and pipes to be cleared")
	}
}

func TestCallCancellationRestartsAndResynchronizesServer(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "first-request.marker")
	t.Setenv("GOFLOW_MCP_RESYNC_HELPER", "1")
	t.Setenv("GOFLOW_MCP_RESYNC_MARKER", marker)
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	client := NewClient(config.MCPServerRef{
		Name:               "resync_helper",
		Command:            binary,
		Args:               []string{"-test.run=TestMCPResyncHelperProcess"},
		EnvAllowlist:       []string{"GOFLOW_MCP_RESYNC_HELPER", "GOFLOW_MCP_RESYNC_MARKER", "PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "TMP", "TEMP"},
		Isolation:          "process_group",
		Timeout:            5 * time.Second,
		MaxRequestBytes:    64 * 1024,
		MaxResponseBytes:   64 * 1024,
		MaxConcurrentCalls: 1,
	})
	client.killTimeout = 100 * time.Millisecond
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, err := client.ListTools(ctx)
		firstResult <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for helper to receive first request")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-firstResult; err == nil || !strings.Contains(err.Error(), "mcp call cancelled: context canceled") {
		t.Fatalf("expected explicit cancellation error, got %v", err)
	}
	if status := client.HealthStatus(context.Background()); !strings.HasPrefix(status, "idle") || strings.Contains(status, "error") {
		t.Fatalf("caller cancellation must not poison MCP health, got %q", status)
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools after cancellation: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "resynced_tool" {
		t.Fatalf("expected clean response from restarted server, got %#v", tools)
	}
	client.mu.Lock()
	restarts := client.restartCount
	client.mu.Unlock()
	if restarts < 2 {
		t.Fatalf("expected server restart after cancellation, got %d starts", restarts)
	}
}

func TestHealthStatusCallerCancellationRemainsIdle(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "health-request-seen")
	if err := os.WriteFile(marker, []byte("allow initial response"), 0o600); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
	t.Setenv("GOFLOW_MCP_RESYNC_HELPER", "1")
	t.Setenv("GOFLOW_MCP_RESYNC_MARKER", marker)
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	client := NewClient(config.MCPServerRef{
		Name:               "health_cancel_helper",
		Command:            binary,
		Args:               []string{"-test.run=TestMCPResyncHelperProcess"},
		EnvAllowlist:       []string{"GOFLOW_MCP_RESYNC_HELPER", "GOFLOW_MCP_RESYNC_MARKER", "PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "TMP", "TEMP"},
		Isolation:          "process_group",
		Timeout:            5 * time.Second,
		MaxRequestBytes:    64 * 1024,
		MaxResponseBytes:   64 * 1024,
		MaxConcurrentCalls: 1,
	})
	client.killTimeout = 100 * time.Millisecond
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("initial ListTools: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("remove marker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan string, 1)
	go func() { result <- client.HealthStatus(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for health request")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	status := <-result
	if !strings.HasPrefix(status, "idle") || strings.Contains(status, "error") {
		t.Fatalf("cancelled health request must remain idle, got %q", status)
	}
}

func TestNormalizeIsolationModeDefaultsToNone(t *testing.T) {
	if got := normalizeIsolationMode(""); got != "none" {
		t.Fatalf("expected empty isolation to normalize to none, got %q", got)
	}
	if got := normalizeIsolationMode(" PROCESS_GROUP "); got != "process_group" {
		t.Fatalf("expected isolation to normalize, got %q", got)
	}
	if got := normalizeIsolationMode(" WINDOWS_JOB "); got != "windows_job" {
		t.Fatalf("expected windows_job isolation to normalize, got %q", got)
	}
	if got := normalizeIsolationMode(" WINDOWS_RESTRICTED_TOKEN "); got != "windows_restricted_token" {
		t.Fatalf("expected windows_restricted_token isolation to normalize, got %q", got)
	}
	if got := normalizeIsolationMode(" LINUX_NETNS "); got != "linux_netns" {
		t.Fatalf("expected linux_netns isolation to normalize, got %q", got)
	}
}

func TestAutomaticUnknownMCPServerNeverResolvesToUnisolatedProcess(t *testing.T) {
	resolved, err := config.ResolveAutomaticMCPIsolation(config.MCPServerRef{Name: "third_party_helper", Command: "go", Isolation: "auto"})
	if err != nil {
		if !strings.Contains(err.Error(), "原生强沙箱") || !strings.Contains(err.Error(), "拒绝") {
			t.Fatalf("expected a clear native sandbox refusal, got %v", err)
		}
		return
	}
	if resolved.Isolation != "native_strong" {
		t.Fatalf("unknown MCP must resolve only to native_strong, got %#v", resolved)
	}
}

func TestBuildAllowedEnvFiltersMissingAndDuplicateValues(t *testing.T) {
	t.Setenv("PATH", "/usr/local/bin")
	_ = os.Unsetenv("NOT_SET")

	env := buildAllowedEnv([]string{"PATH", "", "PATH", "NOT_SET"})
	expected := []string{"PATH=/usr/local/bin"}
	if runtime.GOOS == "windows" {
		expected = append(expected,
			"SYSTEMROOT="+os.Getenv("SYSTEMROOT"),
			"COMSPEC="+os.Getenv("COMSPEC"),
			"PATHEXT="+os.Getenv("PATHEXT"),
		)
	}
	if !reflect.DeepEqual(env, expected) {
		t.Fatalf("unexpected env: %#v", env)
	}
}

func TestBuildAllowedEnvWithoutAllowlistDoesNotLeakEnvironment(t *testing.T) {
	t.Setenv("PATH", "/secret/bin")
	t.Setenv("GOFLOW_SECRET", "do-not-leak")

	env := buildAllowedEnv(nil)
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "GOFLOW_SECRET=do-not-leak") {
		t.Fatalf("unexpected secret env leak: %#v", env)
	}
	if strings.Contains(joined, "PATH=/secret/bin") {
		t.Fatalf("unexpected PATH env leak without allowlist: %#v", env)
	}
}

func TestEnsureStartedReturnsCooldownError(t *testing.T) {
	client := &Client{
		name:          "file_tools",
		restartLimit:  1,
		cooldown:      10 * time.Second,
		cooldownUntil: time.Now().Add(5 * time.Second),
	}

	if err := client.ensureStarted(); err == nil {
		t.Fatal("expected cooldown error")
	}
}

func TestHealthStatusReportsCooldown(t *testing.T) {
	client := &Client{
		name:          "file_tools",
		restartCount:  2,
		cooldownUntil: time.Now().Add(5 * time.Minute),
	}

	status := client.HealthStatus(context.Background())
	if status == "" || status[:8] != "cooldown" {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestListToolsStartsGoServerFromRepoRootWhenWorkDirIsDot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", t.TempDir())
		t.Setenv("LOCALAPPDATA", t.TempDir())
		t.Setenv("SYSTEMROOT", os.Getenv("SYSTEMROOT"))
		t.Setenv("COMSPEC", os.Getenv("COMSPEC"))
		t.Setenv("PATHEXT", os.Getenv("PATHEXT"))
	} else {
		t.Setenv("HOME", t.TempDir())
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	client := NewClient(config.MCPServerRef{
		Name:             "file_tools",
		Command:          "go",
		Args:             []string{"run", "./mcp_servers/file_tools"},
		WorkDir:          repoRoot,
		EnvAllowlist:     []string{"PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "TMP", "TEMP"},
		Timeout:          30 * time.Second,
		MaxRequestBytes:  64 * 1024,
		MaxResponseBytes: 2 * 1024 * 1024,
	})
	t.Cleanup(func() {
		client.mu.Lock()
		defer client.mu.Unlock()
		_ = client.stopProcessLocked()
	})

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected discovered tools")
	}
}

func TestNewClientAddsWorkspaceRootEnv(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	client := NewClient(config.MCPServerRef{
		Name:          "file_tools",
		Command:       "go",
		Args:          []string{"run", "./mcp_servers/file_tools"},
		WorkDir:       ".",
		EnvAllowlist:  []string{"PATH"},
		WorkspaceRoot: workspaceRoot,
	})

	joined := strings.Join(client.env, "\n")
	if !strings.Contains(joined, "GOFLOW_WORKSPACE_ROOT="+workspaceRoot) {
		t.Fatalf("expected workspace env in %#v", client.env)
	}
}
