//go:build windows

package nativesandbox

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestNormalizeWindowsTaskBoundarySpec(t *testing.T) {
	root := t.TempDir()
	readOnly := filepath.Join(root, "input")
	writable := filepath.Join(root, "workspace")
	if err := os.MkdirAll(readOnly, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(writable, 0o755); err != nil {
		t.Fatal(err)
	}
	spec, err := normalizeWindowsTaskBoundarySpec(WindowsTaskBoundarySpec{
		TaskID:        "task-boundary",
		ReadOnlyRoots: []string{readOnly},
		WritableRoot:  writable,
	})
	if err != nil {
		t.Fatalf("normalize task boundary: %v", err)
	}
	if spec.WritableRoot != writable || len(spec.ReadOnlyRoots) != 1 || spec.ReadOnlyRoots[0] != readOnly {
		t.Fatalf("unexpected normalized task boundary: %#v", spec)
	}
}

func TestNormalizeWindowsTaskBoundarySpecRejectsOverlap(t *testing.T) {
	root := t.TempDir()
	writable := filepath.Join(root, "workspace")
	if err := os.MkdirAll(writable, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeWindowsTaskBoundarySpec(WindowsTaskBoundarySpec{
		TaskID:        "task-overlap",
		ReadOnlyRoots: []string{root},
		WritableRoot:  writable,
	}); err == nil {
		t.Fatal("expected overlapping task paths to be rejected")
	}
}

func TestWindowsTaskACLBoundaryRestoresEveryPath(t *testing.T) {
	requireNativeSandboxHost(t)
	root := t.TempDir()
	input := filepath.Join(root, "input")
	writable := filepath.Join(root, "workspace")
	for _, path := range []string{input, writable} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sid, err := windows.StringToSid("S-1-15-2-1")
	if err != nil {
		t.Fatal(err)
	}
	beforeInput := directorySecurityDescriptorString(t, input)
	beforeWritable := directorySecurityDescriptorString(t, writable)
	boundary, err := prepareWindowsTaskACLBoundary(sid, WindowsTaskBoundarySpec{
		TaskID:        "task-acl",
		ReadOnlyRoots: []string{input},
		WritableRoot:  writable,
	})
	if err != nil {
		t.Fatalf("prepare task ACL boundary: %v", err)
	}
	if !directoryACLContainsSID(t, input, sid) || !directoryACLContainsSID(t, writable, sid) {
		t.Fatal("task SID is missing from one or more task paths")
	}
	if err := boundary.Close(); err != nil {
		t.Fatalf("close task ACL boundary: %v", err)
	}
	if got := directorySecurityDescriptorString(t, input); got != beforeInput {
		t.Fatalf("input ACL was not restored\nbefore: %s\nafter:  %s", beforeInput, got)
	}
	if got := directorySecurityDescriptorString(t, writable); got != beforeWritable {
		t.Fatalf("writable ACL was not restored\nbefore: %s\nafter:  %s", beforeWritable, got)
	}
	if err := boundary.Close(); err != nil {
		t.Fatalf("second boundary close should be idempotent: %v", err)
	}
}
