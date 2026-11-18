//go:build windows

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/nativesandbox"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == nativeSandboxCLIFlag {
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "read native sandbox spec: %v\n", err)
			os.Exit(120)
		}
		spec, err := nativesandbox.DecodeWindowsSandboxLaunchSpec(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode native sandbox spec: %v\n", err)
			os.Exit(121)
		}
		exitCode, err := nativesandbox.RunWindowsSandboxLaunchSpec(context.Background(), spec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "run native sandbox spec: %v\n", err)
			os.Exit(122)
		}
		if exitCode != 0 {
			fmt.Fprintf(os.Stderr, "native sandbox child exited with code %d\n", exitCode)
		}
		os.Exit(exitCode)
	}
	os.Exit(m.Run())
}

func TestNativeStrongDiscoveryScopeUsesPrivateScratchRoot(t *testing.T) {
	runtimeHome := t.TempDir()
	client := NewClient(config.MCPServerRef{Name: "third-party", RuntimeHome: runtimeHome})
	scope, owned, err := client.nativeStrongTaskScope(os.Args[0])
	if err != nil {
		t.Fatalf("prepare native discovery scope: %v", err)
	}
	if !owned || !mcpPathWithin(filepath.Join(runtimeHome, "native-mcp-discovery"), scope.Root) {
		t.Fatalf("unexpected native discovery scope: owned=%v scope=%#v", owned, scope)
	}
	for _, path := range []string{scope.Root, scope.Input, scope.Workspace} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("native discovery directory is unavailable: %s info=%v err=%v", path, info, err)
		}
	}
	if err := os.RemoveAll(scope.Root); err != nil {
		t.Fatal(err)
	}
}

func TestValidateNativeStrongArgumentsRejectsOutsideAbsolutePath(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	workspace := filepath.Join(root, "workspace")
	tools := filepath.Join(root, "tools")
	for _, path := range []string{input, workspace, tools} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scope := schema.TaskWorkspaceScope{Root: root, Input: input, Workspace: workspace}
	if err := validateNativeStrongArguments([]string{"--config=" + filepath.Join(input, "server.json")}, scope, tools); err != nil {
		t.Fatalf("task input path should be accepted: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "server.json")
	if err := validateNativeStrongArguments([]string{"--config=" + outside}, scope, tools); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("outside argument path should be rejected, got %v", err)
	}
}

func TestBuildNativeStrongServerCommandCleansPreparationState(t *testing.T) {
	runtimeHome := t.TempDir()
	client := NewClient(config.MCPServerRef{
		Name:        "third-party",
		Command:     os.Args[0],
		Isolation:   "native_strong",
		RuntimeHome: runtimeHome,
	})
	cmd, cleanup, err := client.buildNativeStrongServerCommand(os.Args[0], []string{"-test.run=TestMCPManagerHelperProcess"})
	if err == nil {
		if cmd == nil || cleanup == nil || len(cmd.Args) != 3 || cmd.Args[1] != nativeSandboxCLIFlag {
			t.Fatalf("unexpected native strong launcher command: cmd=%#v cleanup=%v", cmd, cleanup != nil)
		}
		if err := cleanup(); err != nil {
			t.Fatalf("cleanup native strong command: %v", err)
		}
		if err := cleanup(); err != nil {
			t.Fatalf("native strong cleanup should be idempotent: %v", err)
		}
	}
	discoveryRoot := filepath.Join(runtimeHome, "native-mcp-discovery")
	entries, readErr := os.ReadDir(discoveryRoot)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read native discovery root after cleanup: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("native strong preparation leaked task roots after err=%v: %#v", err, entries)
	}
}

