//go:build !windows

package mcp

import "testing"

func TestNewSysProcAttrUsesUnixProcessGroupIsolation(t *testing.T) {
	attr := newSysProcAttr("process_group")
	if attr == nil || !attr.Setpgid {
		t.Fatalf("expected Setpgid for process_group isolation, got %#v", attr)
	}
	plain := newSysProcAttr("none")
	if plain == nil || plain.Setpgid {
		t.Fatalf("expected no Setpgid for none, got %#v", plain)
	}
}
