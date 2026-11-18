//go:build linux

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

const linuxNativeMCPAcceptanceRequired = "GOFLOW_REQUIRE_LINUX_NATIVE_SANDBOX"

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == nativeSandboxCLIFlag {
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "read native sandbox spec: %v\n", err)
			os.Exit(120)
		}
		spec, err := nativesandbox.DecodeLinuxSandboxProcessSpec(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode native sandbox spec: %v\n", err)
			os.Exit(121)
		}
		exitCode, err := nativesandbox.RunLinuxSandboxLaunchSpec(context.Background(), spec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "run native sandbox spec: %v\n", err)
			os.Exit(122)
		}
		os.Exit(exitCode)
	}
	os.Exit(m.Run())
}

func TestLinuxNativeStrongPythonMCPProtocolAndBoundary(t *testing.T) {
	requireLinuxNativeMCPAcceptance(t)
	python, err := resolveNativeStrongCommand("python3")
	if err != nil {
		t.Fatalf("Linux acceptance host must provide Python 3: %v", err)
	}
	runLinuxNativeStrongMCPFixture(t, "python", python, "server.py", "helper.py", "VALUE = 'module-ok'\n", linuxNativeStrongPythonMCPFixture)
}

func TestLinuxNativeStrongNodeMCPProtocolAndBoundary(t *testing.T) {
	requireLinuxNativeMCPAcceptance(t)
	node, err := resolveNativeStrongCommand("node")
	if err != nil {
		t.Fatalf("Linux acceptance host must provide Node.js: %v", err)
	}
	runLinuxNativeStrongMCPFixture(t, "node", node, "server.cjs", "helper.cjs", "exports.VALUE = 'module-ok';\n", linuxNativeStrongNodeMCPFixture)
}

func requireLinuxNativeMCPAcceptance(t *testing.T) {
	t.Helper()
	if os.Getenv(linuxNativeMCPAcceptanceRequired) != "1" {
		t.Skip("set GOFLOW_REQUIRE_LINUX_NATIVE_SANDBOX=1 on a Linux x86_64 acceptance host")
	}
	report := nativesandbox.Probe()
	if !report.StrongReady {
		t.Fatalf("Linux native strong sandbox capability gate failed: %s", report.RefusalReason)
	}
}

func runLinuxNativeStrongMCPFixture(t *testing.T, runtimeName, command, serverName, helperName, helperSource, serverSource string) {
	t.Helper()
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
	source := filepath.Join(t.TempDir(), runtimeName+"-mcp")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, helperName), []byte(helperSource), 0o600); err != nil {
		t.Fatal(err)
	}
	serverPath := filepath.Join(source, serverName)
	if err := os.WriteFile(serverPath, []byte(serverSource), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOFLOW_TEST_MCP_IMMUTABLE", immutable)
	t.Setenv("GOFLOW_TEST_MCP_OUTSIDE", outside)
	client := NewClient(config.MCPServerRef{
		Name:    runtimeName + "-native-strong-fixture",
		Command: command,
		Args:    []string{serverPath},
		Enabled: true,
		Timeout: 15 * time.Second,
		// Native runtimes installed outside the system prefix (for example a
		// user-provided Node.js bundle) may need an explicit dynamic-library
		// search path. Keep it allowlisted instead of inheriting the host env.
		EnvAllowlist:       []string{"GOFLOW_TEST_MCP_IMMUTABLE", "GOFLOW_TEST_MCP_OUTSIDE", "LD_LIBRARY_PATH"},
		Isolation:          "native_strong",
		MaxConcurrentCalls: 1,
		MaxRequestBytes:    64 << 10,
		MaxResponseBytes:   1 << 20,
		RuntimeHome:        root,
	})
	scoped := client.cloneForTaskWorkspace(schema.TaskWorkspaceScope{
		RunID:        "linux-" + runtimeName + "-mcp",
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
		t.Fatalf("list native-strong %s MCP tools: %v", runtimeName, err)
	}
	if len(tools) != 1 || tools[0].Name != "boundary_status" {
		t.Fatalf("unexpected %s MCP tools: %#v", runtimeName, tools)
	}
	arguments, _ := json.Marshal(map[string]any{})
	result, err := scoped.CallTool(ctx, "boundary_status", arguments)
	if err != nil {
		t.Fatalf("call native-strong %s MCP tool: %v", runtimeName, err)
	}
	if result.IsError || !strings.Contains(result.Content, "module-ok") || !strings.Contains(result.Content, "boundary-ok") {
		t.Fatalf("unexpected %s MCP boundary result: %#v", runtimeName, result)
	}
	if content, err := os.ReadFile(filepath.Join(workspace, "inside.txt")); err != nil || strings.TrimSpace(string(content)) != "inside-ok" {
		t.Fatalf("%s MCP did not write its task output: content=%q err=%v", runtimeName, content, err)
	}
	if content, err := os.ReadFile(immutable); err != nil || string(content) != "immutable\n" {
		t.Fatalf("%s MCP changed its read-only input: content=%q err=%v", runtimeName, content, err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("%s MCP escaped the task workspace: %v", runtimeName, err)
	}
}

const linuxNativeStrongPythonMCPFixture = `import json
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

const linuxNativeStrongNodeMCPFixture = `const fs = require("fs");
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
