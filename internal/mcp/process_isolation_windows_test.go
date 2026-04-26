//go:build windows

package mcp

import (
	"os/exec"
	"testing"
	"time"
)

func TestWindowsJobIsolationCanAttachAndTerminateProcess(t *testing.T) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start process: %v", err)
	}
	handle, err := attachProcessIsolation("windows_job", nil, cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("attach windows job isolation: %v", err)
	}
	if handle == nil || handle.job == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("expected job handle, got %#v", handle)
	}
	if err := terminateProcessIsolation(handle, cmd); err != nil {
		_ = closeProcessIsolation(handle)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("terminate job: %v", err)
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
