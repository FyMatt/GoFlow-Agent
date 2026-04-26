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
		Name:             "file_tools",
		Command:          "go",
		Args:             []string{"run", "./mcp_servers/file_tools"},
		WorkDir:          "./sandbox",
		EnvAllowlist:     []string{"PATH", "HOME", "PATH", "MISSING"},
		NetworkDisabled:  true,
		Isolation:        "process_group",
		RestartLimit:     4,
		Cooldown:         15 * time.Second,
		Timeout:          5 * time.Second,
		MaxRequestBytes:  1024,
		MaxResponseBytes: 2048,
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