func TestWindowsNativeStrongPythonMCPProtocolAndBoundary(t *testing.T) {
	if !nativesandbox.Probe().StrongReady {
		t.Skip("Windows native strong sandbox is unavailable on this host")
	}
	python, err := resolveNativeStrongCommand("python")
	if err != nil {
		t.Skipf("Python runtime is unavailable: %v", err)
	}
	if !windowsTrustedNativeRuntimeRoot(filepath.Dir(python)) {
		t.Skipf("Python runtime %s is outside a system-protected installation root", filepath.Dir(python))
	}

	root := t.TempDir()
	input := filepath.Join(root, "input")
	dependencies := filepath.Join(root, "dependencies")
	workspace := filepath.Join(root, "workspace")
	for _, path := range []string{input, dependencies, workspace} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	immutable := filepath.Join(input, "immutable.txt")
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(immutable, []byte("immutable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "python-mcp")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "helper.py"), []byte("VALUE = 'module-ok'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverPath := filepath.Join(source, "server.py")
	if err := os.WriteFile(serverPath, []byte(windowsNativeStrongPythonMCPFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOFLOW_TEST_MCP_IMMUTABLE", immutable)
	t.Setenv("GOFLOW_TEST_MCP_OUTSIDE", outside)
	client := NewClient(config.MCPServerRef{
		Name:               "python-native-strong-fixture",
		Command:            python,
		Args:               []string{serverPath},
		Enabled:            true,
		Timeout:            15 * time.Second,
		EnvAllowlist:       []string{"GOFLOW_TEST_MCP_IMMUTABLE", "GOFLOW_TEST_MCP_OUTSIDE"},
		Isolation:          "native_strong",
		MaxConcurrentCalls: 1,
		MaxRequestBytes:    64 << 10,
		MaxResponseBytes:   1 << 20,
		RuntimeHome:        root,
	})
	scoped := client.cloneForTaskWorkspace(schema.TaskWorkspaceScope{
		RunID:        "windows-python-mcp",
		Root:         root,
		Input:        input,
		Dependencies: dependencies,
		Workspace:    workspace,
	})
	defer scoped.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tools, err := scoped.ListTools(ctx)
	if err != nil {
		t.Fatalf("list native-strong Python MCP tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "boundary_status" {
		t.Fatalf("unexpected Python MCP tools: %#v", tools)
	}
	arguments, _ := json.Marshal(map[string]any{})
	result, err := scoped.CallTool(ctx, "boundary_status", arguments)
	if err != nil {
		t.Fatalf("call native-strong Python MCP tool: %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "module-ok") || !strings.Contains(result.Content, "boundary-ok") {
		t.Fatalf("unexpected Python MCP boundary result: %#v", result)
	}
	if content, err := os.ReadFile(filepath.Join(workspace, "inside.txt")); err != nil || strings.TrimSpace(string(content)) != "inside-ok" {
		t.Fatalf("Python MCP did not write its task output: content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(immutable); err != nil || string(content) != "immutable\n" {
		t.Fatalf("Python MCP changed its read-only input: content=%q err=%v", content, err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("Python MCP escaped the task workspace: %v", err)
	}
}

func TestWindowsNativeStrongRejectsUntrustedPythonRuntime(t *testing.T) {
	root := t.TempDir()
	python := filepath.Join(root, "python.exe")
	if err := os.WriteFile(python, []byte("not an executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(config.MCPServerRef{Name: "untrusted-python", RuntimeHome: t.TempDir()})
	if _, _, err := client.stageWindowsNativeStrongCommand(python, t.TempDir()); err == nil || !strings.Contains(err.Error(), "system-protected installation root") {
		t.Fatalf("expected untrusted Python runtime to be rejected, got %v", err)
	}
}

func nativeSandboxTestHost(t *testing.T) {
	t.Helper()
	if os.Getenv("GOFLOW_NATIVE_SANDBOX_TESTS") == "1" {
		return
	}
	t.Skip("set GOFLOW_NATIVE_SANDBOX_TESTS=1 on a Windows host with AppContainer support to run this test")
}

func TestWindowsNativeStrongNodeMCPProtocolAndBoundary(t *testing.T) {
	nativeSandboxTestHost(t)
	if !nativesandbox.Probe().StrongReady {
		t.Skip("Windows native strong sandbox is unavailable on this host")
	}
	node, err := resolveNativeStrongCommand("node")
	if err != nil {
		t.Skipf("Node runtime is unavailable: %v", err)
	}

	root := t.TempDir()
	input := filepath.Join(root, "input")
	dependencies := filepath.Join(root, "dependencies")
	workspace := filepath.Join(root, "workspace")
	for _, path := range []string{input, dependencies, workspace} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	immutable := filepath.Join(input, "immutable.txt")
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(immutable, []byte("immutable\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "node-mcp")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "helper.cjs"), []byte("exports.VALUE = 'module-ok';\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverPath := filepath.Join(source, "server.cjs")
	if err := os.WriteFile(serverPath, []byte(windowsNativeStrongNodeMCPFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOFLOW_TEST_MCP_IMMUTABLE", immutable)
	t.Setenv("GOFLOW_TEST_MCP_OUTSIDE", outside)
	client := NewClient(config.MCPServerRef{
		Name:               "node-native-strong-fixture",
		Command:            node,
		Args:               []string{serverPath},
		Enabled:            true,
		Timeout:            15 * time.Second,
		EnvAllowlist:       []string{"GOFLOW_TEST_MCP_IMMUTABLE", "GOFLOW_TEST_MCP_OUTSIDE"},
		Isolation:          "native_strong",
		MaxConcurrentCalls: 1,
		MaxRequestBytes:    64 << 10,
		MaxResponseBytes:   1 << 20,
		RuntimeHome:        root,
	})
	scoped := client.cloneForTaskWorkspace(schema.TaskWorkspaceScope{
		RunID:        "windows-node-mcp",
		Root:         root,
		Input:        input,
		Dependencies: dependencies,
		Workspace:    workspace,
	})
	defer scoped.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tools, err := scoped.ListTools(ctx)
	if err != nil {
		t.Fatalf("list native-strong Node MCP tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "boundary_status" {
		t.Fatalf("unexpected Node MCP tools: %#v", tools)
	}
	arguments, _ := json.Marshal(map[string]any{})
	result, err := scoped.CallTool(ctx, "boundary_status", arguments)
	if err != nil {
		t.Fatalf("call native-strong Node MCP tool: %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "module-ok") || !strings.Contains(result.Content, "boundary-ok") {
		t.Fatalf("unexpected Node MCP boundary result: %#v", result)
	}
	if content, err := os.ReadFile(filepath.Join(workspace, "inside.txt")); err != nil || strings.TrimSpace(string(content)) != "inside-ok" {
		t.Fatalf("Node MCP did not write its task output: content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(immutable); err != nil || string(content) != "immutable\n" {
		t.Fatalf("Node MCP changed its read-only input: content=%q err=%v", content, err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("Node MCP escaped the task workspace: %v", err)
	}
}

const windowsNativeStrongPythonMCPFixture = `import json
import os
import socket
import sys

from helper import VALUE

workspace = os.environ["GOFLOW_WORKSPACE_ROOT"]
immutable = os.environ["GOFLOW_TEST_MCP_IMMUTABLE"]
outside = os.environ["GOFLOW_TEST_MCP_OUTSIDE"]
checks = []

with open(os.path.join(workspace, "inside.txt"), "w", encoding="utf-8") as stream:
    stream.write("inside-ok\n")
with open(immutable, "r", encoding="utf-8") as stream:
    checks.append(stream.read() == "immutable\n")
try:
    with open(immutable, "w", encoding="utf-8") as stream:
        stream.write("tampered\n")
    checks.append(False)
except OSError:
    checks.append(True)
try:
    with open(outside, "w", encoding="utf-8") as stream:
        stream.write("escaped\n")
    checks.append(False)
except OSError:
    checks.append(True)
try:
    socket.create_connection(("192.0.2.1", 9), timeout=0.4).close()
    checks.append(False)
except OSError:
    checks.append(True)

status = "boundary-ok" if all(checks) else "boundary-failed"
for line in sys.stdin:
    request = json.loads(line)
    method = request.get("method")
    if method == "tools/list":
        result = {"tools": [{
            "name": "boundary_status",
            "description": "Return native sandbox fixture status.",
            "kind": "read",
            "input_schema": {"type": "object", "properties": {}, "additionalProperties": False}
        }]}
    elif method == "tools/call":
        result = {"content": VALUE + ":" + status, "is_error": status != "boundary-ok"}
    else:
        result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request.get("id"), "result": result}), flush=True)
`

const windowsNativeStrongNodeMCPFixture = `const fs = require("fs");
const net = require("net");
const path = require("path");
const readline = require("readline");
const { VALUE } = require("./helper.cjs");

async function networkIsDenied() {
  return await new Promise((resolve) => {
    const socket = net.createConnection({ host: "192.0.2.1", port: 9 });
    const finish = (denied) => { socket.destroy(); resolve(denied); };
    socket.setTimeout(400, () => finish(true));
    socket.once("error", () => finish(true));
    socket.once("connect", () => finish(false));
  });
}

async function main() {
  const workspace = process.env.GOFLOW_WORKSPACE_ROOT;
  const immutable = process.env.GOFLOW_TEST_MCP_IMMUTABLE;
  const outside = process.env.GOFLOW_TEST_MCP_OUTSIDE;
  const checks = [];
  fs.writeFileSync(path.join(workspace, "inside.txt"), "inside-ok\n", "utf8");
  checks.push(fs.readFileSync(immutable, "utf8") === "immutable\n");
  try { fs.writeFileSync(immutable, "tampered\n", "utf8"); checks.push(false); } catch { checks.push(true); }
  try { fs.writeFileSync(outside, "escaped\n", "utf8"); checks.push(false); } catch { checks.push(true); }
  checks.push(await networkIsDenied());
  const status = checks.every(Boolean) ? "boundary-ok" : "boundary-failed";
  const input = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
  input.on("line", (line) => {
    const request = JSON.parse(line);
    let result;
    if (request.method === "tools/list") {
      result = { tools: [{
        name: "boundary_status",
        description: "Return native sandbox fixture status.",
        kind: "read",
        input_schema: { type: "object", properties: {}, additionalProperties: false }
      }] };
    } else if (request.method === "tools/call") {
      result = { content: VALUE + ":" + status, is_error: status !== "boundary-ok" };
    } else {
      result = {};
    }
    process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: request.id, result }) + "\n");
  });
}

main().catch((error) => { console.error(error); process.exit(1); });
`
