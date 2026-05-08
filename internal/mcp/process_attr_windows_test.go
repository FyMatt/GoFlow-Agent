//go:build windows

package mcp

import (
	"syscall"
	"testing"
)

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
