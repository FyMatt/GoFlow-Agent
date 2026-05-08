//go:build windows

package mcp

import (
	"os/exec"
	"testing"
	"time"
)

func TestWindowsJobIsolationCanAttachAndTerminateProcess(t *testing.T) {
	assertWindowsJobBackedIsolationCanTerminate(t, "windows_job")
}

func TestWindowsRestrictedTokenIsolationGetsJobLifecycleCleanup(t *testing.T) {
	assertWindowsJobBackedIsolationCanTerminate(t, "windows_restricted_token")
}

func assertWindowsJobBackedIsolationCanTerminate(t *testing.T, isolation string) {
	t.Helper()
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start process: %v", err)
	}
	handle, err := attachProcessIsolation(isolation, nil, cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("attach %s isolation: %v", isolation, err)
	}
	if handle == nil || handle.job == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("expected %s job handle, got %#v", isolation, handle)
	}
	if err := terminateProcessIsolation(handle, cmd); err != nil {
		_ = closeProcessIsolation(handle)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("terminate %s job: %v", isolation, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = closeProcessIsolation(handle)
		_ = cmd.Process.Kill()
		t.Fatal("expected process to exit after job termination")
	}
	if err := closeProcessIsolation(handle); err != nil {
		t.Fatalf("close job: %v", err)
	}
}
