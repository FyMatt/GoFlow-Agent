package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

func TestWorkspaceScopedFileToolsWriteOnlyTaskCopy(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	realWorkspace := t.TempDir()
	taskWorkspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(realWorkspace, "demo.txt"), []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskWorkspace, "demo.txt"), []byte("staged-before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	envAllowlist := []string{"PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "TMP", "TEMP", "GOPATH", "GOCACHE", "GOMODCACHE"}
	manager := NewManager([]config.MCPServerRef{{
		Name:               "file_tools",
		Command:            "go",
		Args:               []string{"run", "./mcp_servers/file_tools"},
		Enabled:            true,
		Timeout:            30 * time.Second,
		WorkDir:            moduleRoot,
		EnvAllowlist:       envAllowlist,
		Isolation:          "process_group",
		MaxConcurrentCalls: 1,
		MaxRequestBytes:    65536,
		MaxResponseBytes:   2097152,
		WorkspaceRoot:      realWorkspace,
	}})
	defer manager.Close()
	if _, err := manager.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	baselineHash := sha256.Sum256([]byte("staged-before\n"))
	arguments, err := json.Marshal(map[string]any{"path": "demo.txt", "content": "staged-after\n", "expected_sha256": fmt.Sprintf("%x", baselineHash[:])})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.CallToolInWorkspace(context.Background(), "file_tools/write_file", arguments, taskWorkspace)
	if err != nil {
		t.Fatalf("CallToolInWorkspace: %v", err)
	}
	if result.IsError {
		t.Fatalf("workspace-scoped file tool returned error: %s", result.Content)
	}
	staged, err := os.ReadFile(filepath.Join(taskWorkspace, "demo.txt"))
	if err != nil || string(staged) != "staged-after\n" {
		t.Fatalf("unexpected staged file: %q err=%v", staged, err)
	}
	real, err := os.ReadFile(filepath.Join(realWorkspace, "demo.txt"))
	if err != nil || string(real) != "real\n" {
		t.Fatalf("real workspace was modified by scoped call: %q err=%v", real, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(taskWorkspace, "demo.txt")); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("staged output is not a regular file: info=%v err=%v", info, err)
		}
	}
}
