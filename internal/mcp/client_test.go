package mcp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

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
	if got := normalizeIsolationMode(" CONTAINER "); got != "container" {
		t.Fatalf("expected container isolation to normalize, got %q", got)
	}
	if got := normalizeIsolationMode(" LINUX_NETNS "); got != "linux_netns" {
		t.Fatalf("expected linux_netns isolation to normalize, got %q", got)
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

func TestBuildContainerRunCommandUsesWorkspaceMountEnvAndLimits(t *testing.T) {
	t.Setenv("PATH", "/usr/local/bin")
	t.Setenv("API_TOKEN", "secret")
	workspaceRoot := t.TempDir()

	runtimeCommand, args, env, err := buildContainerRunCommand(containerRunConfig{
		ServerName:      "file_tools",
		Command:         "/app/bin/file_tools",
		Args:            []string{"--stdio"},
		EnvAllowlist:    []string{"API_TOKEN", "API_TOKEN", "MISSING"},
		WorkspaceRoot:   workspaceRoot,
		NetworkDisabled: true,
		Options: map[string]string{
			"image":             "goflow/mcp-tools:latest",
			"runtime":           "docker",
			"workspace_mount":   "ro",
			"workspace_target":  "/workspace",
			"container_workdir": "/app",
			"ipc":               "none",
			"userns":            "auto",
			"tool_source":       filepath.Join(workspaceRoot, "tools", "helper.py"),
			"tool_target":       "/goflow-tools/helper.py",
			"tool_mount":        "ro",
			"memory":            "256m",
			"memory_swap":       "256m",
			"cpus":              "0.5",
			"pids_limit":        "64",
			"pull_policy":       "missing",
			"readonly_rootfs":   "true",
			"no_new_privileges": "true",
			"cap_drop":          "all",
			"security_opt":      "seccomp=/etc/goflow/seccomp.json;apparmor=goflow-mcp",
			"tmpfs":             "/tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m",
			"init":              "true",
		},
	})
	if err != nil {
		t.Fatalf("build container command: %v", err)
	}
	if runtimeCommand != "docker" {
		t.Fatalf("expected docker runtime, got %q", runtimeCommand)
	}
	joinedArgs := strings.Join(args, "\n")
	for _, want := range []string{
		"run",
		"--rm",
		"--network\nnone",
		"--ipc\nnone",
		"--userns\nauto",
		"--memory\n256m",
		"--memory-swap\n256m",
		"--cpus\n0.5",
		"--pids-limit\n64",
		"--read-only",
		"--security-opt\nno-new-privileges",
		"--security-opt\nseccomp=/etc/goflow/seccomp.json",
		"--security-opt\napparmor=goflow-mcp",
		"--init",
		"--cap-drop\nall",
		"--tmpfs\n/tmp:rw,noexec,nosuid,size=64m",
		"--tmpfs\n/run:rw,noexec,nosuid,size=8m",
		"--workdir\n/app",
		"--env\nAPI_TOKEN=secret",
		"--env\nGOFLOW_WORKSPACE_ROOT=/workspace",
		"--pull\nmissing",
		"goflow/mcp-tools:latest",
		"/app/bin/file_tools",
		"--stdio",
	} {
		if !strings.Contains(joinedArgs, want) {
			t.Fatalf("expected args to contain %q, got %#v", want, args)
		}
	}
	mount := "type=bind,source=" + filepath.Clean(workspaceRoot) + ",target=/workspace,readonly"
	if !strings.Contains(joinedArgs, mount) {
		t.Fatalf("expected read-only workspace mount %q in %#v", mount, args)
	}
	toolMount := "type=bind,source=" + filepath.Clean(filepath.Join(workspaceRoot, "tools", "helper.py")) + ",target=/goflow-tools/helper.py,readonly"
	if !strings.Contains(joinedArgs, toolMount) {
		t.Fatalf("expected read-only tool mount %q in %#v", toolMount, args)
	}
	if strings.Contains(joinedArgs, "MISSING=") {
		t.Fatalf("unexpected missing env in args %#v", args)
	}
	if !containsEnv(env, "PATH=/usr/local/bin") {
		t.Fatalf("expected docker runtime PATH env, got %#v", env)
	}
}

func TestBuildContainerRunCommandSupportsNoWorkspaceMount(t *testing.T) {
	_, args, _, err := buildContainerRunCommand(containerRunConfig{
		ServerName: "stateless",
		Command:    "helper",
		Options: map[string]string{
			"image":           "goflow/stateless:latest",
			"workspace_mount": "none",
		},
	})
	if err != nil {
		t.Fatalf("build stateless container command: %v", err)
	}
	if strings.Contains(strings.Join(args, "\n"), "type=bind") {
		t.Fatalf("did not expect a workspace mount in %#v", args)
	}
}

func containsEnv(env []string, target string) bool {
	for _, item := range env {
		if item == target {
			return true
		}
	}
	return false
}
