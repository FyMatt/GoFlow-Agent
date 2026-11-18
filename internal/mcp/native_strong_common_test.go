package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestStageNativeStrongArgumentsUsesContentAddressedReadOnlyCache(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "source")
	dependencies := filepath.Join(root, "dependencies")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dependencies, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(workDir, "helper.py")
	if err := os.WriteFile(source, []byte("print('cached')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(config.MCPServerRef{Name: "third-party", RuntimeHome: filepath.Join(root, "runtime"), WorkDir: workDir})
	scope := schema.TaskWorkspaceScope{Root: root, Dependencies: dependencies, Workspace: filepath.Join(root, "workspace")}
	args, err := client.stageNativeStrongArguments([]string{"--script=" + source}, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] == "--script="+source {
		t.Fatalf("expected script argument to be rewritten into dependency cache: %#v", args)
	}
	materialized := args[0][len("--script="):]
	if !mcpPathWithin(dependencies, materialized) {
		t.Fatalf("materialized dependency escaped task directory: %s", materialized)
	}
	content, err := os.ReadFile(materialized)
	if err != nil || string(content) != "print('cached')\n" {
		t.Fatalf("unexpected materialized dependency: %q err=%v", content, err)
	}
	if info, err := os.Stat(materialized); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("materialized dependency must be read-only: info=%#v err=%v", info, err)
	}
	if err := validateNativeStrongArguments(args, scope, filepath.Join(root, "tools")); err != nil {
		t.Fatalf("cached dependency argument should be allowed: %v", err)
	}
}

func TestStageNativeStrongArgumentsPreservesScriptImportsAndSkipsSecrets(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "source", "server")
	dependencies := filepath.Join(root, "dependencies")
	workspace := filepath.Join(root, "workspace")
	for _, path := range []string{packageDir, dependencies, workspace} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	entrypoint := filepath.Join(packageDir, "server.py")
	for path, content := range map[string]string{
		entrypoint:                              "from helpers import message\nprint(message)\n",
		filepath.Join(packageDir, "helpers.py"): "message = 'ready'\n",
		filepath.Join(packageDir, ".env"):       "SECRET=must-not-be-staged\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	client := NewClient(config.MCPServerRef{Name: "script-server", RuntimeHome: filepath.Join(root, "runtime"), WorkDir: packageDir})
	scope := schema.TaskWorkspaceScope{Root: root, Dependencies: dependencies, Workspace: workspace}
	args, err := client.stageNativeStrongArguments([]string{entrypoint}, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] == entrypoint || !mcpPathWithin(dependencies, args[0]) {
		t.Fatalf("script entrypoint was not staged inside task dependencies: %#v", args)
	}
	helper := filepath.Join(filepath.Dir(args[0]), "helpers.py")
	if content, err := os.ReadFile(helper); err != nil || string(content) != "message = 'ready'\n" {
		t.Fatalf("local script import was not preserved: content=%q err=%v", content, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(args[0]), ".env")); !os.IsNotExist(err) {
		t.Fatalf("sensitive script sidecar must not enter the dependency bundle: %v", err)
	}
	if info, err := os.Stat(args[0]); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("staged script must be read-only: info=%v err=%v", info, err)
	}
}
