package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalGoRunSpecOnlyMatchesBuiltInMCPPackages(t *testing.T) {
	packagePath, runtimeArgs, ok := localGoRunSpec("go.exe", []string{"run", "./mcp_servers/skill_runner", "--skills-dir", "./skills"})
	if !ok || packagePath != "./mcp_servers/skill_runner" {
		t.Fatalf("unexpected built-in MCP spec: ok=%v package=%q", ok, packagePath)
	}
	if len(runtimeArgs) != 2 || runtimeArgs[0] != "--skills-dir" || runtimeArgs[1] != "./skills" {
		t.Fatalf("unexpected runtime args: %#v", runtimeArgs)
	}
	absolute := filepath.Join(t.TempDir(), "mcp_servers", "web_tools")
	if got, _, ok := localGoRunSpec("go", []string{"run", absolute}); !ok || got != absolute {
		t.Fatalf("expected absolute built-in MCP package to match, got ok=%v path=%q", ok, got)
	}
	for _, test := range []struct {
		command string
		args    []string
	}{
		{command: "python", args: []string{"server.py"}},
		{command: "go", args: []string{"test", "./..."}},
		{command: "go", args: []string{"run", "./cmd/custom"}},
		{command: "go", args: []string{"run", "./mcp_servers/../custom"}},
		{command: "go", args: []string{"run", "./mcp_servers/custom/main.go"}},
	} {
		if _, _, ok := localGoRunSpec(test.command, test.args); ok {
			t.Fatalf("unexpected cache rewrite for %q %#v", test.command, test.args)
		}
	}
}

func TestResolveLocalMCPPackageFromAbsoluteLoaderPath(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "mcp_servers", "web_tools")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.invalid/demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, resolvedPackage, buildPackage, err := resolveLocalMCPPackage(filepath.Join(root, "configs"), packageDir)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedRoot != root || resolvedPackage != packageDir || buildPackage != "./mcp_servers/web_tools" {
		t.Fatalf("unexpected resolution: root=%q package=%q build=%q", resolvedRoot, resolvedPackage, buildPackage)
	}
}

func TestLocalGoPackageFingerprintChangesWithSource(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "mcp_servers", "demo")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.invalid/demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(packageDir, "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := localGoPackageFingerprint(root, packageDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package main\nfunc main() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := localGoPackageFingerprint(root, packageDir)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected source change to invalidate cached MCP binary")
	}
}

func TestLocalMCPBinaryCacheDirUsesAbsoluteOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "mcp-cache")
	t.Setenv("GOFLOW_MCP_BIN_CACHE_DIR", override)
	got, err := localMCPBinaryCacheDir(t.TempDir())
	if err != nil || got != filepath.Clean(override) {
		t.Fatalf("cache dir=%q err=%v, want %q", got, err, filepath.Clean(override))
	}
	t.Setenv("GOFLOW_MCP_BIN_CACHE_DIR", "relative-cache")
	if _, err := localMCPBinaryCacheDir(t.TempDir()); err == nil {
		t.Fatal("relative cache override must be rejected")
	}
}
