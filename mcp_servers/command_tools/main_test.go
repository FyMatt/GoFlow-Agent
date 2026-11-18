package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandHelperProcess(t *testing.T) {
	if os.Getenv("GOFLOW_COMMAND_HELPER") != "1" {
		return
	}
	args := os.Args
	separator := -1
	for index, arg := range args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(args) {
		os.Exit(2)
	}
	switch args[separator+1] {
	case "success":
		fmt.Fprint(os.Stdout, "command output")
		fmt.Fprint(os.Stderr, "command warning")
	case "cwd":
		cwd, _ := os.Getwd()
		fmt.Fprint(os.Stdout, cwd)
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("x", inlineOutputLimit+1024))
	case "sleep":
		time.Sleep(10 * time.Second)
	case "spawn-child":
		if separator+2 >= len(args) {
			os.Exit(2)
		}
		child := exec.Command(os.Args[0], "-test.run=TestCommandHelperProcess", "--", "delayed-marker", args[separator+2])
		child.Env = append(os.Environ(), "GOFLOW_COMMAND_HELPER=1")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		_ = child.Wait()
	case "delayed-marker":
		if separator+2 >= len(args) {
			os.Exit(2)
		}
		time.Sleep(3 * time.Second)
		if err := os.WriteFile(args[separator+2], []byte("grandchild survived"), 0o600); err != nil {
			os.Exit(4)
		}
	case "exit":
		fmt.Fprint(os.Stderr, "failed")
		os.Exit(7)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestBuiltinToolsExposeStructuredRunCommand(t *testing.T) {
	tools := builtinTools()
	if len(tools) != 4 || tools[0].Name != "run_command" || tools[1].Name != "start_process" || tools[2].Name != "process_status" || tools[3].Name != "stop_process" || tools[0].Kind != "exec" {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	if !bytes.Contains(tools[0].InputSchema, []byte(`"program"`)) || !bytes.Contains(tools[0].InputSchema, []byte(`"reuse_if_unchanged"`)) || bytes.Contains(tools[0].InputSchema, []byte(`"shell"`)) {
		t.Fatalf("expected structured non-shell schema: %s", tools[0].InputSchema)
	}
}

func TestCombinedProgramNameExplainsArgumentError(t *testing.T) {
	root := t.TempDir()
	_, _, _, err := resolveCommandProgram("go test", root, root)
	if err == nil || !strings.Contains(err.Error(), "程序参数格式错误") || !strings.Contains(err.Error(), "args") || !strings.Contains(err.Error(), "未执行任何程序") {
		t.Fatalf("expected an argument correction instead of a missing Go diagnosis, got %v", err)
	}
}

func TestManagedProcessLifecycle(t *testing.T) {
	workspaceRoot = t.TempDir()
	program := configureHelperProgram(t)
	t.Cleanup(stopAllManagedProcesses)
	payload, err := startManagedProcess(managedProcessInput{
		Program:            program,
		Args:               []string{"-test.run=TestCommandHelperProcess", "--", "sleep"},
		EnvRefs:            []string{"GOFLOW_COMMAND_HELPER"},
		StartupWaitSeconds: 1,
	})
	if err != nil {
		t.Fatalf("start managed process: %v", err)
	}
	processID, _ := payload["process_id"].(string)
	if processID == "" || payload["running"] != true {
		t.Fatalf("expected running managed process, got %#v", payload)
	}
	status, err := managedProcessStatus(processID)
	if err != nil || status["running"] != true {
		t.Fatalf("managed process status: %#v err=%v", status, err)
	}
	stopped, err := stopManagedProcess(processID)
	if err != nil {
		t.Fatalf("stop managed process: %v", err)
	}
	if stopped["running"] != false || stopped["stop_requested"] != true {
		t.Fatalf("unexpected stopped process state: %#v", stopped)
	}
}

func TestRunCommandCapturesOutputAndCWD(t *testing.T) {
	root := t.TempDir()
	workspaceRoot = root
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	program := configureHelperProgram(t)
	payload, err := runCommand(context.Background(), commandInput{
		Program: program,
		Args:    []string{"-test.run=TestCommandHelperProcess", "--", "cwd"},
		CWD:     "nested",
		EnvRefs: []string{"GOFLOW_COMMAND_HELPER"},
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if payload["exit_code"] != 0 || payload["relative_cwd"] != "nested" {
		t.Fatalf("unexpected command metadata: %#v", payload)
	}
	if payload["classification"] == "" || payload["risk_level"] == "" {
		t.Fatalf("expected command classification metadata: %#v", payload)
	}
	if payload["workspace_fingerprint_before_hash"] == "" || payload["workspace_fingerprint_after_hash"] == "" {
		t.Fatalf("expected workspace fingerprints in command evidence: %#v", payload)
	}
	if strings.TrimSpace(payload["stdout"].(string)) != nested {
		t.Fatalf("expected helper cwd %q, got %q", nested, payload["stdout"])
	}
	if payload["stdout_artifact"] != "" {
		t.Fatalf("small output should not retain an artifact: %#v", payload)
	}
}

func TestRunCommandReturnsNonZeroExitWithoutProtocolError(t *testing.T) {
	workspaceRoot = t.TempDir()
	program := configureHelperProgram(t)
	payload, err := runCommand(context.Background(), commandInput{
		Program: program,
		Args:    []string{"-test.run=TestCommandHelperProcess", "--", "exit"},
		EnvRefs: []string{"GOFLOW_COMMAND_HELPER"},
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if payload["exit_code"] != 7 || !strings.Contains(payload["stderr"].(string), "failed") {
		t.Fatalf("expected exit metadata, got %#v", payload)
	}
	if !commandPayloadIsError(payload) {
		t.Fatalf("non-zero command exit must be exposed as a tool error: %#v", payload)
	}
}

func TestCommandPayloadErrorClassification(t *testing.T) {
	if commandPayloadIsError(map[string]any{"exit_code": 0, "timed_out": false, "cancelled": false}) {
		t.Fatal("successful command must not be marked as an error")
	}
	if !commandPayloadIsError(map[string]any{"exit_code": 0, "timed_out": true, "cancelled": false}) {
		t.Fatal("timed-out command must be marked as an error")
	}
	if !commandPayloadIsError(map[string]any{"exit_code": 0, "timed_out": false, "cancelled": true}) {
		t.Fatal("cancelled command must be marked as an error")
	}
}

func TestRunCommandRecommendsBuiltInFallbackWhenPytestIsMissing(t *testing.T) {
	if recommendation := commandFailureRecommendation(1, "python: No module named pytest"); !strings.Contains(recommendation, "do not retry") || !strings.Contains(recommendation, "unittest") {
		t.Fatalf("missing pytest should recommend a built-in fallback, got %q", recommendation)
	}
	if recommendation := commandFailureRecommendation(0, ""); recommendation != "" {
		t.Fatalf("successful command must not have a failure recommendation, got %q", recommendation)
	}
}

func TestRunCommandPersistsLargeOutputArtifact(t *testing.T) {
	workspaceRoot = t.TempDir()
	program := configureHelperProgram(t)
	payload, err := runCommand(context.Background(), commandInput{
		Program: program,
		Args:    []string{"-test.run=TestCommandHelperProcess", "--", "large"},
		EnvRefs: []string{"GOFLOW_COMMAND_HELPER"},
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if payload["stdout_truncated"] != true {
		t.Fatalf("expected inline truncation, got %#v", payload)
	}
	artifact, _ := payload["stdout_artifact"].(string)
	if artifact == "" {
		t.Fatalf("expected artifact path, got %#v", payload)
	}
	data, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(artifact)))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if len(data) != inlineOutputLimit+1024 {
		t.Fatalf("expected complete artifact, got %d bytes", len(data))
	}
	hash := sha256.Sum256(data)
	if payload["stdout_artifact_sha256"] != fmt.Sprintf("%x", hash) {
		t.Fatalf("expected artifact hash %x, got %#v", hash, payload["stdout_artifact_sha256"])
	}
	if payload["stdout_artifact_bytes"] != int64(len(data)) {
		t.Fatalf("expected artifact byte count %d, got %#v", len(data), payload["stdout_artifact_bytes"])
	}
	if payload["stdout_artifact_retention"] != "workspace:168h" || strings.TrimSpace(payload["stdout_artifact_expires_at"].(string)) == "" {
		t.Fatalf("expected artifact retention metadata, got %#v", payload)
	}
}

func TestRunCommandTimesOutAndTerminates(t *testing.T) {
	workspaceRoot = t.TempDir()
	program := configureHelperProgram(t)
	started := time.Now()
	payload, err := runCommand(context.Background(), commandInput{
		Program:        program,
		Args:           []string{"-test.run=TestCommandHelperProcess", "--", "sleep"},
		TimeoutSeconds: 1,
		EnvRefs:        []string{"GOFLOW_COMMAND_HELPER"},
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if payload["timed_out"] != true {
		t.Fatalf("expected timeout metadata, got %#v", payload)
	}
	if time.Since(started) > 6*time.Second {
		t.Fatalf("timeout did not terminate promptly")
	}
}

func TestRunCommandTimesOutAndTerminatesGrandchild(t *testing.T) {
	workspaceRoot = t.TempDir()
	program := configureHelperProgram(t)
	marker := filepath.Join(workspaceRoot, "grandchild-survived")
	payload, err := runCommand(context.Background(), commandInput{
		Program:        program,
		Args:           []string{"-test.run=TestCommandHelperProcess", "--", "spawn-child", marker},
		TimeoutSeconds: 1,
		EnvRefs:        []string{"GOFLOW_COMMAND_HELPER"},
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if payload["timed_out"] != true {
		t.Fatalf("expected timeout metadata, got %#v", payload)
	}
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("grandchild survived process-tree termination")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat grandchild marker: %v", err)
	}
}

func TestCommandArtifactRetentionCleansExpiredAndExcessFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".goflow", "artifacts", "commands")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir artifacts: %v", err)
	}
	old := filepath.Join(dir, "old-stdout.log")
	currentA := filepath.Join(dir, "current-a.log")
	currentB := filepath.Join(dir, "current-b.log")
	for _, path := range []string{old, currentA, currentB} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatalf("write artifact %s: %v", path, err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatalf("age artifact: %v", err)
	}
	t.Setenv("GOFLOW_COMMAND_ARTIFACT_RETENTION_HOURS", "24")
	t.Setenv("GOFLOW_COMMAND_ARTIFACT_MAX_FILES", "1")
	if err := cleanupCommandArtifacts(root, commandArtifactPolicy()); err != nil {
		t.Fatalf("cleanup artifacts: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expected expired artifact removed, err=%v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read artifacts: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one current artifact after cap, got %d", len(entries))
	}
}

func TestResolveWorkspaceDirectoryTruncatedAncestorUsesRoot(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "task-staging", "wf-security-audit-evidence-gate-1787423726321238600", "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	truncated := filepath.Join(root, "task-staging", "wf-security-audit-evidence-gate-178742372")
	got, err := resolveWorkspaceDirectory(truncated, workspace)
	if err != nil {
		t.Fatalf("truncated ancestor cwd should fall back to workspace: %v", err)
	}
	want, _ := filepath.Abs(workspace)
	if got != want {
		t.Fatalf("cwd = %q, want workspace %q", got, want)
	}
}

func TestRunCommandRejectsShellAndWorkspaceEscape(t *testing.T) {
	workspaceRoot = t.TempDir()
	if _, err := runCommand(context.Background(), commandInput{Program: "powershell", Args: []string{"-Command", "echo unsafe"}}); err == nil || !strings.Contains(err.Error(), "shell program") {
		t.Fatalf("expected shell rejection, got %v", err)
	}
	program := configureHelperProgram(t)
	if _, err := runCommand(context.Background(), commandInput{Program: program, CWD: ".."}); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("expected cwd escape rejection, got %v", err)
	}
}

func TestRunCommandAllowsExecutableInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	workspaceRoot = root
	source, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	relative := filepath.Join(".venv", "Scripts", filepath.Base(source))
	target := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLOW_COMMAND_HELPER", "1")
	payload, err := runCommand(context.Background(), commandInput{
		Program: relative,
		Args:    []string{"-test.run=TestCommandHelperProcess", "--", "success"},
		EnvRefs: []string{"GOFLOW_COMMAND_HELPER"},
	})
	if err != nil {
		t.Fatalf("workspace executable should run: %v", err)
	}
	if payload["exit_code"] != 0 || payload["executable_scope"] != "workspace" || !strings.Contains(payload["stdout"].(string), "command output") {
		t.Fatalf("unexpected workspace executable result: %#v", payload)
	}
}

func TestRunCommandRejectsExecutableOutsideWorkspace(t *testing.T) {
	workspaceRoot = t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-program")
	if err := os.WriteFile(outside, []byte("not executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(context.Background(), commandInput{Program: outside}); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("outside executable must be rejected: %v", err)
	}
}

func TestRunCommandRejectsGitRepositoryDiscoveredAboveWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspaceRoot = filepath.Join(parent, "task-staging", "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(context.Background(), commandInput{Program: "git", Args: []string{"diff", "--stat"}}); err == nil || !strings.Contains(err.Error(), "parent-repository discovery") {
		t.Fatalf("git must not escape to a parent repository: %v", err)
	}
}

func TestRunCommandRejectsSymlinkWorkspaceEscape(t *testing.T) {
	workspaceRoot = t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspaceRoot, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	program := configureHelperProgram(t)
	if _, err := runCommand(context.Background(), commandInput{Program: program, CWD: "outside-link"}); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func TestCommandEnvironmentUsesSafeDefaultsAndExplicitReferences(t *testing.T) {
	t.Setenv("GOFLOW_TEST_VISIBLE", "visible")
	t.Setenv("GOFLOW_TEST_SECRET_TOKEN", "hidden")
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	t.Setenv("GOTMPDIR", filepath.Join(t.TempDir(), "gotmp"))
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "gomodcache"))
	env, refs, err := commandEnvironment([]string{"GOFLOW_TEST_VISIBLE"})
	if err != nil {
		t.Fatalf("command environment: %v", err)
	}
	if len(refs) != 1 || refs[0] != "GOFLOW_TEST_VISIBLE" {
		t.Fatalf("expected normalized env refs, got %#v", refs)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GOFLOW_TEST_VISIBLE=visible") || strings.Contains(joined, "GOFLOW_TEST_SECRET_TOKEN") {
		t.Fatalf("unexpected command environment: %s", joined)
	}
	for _, name := range []string{"GOCACHE", "GOTMPDIR", "GOMODCACHE"} {
		if !strings.Contains(joined, name+"=") {
			t.Fatalf("expected %s to be passed to Go commands: %s", name, joined)
		}
	}
	if _, _, err := commandEnvironment([]string{"GOFLOW_TEST_SECRET_TOKEN"}); err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("expected sensitive env rejection, got %v", err)
	}
}

func TestServeListsCommandTool(t *testing.T) {
	requestLine := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"
	var out bytes.Buffer
	if err := serve(context.Background(), strings.NewReader(requestLine), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(out.String(), "run_command") || !strings.Contains(out.String(), "start_process") || !strings.Contains(out.String(), "process_status") || !strings.Contains(out.String(), "stop_process") {
		t.Fatalf("expected command and managed process tools in response: %s", out.String())
	}
}

func configureHelperProgram(t *testing.T) string {
	t.Helper()
	t.Setenv("GOFLOW_COMMAND_HELPER", "1")
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	path := filepath.Dir(binary) + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", path)
	return filepath.Base(binary)
}
