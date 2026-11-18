//go:build windows

package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const windowsRestrictedTokenChild = "GOFLOW_WINDOWS_RESTRICTED_TOKEN_CHILD"

func TestNewSysProcAttrUsesWindowsProcessGroupIsolation(t *testing.T) {
	attr, cleanup, err := newSysProcAttr("process_group")
	if err != nil || cleanup != nil {
		t.Fatalf("unexpected process_group attr setup result: cleanup=%v err=%v", cleanup != nil, err)
	}
	if attr == nil || attr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("expected CREATE_NEW_PROCESS_GROUP flag, got %#v", attr)
	}
	plain, cleanup, err := newSysProcAttr("none")
	if err != nil || cleanup != nil {
		t.Fatalf("unexpected plain attr setup result: cleanup=%v err=%v", cleanup != nil, err)
	}
	if plain == nil || plain.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP != 0 {
		t.Fatalf("expected no process group flag for none, got %#v", plain)
	}
}

func TestNewSysProcAttrUsesWindowsProcessGroupForJobIsolation(t *testing.T) {
	attr, cleanup, err := newSysProcAttr("windows_job")
	if err != nil || cleanup != nil {
		t.Fatalf("unexpected windows_job attr setup result: cleanup=%v err=%v", cleanup != nil, err)
	}
	if attr == nil || attr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("expected CREATE_NEW_PROCESS_GROUP flag for windows_job, got %#v", attr)
	}
}

func TestNewSysProcAttrUsesRestrictedTokenIsolation(t *testing.T) {
	attr, cleanup, err := newSysProcAttr("windows_restricted_token")
	if err != nil {
		t.Fatalf("create restricted-token attr: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected restricted-token cleanup")
	}
	defer cleanup()
	if attr == nil || attr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("expected process-group flag for restricted token, got %#v", attr)
	}
	if attr.Token == 0 {
		t.Fatalf("expected restricted primary token in attr, got %#v", attr)
	}
}

func TestWindowsRestrictedTokenBlocksMediumIntegrityWrite(t *testing.T) {
	target := filepath.Join(t.TempDir(), "blocked.txt")
	attr, cleanup, err := newSysProcAttr("windows_restricted_token")
	if err != nil {
		t.Fatalf("create restricted-token attr: %v", err)
	}
	defer cleanup()

	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsRestrictedTokenMaliciousChild$", "-test.v")
	cmd.Env = append(os.Environ(), windowsRestrictedTokenChild+"=1", "GOFLOW_WINDOWS_RESTRICTED_TOKEN_TARGET="+target)
	cmd.SysProcAttr = attr
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run restricted-token child: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "GOFLOW_WINDOWS_RESTRICTED_TOKEN_OK") {
		t.Fatalf("restricted-token child did not produce success evidence:\n%s", output)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("restricted-token child wrote a medium-integrity file: %v", err)
	}
}

func TestWindowsRestrictedTokenMaliciousChild(t *testing.T) {
	if os.Getenv(windowsRestrictedTokenChild) != "1" {
		return
	}
	target := os.Getenv("GOFLOW_WINDOWS_RESTRICTED_TOKEN_TARGET")
	if err := os.WriteFile(target, []byte("escaped\n"), 0o600); err == nil {
		t.Fatal("restricted-token process unexpectedly wrote a medium-integrity file")
	}
	t.Log("GOFLOW_WINDOWS_RESTRICTED_TOKEN_OK")
}
