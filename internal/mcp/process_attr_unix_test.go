//go:build !windows

package mcp

import "testing"

func TestNewSysProcAttrUsesUnixProcessGroupIsolation(t *testing.T) {
	attr, cleanup, err := newSysProcAttr("process_group")
	if err != nil || cleanup != nil {
		t.Fatalf("unexpected attr setup result: cleanup=%v err=%v", cleanup != nil, err)
	}
	if attr == nil || !attr.Setpgid {
		t.Fatalf("expected Setpgid for process_group isolation, got %#v", attr)
	}
	plain, cleanup, err := newSysProcAttr("none")
	if err != nil || cleanup != nil {
		t.Fatalf("unexpected plain attr setup result: cleanup=%v err=%v", cleanup != nil, err)
	}
	if plain == nil || plain.Setpgid {
		t.Fatalf("expected no Setpgid for none, got %#v", plain)
	}
}
