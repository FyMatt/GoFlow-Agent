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
	for _, name := range []string{".", "..", "../escape", "bad/name", "bad name"} {
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
	assertFileContent(t, filepath.Join(dir, "memory.max"), "268435456")
	assertFileContent(t, filepath.Join(dir, "pids.max"), "64")
	assertFileContent(t, filepath.Join(dir, "cpu.max"), "50000 100000")
}

func TestNormalizeLinuxCgroupMemoryMax(t *testing.T) {
	cases := map[string]string{
		"1":    "1",
		"256M": "268435456",
		"1G":   "1073741824",
		"2k":   "2048",
		"max":  "max",
		"MAX":  "max",
	}
	for input, want := range cases {
		got, err := normalizeLinuxCgroupMemoryMax(input)
		if err != nil {
			t.Fatalf("normalizeLinuxCgroupMemoryMax(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeLinuxCgroupMemoryMax(%q): expected %q, got %q", input, want, got)
		}
	}
}

func TestNormalizeLinuxCgroupMemoryMaxRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{"", "0", "-1", "bad", "1MB", "9223372036854775807T"} {
		if got, err := normalizeLinuxCgroupMemoryMax(input); err == nil {
			t.Fatalf("expected normalizeLinuxCgroupMemoryMax(%q) to fail, got %q", input, got)
		}
	}
}

func TestNormalizeLinuxCgroupMaxOrPositiveInteger(t *testing.T) {
	cases := map[string]string{
		"1":   "1",
		"64":  "64",
		"max": "max",
		"MAX": "max",
	}
	for input, want := range cases {
		got, err := normalizeLinuxCgroupMaxOrPositiveInteger(input)
		if err != nil {
			t.Fatalf("normalizeLinuxCgroupMaxOrPositiveInteger(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeLinuxCgroupMaxOrPositiveInteger(%q): expected %q, got %q", input, want, got)
		}
	}
}

func TestNormalizeLinuxCgroupCPUMax(t *testing.T) {
	cases := map[string]string{
		"max":           "max",
		"MAX":           "max",
		"50000 100000":  "50000 100000",
		"max 100000":    "max 100000",
		" 50000 100000": "50000 100000",
	}
	for input, want := range cases {
		got, err := normalizeLinuxCgroupCPUMax(input)
		if err != nil {
			t.Fatalf("normalizeLinuxCgroupCPUMax(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeLinuxCgroupCPUMax(%q): expected %q, got %q", input, want, got)
		}
	}
}

func TestNormalizeLinuxCgroupCPUMaxRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{"", "50000", "0 100000", "50000 max", "50000 0", "bad 100000"} {
		if got, err := normalizeLinuxCgroupCPUMax(input); err == nil {
			t.Fatalf("expected normalizeLinuxCgroupCPUMax(%q) to fail, got %q", input, got)
		}
	}
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
