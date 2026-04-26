//go:build linux

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxCgroupPathDefaults(t *testing.T) {
	path, err := linuxCgroupPath(nil, 1234)
	if err != nil {
		t.Fatalf("linuxCgroupPath: %v", err)
	}
	want := filepath.Join(defaultLinuxCgroupParent, "mcp-1234")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestLinuxCgroupPathUsesExplicitParentAndName(t *testing.T) {
	parent := t.TempDir()
	path, err := linuxCgroupPath(map[string]string{
		"cgroup_parent": parent,
		"cgroup_name":   "web-tools_1.2",
	}, 1234)
	if err != nil {
		t.Fatalf("linuxCgroupPath: %v", err)
	}
	want := filepath.Join(parent, "web-tools_1.2")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestLinuxCgroupPathRejectsRelativeParent(t *testing.T) {
	_, err := linuxCgroupPath(map[string]string{
		"cgroup_parent": "relative/cgroup",
		"cgroup_name":   "helper",
	}, 1234)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute parent error, got %v", err)
	}
}

func TestLinuxCgroupPathRejectsUnsafeName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "bad/name", "bad name"} {
		_, err := linuxCgroupPath(map[string]string{
			"cgroup_parent": t.TempDir(),
			"cgroup_name":   name,
		}, 1234)
		if err == nil {
			t.Fatalf("expected unsafe name %q to fail", name)
		}
	}
}

func TestIsSafeCgroupName(t *testing.T) {
	for _, name := range []string{"mcp-1", "python_notes", "web.tools"} {
		if !isSafeCgroupName(name) {
			t.Fatalf("expected %q to be safe", name)
		}
	}
	for _, name := range []string{"", ".", "..", "name/path", "bad name", "name:1"} {
		if isSafeCgroupName(name) {
			t.Fatalf("expected %q to be unsafe", name)
		}
	}
}

func TestWriteLinuxCgroupLimits(t *testing.T) {
	dir := t.TempDir()
	err := writeLinuxCgroupLimits(dir, map[string]string{
		"memory_max": "256M",
		"pids_max":   "64",
		"cpu_max":    "50000 100000",
	})
	if err != nil {
		t.Fatalf("writeLinuxCgroupLimits: %v", err)
	}
	assertFileContent(t, filepath.Join(dir, "memory.max"), "256M")
	assertFileContent(t, filepath.Join(dir, "pids.max"), "64")
	assertFileContent(t, filepath.Join(dir, "cpu.max"), "50000 100000")
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("expected %s to contain %q, got %q", path, want, string(data))
	}
}
