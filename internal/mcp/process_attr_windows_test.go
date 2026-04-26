//go:build windows

package mcp

import (
	"syscall"
	"testing"
)

func TestNewSysProcAttrUsesWindowsProcessGroupIsolation(t *testing.T) {
	attr := newSysProcAttr("process_group")
	if attr == nil || attr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("expected CREATE_NEW_PROCESS_GROUP flag, got %#v", attr)
	}
	plain := newSysProcAttr("none")
	if plain == nil || plain.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP != 0 {
		t.Fatalf("expected no process group flag for none, got %#v", plain)
	}
}

func TestNewSysProcAttrUsesWindowsProcessGroupForJobIsolation(t *testing.T) {
	attr := newSysProcAttr("windows_job")
	if attr == nil || attr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("expected CREATE_NEW_PROCESS_GROUP flag for windows_job, got %#v", attr)
	}
}
