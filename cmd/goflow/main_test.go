package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	apppkg "github.com/FyMatt/GoFlow-Agent/internal/app"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	skillpkg "github.com/FyMatt/GoFlow-Agent/internal/skill"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

func TestResolvePathsUsesHTTPFlag(t *testing.T) {
	runtimeHome := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	configPath, resolvedWorkspace, httpAddr, err := resolvePaths(runtimeHome, []string{"--workspace", workspaceRoot, "--http", ":8080"})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	wantConfigPath := filepath.Join(runtimeHome, "configs", "goflow.yaml")
	if configPath != wantConfigPath {
		t.Fatalf("expected config path %q, got %q", wantConfigPath, configPath)
	}
	if resolvedWorkspace != workspaceRoot {
		t.Fatalf("expected workspace %q, got %q", workspaceRoot, resolvedWorkspace)
	}
	if httpAddr != ":8080" {
		t.Fatalf("expected http addr %q, got %q", ":8080", httpAddr)
	}
}

func TestResolvePathsAcceptsConfigFlag(t *testing.T) {
	runtimeHome := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	configPath, resolvedWorkspace, httpAddr, err := resolvePaths(runtimeHome, []string{"--config", "configs/goflow.docker.yaml", "--workspace", workspaceRoot, "--http", ":8080"})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	wantConfigPath := filepath.Join(runtimeHome, "configs", "goflow.docker.yaml")
	if configPath != wantConfigPath {
		t.Fatalf("expected config path %q, got %q", wantConfigPath, configPath)
	}
	if resolvedWorkspace != workspaceRoot {
		t.Fatalf("expected workspace %q, got %q", workspaceRoot, resolvedWorkspace)
	}
	if httpAddr != ":8080" {
		t.Fatalf("expected http addr %q, got %q", ":8080", httpAddr)
	}
}

func TestResolvePathsRequiresConfigValue(t *testing.T) {
	runtimeHome := t.TempDir()

	_, _, _, err := resolvePaths(runtimeHome, []string{"--config"})
	if err == nil {
		t.Fatal("expected missing config value error")
	}
	if !strings.Contains(err.Error(), "missing value for --config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePathsRequiresHTTPValue(t *testing.T) {
	runtimeHome := t.TempDir()

	_, _, _, err := resolvePaths(runtimeHome, []string{"--http"})
	if err == nil {
		t.Fatal("expected missing http value error")
	}
	if !strings.Contains(err.Error(), "missing value for --http") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePathsDefaultsWorkspaceToCurrentDirectory(t *testing.T) {
	runtimeHome := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	configPath, resolvedWorkspace, _, err := resolvePaths(runtimeHome, nil)
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	wantConfigPath := filepath.Join(runtimeHome, "configs", "goflow.yaml")
	if configPath != wantConfigPath {
		t.Fatalf("expected config path %q, got %q", wantConfigPath, configPath)
	}
	if resolvedWorkspace != cwd {
		t.Fatalf("expected workspace %q, got %q", cwd, resolvedWorkspace)
	}
}

func TestResolveRuntimeHomeUsesParentWhenRunningFromBin(t *testing.T) {
	runtimeHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runtimeHome, "configs"), 0o755); err != nil {
		t.Fatalf("mkdir configs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeHome, "configs", "goflow.binary.yaml"), []byte("agent: {}\n"), 0o644); err != nil {
		t.Fatalf("write binary config: %v", err)
	}
	binDir := filepath.Join(runtimeHome, "bin")
	exePath := filepath.Join(binDir, "goflow.exe")

	info := resolveRuntimeHomeFrom(binDir, exePath)
	if info.Root != runtimeHome {
		t.Fatalf("expected runtime home %q, got %#v", runtimeHome, info)
	}
	if !info.BinaryArchive {
		t.Fatalf("expected binary archive runtime, got %#v", info)
	}
	if got := defaultConfigPathForRuntime(info); got != filepath.Join(runtimeHome, "configs", "goflow.binary.yaml") {
		t.Fatalf("expected binary config, got %q", got)
	}
}

func TestResolvePathSettingsWithDefaultConfig(t *testing.T) {
	runtimeHome := t.TempDir()
	customConfig := filepath.Join(runtimeHome, "configs", "goflow.binary.yaml")

	settings, err := resolvePathSettingsWithDefault(runtimeHome, nil, customConfig)
	if err != nil {
		t.Fatalf("resolve path settings: %v", err)
	}
	if settings.ConfigPath != customConfig {
		t.Fatalf("expected custom default config %q, got %q", customConfig, settings.ConfigPath)
	}
}

func TestApplyStartupEnvDefaultsMirrorsBackupProviderFromPrimary(t *testing.T) {
	t.Setenv("GOFLOW_BASE_URL", "https://example.test/v1")
	t.Setenv("GOFLOW_API_KEY", "primary-key")
	t.Setenv("GOFLOW_MODEL", "primary-model")
	t.Setenv("GOFLOW_BACKUP_BASE_URL", "")
	t.Setenv("GOFLOW_BACKUP_API_KEY", "")
	t.Setenv("GOFLOW_BACKUP_MODEL", "")

	applyStartupEnvDefaults(runtimeHomeInfo{})

	if os.Getenv("GOFLOW_BACKUP_BASE_URL") != "https://example.test/v1" {
		t.Fatalf("expected backup base url fallback, got %q", os.Getenv("GOFLOW_BACKUP_BASE_URL"))
	}
	if os.Getenv("GOFLOW_BACKUP_API_KEY") != "primary-key" {
		t.Fatalf("expected backup api key fallback, got %q", os.Getenv("GOFLOW_BACKUP_API_KEY"))
	}
	if os.Getenv("GOFLOW_BACKUP_MODEL") != "primary-model" {
		t.Fatalf("expected backup model fallback, got %q", os.Getenv("GOFLOW_BACKUP_MODEL"))
	}
}

func TestResolvePathSettingsMarksDefaultWorkspaceUnconfirmed(t *testing.T) {
	runtimeHome := t.TempDir()

	settings, err := resolvePathSettings(runtimeHome, nil)
	if err != nil {
		t.Fatalf("resolve path settings: %v", err)
	}
	if settings.WorkspaceExplicit {
		t.Fatalf("expected default workspace to be implicit, got %#v", settings)
	}
	workspace := newWorkspaceLifecycle(settings.WorkspaceRoot, settings.WorkspaceExplicit)
	if workspace.Confirmed() {
		t.Fatalf("expected implicit workspace to require confirmation")
	}
}

func TestResolvePathSettingsMarksWorkspaceFlagExplicit(t *testing.T) {
	runtimeHome := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")

	settings, err := resolvePathSettings(runtimeHome, []string{"--workspace", workspaceRoot})
	if err != nil {
		t.Fatalf("resolve path settings: %v", err)
	}
	if !settings.WorkspaceExplicit {
		t.Fatalf("expected --workspace to mark workspace explicit")
	}
	workspace := newWorkspaceLifecycle(settings.WorkspaceRoot, settings.WorkspaceExplicit)
	if !workspace.Confirmed() {
		t.Fatalf("expected explicit workspace to start confirmed")
	}
}

func TestResolvePathsRequiresWorkspaceValue(t *testing.T) {
	runtimeHome := t.TempDir()

	_, _, _, err := resolvePaths(runtimeHome, []string{"--workspace"})
	if err == nil {
		t.Fatal("expected missing workspace value error")
	}
	if !strings.Contains(err.Error(), "missing value for --workspace") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePathsAcceptsRelativeWorkspaceAndNormalizesIt(t *testing.T) {
	runtimeHome := t.TempDir()
	relative := filepath.Join("testdata", "workspace")
	want, err := filepath.Abs(relative)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}

	_, resolvedWorkspace, _, err := resolvePaths(runtimeHome, []string{"--workspace", relative})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	if resolvedWorkspace != filepath.Clean(want) {
		t.Fatalf("expected workspace %q, got %q", filepath.Clean(want), resolvedWorkspace)
	}
}

func legacyWorkspaceRequirementForInputDetectsWorkspaceTasks(t *testing.T) {
	cases := []string{
		"帮我用python写个计算器",
		"optimize and extend this project",
		"@README.md 总结一下",
		"run tests",
	}
	for _, input := range cases {
		if req := workspaceRequirementForInput(input); !req.Required {
			t.Fatalf("expected workspace requirement for %q", input)
		}
	}
	if req := workspaceRequirementForInput("Go 终端中如何监听按键？"); req.Required {
		t.Fatalf("expected pure chat to avoid workspace requirement, got %#v", req)
	}
}

func TestWorkspaceRequirementForInputDetectsWorkspaceTasks(t *testing.T) {
	cases := []string{
		"帮我用 python 写个计算器",
		"optimize and extend this project",
		"帮我优化拓展这个项目",
		"生成文档",
		"运行测试",
		"@README.md 总结一下",
		"run tests",
	}
	for _, input := range cases {
		if req := workspaceRequirementForInput(input); !req.Required {
			t.Fatalf("expected workspace requirement for %q", input)
		}
	}
	if req := workspaceRequirementForInput("Go 终端中如何监听按键？"); req.Required {
		t.Fatalf("expected pure chat to avoid workspace requirement, got %#v", req)
	}
}

func TestWorkspaceLifecycleConfirmCommand(t *testing.T) {
	workspace := newWorkspaceLifecycle(`D:\Projects\test`, false)
	if workspace.Confirmed() {
		t.Fatal("expected implicit workspace to start unconfirmed")
	}
	if handled := handleCommand(context.Background(), "/workspace confirm", nil, nil, nil, workspace); !handled {
		t.Fatal("expected /workspace confirm to be handled")
	}
	if !workspace.Confirmed() {
		t.Fatal("expected /workspace confirm to mark workspace confirmed")
	}
}

func TestParseWorkspaceUseInputNormalizesTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "workspace with space")
	path, ok, usage := parseWorkspaceUseInput("/workspace use " + target)
	if !ok {
		t.Fatal("expected /workspace use to be detected")
	}
	if usage != "" {
		t.Fatalf("unexpected usage error: %s", usage)
	}
	if path != filepath.Clean(target) {
		t.Fatalf("expected normalized path %q, got %q", filepath.Clean(target), path)
	}
}

func TestParseWorkspaceUseInputReportsMissingTarget(t *testing.T) {
	_, ok, usage := parseWorkspaceUseInput("/workspace use")
	if !ok {
		t.Fatal("expected /workspace use to be detected")
	}
	if !strings.Contains(usage, "usage: /workspace use <path>") {
		t.Fatalf("unexpected usage error: %q", usage)
	}
}

func TestParseWorkspaceChooseInput(t *testing.T) {
	for _, input := range []string{"/workspace choose", "/workspace pick"} {
		if !parseWorkspaceChooseInput(input) {
			t.Fatalf("expected %q to be detected", input)
		}
	}
	if parseWorkspaceChooseInput("/workspace use C:/demo") {
		t.Fatal("did not expect workspace use to be detected as choose")
	}
}

func TestCLIWorkspaceRebindBlockersDetectPendingState(t *testing.T) {
	state := session.New(10)
	state.SetPendingHandoff(session.PendingHandoffSnapshot{TargetAgent: "fixer", ExpectedAction: "confirm_execution"})
	state.SetWorkflow(session.WorkflowSnapshot{Name: "plan-fix-audit", Status: "awaiting_input"})
	state.StartAgentRun("long task")
	state.StartWorkflowRun("workflow", "long workflow")
	runtimeRef, err := agent.NewRuntime(&config.Config{
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat": {Provider: "stub", Model: "stub", Mode: "chat"},
		},
	}, map[string]interfaces.LLMClient{"stub": &workflowStubLLMClient{}}, nil, &mainTestMCP{}, state, nil)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	state.SetPendingApprovals([]session.PendingApprovalSnapshot{{CallID: "call-1", ToolName: "write_file"}})
	app := &apppkg.RuntimeApp{Runtime: runtimeRef}

	blockers := cliWorkspaceRebindBlockers(app)
	for _, want := range []string{"pending_approvals", "pending_handoff", "active_workflow", "active_agent_run", "active_workflow_run"} {
		if !slices.Contains(blockers, want) {
			t.Fatalf("expected blocker %q in %#v", want, blockers)
		}
	}
}

func TestRenderStartupBannerDoesNotIndentAsciiArt(t *testing.T) {
	output := renderStartupBanner(startupDisplayRow{RuntimeHome: `C:\runtime`, WorkspaceRoot: `D:\workspace`})
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if strings.TrimSpace(lines[0]) == "" || strings.HasPrefix(lines[0], " ") {
		t.Fatalf("expected logo to start immediately on first line, got %q", lines[0])
	}
}

func TestRenderStartupBannerUsesStableAlignedLayout(t *testing.T) {
	output := renderStartupBanner(startupDisplayRow{
		RuntimeHome:           `C:\runtime`,
		WorkspaceRoot:         `D:\workspace`,
		ActiveAgent:           "fixer",
		Mode:                  "fix",
		ToolPolicy:            "confirm",
		AllowedToolKinds:      []string{"read", "write", "network"},
		AllowedTools:          []string{"read_file", "write_file", "fetch_url"},
		WorkflowSummary:       "plan-fix-audit (awaiting_tool_approval)",
		PendingHandoffSummary: "target=fixer mode=fix action=confirm_execution",
		LastRoutingSummary:    "outcome=waiting_confirmation target=fixer mode=fix reason=planner result queued for execution handoff",
		PendingApprovals:      2,
	})
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 6 {
		t.Fatalf("expected banner lines, got %q", output)
	}
	if !strings.Contains(output, "██████") || !strings.Contains(output, "GoFlow Agent") {
		t.Fatalf("expected clear 3D startup title, got %q", output)
	}
	if strings.Contains(output, "�") {
		t.Fatalf("expected startup title to avoid replacement characters, got %q", output)
	}
	if !strings.Contains(output, "runtime") || !strings.Contains(output, `C:\runtime`) {
		t.Fatalf("expected runtime path, got %q", output)
	}
	if !strings.Contains(output, "workspace") || !strings.Contains(output, `D:\workspace`) {
		t.Fatalf("expected workspace path, got %q", output)
	}
	if !strings.Contains(output, "agent") || !strings.Contains(output, "fixer") {
		t.Fatalf("expected active agent summary, got %q", output)
	}
	if !strings.Contains(output, "mode") || !strings.Contains(output, "fix") {
		t.Fatalf("expected mode summary, got %q", output)
	}
	if !strings.Contains(output, "policy") || !strings.Contains(output, "confirm") {
		t.Fatalf("expected policy summary, got %q", output)
	}
	if !strings.Contains(output, "tool_kinds") || !strings.Contains(output, "read, write, network") {
		t.Fatalf("expected allowed kinds summary, got %q", output)
	}
	if !strings.Contains(output, "tool_allowlist") || !strings.Contains(output, "read_file, write_file, fetch_url") {
		t.Fatalf("expected allowed tools summary, got %q", output)
	}
	if !strings.Contains(output, "workflow") || !strings.Contains(output, "plan-fix-audit") {
		t.Fatalf("expected workflow summary, got %q", output)
	}
	if !strings.Contains(output, "handoff") || !strings.Contains(output, "confirm_execution") {
		t.Fatalf("expected pending handoff summary, got %q", output)
	}
	if !strings.Contains(output, "approvals") || !strings.Contains(output, "2") {
		t.Fatalf("expected pending approvals summary, got %q", output)
	}
	if !strings.Contains(output, "/help") {
		t.Fatalf("expected help hint, got %q", output)
	}
}

func TestBuildStartupDisplayRowIncludesWorkflowAndPendingApprovalSummary(t *testing.T) {
	runtimeRef := &agent.Runtime{}
	state := session.New(8)
	state.SetActiveAgent("fixer")
	state.SetMode("fix")
	state.SetWorkflow(session.WorkflowSnapshot{Name: "plan-fix-audit", Status: "awaiting_tool_approval", NextStage: "fix", Summary: "planner finished"})
	state.SetPendingHandoff(session.PendingHandoffSnapshot{Request: "build demo", TargetAgent: "fixer", TargetMode: "fix", ExpectedAction: "confirm_execution", PlanSummary: "Implement the approved plan"})
	state.SetLastRouting(session.RoutingSnapshot{Request: "build demo", SourceAgent: "planner", TargetAgent: "fixer", TargetMode: "fix", Outcome: "waiting_confirmation", Reason: "planner result queued for execution handoff"})
	state.SetPendingApprovals([]session.PendingApprovalSnapshot{{CallID: "call-1", ToolName: "write_file", AgentID: "fixer"}, {CallID: "call-2", ToolName: "fetch_url", AgentID: "fixer"}})
	state.RememberApprovedTool(`D:\workspace`, "write_file")

	runtimeValue := reflect.ValueOf(runtimeRef).Elem()
	reflect.NewAt(runtimeValue.FieldByName("session").Type(), unsafe.Pointer(runtimeValue.FieldByName("session").UnsafeAddr())).Elem().Set(reflect.ValueOf(state))
	reflect.NewAt(runtimeValue.FieldByName("active").Type(), unsafe.Pointer(runtimeValue.FieldByName("active").UnsafeAddr())).Elem().SetString("fixer")
	runners := map[string]*agent.AgentRunner{}
	reflect.NewAt(runtimeValue.FieldByName("runners").Type(), unsafe.Pointer(runtimeValue.FieldByName("runners").UnsafeAddr())).Elem().Set(reflect.ValueOf(runners))

	row := buildStartupDisplayRow(runtimeRef, `C:\runtime`, `D:\workspace`)
	if row.RuntimeHome != `C:\runtime` || row.WorkspaceRoot != `D:\workspace` {
		t.Fatalf("unexpected paths: %#v", row)
	}
	if row.ActiveAgent != "fixer" || row.Mode != "fix" {
		t.Fatalf("expected active agent and mode, got %#v", row)
	}
	if row.WorkflowSummary == "" || !strings.Contains(row.WorkflowSummary, "plan-fix-audit") {
		t.Fatalf("expected workflow summary, got %#v", row)
	}
	if row.PendingHandoffSummary == "" || !strings.Contains(row.PendingHandoffSummary, "confirm_execution") {
		t.Fatalf("expected pending handoff summary, got %#v", row)
	}
	if row.LastRoutingSummary == "" || !strings.Contains(row.LastRoutingSummary, "waiting_confirmation") {
		t.Fatalf("expected last routing summary, got %#v", row)
	}
	if row.PendingApprovals != 2 {
		t.Fatalf("expected pending approval count, got %#v", row)
	}
}

func TestBuildTerminalTitleUsesWorkspaceBaseName(t *testing.T) {
	title := buildTerminalTitle(`D:\Projects\test`)
	if title != "GoFlow Agent - test" {
		t.Fatalf("unexpected terminal title %q", title)
	}
}

func TestFormatPromptIncludesAgentAndMode(t *testing.T) {
	runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()
	output := formatPrompt(runtimeRef)
	if !strings.Contains(output, "goflow") || !strings.Contains(output, "[planner/plan]") {
		t.Fatalf("expected prompt to include agent and mode, got %q", output)
	}
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected colored prompt, got %q", output)
	}
}

func TestHandleCommandIgnoresColonPrefixedInput(t *testing.T) {
	if handled := handleCommand(context.Background(), ":stauts", nil, nil, nil); handled {
		t.Fatal("expected colon-prefixed input to be treated as normal text")
	}
}

func TestHandleCommandTreatsUnknownSlashInputAsCommandTypo(t *testing.T) {
	output := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/stauts", nil, nil, nil); !handled {
			t.Fatal("expected unknown slash command to be handled")
		}
	})
	if !strings.Contains(output, "Unknown command") || !strings.Contains(output, "/status") {
		t.Fatalf("expected typo guidance for unknown command, got %q", output)
	}
}

func TestDoubleEscCancellerRequiresSecondPressWithinWindow(t *testing.T) {
	canceller := newDoubleEscCanceller(2 * time.Second)
	start := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	if action := canceller.press(start); action != doubleEscActionWarn {
		t.Fatalf("expected first Esc to warn, got %v", action)
	}
	if action := canceller.press(start.Add(1500 * time.Millisecond)); action != doubleEscActionCancel {
		t.Fatalf("expected second Esc inside the window to cancel, got %v", action)
	}
	if action := canceller.press(start.Add(5 * time.Second)); action != doubleEscActionWarn {
		t.Fatalf("expected next isolated Esc to warn again, got %v", action)
	}
}

func TestDoubleEscCancellerExpiresFirstPress(t *testing.T) {
	canceller := newDoubleEscCanceller(2 * time.Second)
	start := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	if action := canceller.press(start); action != doubleEscActionWarn {
		t.Fatalf("expected first Esc to warn, got %v", action)
	}
	if action := canceller.press(start.Add(3 * time.Second)); action != doubleEscActionWarn {
		t.Fatalf("expected delayed second Esc to warn instead of cancel, got %v", action)
	}
}

func TestRunHTTPServerStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{
		Addr:    "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	}
	var output bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- runHTTPServer(ctx, server, &output)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runHTTPServer: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected HTTP server to stop after context cancellation")
	}
	if got := output.String(); !strings.Contains(got, "shutting down") || !strings.Contains(got, "stopped") {
		t.Fatalf("expected shutdown status output, got %q", got)
	}
}

func TestDisplayHTTPURLAlwaysUsesPlainHTTP(t *testing.T) {
	cases := map[string]string{
		":8080":          "http://127.0.0.1:8080/console",
		"127.0.0.1:9090": "http://127.0.0.1:9090/console",
		"localhost:8081": "http://localhost:8081/console",
		"0.0.0.0:8082":   "http://127.0.0.1:8082/console",
		"[::]:8083":      "http://127.0.0.1:8083/console",
	}
	for addr, want := range cases {
		if got := displayHTTPURL(addr, "console"); got != want {
			t.Fatalf("displayHTTPURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestContinuationOnlyInputIsDetected(t *testing.T) {
	for _, input := range []string{"继续", "继续吧", "可以，继续", "重试", "continue", "go ahead", "retry"} {
		if !isContinuationOnlyInput(input) {
			t.Fatalf("expected %q to be treated as continuation-only input", input)
		}
	}
	for _, input := range []string{"继续优化这个项目", "continue fixing parser", "帮我继续写 README"} {
		if isContinuationOnlyInput(input) {
			t.Fatalf("expected %q to be treated as a concrete new request", input)
		}
	}
}

func TestFormatRetryingCancelledTaskIncludesLastRequest(t *testing.T) {
	message := formatRetryingCancelledTask(cancelledTaskState{
		Request:     "帮我优化下这个代码 拓展功能",
		Agent:       "fixer",
		Mode:        "fix",
		CancelledAt: time.Now(),
	})
	if !strings.Contains(message, "Re-running") || !strings.Contains(message, "帮我优化下这个代码") || !strings.Contains(message, "fixer/fix") {
		t.Fatalf("unexpected cancelled-task retry message: %q", message)
	}
}

func TestStartDoubleEscCancelWatcherNoopsForNonTerminalInput(t *testing.T) {
	cancelled := false
	stop := startDoubleEscCancelWatcher(context.Background(), strings.NewReader(""), io.Discard, func() { cancelled = true })
	if stop() {
		t.Fatal("expected non-terminal watcher to report no Esc cancellation")
	}
	if cancelled {
		t.Fatal("expected non-terminal watcher not to call cancel")
	}
}

func TestRunNumericApprovalPromptApprovesOnChoiceOne(t *testing.T) {
	input := strings.NewReader("1\n")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	err := runNumericApprovalPrompt(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != nil {
		t.Fatalf("runNumericApprovalPrompt: %v", err)
	}
	if !approved || denied || approvedAll {
		t.Fatalf("expected approve path only, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
}

func TestRunNumericApprovalPromptRemembersToolOnChoiceFourWhenAvailable(t *testing.T) {
	input := strings.NewReader("4\n")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false
	remembered := false

	err := runNumericApprovalPrompt(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	}, func() error {
		remembered = true
		return nil
	})
	if err != nil {
		t.Fatalf("runNumericApprovalPrompt: %v", err)
	}
	if approved || denied || approvedAll || !remembered {
		t.Fatalf("expected remember path only, approved=%t denied=%t approvedAll=%t remembered=%t", approved, denied, approvedAll, remembered)
	}
	if !strings.Contains(output.String(), "Approve and remember this tool for this session") {
		t.Fatalf("expected remember option in output, got %q", output.String())
	}
}

func TestRunNumericApprovalPromptUsesChoiceThreeForRememberWhenApproveAllUnavailable(t *testing.T) {
	input := strings.NewReader("3\n")
	var output bytes.Buffer
	approved := false
	denied := false
	remembered := false

	err := runNumericApprovalPrompt(input, &output, "Tool call call-1 requires approval.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, nil, func() error {
		remembered = true
		return nil
	})
	if err != nil {
		t.Fatalf("runNumericApprovalPrompt: %v", err)
	}
	if approved || denied || !remembered {
		t.Fatalf("expected remember path only, approved=%t denied=%t remembered=%t", approved, denied, remembered)
	}
	if strings.Contains(output.String(), "Approve all pending tool calls") {
		t.Fatalf("expected approve-all option to be hidden, got %q", output.String())
	}
	if !strings.Contains(output.String(), "3.") || !strings.Contains(output.String(), "Approve and remember this tool for this session") {
		t.Fatalf("expected remember option as choice 3, got %q", output.String())
	}
}

func TestRunInteractiveApprovalMenuSelectsApproveAllWithArrowKeys(t *testing.T) {
	input := strings.NewReader("\x1b[B\x1b[B\r")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	err := runInteractiveApprovalMenu(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != nil {
		t.Fatalf("runInteractiveApprovalMenu: %v", err)
	}
	if approved || denied || !approvedAll {
		t.Fatalf("expected approve-all selection, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
	if !strings.Contains(output.String(), "Approve all pending tool calls") {
		t.Fatalf("expected menu output, got %q", output.String())
	}
}

func TestRunInteractiveApprovalMenuSelectsRememberWithArrowKeys(t *testing.T) {
	input := strings.NewReader("\x1b[B\x1b[B\x1b[B\r")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false
	remembered := false

	err := runInteractiveApprovalMenu(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	}, func() error {
		remembered = true
		return nil
	})
	if err != nil {
		t.Fatalf("runInteractiveApprovalMenu: %v", err)
	}
	if approved || denied || approvedAll || !remembered {
		t.Fatalf("expected remember selection, approved=%t denied=%t approvedAll=%t remembered=%t", approved, denied, approvedAll, remembered)
	}
	if !strings.Contains(output.String(), "Approve and remember this tool for this session") {
		t.Fatalf("expected remember menu output, got %q", output.String())
	}
}

func TestRunInteractiveApprovalMenuRedrawsSingleMenuInPlace(t *testing.T) {
	input := strings.NewReader("\x1b[B\x1b[B\r")
	var output bytes.Buffer

	err := runInteractiveApprovalMenu(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		return nil
	}, func() error {
		return nil
	}, func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("runInteractiveApprovalMenu: %v", err)
	}
	if got := strings.Count(output.String(), "Workflow is waiting for approval of tool call call-1 by agent fixer."); got != 1 {
		t.Fatalf("expected prompt to be rendered once and updated in place, got %d copies in %q", got, output.String())
	}
	if !strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("expected ansi cursor controls for in-place redraw, got %q", output.String())
	}
}

func TestRenderApprovalMenuSelectionKeepsPromptLineAnchored(t *testing.T) {
	options := []approvalOption{
		{label: "Approve", kind: "ready"},
		{label: "Deny", kind: "denied"},
		{label: "Approve all pending tool calls", kind: "ready"},
	}
	var output bytes.Buffer

	renderApprovalMenu(&output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", options, 0)
	renderApprovalMenuSelection(&output, options, 1)

	if !strings.Contains(output.String(), "\r\x1b[4A") {
		t.Fatalf("expected redraw to move back only across transient menu lines, got %q", output.String())
	}
	if strings.Contains(output.String(), "\r\x1b[5A") {
		t.Fatalf("expected redraw to avoid clearing the prompt line, got %q", output.String())
	}
}

func TestRunInteractiveApprovalMenuIgnoresTypedSentenceUntilExplicitConfirmation(t *testing.T) {
	input := strings.NewReader("这个上下键选择并没有阻塞到选择\n")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	err := runInteractiveApprovalMenu(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != io.EOF {
		t.Fatalf("expected input exhaustion after ignoring incidental typed input, got %v", err)
	}
	if approved || denied || approvedAll {
		t.Fatalf("expected no approval action from incidental typed input, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
	if !strings.Contains(output.String(), "Use ↑/↓ to choose and Enter to confirm.") {
		t.Fatalf("expected invalid input guidance, got %q", output.String())
	}
}

func TestRunInteractiveApprovalMenuKeepsWaitingThroughInvalidInputUntilExplicitSelection(t *testing.T) {
	input := strings.NewReader("bad input\n2\n")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	err := runNumericApprovalPrompt(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != nil {
		t.Fatalf("runNumericApprovalPrompt: %v", err)
	}
	if approved || !denied || approvedAll {
		t.Fatalf("expected prompt to continue blocking until explicit deny, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
	if strings.Count(output.String(), "Workflow is waiting for approval of tool call call-1 by agent fixer.") != 2 {
		t.Fatalf("expected prompt to be rendered again after invalid input, got %q", output.String())
	}
	if !strings.Contains(output.String(), "Please enter 1, 2, or 3.") {
		t.Fatalf("expected invalid input guidance, got %q", output.String())
	}
}

func TestRunBlockingApprovalPromptFallsBackToNumericWhenInteractiveDisabled(t *testing.T) {
	input := strings.NewReader("2\n")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	prev := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return false }
	defer func() { terminalInputSupported = prev }()

	err := runBlockingApprovalPrompt(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != nil {
		t.Fatalf("runBlockingApprovalPrompt: %v", err)
	}
	if approved || !denied || approvedAll {
		t.Fatalf("expected numeric fallback deny path, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
}

func TestRunBlockingApprovalPromptUsesInteractiveMenuWhenTerminalSupported(t *testing.T) {
	input := strings.NewReader("\x1b[B\r")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	prev := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return true }
	defer func() { terminalInputSupported = prev }()

	prevReader := newApprovalInputReader
	newApprovalInputReader = func(io.Reader) approvalInputReader {
		return bufio.NewReader(input)
	}
	defer func() { newApprovalInputReader = prevReader }()

	err := runBlockingApprovalPrompt(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != nil {
		t.Fatalf("runBlockingApprovalPrompt: %v", err)
	}
	if approved || !denied || approvedAll {
		t.Fatalf("expected interactive menu deny path, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
	if !strings.Contains(output.String(), "Enter to confirm • ↑/↓ to move") {
		t.Fatalf("expected interactive menu instructions, got %q", output.String())
	}
}

func TestRunBlockingApprovalPromptFallsBackWhenInteractiveReaderUnavailable(t *testing.T) {
	input := strings.NewReader("2\n")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	prev := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return true }
	defer func() { terminalInputSupported = prev }()

	prevReader := newApprovalInputReader
	newApprovalInputReader = func(io.Reader) approvalInputReader {
		return nil
	}
	defer func() { newApprovalInputReader = prevReader }()

	err := runBlockingApprovalPrompt(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != nil {
		t.Fatalf("runBlockingApprovalPrompt: %v", err)
	}
	if approved || !denied || approvedAll {
		t.Fatalf("expected numeric fallback deny path, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
}

func TestCLIStreamRendererShowsToolKindWhenProvided(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "read_file", Content: "read"})
	})
	if !strings.Contains(output, "calling read_file (read)") {
		t.Fatalf("expected tool kind output, got %q", output)
	}
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected ansi styling, got %q", output)
	}
}

func TestCLIStreamRendererShowsToolArgumentSummary(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "read_file", ToolCallID: "call-1", Content: "read", ArgumentsSummary: "path=README.md"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventApproval, ToolName: "write_file", ToolCallID: "call-2", ArgumentsSummary: "path=calculator/cli.py content=1200 chars"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "write_file", ToolCallID: "call-2", ArgumentsSummary: "path=calculator/cli.py content=1200 chars", Content: `{"path":"calculator/cli.py","bytes_written":1200,"status":"modified","old_range":"3-5","new_range":"3-7","added_lines":4,"deleted_lines":3,"line_summary":"changed old lines 3-5 -> new lines 3-7 (+4 -3)","diff_preview":"@@ -3,3 +3,4 @@\n-    3      | old\n+         3 | new"}`})
	})
	if !strings.Contains(output, "path=README.md") {
		t.Fatalf("expected read path summary, got %q", output)
	}
	if !strings.Contains(output, "path=calculator/cli.py") || !strings.Contains(output, "content=1200 chars") {
		t.Fatalf("expected write argument summary, got %q", output)
	}
	if !strings.Contains(output, "bytes=1200") || !strings.Contains(output, "status=modified") || !strings.Contains(output, "lines=+4 -3") {
		t.Fatalf("expected write result summary, got %q", output)
	}
	if !strings.Contains(output, "[write]") || !strings.Contains(output, "M") || !strings.Contains(output, "old 3-5 -> new 3-7") || !strings.Contains(output, "1200 bytes") {
		t.Fatalf("expected prominent git-like changed-file line, got %q", output)
	}
	if !strings.Contains(output, "[diff]") || !strings.Contains(output, "diff --goflow") || !strings.Contains(output, "--- a/calculator/cli.py") || !strings.Contains(output, "+++ b/calculator/cli.py") || !strings.Contains(output, "-    3") || !strings.Contains(output, "+         3") {
		t.Fatalf("expected git-like diff preview output, got %q", output)
	}
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected colored tool summary output, got %q", output)
	}
}

func TestCLIStreamRendererPrintsMultiFileWriteSummaryOnFinish(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "write_file", ToolCallID: "call-1", Content: `{"path":"a.txt","bytes_written":10,"status":"created","added_lines":2,"deleted_lines":0,"new_range":"1-2"}`})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "write_file", ToolCallID: "call-2", Content: `{"path":"b.txt","bytes_written":20,"status":"modified","added_lines":3,"deleted_lines":1,"old_range":"4","new_range":"4-6"}`})
		renderer.Finish("done")
	})
	if !strings.Contains(output, "done") || !strings.Contains(output, "[write-summary]") {
		t.Fatalf("expected final answer and write summary, got %q", output)
	}
	if strings.Index(output, "done") > strings.Index(output, "[write-summary]") {
		t.Fatalf("expected write summary after final answer, got %q", output)
	}
	if !strings.Contains(output, "2 files") || !strings.Contains(output, "+5 -1") || !strings.Contains(output, "30 bytes") {
		t.Fatalf("expected aggregate write totals, got %q", output)
	}
	if !strings.Contains(output, "created=1") || !strings.Contains(output, "modified=1") || !strings.Contains(output, "a.txt") || !strings.Contains(output, "b.txt") {
		t.Fatalf("expected per-file write summary, got %q", output)
	}
}

func TestCLIStreamRendererPrintsTokenUsageSummaryOnFinish(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventTokenUsage, PromptTokens: 1234, OutputTokens: 56, CachedTokens: 100})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventTokenUsage, PromptTokens: 10, OutputTokens: 4})
		renderer.Finish("done")
	})
	if !strings.Contains(output, "done") || !strings.Contains(output, "[tokens]") {
		t.Fatalf("expected final answer and token summary, got %q", output)
	}
	for _, want := range []string{"input=1,244", "output=60", "total=1,304", "cached=100"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected token summary to include %q, got %q", want, output)
		}
	}
	if strings.Index(output, "done") > strings.Index(output, "[tokens]") {
		t.Fatalf("expected token summary after final answer, got %q", output)
	}
}

func TestCLIStreamRendererDoesNotPrintWriteSummaryForSingleFile(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "write_file", ToolCallID: "call-1", Content: `{"path":"a.txt","bytes_written":10,"status":"created","added_lines":2,"deleted_lines":0,"new_range":"1-2"}`})
		renderer.Finish("done")
	})
	if strings.Contains(output, "[write-summary]") {
		t.Fatalf("expected no batch summary for one write, got %q", output)
	}
}

func TestCLIStreamRendererDoesNotShowChangedLineForReadTool(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "read_file", ToolCallID: "call-1", Content: `{"path":"README.md","size":42}`})
	})
	if strings.Contains(output, "[changed]") {
		t.Fatalf("expected no changed-file line for read tool, got %q", output)
	}
	if !strings.Contains(output, "size=42") {
		t.Fatalf("expected read result summary to remain visible, got %q", output)
	}
}

func TestCLIStreamRendererShowsOperatorVisibleStatusWithoutTrace(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "waiting for model response after tool results...", NeedsAction: true})
	})
	if !strings.Contains(output, "waiting for model response") {
		t.Fatalf("expected operator-visible status output, got %q", output)
	}
}

func TestCLIStreamRendererShowsTaskStage(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventTaskStage, TaskStage: "inspect", AgentID: "fixer", Mode: "fix", Content: "gathering context"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventTaskStage, TaskStage: "inspect", AgentID: "fixer", Mode: "fix", Content: "gathering context"})
	})
	if strings.Count(output, "[stage]") != 1 || !strings.Contains(output, "inspect") || !strings.Contains(output, "gathering context") {
		t.Fatalf("expected one task stage line, got %q", output)
	}
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected colored stage output, got %q", output)
	}
}

func TestCLIStreamRendererHandlesHTTPParityEvents(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "waiting for model response...", NeedsAction: true})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventTaskStage, TaskStage: "inspect", AgentID: "fixer", Mode: "fix", Content: "gathering context"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventApproval, ToolName: "write_file", ToolCallID: "call-1", ArgumentsSummary: "path=demo.txt"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "write_file", ToolCallID: "call-1", ArgumentsSummary: "path=demo.txt", Content: `{"path":"demo.txt","bytes_written":12,"status":"created","added_lines":1,"deleted_lines":0,"new_range":"1"}`})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventTokenUsage, PromptTokens: 20, OutputTokens: 5, CachedTokens: 3})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventFinalMessage, Content: "done after approval"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventWorkflowResult, RunID: "wf-1", WorkflowName: "plan-fix-audit", WorkflowStatus: "completed"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventError, Content: "workspace confirmation required", IsError: true, NeedsAction: true})
		renderer.Finish("")
	})
	for _, want := range []string{
		"[status]",
		"[stage]",
		"[approval]",
		"[write]",
		"done after approval",
		"[workflow]",
		"plan-fix-audit",
		"status=completed",
		"[error]",
		"[tokens]",
		"input=20",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected HTTP parity renderer output to include %q, got %q", want, output)
		}
	}
}

func TestCLIStreamRendererSuppressesBarePartialToolCall(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "read_file"})
	})
	if strings.TrimSpace(output) != "" {
		t.Fatalf("expected incomplete tool call announcement to stay quiet, got %q", output)
	}
}

func TestCLIStreamRendererDeduplicatesRepeatedToolCallsForSameCallID(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "write_file", ToolCallID: "call-1"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "write_file", ToolCallID: "call-1"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "write_file", ToolCallID: "call-1", Content: "write"})
	})
	if strings.Count(output, "calling write_file") != 1 {
		t.Fatalf("expected only the useful final tool call line, got %q", output)
	}
	if !strings.Contains(output, "calling write_file (write)") {
		t.Fatalf("expected final tool kind output, got %q", output)
	}
}

func TestCLIStreamRendererDeduplicatesAnonymousToolCalls(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "write_file"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "write_file"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "write_file", Content: "write", ArgumentsSummary: "path=calculator.py content=120 chars"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "write_file", Content: "write", ArgumentsSummary: "path=calculator.py content=120 chars"})
	})
	if strings.Count(output, "calling write_file") != 1 {
		t.Fatalf("expected one useful anonymous tool call line, got %q", output)
	}
	if !strings.Contains(output, "path=calculator.py") {
		t.Fatalf("expected detailed argument summary, got %q", output)
	}
}

func TestCLIStreamRendererDoesNotRepeatStreamedPreludeOnFinish(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventText, Content: "I will create the project."})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "write_file", ToolCallID: "call-1", Content: "write", ArgumentsSummary: "path=calculator.py content=120 chars"})
		renderer.Finish("I will create the project.")
	})
	if strings.Count(output, "I will create the project.") != 1 {
		t.Fatalf("expected streamed prelude to appear once, got %q", output)
	}
}

func TestCLIStreamRendererShowsApprovalAndFailure(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventApproval, ToolName: "write_file"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "write_file", IsError: true})
	})
	if !strings.Contains(output, "write_file requires confirmation") {
		t.Fatalf("expected approval output, got %q", output)
	}
	if !strings.Contains(output, "write_file") || !strings.Contains(output, "failed") {
		t.Fatalf("expected failed tool output, got %q", output)
	}
}

func TestCLIStreamRendererShowsSuspendedToolState(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "write_file", Content: "approval required", Suspended: true})
	})
	if !strings.Contains(output, "write_file") || !strings.Contains(output, "suspended") {
		t.Fatalf("expected suspended tool output, got %q", output)
	}
}

func TestRunBlockingApprovalPromptApprovesOnChoiceOne(t *testing.T) {
	input := strings.NewReader("1\n")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	err := runBlockingApprovalPrompt(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != nil {
		t.Fatalf("runBlockingApprovalPrompt: %v", err)
	}
	if !approved || denied || approvedAll {
		t.Fatalf("expected approve path only, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
	if !strings.Contains(output.String(), "Approve") || !strings.Contains(output.String(), "Deny") || !strings.Contains(output.String(), "Approve all") {
		t.Fatalf("expected approval choices, got %q", output.String())
	}
}

func TestRunBlockingApprovalPromptDeniesOnChoiceTwo(t *testing.T) {
	input := strings.NewReader("2\n")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	err := runBlockingApprovalPrompt(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != nil {
		t.Fatalf("runBlockingApprovalPrompt: %v", err)
	}
	if approved || !denied || approvedAll {
		t.Fatalf("expected deny path only, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
}

func TestRunBlockingApprovalPromptApprovesAllOnChoiceThree(t *testing.T) {
	input := strings.NewReader("3\n")
	var output bytes.Buffer
	approved := false
	denied := false
	approvedAll := false

	err := runBlockingApprovalPrompt(input, &output, "Workflow is waiting for approval of tool call call-1 by agent fixer.", func() error {
		approved = true
		return nil
	}, func() error {
		denied = true
		return nil
	}, func() error {
		approvedAll = true
		return nil
	})
	if err != nil {
		t.Fatalf("runBlockingApprovalPrompt: %v", err)
	}
	if approved || denied || !approvedAll {
		t.Fatalf("expected approve-all path only, approved=%t denied=%t approvedAll=%t", approved, denied, approvedAll)
	}
}

func TestExtractApprovalCallID(t *testing.T) {
	callID := extractApprovalCallID("Workflow is waiting for approval of tool call call-1 by agent fixer.")
	if callID != "call-1" {
		t.Fatalf("expected call-1, got %q", callID)
	}
}

func TestHandleWorkflowCommandRememberToolResumesWithoutPrintingPrematureCompletion(t *testing.T) {
	prevInteractive := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return false }
	defer func() { terminalInputSupported = prevInteractive }()

	prevStdin := os.Stdin
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin: %v", err)
	}
	_, _ = writePipe.WriteString("3\n")
	_ = writePipe.Close()
	os.Stdin = readPipe
	defer func() {
		os.Stdin = prevStdin
		_ = readPipe.Close()
	}()

	stdout := captureStdout(t, func() {
		stderr := captureStderr(t, func() {
			runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()
			handled := handleWorkflowCommand(context.Background(), []string{"plan-fix-audit", "--approve", "build", "demo"}, runtimeRef)
			if !handled {
				t.Fatal("expected workflow command to be handled")
			}
		})
		if strings.TrimSpace(stderr) != "" {
			t.Fatalf("expected no stderr output, got %q", stderr)
		}
	})
	if strings.Count(stdout, "[workflow] plan-fix-audit status=completed") != 1 {
		t.Fatalf("expected exactly one workflow completion line, got %q", stdout)
	}
	if !strings.Contains(stdout, "suspended") {
		t.Fatalf("expected suspended tool output before approval resolution, got %q", stdout)
	}
	if !strings.Contains(stdout, "done") {
		t.Fatalf("expected approved tool output after resume, got %q", stdout)
	}
	if !strings.Contains(stdout, "Completed stages") {
		t.Fatalf("expected final workflow summary, got %q", stdout)
	}
	if strings.Contains(stdout, "Approve all pending tool calls") {
		t.Fatalf("expected workflow tool approval prompt to hide approve-all option, got %q", stdout)
	}
}

func TestHandleWorkflowCommandRememberToolDoesNotResolveUnrelatedWorkflowApproval(t *testing.T) {
	prevInteractive := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return false }
	defer func() { terminalInputSupported = prevInteractive }()

	prevStdin := os.Stdin
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin: %v", err)
	}
	_, _ = writePipe.WriteString("3\n")
	_ = writePipe.Close()
	os.Stdin = readPipe
	defer func() {
		os.Stdin = prevStdin
		_ = readPipe.Close()
	}()

	stderr := captureStderr(t, func() {
		_ = captureStdout(t, func() {
			runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()
			runtimeRef.WorkflowRunner().Run(context.Background(), "other-workflow", "do something else", true, nil)
			handled := handleWorkflowCommand(context.Background(), []string{"plan-fix-audit", "--approve", "build", "demo"}, runtimeRef)
			if !handled {
				t.Fatal("expected workflow command to be handled")
			}
		})
	})
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected no stderr output when unrelated workflow approvals exist, got %q", stderr)
	}
}

func TestHandleWorkflowCommandPassesRawStdinToInteractiveApproval(t *testing.T) {
	prevInteractive := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return true }
	defer func() { terminalInputSupported = prevInteractive }()

	prevReader := newApprovalInputReader
	var sawOnlyFiles = true
	newApprovalInputReader = func(input io.Reader) approvalInputReader {
		if _, ok := input.(*os.File); !ok {
			sawOnlyFiles = false
		}
		return bufio.NewReader(strings.NewReader("\r\r"))
	}
	defer func() { newApprovalInputReader = prevReader }()

	stderr := captureStderr(t, func() {
		_ = captureStdout(t, func() {
			runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()
			handled := handleWorkflowCommand(context.Background(), []string{"plan-fix-audit", "build", "demo"}, runtimeRef)
			if !handled {
				t.Fatal("expected workflow command to be handled")
			}
		})
	})
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected no stderr output, got %q", stderr)
	}
	if !sawOnlyFiles {
		t.Fatal("expected interactive approval to receive raw os.Stdin instead of a buffered reader")
	}
}

func TestHandleWorkflowCommandWithoutApproveDoesNotTreatStageApprovalAsToolApproval(t *testing.T) {
	prevInteractive := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return false }
	defer func() { terminalInputSupported = prevInteractive }()

	prevStdin := os.Stdin
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin: %v", err)
	}
	_, _ = writePipe.WriteString("1\n3\n")
	_ = writePipe.Close()
	os.Stdin = readPipe
	defer func() {
		os.Stdin = prevStdin
		_ = readPipe.Close()
	}()

	stderr := captureStderr(t, func() {
		stdout := captureStdout(t, func() {
			runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()
			handled := handleWorkflowCommand(context.Background(), []string{"plan-fix-audit", "build", "demo"}, runtimeRef)
			if !handled {
				t.Fatal("expected workflow command to be handled")
			}
		})
		if !strings.Contains(stdout, "Workflow requires approval before running fixer.") {
			t.Fatalf("expected stage approval prompt, got %q", stdout)
		}
		if !strings.Contains(stdout, "Workflow is waiting for approval of tool call call-1 by agent fixer.") {
			t.Fatalf("expected tool approval prompt after stage approval, got %q", stdout)
		}
		if strings.Contains(stdout, "Approve all pending tool calls") {
			t.Fatalf("expected workflow prompts to hide approve-all option, got %q", stdout)
		}
	})
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected stage approval path to avoid tool approval errors, got %q", stderr)
	}
}

func TestHandleWorkflowCommandApproveWithoutPendingCallIDDoesNotDropIntoChatLoop(t *testing.T) {
	prevInteractive := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return false }
	defer func() { terminalInputSupported = prevInteractive }()

	prevStdin := os.Stdin
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin: %v", err)
	}
	_, _ = writePipe.WriteString("1\n3\n")
	_ = writePipe.Close()
	os.Stdin = readPipe
	defer func() {
		os.Stdin = prevStdin
		_ = readPipe.Close()
	}()

	stderr := captureStderr(t, func() {
		stdout := captureStdout(t, func() {
			runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()
			handled := handleWorkflowCommand(context.Background(), []string{"plan-fix-audit", "build", "demo"}, runtimeRef)
			if !handled {
				t.Fatal("expected workflow command to be handled")
			}
		})
		if strings.Contains(stdout, "Let me check the current workspace status first.") {
			t.Fatalf("expected workflow command to stay in approval flow, got %q", stdout)
		}
	})
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected no approval routing error after stage approval transitions, got %q", stderr)
	}
}

func TestHandlePendingHandoffInputConfirmsAndRunsFixer(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()
	runtimeRef.SessionSnapshot()

	runtimeValue := reflect.ValueOf(runtimeRef).Elem()
	stateField := runtimeValue.FieldByName("session")
	state := reflect.NewAt(stateField.Type(), unsafe.Pointer(stateField.UnsafeAddr())).Elem().Interface().(*session.State)
	state.SetPendingHandoff(session.PendingHandoffSnapshot{
		Request:        "build demo",
		SourceAgent:    "planner",
		TargetAgent:    "fixer",
		TargetMode:     "fix",
		PlanSummary:    "Implement the approved plan",
		ExpectedAction: "confirm_execution",
	})

	prevInteractive := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return false }
	defer func() { terminalInputSupported = prevInteractive }()

	stdout := captureStdout(t, func() {
		stderr := captureStderr(t, func() {
			if handled := handlePendingHandoffInput(ctx, "可以", strings.NewReader("1\n"), os.Stdout, runtimeRef); !handled {
				t.Fatal("expected pending handoff input to be handled")
			}
		})
		if strings.TrimSpace(stderr) != "" {
			t.Fatalf("expected no stderr output, got %q", stderr)
		}
	})
	if !strings.Contains(stdout, "write_file requires confirmation") {
		t.Fatalf("expected follow-up approval prompt, got %q", stdout)
	}
	if !strings.Contains(stdout, "Approved call-1 -> write_file") {
		t.Fatalf("expected approved tool output, got %q", stdout)
	}
	if runtimeRef.HasPendingHandoff() {
		t.Fatalf("expected pending handoff to be cleared")
	}
	if runtimeRef.ActiveAgent() != "planner" {
		t.Fatalf("expected active agent to return to default planner, got %q", runtimeRef.ActiveAgent())
	}
}

func TestHandlePendingHandoffInputRejectsPendingExecution(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()
	runtimeRef.SessionSnapshot()

	runtimeValue := reflect.ValueOf(runtimeRef).Elem()
	stateField := runtimeValue.FieldByName("session")
	state := reflect.NewAt(stateField.Type(), unsafe.Pointer(stateField.UnsafeAddr())).Elem().Interface().(*session.State)
	state.SetPendingHandoff(session.PendingHandoffSnapshot{
		Request:        "build demo",
		SourceAgent:    "planner",
		TargetAgent:    "fixer",
		TargetMode:     "fix",
		PlanSummary:    "Implement the approved plan",
		ExpectedAction: "confirm_execution",
	})

	var output bytes.Buffer
	stderr := captureStderr(t, func() {
		if handled := handlePendingHandoffInput(ctx, "取消", strings.NewReader(""), &output, runtimeRef); !handled {
			t.Fatal("expected pending handoff rejection to be handled")
		}
	})
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected no stderr output, got %q", stderr)
	}
	if !strings.Contains(output.String(), "Cancelled pending implementation handoff") {
		t.Fatalf("expected cancellation message, got %q", output.String())
	}
	if runtimeRef.HasPendingHandoff() {
		t.Fatalf("expected pending handoff to be cleared")
	}
}

func TestHandlePendingApprovalInputTreatsChineseConfirmationAsApproval(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()

	first, err := runtimeRef.WorkflowRunner().Run(ctx, "plan-fix-audit", "build demo", true, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if first.Status != "awaiting_tool_approval" {
		t.Fatalf("expected awaiting_tool_approval, got %q", first.Status)
	}
	if len(runtimeRef.PendingApprovals()) != 1 {
		t.Fatalf("expected one pending approval before confirmation, got %#v", runtimeRef.PendingApprovals())
	}

	stdout := captureStdout(t, func() {
		stderr := captureStderr(t, func() {
			handled := handlePendingApprovalInput(ctx, "直接修改即可", runtimeRef)
			if !handled {
				t.Fatal("expected pending approval input to be handled")
			}
		})
		if strings.TrimSpace(stderr) != "" {
			t.Fatalf("expected no stderr output, got %q", stderr)
		}
	})
	if !strings.Contains(stdout, "Approved call-1 -> write_file") {
		t.Fatalf("expected approval output, got %q", stdout)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected pending approval to be cleared, got %#v", runtimeRef.PendingApprovals())
	}
}

func TestHandleOrdinaryChatPendingApprovalBlocksWithNumericPrompt(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()

	result, err := runtimeRef.RunStream(ctx, "implement the login form validation and update the handler", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(result.ToolResults) == 0 || !result.ToolResults[len(result.ToolResults)-1].Suspended {
		t.Fatalf("expected suspended tool result, got %#v", result.ToolResults)
	}
	if len(runtimeRef.PendingApprovals()) != 1 {
		t.Fatalf("expected one pending approval before blocking prompt, got %#v", runtimeRef.PendingApprovals())
	}

	prevInteractive := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return false }
	defer func() { terminalInputSupported = prevInteractive }()

	stdout := captureStdout(t, func() {
		stderr := captureStderr(t, func() {
			renderer := newCLIStreamRenderer(false)
			if err := handleOrdinaryChatPendingApproval(ctx, strings.NewReader("1\n"), os.Stdout, runtimeRef, renderer); err != nil {
				t.Fatalf("handleOrdinaryChatPendingApproval: %v", err)
			}
		})
		if strings.TrimSpace(stderr) != "" {
			t.Fatalf("expected no stderr output, got %q", stderr)
		}
	})
	if !strings.Contains(stdout, "write_file requires confirmation") {
		t.Fatalf("expected blocking approval prompt, got %q", stdout)
	}
	if !strings.Contains(stdout, "Approved call-1 -> write_file") {
		t.Fatalf("expected immediate approval output, got %q", stdout)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected pending approval to be cleared, got %#v", runtimeRef.PendingApprovals())
	}
}

func TestHandleOrdinaryChatPendingApprovalUsesInteractiveMenuWhenAvailable(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newMainWorkflowTestRuntimeWithSuspendedFixTool()

	_, err := runtimeRef.RunStream(ctx, "implement the login form validation and update the handler", nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(runtimeRef.PendingApprovals()) != 1 {
		t.Fatalf("expected one pending approval before interactive prompt, got %#v", runtimeRef.PendingApprovals())
	}

	prevInteractive := terminalInputSupported
	terminalInputSupported = func(io.Reader, io.Writer) bool { return true }
	defer func() { terminalInputSupported = prevInteractive }()

	prevReader := newApprovalInputReader
	newApprovalInputReader = func(io.Reader) approvalInputReader {
		return bufio.NewReader(strings.NewReader("\r"))
	}
	defer func() { newApprovalInputReader = prevReader }()

	stdout := captureStdout(t, func() {
		stderr := captureStderr(t, func() {
			renderer := newCLIStreamRenderer(false)
			if err := handleOrdinaryChatPendingApproval(ctx, strings.NewReader("ignored\n"), os.Stdout, runtimeRef, renderer); err != nil {
				t.Fatalf("handleOrdinaryChatPendingApproval: %v", err)
			}
		})
		if strings.TrimSpace(stderr) != "" {
			t.Fatalf("expected no stderr output, got %q", stderr)
		}
	})
	if !strings.Contains(stdout, "Enter to confirm • ↑/↓ to move") {
		t.Fatalf("expected interactive approval menu instructions, got %q", stdout)
	}
	if !strings.Contains(stdout, "Approved call-1 -> write_file") {
		t.Fatalf("expected immediate approval output, got %q", stdout)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected pending approval to be cleared, got %#v", runtimeRef.PendingApprovals())
	}
}

func TestFormatStatusLinesHighlightsPendingApproval(t *testing.T) {
	output := formatStatusLines([]string{
		"active agent: fixer",
		"mode: fix",
		"trace: false",
		"task: stage=modify agent=fixer mode=fix detail=running write tools",
		"approval call-1: write_file (fixer)",
		"mcp file_tools: ready",
	})

	if !strings.Contains(output, "Workflow / approvals") {
		t.Fatalf("expected workflow section, got %q", output)
	}
	if !strings.Contains(output, "PENDING approval call-1: write_file (fixer)") {
		t.Fatalf("expected highlighted approval line, got %q", output)
	}
}

func TestFormatStatusLinesShowsWorkflowSnapshotAndPendingApprovals(t *testing.T) {
	output := formatStatusLines([]string{
		"active agent: fixer",
		"mode: fix",
		"trace: false",
		"workflow plan-fix-audit: awaiting_tool_approval (next=fix)",
		"approval call-1: write_file (fixer) workflow=plan-fix-audit stage=fix args={\"path\":\"a.txt\"}",
		"mcp file_tools: ready",
	})
	if !strings.Contains(output, "Workflow / approvals") {
		t.Fatalf("expected workflow section, got %q", output)
	}
	if !strings.Contains(output, "plan-fix-audit") || !strings.Contains(output, "awaiting_tool_approval") {
		t.Fatalf("expected workflow snapshot line, got %q", output)
	}
	if !strings.Contains(output, "write_file") || !strings.Contains(output, "a.txt") {
		t.Fatalf("expected approval details, got %q", output)
	}
}

func TestFormatSessionOutputShowsWorkflowAndPendingApprovalSections(t *testing.T) {
	output := formatSessionOutput(
		"planner",
		"fix",
		"code-review",
		[]string{"prompt one"},
		[]string{"write_file: suspended"},
		workflowDisplayRow{Name: "plan-fix-audit", Status: "awaiting_tool_approval", NextStage: "fix", Summary: "planner finished"},
		handoffDisplayRow{},
		routingDisplayRow{Request: "build demo", SourceAgent: "planner", TargetAgent: "fixer", TargetMode: "fix", Outcome: "waiting_confirmation", Reason: "planner result queued for execution handoff"},
		[]approvalDisplayRow{{CallID: "call-1", ToolName: "write_file", AgentID: "fixer", Stage: "fix", ArgumentsSummary: `{"path":"a.txt"}`}},
	)
	if !strings.Contains(output, "Workflow") || !strings.Contains(output, "Pending approvals") {
		t.Fatalf("expected workflow and approval sections, got %q", output)
	}
	if !strings.Contains(output, "plan-fix-audit") || !strings.Contains(output, "call-1") {
		t.Fatalf("expected structured session state, got %q", output)
	}
	if !strings.Contains(output, "Last routing") || !strings.Contains(output, "waiting_confirmation") || !strings.Contains(output, "build demo") {
		t.Fatalf("expected routing section details, got %q", output)
	}
}
func TestFormatWorkflowSummaryAddsStageSections(t *testing.T) {
	summary := formatWorkflowSummary([]string{
		"[plan/planner] scoped the work",
		"[fix/fixer] updated approval flow",
		"[audit/auditor] no regressions found",
	})
	if !strings.Contains(summary, "Completed stages") {
		t.Fatalf("expected completed stages header, got %q", summary)
	}
	if !strings.Contains(summary, "plan/planner") || !strings.Contains(summary, "audit/auditor") {
		t.Fatalf("expected grouped workflow stages, got %q", summary)
	}
}

func TestFormatHelpOutputUsesSections(t *testing.T) {
	help := formatHelpOutput()
	if !strings.Contains(help, "Commands") {
		t.Fatalf("expected commands header, got %q", help)
	}
	if !strings.Contains(help, "Explore") || !strings.Contains(help, "Control") || !strings.Contains(help, "Approval") {
		t.Fatalf("expected grouped help sections, got %q", help)
	}
	if !strings.Contains(help, "/workflow") || !strings.Contains(help, "/status") {
		t.Fatalf("expected key commands in help output, got %q", help)
	}
	if !strings.Contains(help, "/workflow <custom-name>") {
		t.Fatalf("expected custom workflow command in help output, got %q", help)
	}
	if !strings.Contains(help, "/teams [name]") {
		t.Fatalf("expected team template command in help output, got %q", help)
	}
	if !strings.Contains(help, "/team-state [run-id]") {
		t.Fatalf("expected team state command in help output, got %q", help)
	}
	if !strings.Contains(help, "/policy-rules [name]") || !strings.Contains(help, "/new-policy-rule") || !strings.Contains(help, "/new-team") || !strings.Contains(help, "/workflow-templates [name]") || !strings.Contains(help, "/new-workflow-template") {
		t.Fatalf("expected policy/workflow template commands in help output, got %q", help)
	}
	if !strings.Contains(help, "/workflow-node-metadata [type]") || !strings.Contains(help, "/expression-helpers [name]") {
		t.Fatalf("expected workflow metadata discovery commands in help output, got %q", help)
	}
	if !strings.Contains(help, "/skill-templates") || !strings.Contains(help, "/new-skill") || !strings.Contains(help, "/new-kit") {
		t.Fatalf("expected skill scaffold commands in help output, got %q", help)
	}
}

func TestFormatPolicyRulesOutput(t *testing.T) {
	rows := []policyRuleDisplayRow{{
		Name:        "risk_at_least",
		Label:       "Risk At Least",
		Description: "Pass when severity meets a threshold.",
		Source:      "built_in",
		Operator:    "risk_at_least",
		Params: []agent.WorkflowNodeFieldOption{{
			Name:        "params.minimum",
			Type:        "select",
			Description: "Minimum severity.",
			Options:     []string{"low", "medium", "high"},
		}},
	}}
	output := formatPolicyRulesOutput(rows)
	if !strings.Contains(output, "Workflow Policy Rules") || !strings.Contains(output, "risk_at_least") || !strings.Contains(output, "/new-policy-rule") {
		t.Fatalf("expected policy rule list output, got %q", output)
	}
	detail := formatPolicyRuleDetailOutput(rows[0])
	if !strings.Contains(detail, "Workflow Policy Rule") || !strings.Contains(detail, "params.minimum") || !strings.Contains(detail, "Minimum severity") {
		t.Fatalf("expected policy rule detail output, got %q", detail)
	}
}

func TestFormatWorkflowTemplatesOutput(t *testing.T) {
	rows := []workflowTemplateDisplayRow{{
		Name:        "plan-fix-audit",
		Title:       "Plan Fix Audit",
		Description: "Plan, implement, and review.",
		Category:    "software",
		Tags:        []string{"coding"},
		Stages:      3,
		Source:      "built_in",
		StageNames:  []string{"plan", "implement", "audit"},
	}}
	output := formatWorkflowTemplatesOutput(rows)
	if !strings.Contains(output, "Workflow Templates") || !strings.Contains(output, "plan-fix-audit") || !strings.Contains(output, "/new-workflow-template") {
		t.Fatalf("expected workflow template list output, got %q", output)
	}
	detail := formatWorkflowTemplateDetailOutput(rows[0])
	if !strings.Contains(detail, "Workflow Template") || !strings.Contains(detail, "plan -> implement -> audit") {
		t.Fatalf("expected workflow template detail output, got %q", detail)
	}
}

func TestFormatWorkflowMetadataDiscoveryOutputs(t *testing.T) {
	node := agent.WorkflowNodeTypeOption{
		Type:        "policy_guard",
		Label:       "Policy Guard",
		Category:    "control",
		Description: "Evaluate reusable policy rules.",
		Control:     true,
		Fields: []agent.WorkflowNodeFieldOption{{
			Name:        "policy",
			Type:        "policy",
			Required:    true,
			Description: "Policy rule name.",
		}},
		Outputs: []agent.WorkflowNodeVariableOption{{Name: "passed", Description: "Whether the policy passed."}},
	}
	nodeList := formatWorkflowNodeMetadataOutput([]agent.WorkflowNodeTypeOption{node})
	if !strings.Contains(nodeList, "Workflow Node Metadata") || !strings.Contains(nodeList, "policy_guard") || !strings.Contains(nodeList, "metadata/workflow_nodes") {
		t.Fatalf("expected workflow node metadata list output, got %q", nodeList)
	}
	nodeDetail := formatWorkflowNodeMetadataDetailOutput(node)
	for _, want := range []string{"Workflow Node Metadata", "policy_guard", "policy", "passed"} {
		if !strings.Contains(nodeDetail, want) {
			t.Fatalf("expected workflow node metadata detail to contain %q, got %q", want, nodeDetail)
		}
	}

	helper := agent.WorkflowExpressionFunctionOption{
		Name:        "risk_rank",
		Label:       "Risk Rank",
		Category:    "risk",
		Description: "Map severity to a number.",
		Signature:   "risk_rank(value)",
		InsertText:  "risk_rank(${reference})",
		ReturnType:  "number",
		MinArgs:     1,
		MaxArgs:     1,
		Modes:       []string{"condition", "policy"},
		NodeTypes:   []string{"policy_guard"},
		Args:        []agent.WorkflowExpressionFunctionArgument{{Name: "value", Type: "reference", Required: true}},
		Examples:    []string{`risk_rank(stages.audit.outputs.risk) >= 4`},
	}
	helperList := formatExpressionHelpersOutput([]agent.WorkflowExpressionFunctionOption{helper}, "policy", "policy_guard")
	if !strings.Contains(helperList, "Expression Helpers") || !strings.Contains(helperList, "risk_rank") || !strings.Contains(helperList, "metadata/expression_helpers") {
		t.Fatalf("expected expression helper list output, got %q", helperList)
	}
	helperDetail := formatExpressionHelperDetailOutput(helper)
	for _, want := range []string{"Expression Helper", "risk_rank(value)", "arguments", "value", "examples"} {
		if !strings.Contains(helperDetail, want) {
			t.Fatalf("expected expression helper detail to contain %q, got %q", want, helperDetail)
		}
	}
}

func TestFormatWorkflowSchemasOutput(t *testing.T) {
	schema := session.WorkflowSchemaSnapshot{
		Workflow:  "security-review",
		UpdatedAt: "2026-05-03T12:00:00Z",
		RunIDs:    []string{"wf-2", "wf-1"},
		Stages: map[string]session.WorkflowStageSchemaSnapshot{
			"audit": {
				Stage:    "audit",
				NodeType: "agent",
				AgentID:  "auditor",
				Skill:    "code-audit",
				Outputs: map[string]session.WorkflowValueSchemaSnapshot{
					"finding": {
						Type:     "object",
						Observed: 2,
						Fields: map[string]session.WorkflowValueSchemaSnapshot{
							"risk": {Type: "string", Observed: 2, LastRunID: "wf-2"},
						},
					},
				},
			},
		},
	}
	output := formatWorkflowSchemasOutput([]session.WorkflowSchemaSnapshot{schema})
	if !strings.Contains(output, "Workflow Schemas") || !strings.Contains(output, "security-review") || !strings.Contains(output, "/workflow-schemas --rebuild") {
		t.Fatalf("expected workflow schema list output, got %q", output)
	}
	detail := formatWorkflowSchemaDetailOutput(schema)
	for _, want := range []string{"Workflow Schema", "security-review", "audit", "finding", "risk", "last_run", "wf-2"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("expected workflow schema detail to contain %q, got %q", want, detail)
		}
	}
}

func TestFormatTeamTemplateOutputs(t *testing.T) {
	list := formatTeamTemplatesOutput(agent.TeamTemplates())
	if !strings.Contains(list, "Team Templates") || !strings.Contains(list, "software-task-team") || !strings.Contains(list, "web-research-team") || !strings.Contains(list, "customer-support-team") {
		t.Fatalf("expected team template list, got %q", list)
	}

	template, ok := agent.LoadTeamTemplate("web-research-team")
	if !ok {
		t.Fatal("expected web research team template")
	}
	detail := formatTeamTemplateDetailOutput(template)
	if !strings.Contains(detail, "asset-collector") || !strings.Contains(detail, "handoffs") || !strings.Contains(detail, "blackboard") || !strings.Contains(detail, "web-research-risk") {
		t.Fatalf("expected team template detail, got %q", detail)
	}
}

func TestFormatTeamStateOutput(t *testing.T) {
	state := agent.TeamState{
		RunID:       "wf-1",
		Workflow:    "team-flow",
		Status:      "running",
		Team:        "web-research-team",
		ActiveOwner: "auditor",
		Handoffs: []session.CollaborationMessageSnapshot{{
			FromAgent: "auditor",
			ToAgent:   "planner",
			Kind:      "team_handoff",
			Subject:   "Risk handoff",
		}},
		UnresolvedItems: []session.BlackboardEntrySnapshot{{
			Kind:   "decision",
			Status: "open",
			Title:  "Verify risk",
		}},
	}
	output := formatTeamStateOutput(state)
	if !strings.Contains(output, "Team State") || !strings.Contains(output, "web-research-team") || !strings.Contains(output, "Risk handoff") || !strings.Contains(output, "Verify risk") {
		t.Fatalf("expected formatted team state, got %q", output)
	}
}

func TestCompleteCommandTokenCompletesUniqueCommand(t *testing.T) {
	next, changed, message := completeCommandToken([]rune("/sta"), len([]rune("/sta")))
	if !changed || message != "" || string(next) != "/status " {
		t.Fatalf("expected /status completion, changed=%t message=%q next=%q", changed, message, string(next))
	}

	next, changed, message = completeCommandToken([]rune("/tea"), len([]rune("/tea")))
	if !changed || message != "" || string(next) != "/team" {
		t.Fatalf("expected shared /team prefix completion, changed=%t message=%q next=%q", changed, message, string(next))
	}

	next, changed, message = completeCommandToken([]rune("/team-s"), len([]rune("/team-s")))
	if !changed || message != "" || string(next) != "/team-state " {
		t.Fatalf("expected /team-state completion, changed=%t message=%q next=%q", changed, message, string(next))
	}

	next, changed, message = completeCommandToken([]rune("/re"), len([]rune("/re")))
	if !changed || message != "" || string(next) != "/reload" {
		t.Fatalf("expected common /reload prefix, changed=%t message=%q next=%q", changed, message, string(next))
	}
}

func TestCompleteCommandTokenIgnoresColonPrefix(t *testing.T) {
	next, changed, message := completeCommandToken([]rune(":sta"), len([]rune(":sta")))
	if changed || message != "" || string(next) != ":sta" {
		t.Fatalf("expected colon prefix to be ignored, changed=%t message=%q next=%q", changed, message, string(next))
	}
}

func TestCompleteCommandTokenUsesFuzzyMatchWhenPrefixMisses(t *testing.T) {
	next, changed, message := completeCommandToken([]rune("/hlp"), len([]rune("/hlp")))
	if !changed || message != "" || string(next) != "/help " {
		t.Fatalf("expected fuzzy /help completion, changed=%t message=%q next=%q", changed, message, string(next))
	}
}

func TestCompleteCommandTokenListsFuzzyMatches(t *testing.T) {
	next, changed, message := completeCommandToken([]rune("/skl"), len([]rune("/skl")))
	if changed || string(next) != "/skl" {
		t.Fatalf("expected no direct completion for ambiguous fuzzy query, changed=%t next=%q", changed, string(next))
	}
	if !strings.Contains(message, "Fuzzy command matches") || !strings.Contains(message, "/new-skill") || !strings.Contains(message, "/skill-templates") {
		t.Fatalf("expected fuzzy command suggestions, got %q", message)
	}
}

func TestCompleteInputTokenIgnoresPlainText(t *testing.T) {
	next, changed, message := completeInputToken([]rune("hello world"), len([]rune("hello world")), t.TempDir())
	if changed || message != "" || string(next) != "hello world" {
		t.Fatalf("expected plain input tab to be ignored, changed=%t message=%q next=%q", changed, message, string(next))
	}
}

func TestInputEscapeSequencesEditLineWithoutInsertingBytes(t *testing.T) {
	if action := readInputEscapeAction(bufio.NewReader(strings.NewReader("D")), '['); action != inputEscapeLeft {
		t.Fatalf("expected left action, got %q", action)
	}
	if action := readInputEscapeAction(bufio.NewReader(strings.NewReader("A")), '['); action != inputEscapeUp {
		t.Fatalf("expected up action, got %q", action)
	}
	if action := readInputEscapeAction(bufio.NewReader(strings.NewReader("B")), '['); action != inputEscapeDown {
		t.Fatalf("expected down action, got %q", action)
	}
	if action := readInputEscapeAction(bufio.NewReader(strings.NewReader("H")), '['); action != inputEscapeHome {
		t.Fatalf("expected home action, got %q", action)
	}
	if action := readInputEscapeAction(bufio.NewReader(strings.NewReader("3~")), '['); action != inputEscapeDelete {
		t.Fatalf("expected delete action, got %q", action)
	}

	buffer := []rune("abc")
	cursor := 1
	var output bytes.Buffer
	applyInputEscapeAction(&output, "> ", &buffer, &cursor, inputEscapeDelete)
	if string(buffer) != "ac" || cursor != 1 {
		t.Fatalf("expected delete at cursor without escape bytes in buffer, buffer=%q cursor=%d", string(buffer), cursor)
	}
}

func TestInteractiveInputHistoryNavigationRestoresDraft(t *testing.T) {
	reader := &interactiveInputLineReader{}
	reader.rememberInput("")
	reader.rememberInput("/status")
	reader.rememberInput("hello")
	reader.rememberInput("hello")
	if len(reader.history) != 2 {
		t.Fatalf("expected empty and consecutive duplicate inputs to be skipped, got %#v", reader.history)
	}

	buffer := []rune("dra")
	historyIndex := len(reader.history)
	var draft []rune
	var changed bool
	buffer, changed = reader.navigateInputHistory(buffer, &historyIndex, &draft, inputEscapeUp)
	if !changed || string(buffer) != "hello" || historyIndex != 1 {
		t.Fatalf("expected newest history entry, changed=%t buffer=%q index=%d", changed, string(buffer), historyIndex)
	}
	buffer, changed = reader.navigateInputHistory(buffer, &historyIndex, &draft, inputEscapeUp)
	if !changed || string(buffer) != "/status" || historyIndex != 0 {
		t.Fatalf("expected oldest history entry, changed=%t buffer=%q index=%d", changed, string(buffer), historyIndex)
	}
	buffer, changed = reader.navigateInputHistory(buffer, &historyIndex, &draft, inputEscapeDown)
	if !changed || string(buffer) != "hello" || historyIndex != 1 {
		t.Fatalf("expected newer history entry, changed=%t buffer=%q index=%d", changed, string(buffer), historyIndex)
	}
	buffer, changed = reader.navigateInputHistory(buffer, &historyIndex, &draft, inputEscapeDown)
	if !changed || string(buffer) != "dra" || historyIndex != len(reader.history) {
		t.Fatalf("expected draft restoration, changed=%t buffer=%q index=%d", changed, string(buffer), historyIndex)
	}
}

func TestFormatInlineCommandSuggestionsOnlyAtLineStart(t *testing.T) {
	if message := formatInlineCommandSuggestions([]rune("/")); !strings.Contains(message, "/help") {
		t.Fatalf("expected command suggestions for command prefix, got %q", message)
	}
	if message := formatInlineCommandSuggestions([]rune(":")); message != "" {
		t.Fatalf("expected no command suggestions for colon prefix, got %q", message)
	}
	if message := formatInlineCommandSuggestions([]rune("say :")); message != "" {
		t.Fatalf("expected no command suggestions for colon inside normal text, got %q", message)
	}
}

func TestWorkflowUsageListsCustomWorkflowFiles(t *testing.T) {
	runtimeHome := t.TempDir()
	for _, name := range []string{"release-check", "security-review"} {
		dir := filepath.Join(runtimeHome, "workflows", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll workflow dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte("name: "+name+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile workflow.yaml: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(runtimeHome, "workflows", "draft-only"), 0o755); err != nil {
		t.Fatalf("MkdirAll draft workflow dir: %v", err)
	}

	names := availableWorkflowNamesFromRoot(runtimeHome)
	joined := strings.Join(names, ", ")
	for _, want := range []string{"plan-fix-audit", "skill-chain", "release-check", "security-review"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected workflow %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "draft-only") {
		t.Fatalf("expected workflow dir without workflow.yaml to be ignored, got %q", joined)
	}
	usage := workflowUsage(nil)
	if !strings.Contains(usage, "custom workflows load from workflows/<name>/workflow.yaml") {
		t.Fatalf("expected custom workflow guidance, got %q", usage)
	}
}

func TestFormatSkillTemplatesOutputListsScaffolds(t *testing.T) {
	output := formatSkillTemplatesOutput([]skillTemplateDisplayRow{{
		Name:           "code-audit",
		Description:    "Review code",
		Mode:           "audit",
		PreferredAgent: "auditor",
		OutputKind:     "findings",
	}})
	if !strings.Contains(output, "Skill templates") || !strings.Contains(output, "code-audit") {
		t.Fatalf("expected skill template output, got %q", output)
	}
	if !strings.Contains(output, "mode") || !strings.Contains(output, "auditor") || !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected styled template metadata, got %q", output)
	}
}

func TestHandleNewSkillCommandCreatesValidSkill(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "existing-skill")
	manager, err := skillpkg.NewManager(root)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	output := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/new-skill code-audit custom-audit", manager, nil, nil); !handled {
			t.Fatal("expected /new-skill to be handled")
		}
	})
	if !strings.Contains(output, "created custom-audit") {
		t.Fatalf("expected creation output, got %q", output)
	}

	path := filepath.Join(root, "custom-audit", "SKILL.md")
	created, err := skillpkg.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if created.Name != "custom-audit" || created.PreferredAgent != "auditor" || created.Mode != "audit" {
		t.Fatalf("unexpected generated skill: %#v", created)
	}
	assertFileContains(t, path, "## Example Request")
	assertFileContains(t, path, "## Extension Points")
}

func TestHandleScaffoldCommandsCreateToolAgentAndWorkflow(t *testing.T) {
	runtimeHome := t.TempDir()
	skillRoot := filepath.Join(runtimeHome, "skills")
	writeTestSkill(t, skillRoot, "existing-skill")
	manager, err := skillpkg.NewManager(skillRoot)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	output := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/new-tool python notes-helper", manager, nil, nil); !handled {
			t.Fatal("expected /new-tool to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-agent researcher", manager, nil, nil); !handled {
			t.Fatal("expected /new-agent to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-provider deepseek", manager, nil, nil); !handled {
			t.Fatal("expected /new-provider to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-workflow release-check", manager, nil, nil); !handled {
			t.Fatal("expected /new-workflow to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-workflow security-review --template human-input-security-review", manager, nil, nil); !handled {
			t.Fatal("expected templated /new-workflow to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-kit software-engineering acme-platform", manager, nil, nil); !handled {
			t.Fatal("expected /new-kit to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-kit agent-framework framework-starter", manager, nil, nil); !handled {
			t.Fatal("expected agent-framework /new-kit to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-kit customer-support support-desk", manager, nil, nil); !handled {
			t.Fatal("expected customer-support /new-kit to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-kit software-engineering acme-platform-full --materialize", manager, nil, nil); !handled {
			t.Fatal("expected materialized /new-kit to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-policy-rule risk-threshold high-risk-gate", manager, nil, nil); !handled {
			t.Fatal("expected /new-policy-rule to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-team software-review custom-review-team", manager, nil, nil); !handled {
			t.Fatal("expected /new-team to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-team agent-framework custom-framework-team", manager, nil, nil); !handled {
			t.Fatal("expected agent-framework /new-team to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-team customer-support custom-support-team", manager, nil, nil); !handled {
			t.Fatal("expected customer-support /new-team to be handled")
		}
		if handled := handleCommand(context.Background(), "/new-workflow-template plan-fix-audit custom-plan-template", manager, nil, nil); !handled {
			t.Fatal("expected /new-workflow-template to be handled")
		}
	})
	if !strings.Contains(output, "notes-helper.py") || !strings.Contains(output, "researcher.yaml") || !strings.Contains(output, "deepseek.yaml") || !strings.Contains(output, "release-check") || !strings.Contains(output, "template=human-input-security-review") || !strings.Contains(output, "acme-platform") || !strings.Contains(output, "framework-starter") || !strings.Contains(output, "support-desk") || !strings.Contains(output, "acme-platform-full-agent") || !strings.Contains(output, "high-risk-gate") || !strings.Contains(output, "custom-review-team") || !strings.Contains(output, "custom-framework-team") || !strings.Contains(output, "custom-support-team") || !strings.Contains(output, "custom-plan-template") {
		t.Fatalf("expected scaffold output, got %q", output)
	}
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "notes-helper.py"), "WORKSPACE_ROOT")
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "notes-helper.py"), "resolve_path")
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "notes-helper.py"), "relative_path")
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "notes-helper.py"), "is_error")
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "notes-helper.py"), "escapes workspace root")
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "notes-helper.py"), "resolves outside workspace root")
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "notes-helper.py"), "write_text")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "mcp_servers", "notes-helper.yaml"), "notes_helper")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "mcp_servers", "notes-helper.yaml"), "configs/mcp_servers")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "agents", "researcher.yaml"), "researcher:")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "agents", "researcher.yaml"), "allowed_tools")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "providers", "deepseek.yaml"), "deepseek:")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "providers", "deepseek.yaml"), "api_key: ${DEEPSEEK_API_KEY}")
	assertFileContains(t, filepath.Join(runtimeHome, "workflows", "release-check", "workflow.yaml"), "skill: execution-plan")
	assertFileContains(t, filepath.Join(runtimeHome, "workflows", "release-check", "WORKFLOW.md"), "Workflow")
	assertFileContains(t, filepath.Join(runtimeHome, "workflows", "security-review", "workflow.yaml"), "node_type: input_gate")
	assertFileContains(t, filepath.Join(runtimeHome, "workflows", "security-review", "workflow.yaml"), "web-vulnerability-research")
	assertFileContains(t, filepath.Join(runtimeHome, "kits", "acme-platform", "kit.yaml"), "kind: goflow.kit")
	assertFileContains(t, filepath.Join(runtimeHome, "kits", "acme-platform", "kit.yaml"), "software-engineering")
	assertFileContains(t, filepath.Join(runtimeHome, "kits", "acme-platform", "kit.yaml"), "plan-fix-audit")
	assertFileContains(t, filepath.Join(runtimeHome, "kits", "framework-starter", "kit.yaml"), "agent-framework-extension")
	assertFileContains(t, filepath.Join(runtimeHome, "kits", "framework-starter", "kit.yaml"), "framework-extension-team")
	assertFileContains(t, filepath.Join(runtimeHome, "kits", "support-desk", "kit.yaml"), "customer-support-triage")
	assertFileContains(t, filepath.Join(runtimeHome, "kits", "support-desk", "kit.yaml"), "customer-support-team")
	assertFileContains(t, filepath.Join(runtimeHome, "kits", "acme-platform-full", "kit.yaml"), "materialized")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "agents", "acme-platform-full-agent.yaml"), "acme_platform_full_helper/read_text")
	assertFileContains(t, filepath.Join(runtimeHome, "skills", "acme-platform-full-skill", "SKILL.md"), "workflow-handoff")
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "acme-platform-full-helper.py"), "WORKSPACE_ROOT")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "mcp_servers", "acme-platform-full-helper.yaml"), "isolation: container")
	assertFileContains(t, filepath.Join(runtimeHome, "workflows", "acme-platform-full-workflow", "workflow.yaml"), "policy_guard")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "workflows", "acme-platform-full-template.yaml"), "goflow.workflow_template_resource")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "teams", "acme-platform-full-team.yaml"), "goflow.team_template_resource")
	assertFileContains(t, filepath.Join(runtimeHome, "policies", "workflow_rules", "acme-platform-full-gate.yaml"), "ref_truthy")
	assertFileContains(t, filepath.Join(runtimeHome, "policies", "workflow_rules", "high-risk-gate.yaml"), "kind: goflow.workflow_policy_rule")
	assertFileContains(t, filepath.Join(runtimeHome, "policies", "workflow_rules", "high-risk-gate.yaml"), "operator: risk_at_least")
	assertFileContains(t, filepath.Join(runtimeHome, "policies", "workflow_rules", "high-risk-gate.yaml"), "minimum: high")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "teams", "custom-review-team.yaml"), "kind: goflow.team_template_resource")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "teams", "custom-review-team.yaml"), "role_templates:")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "teams", "custom-review-team.yaml"), "quorum_presets:")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "teams", "custom-framework-team.yaml"), "agent-framework-extension")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "teams", "custom-framework-team.yaml"), "extension-review")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "teams", "custom-support-team.yaml"), "customer-support-triage")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "teams", "custom-support-team.yaml"), "support-review")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "workflows", "custom-plan-template.yaml"), "kind: goflow.workflow_template_resource")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "workflows", "custom-plan-template.yaml"), "graph:")
	assertFileContains(t, filepath.Join(runtimeHome, "templates", "workflows", "custom-plan-template.yaml"), "name: custom-plan-template")
}

func TestGeneratedPythonMCPScaffoldRunsWithWorkspaceSafety(t *testing.T) {
	python, args := findPythonForScaffoldTest()
	if python == "" {
		t.Skip("python3/python not found on PATH")
	}
	runtimeHome := t.TempDir()
	toolPath := filepath.Join(runtimeHome, "mcp_servers", "safe-helper.py")
	if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	if err := os.WriteFile(toolPath, []byte(renderPythonMCPServerTemplate("safe-helper")), 0o644); err != nil {
		t.Fatalf("write generated scaffold: %v", err)
	}
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "notes.txt"), []byte("hello scaffold\n"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	outsideRoot := t.TempDir()
	outsideDir := filepath.Join(outsideRoot, "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	symlinkSupported := true
	if err := os.Symlink(outsideDir, filepath.Join(workspaceRoot, "outside-link")); err != nil {
		symlinkSupported = false
	}

	requests := []string{
		scaffoldMCPRequest(1, "read_text", map[string]any{"path": "notes.txt"}),
		scaffoldMCPRequest(2, "write_text", map[string]any{"path": "created.txt", "content": "created", "overwrite": true}),
		scaffoldMCPRequest(3, "read_text", map[string]any{"path": "../outside.txt"}),
	}
	if symlinkSupported {
		requests = append(requests, scaffoldMCPRequest(4, "write_text", map[string]any{"path": "outside-link/escape.txt", "content": "escape", "overwrite": true}))
	}
	cmdArgs := append(append([]string(nil), args...), toolPath)
	cmd := exec.Command(python, cmdArgs...)
	cmd.Env = append(os.Environ(), "GOFLOW_WORKSPACE_ROOT="+workspaceRoot)
	cmd.Dir = runtimeHome
	cmd.Stdin = strings.NewReader(strings.Join(requests, "\n") + "\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated scaffold failed: %v\n%s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if want := len(requests); len(lines) != want {
		t.Fatalf("expected %d scaffold responses, got %d: %s", want, len(lines), output)
	}
	readPayload := scaffoldMCPResultContent(t, lines[0])
	if readPayload["relative_path"] != "notes.txt" || !strings.Contains(fmt.Sprint(readPayload["content"]), "hello scaffold") {
		t.Fatalf("unexpected read_text payload: %#v", readPayload)
	}
	writePayload := scaffoldMCPResultContent(t, lines[1])
	if writePayload["relative_path"] != "created.txt" || writePayload["bytes_written"].(float64) != 7 {
		t.Fatalf("unexpected write_text payload: %#v", writePayload)
	}
	if content, err := os.ReadFile(filepath.Join(workspaceRoot, "created.txt")); err != nil || string(content) != "created" {
		t.Fatalf("expected created workspace file, content=%q err=%v", content, err)
	}
	scaffoldMCPExpectToolError(t, lines[2], "escapes workspace root")
	if symlinkSupported {
		scaffoldMCPExpectToolError(t, lines[3], "resolves outside workspace root")
		if _, err := os.Stat(filepath.Join(outsideDir, "escape.txt")); !os.IsNotExist(err) {
			t.Fatalf("expected symlink-parent write to be rejected, stat err=%v", err)
		}
	}
}

func findPythonForScaffoldTest() (string, []string) {
	for _, candidate := range []struct {
		name string
		args []string
	}{
		{name: "python3"},
		{name: "python"},
		{name: "py", args: []string{"-3"}},
	} {
		path, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		versionArgs := append(append([]string(nil), candidate.args...), "--version")
		if err := exec.Command(path, versionArgs...).Run(); err != nil {
			continue
		}
		return path, candidate.args
	}
	return "", nil
}

func scaffoldMCPRequest(id int, name string, arguments map[string]any) string {
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	})
	return string(data)
}

func scaffoldMCPResultContent(t *testing.T, line string) map[string]any {
	t.Helper()
	var response struct {
		Result struct {
			Content string `json:"content"`
			IsError bool   `json:"is_error"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		t.Fatalf("decode scaffold response %q: %v", line, err)
	}
	if response.Result.IsError {
		t.Fatalf("expected successful scaffold response, got %q", line)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Result.Content), &payload); err != nil {
		t.Fatalf("decode scaffold content %q: %v", response.Result.Content, err)
	}
	return payload
}

func scaffoldMCPExpectToolError(t *testing.T, line, want string) {
	t.Helper()
	var response struct {
		Result struct {
			Content string `json:"content"`
			IsError bool   `json:"is_error"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		t.Fatalf("decode scaffold error response %q: %v", line, err)
	}
	if !response.Result.IsError || !strings.Contains(response.Result.Content, want) {
		t.Fatalf("expected scaffold tool error containing %q, got %q", want, line)
	}
}

func TestScaffoldValidationRejectsIncompleteTemplates(t *testing.T) {
	if err := validatePythonMCPScaffold("tools/list only"); err == nil {
		t.Fatal("expected incomplete python MCP scaffold to fail validation")
	}
	if err := validateAgentScaffold("worker", "worker:\n  provider: primary\n"); err == nil {
		t.Fatal("expected incomplete agent scaffold to fail validation")
	}
	if err := validateProviderScaffold("primary", "primary:\n  provider: openai-compatible\n"); err == nil {
		t.Fatal("expected incomplete provider scaffold to fail validation")
	}
	if err := validateWorkflowScaffold("release", "name: release\n"); err == nil {
		t.Fatal("expected incomplete workflow scaffold to fail validation")
	}
}

func TestFormatAgentsOutputMarksActiveAgent(t *testing.T) {
	output := formatAgentsOutput([]agentDisplayRow{{Name: "planner", Description: "Plans changes", Provider: "primary", Mode: "plan", ToolPolicy: "allow", AllowedToolKinds: []string{"read", "unknown"}, Active: true}, {Name: "fixer", Description: "Applies approved changes", Provider: "primary", Mode: "fix", ToolPolicy: "confirm", AllowedToolKinds: []string{"read", "write"}}})
	if !strings.Contains(output, "planner") || !strings.Contains(output, "fixer") {
		t.Fatalf("expected agents in output, got %q", output)
	}
	if !strings.Contains(output, "Plans changes") || !strings.Contains(output, "policy") || !strings.Contains(output, "allow") {
		t.Fatalf("expected rich agent metadata, got %q", output)
	}
	if !strings.Contains(output, "active") || !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected styled active agent marker, got %q", output)
	}
	if !strings.Contains(output, "summary") || !strings.Contains(output, "total=2") || !strings.Contains(output, "policies=allow=1,confirm=1") {
		t.Fatalf("expected agent summary counts, got %q", output)
	}
	if !strings.Contains(output, "mode") || !strings.Contains(output, "plan") || !strings.Contains(output, "fix") {
		t.Fatalf("expected agents grouped by mode, got %q", output)
	}
}

func TestFormatSkillsOutputIncludesActivationMetadata(t *testing.T) {
	output := formatSkillsOutput([]skillDisplayRow{{
		Name:           "code-audit",
		Description:    "Review code",
		Mode:           "audit",
		PreferredAgent: "auditor",
		OutputKind:     "findings",
		Keywords:       []string{"security", "audit"},
		NextSkills:     []string{"code-writing"},
	}})
	if !strings.Contains(output, "code-audit") || !strings.Contains(output, "Review code") {
		t.Fatalf("expected skill summary, got %q", output)
	}
	if !strings.Contains(output, "mode") || !strings.Contains(output, "audit") || !strings.Contains(output, "activates") {
		t.Fatalf("expected skill metadata, got %q", output)
	}
	if !strings.Contains(output, "next") || !strings.Contains(output, "code-writing") {
		t.Fatalf("expected follow-up skill metadata, got %q", output)
	}
	if !strings.Contains(output, "summary") || !strings.Contains(output, "total=1") || !strings.Contains(output, "modes=audit=1") {
		t.Fatalf("expected skill summary counts, got %q", output)
	}
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected colored skills output, got %q", output)
	}
}

func TestHandleCommandAgentsIncludesAllowedTools(t *testing.T) {
	runtimeRef, err := agent.NewRuntime(&config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "planner",
		Agents: map[string]config.AgentProfile{
			"planner": {
				Name:             "Planner",
				Description:      "Plans changes",
				Provider:         "planner",
				Mode:             "plan",
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindUnknown},
				AllowedTools:     []string{"z_read", "a_read"},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}, map[string]interfaces.LLMClient{
		"planner": &workflowStubLLMClient{},
	}, nil, &mainTestMCP{}, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	output := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/agents", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /agents command to be handled")
		}
	})
	if !strings.Contains(output, "allowlist") || !strings.Contains(output, "a_read, z_read") {
		t.Fatalf("expected allowed tools in sorted order, got %q", output)
	}
	if !strings.Contains(output, "kinds") || !strings.Contains(output, "read, unknown") {
		t.Fatalf("expected allowed tool kinds in sorted order, got %q", output)
	}
}

func TestHandleCommandKitsListsAndShowsManifest(t *testing.T) {
	runtimeHome := t.TempDir()
	kitDir := filepath.Join(runtimeHome, "kits", "acme-platform")
	if err := os.MkdirAll(kitDir, 0o755); err != nil {
		t.Fatalf("mkdir kit: %v", err)
	}
	kitBody := `kind: goflow.kit
version: 1
name: acme-platform
title: Acme Platform Kit
description: Reusable platform engineering setup.
category: software
tags: [platform, coding]
providers: [primary]
agents: [chat, fixer]
skills: [execution-plan, code-writing]
tools: [read_file, write_file]
workflows: [plan-fix-audit]
workflow_templates: [software-team-review-gate]
team_templates: [software-task-team]
policy_rules: [expression]
required_env: [GOFLOW_MISSING_TEST_ENV]
examples:
  - title: Improve project
    request: Optimize this project.
    workflow: plan-fix-audit
    agent: chat
metadata:
  owner: local
`
	if err := os.WriteFile(filepath.Join(kitDir, "kit.yaml"), []byte(kitBody), 0o644); err != nil {
		t.Fatalf("write kit: %v", err)
	}
	runtimeRef, err := agent.NewRuntime(&config.Config{
		RuntimeHome:   runtimeHome,
		Audit:         config.AuditConfig{},
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		Providers:     map[string]config.LLMConfig{"primary": {Provider: "openai-compatible", Model: "test"}},
		WorkspaceRoot: filepath.Join(t.TempDir(), "workspace"),
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:       "Chat",
				Provider:   "primary",
				Mode:       "chat",
				ToolPolicy: config.ToolPolicyAllow,
			},
		},
	}, map[string]interfaces.LLMClient{
		"primary": &workflowStubLLMClient{},
	}, nil, &mainTestMCP{}, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	listOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/kits", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /kits command to be handled")
		}
	})
	if !strings.Contains(listOutput, "Vertical Kits") || !strings.Contains(listOutput, "acme-platform") || !strings.Contains(listOutput, "warnings=1") || !strings.Contains(listOutput, "required environment variable GOFLOW_MISSING_TEST_ENV is not set") {
		t.Fatalf("expected kit list with warning, got %q", listOutput)
	}

	detailOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/kits acme-platform", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /kits detail command to be handled")
		}
	})
	for _, want := range []string{"Vertical Kit", "Acme Platform Kit", "providers", "primary", "workflow_templates", "software-team-review-gate", "examples", "Optimize this project.", "metadata", "owner=local"} {
		if !strings.Contains(detailOutput, want) {
			t.Fatalf("expected kit detail to contain %q, got %q", want, detailOutput)
		}
	}
}

func TestHandleCommandKitsExportsAndImportsBundle(t *testing.T) {
	sourceHome := t.TempDir()
	kitDir := filepath.Join(sourceHome, "kits", "acme-platform")
	if err := os.MkdirAll(kitDir, 0o755); err != nil {
		t.Fatalf("mkdir source kit: %v", err)
	}
	kitBody := `kind: goflow.kit
version: 1
name: acme-platform
title: Acme Platform Kit
category: software
workflows: [delivery-flow]
workflow_templates: [software-team-review-gate]
team_templates: [software-task-team]
policy_rules: [expression]
`
	if err := os.WriteFile(filepath.Join(kitDir, "kit.yaml"), []byte(kitBody), 0o644); err != nil {
		t.Fatalf("write source kit: %v", err)
	}
	workflowDir := filepath.Join(sourceHome, "workflows", "delivery-flow")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatalf("mkdir workflow: %v", err)
	}
	workflowBody := `name: delivery-flow
description: Delivery workflow.
stages:
  - name: plan
    agent: chat
    skill: execution-plan
`
	if err := os.WriteFile(filepath.Join(workflowDir, "workflow.yaml"), []byte(workflowBody), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	sourceRuntime := newMainTestRuntimeForHome(t, sourceHome)

	exportOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/kits acme-platform --export", nil, nil, sourceRuntime); !handled {
			t.Fatal("expected /kits --export command to be handled")
		}
	})
	if !strings.Contains(exportOutput, `"kind": "goflow.kit_bundle"`) ||
		!strings.Contains(exportOutput, `"delivery-flow"`) ||
		!strings.Contains(exportOutput, `"software-team-review-gate"`) {
		t.Fatalf("expected kit bundle export, got %q", exportOutput)
	}

	targetHome := t.TempDir()
	targetRuntime := newMainTestRuntimeForHome(t, targetHome)
	bundlePath := filepath.Join(t.TempDir(), "kit-bundle.json")
	if err := os.WriteFile(bundlePath, []byte(exportOutput), 0o644); err != nil {
		t.Fatalf("write exported bundle: %v", err)
	}
	importOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/kits --import "+bundlePath+" --replace", nil, nil, targetRuntime); !handled {
			t.Fatal("expected /kits --import command to be handled")
		}
	})
	if !strings.Contains(importOutput, "Kit Bundle Import") ||
		!strings.Contains(importOutput, "saved") ||
		!strings.Contains(importOutput, "workflow/delivery-flow") ||
		!strings.Contains(importOutput, "kit/acme-platform") {
		t.Fatalf("expected kit bundle import summary, got %q", importOutput)
	}
	assertFileContains(t, filepath.Join(targetHome, "kits", "acme-platform", "kit.yaml"), "kind: goflow.kit")
	assertFileContains(t, filepath.Join(targetHome, "workflows", "delivery-flow", "workflow.yaml"), "name: delivery-flow")
	assertFileContains(t, filepath.Join(targetHome, "templates", "workflows", "software-team-review-gate.yaml"), "kind: goflow.workflow_template_resource")
	assertFileContains(t, filepath.Join(targetHome, "templates", "teams", "software-task-team.yaml"), "kind: goflow.team_template_resource")
}

func TestHandleCommandWorkflowSchemasListsShowsAndRebuilds(t *testing.T) {
	state := session.New(8)
	_, err := state.ImportWorkflowSchema(session.WorkflowSchemaSnapshot{
		Workflow:  "expression-flow",
		UpdatedAt: "2026-05-03T12:00:00Z",
		RunIDs:    []string{"wf-1"},
		Stages: map[string]session.WorkflowStageSchemaSnapshot{
			"collect": {
				Stage:    "collect",
				NodeType: "agent",
				AgentID:  "planner",
				Outputs: map[string]session.WorkflowValueSchemaSnapshot{
					"payload": {
						Type: "object",
						Fields: map[string]session.WorkflowValueSchemaSnapshot{
							"risk": {Type: "string", Observed: 1, LastRunID: "wf-1"},
						},
					},
				},
			},
		},
	}, false)
	if err != nil {
		t.Fatalf("ImportWorkflowSchema: %v", err)
	}
	runtimeRef, err := agent.NewRuntime(&config.Config{
		RuntimeHome:   t.TempDir(),
		Audit:         config.AuditConfig{},
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		Providers:     map[string]config.LLMConfig{"primary": {Provider: "openai-compatible", Model: "test"}},
		WorkspaceRoot: filepath.Join(t.TempDir(), "workspace"),
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:       "Chat",
				Provider:   "primary",
				Mode:       "chat",
				ToolPolicy: config.ToolPolicyAllow,
			},
		},
	}, map[string]interfaces.LLMClient{
		"primary": &workflowStubLLMClient{},
	}, nil, &mainTestMCP{}, state, runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	listOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-schemas", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-schemas command to be handled")
		}
	})
	if !strings.Contains(listOutput, "Workflow Schemas") || !strings.Contains(listOutput, "expression-flow") {
		t.Fatalf("expected workflow schema list, got %q", listOutput)
	}

	detailOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-schemas expression-flow", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-schemas detail command to be handled")
		}
	})
	if !strings.Contains(detailOutput, "Workflow Schema") || !strings.Contains(detailOutput, "payload") || !strings.Contains(detailOutput, "risk") {
		t.Fatalf("expected workflow schema detail, got %q", detailOutput)
	}

	jsonOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-schemas expression-flow --json", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-schemas --json command to be handled")
		}
	})
	if !strings.Contains(jsonOutput, `"workflow": "expression-flow"`) || !strings.Contains(jsonOutput, `"payload"`) {
		t.Fatalf("expected workflow schema json detail, got %q", jsonOutput)
	}

	exportOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-schemas expression-flow --export", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-schemas --export command to be handled")
		}
	})
	if !strings.Contains(exportOutput, `"kind": "goflow.workflow_schemas"`) || !strings.Contains(exportOutput, `"version": 2`) || !strings.Contains(exportOutput, `"schemas"`) {
		t.Fatalf("expected workflow schema export bundle, got %q", exportOutput)
	}

	clearOneOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-schemas expression-flow --clear", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-schemas --clear command to be handled")
		}
	})
	if !strings.Contains(clearOneOutput, "workflow schema cleared") {
		t.Fatalf("expected clear one output, got %q", clearOneOutput)
	}
	if _, ok := runtimeRef.WorkflowSchema("expression-flow"); ok {
		t.Fatalf("expected expression-flow schema to be cleared")
	}

	importPath := filepath.Join(t.TempDir(), "schema-bundle.json")
	if err := os.WriteFile(importPath, []byte(exportOutput), 0o644); err != nil {
		t.Fatalf("write schema import bundle: %v", err)
	}
	importOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-schemas --import "+importPath, nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-schemas --import command to be handled")
		}
	})
	if !strings.Contains(importOutput, "workflow schemas imported") || !strings.Contains(importOutput, "1") {
		t.Fatalf("expected workflow schema import output, got %q", importOutput)
	}
	if _, ok := runtimeRef.WorkflowSchema("expression-flow"); !ok {
		t.Fatalf("expected expression-flow schema to be imported")
	}
	runtimeRef.ClearWorkflowSchema("expression-flow")

	if _, err := state.ImportWorkflowSchema(session.WorkflowSchemaSnapshot{
		Workflow: "rebuild-flow",
		Stages: map[string]session.WorkflowStageSchemaSnapshot{
			"done": {Stage: "done", Outputs: map[string]session.WorkflowValueSchemaSnapshot{"summary": {Type: "string"}}},
		},
	}, false); err != nil {
		t.Fatalf("reimport workflow schema for rebuild: %v", err)
	}
	rebuildOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-schemas --rebuild", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-schemas --rebuild command to be handled")
		}
	})
	if !strings.Contains(rebuildOutput, "Workflow Schemas") {
		t.Fatalf("expected workflow schema rebuild output, got %q", rebuildOutput)
	}

	if _, err := state.ImportWorkflowSchema(session.WorkflowSchemaSnapshot{
		Workflow: "another-flow",
		Stages: map[string]session.WorkflowStageSchemaSnapshot{
			"done": {Stage: "done", Outputs: map[string]session.WorkflowValueSchemaSnapshot{"summary": {Type: "string"}}},
		},
	}, false); err != nil {
		t.Fatalf("reimport workflow schema: %v", err)
	}
	clearAllOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-schemas --clear", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-schemas --clear command to be handled")
		}
	})
	if !strings.Contains(clearAllOutput, "workflow schemas cleared") || !strings.Contains(clearAllOutput, "1") {
		t.Fatalf("expected clear all output, got %q", clearAllOutput)
	}
	if schemas := runtimeRef.WorkflowSchemas(); len(schemas) != 0 {
		t.Fatalf("expected all workflow schemas to be cleared, got %#v", schemas)
	}
}

func TestHandleCommandWorkflowMetadataDiscovery(t *testing.T) {
	runtimeRef, err := agent.NewRuntime(&config.Config{
		RuntimeHome:   t.TempDir(),
		Audit:         config.AuditConfig{},
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		Providers:     map[string]config.LLMConfig{"primary": {Provider: "openai-compatible", Model: "test"}},
		WorkspaceRoot: filepath.Join(t.TempDir(), "workspace"),
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:       "Chat",
				Provider:   "primary",
				Mode:       "chat",
				ToolPolicy: config.ToolPolicyAllow,
			},
		},
	}, map[string]interfaces.LLMClient{
		"primary": &workflowStubLLMClient{},
	}, nil, &mainTestMCP{}, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	nodeList := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-node-metadata", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-node-metadata command to be handled")
		}
	})
	if !strings.Contains(nodeList, "Workflow Node Metadata") || !strings.Contains(nodeList, "policy_guard") || !strings.Contains(nodeList, "custom metadata") {
		t.Fatalf("expected workflow node metadata list, got %q", nodeList)
	}
	nodeDetail := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-node-metadata policy_guard", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-node-metadata detail command to be handled")
		}
	})
	if !strings.Contains(nodeDetail, "Workflow Node Metadata") || !strings.Contains(nodeDetail, "policy_guard") || !strings.Contains(nodeDetail, "outputs") {
		t.Fatalf("expected workflow node metadata detail, got %q", nodeDetail)
	}

	helperList := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/expression-helpers --mode policy --node-type policy_guard", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /expression-helpers command to be handled")
		}
	})
	if !strings.Contains(helperList, "Expression Helpers") || !strings.Contains(helperList, "risk_rank") || !strings.Contains(helperList, "filter") {
		t.Fatalf("expected expression helper list, got %q", helperList)
	}
	helperDetail := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/workflow-expression-functions risk_rank", nil, nil, runtimeRef); !handled {
			t.Fatal("expected /workflow-expression-functions detail command to be handled")
		}
	})
	if !strings.Contains(helperDetail, "Expression Helper") || !strings.Contains(helperDetail, "risk_rank") || !strings.Contains(helperDetail, "arguments") {
		t.Fatalf("expected expression helper detail, got %q", helperDetail)
	}
}

func TestHandleCommandConfigDiagnostics(t *testing.T) {
	runtimeHome := t.TempDir()
	configDir := filepath.Join(runtimeHome, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "goflow.yaml")
	if err := os.WriteFile(configPath, []byte(`
runtime_home: `+quoteYAMLPath(runtimeHome)+`
workspace_root: workspace
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    model: test
    base_url: http://127.0.0.1
agents:
  chat:
    provider: primary
    mode: chat
    tool_policy: confirm
    allowed_tool_kinds: [read]
skill:
  directory: skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runtimeRef, err := agent.NewRuntime(&config.Config{
		RuntimeHome:   runtimeHome,
		ConfigPath:    configPath,
		Audit:         config.AuditConfig{},
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		Providers:     map[string]config.LLMConfig{"primary": {Provider: "openai-compatible", Model: "test", BaseURL: "http://127.0.0.1"}},
		WorkspaceRoot: filepath.Join(runtimeHome, "workspace"),
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:             "Chat",
				Provider:         "primary",
				Mode:             "chat",
				ToolPolicy:       config.ToolPolicyConfirm,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
			},
		},
	}, map[string]interfaces.LLMClient{
		"primary": &workflowStubLLMClient{},
	}, nil, &mainTestMCP{}, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	output := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/config-diagnostics", nil, nil, runtimeRef, newWorkspaceLifecycle(runtimeRef.WorkspaceRoot(), true)); !handled {
			t.Fatal("expected /config-diagnostics command to be handled")
		}
	})
	if !strings.Contains(output, "Config Diagnostics") || !strings.Contains(output, "config_load_ok") || !strings.Contains(output, "modules") || !strings.Contains(output, "optional extension-directory") {
		t.Fatalf("expected config diagnostics output, got %q", output)
	}
	jsonOutput := captureStdout(t, func() {
		if handled := handleCommand(context.Background(), "/config-diagnostics --json", nil, nil, runtimeRef, newWorkspaceLifecycle(runtimeRef.WorkspaceRoot(), true)); !handled {
			t.Fatal("expected /config-diagnostics --json command to be handled")
		}
	})
	if !strings.Contains(jsonOutput, `"status"`) || !strings.Contains(jsonOutput, `"config_path"`) || !strings.Contains(jsonOutput, `"diagnostics"`) {
		t.Fatalf("expected config diagnostics json output, got %q", jsonOutput)
	}
}

func TestDecodeCLIWorkflowSchemaImportFormats(t *testing.T) {
	rawSchema := []byte(`{
		"workflow": "raw-flow",
		"stages": {"done": {"stage":"done", "outputs":{"summary":{"type":"string"}}}}
	}`)
	schemas, merge, err := decodeCLIWorkflowSchemaImport(rawSchema, true)
	if err != nil || !merge || len(schemas) != 1 || schemas[0].Workflow != "raw-flow" {
		t.Fatalf("expected raw schema import, schemas=%#v merge=%t err=%v", schemas, merge, err)
	}
	rawList := []byte(`[{
		"workflow": "list-flow",
		"stages": {"done": {"stage":"done", "outputs":{"summary":{"type":"string"}}}}
	}]`)
	schemas, merge, err = decodeCLIWorkflowSchemaImport(rawList, false)
	if err != nil || merge || len(schemas) != 1 || schemas[0].Workflow != "list-flow" {
		t.Fatalf("expected raw list import, schemas=%#v merge=%t err=%v", schemas, merge, err)
	}
	bundle := []byte(`{
		"kind": "goflow.workflow_schemas",
		"version": 2,
		"merge": false,
		"schemas": [{
			"workflow": "bundle-flow",
			"stages": {"done": {"stage":"done", "outputs":{"summary":{"type":"string"}}}}
		}]
	}`)
	schemas, merge, err = decodeCLIWorkflowSchemaImport(bundle, true)
	if err != nil || merge || len(schemas) != 1 || schemas[0].Workflow != "bundle-flow" {
		t.Fatalf("expected bundle import with merge override, schemas=%#v merge=%t err=%v", schemas, merge, err)
	}
	if _, _, err := decodeCLIWorkflowSchemaImport([]byte(`{"kind":"wrong","schemas":[]}`), true); err == nil {
		t.Fatal("expected unsupported bundle kind to fail")
	}
	if _, _, err := decodeCLIWorkflowSchemaImport([]byte(`{"kind":"goflow.workflow_schemas","version":99,"schemas":[]}`), true); err == nil {
		t.Fatal("expected future bundle version to fail")
	}
}

func TestFormatSessionOutputUsesSections(t *testing.T) {
	output := formatSessionOutput("planner", "fix", "code-review", []string{"prompt one"}, []string{"write_file: suspended"}, workflowDisplayRow{}, handoffDisplayRow{}, routingDisplayRow{}, nil, taskStageDisplayRow{Stage: "verify", AgentID: "auditor", Mode: "audit", Detail: "checking final result"})
	if !strings.Contains(output, "Session") {
		t.Fatalf("expected session header, got %q", output)
	}
	if !strings.Contains(output, "Recent prompts") || !strings.Contains(output, "Recent tools") {
		t.Fatalf("expected session sections, got %q", output)
	}
	if !strings.Contains(output, "Task stage") || !strings.Contains(output, "verify") {
		t.Fatalf("expected task stage section, got %q", output)
	}
}

func TestFormatCostOutputSummarizesPromptAndTokenHistory(t *testing.T) {
	output := formatCostOutput(session.Snapshot{
		PromptBudget: &schema.PromptBudget{
			AgentID:                        "fixer",
			Mode:                           "fix",
			TaskStage:                      "modify",
			EstimatedPromptTokens:          7200,
			SystemTokens:                   1200,
			MessageTokens:                  3200,
			ToolSchemaTokens:               1800,
			CacheablePrefixTokens:          2600,
			ExposedToolCount:               3,
			TotalToolCount:                 12,
			FilteredToolCount:              9,
			HistoryPromptItems:             8,
			HistoryPromptRetainedItems:     4,
			HistoryPromptDeduplicatedItems: 2,
			HistoryToolItems:               10,
			HistoryToolRetainedItems:       6,
			HistoryToolCompactedOlderItems: 3,
			HistoryEstimatedSavedTokens:    160,
			PromptPrefixHash:               "prefix-d",
		},
		PromptBudgets: []schema.PromptBudget{
			{AgentID: "planner", Mode: "plan", EstimatedPromptTokens: 2000, CacheablePrefixTokens: 1200, PromptPrefixHash: "prefix-a"},
			{AgentID: "fixer", Mode: "fix", EstimatedPromptTokens: 6400, CacheablePrefixTokens: 2400, PromptPrefixHash: "prefix-b"},
			{AgentID: "fixer", Mode: "fix", EstimatedPromptTokens: 7000, CacheablePrefixTokens: 2600, PromptPrefixHash: "prefix-c"},
			{AgentID: "fixer", Mode: "fix", TaskStage: "modify", EstimatedPromptTokens: 7200, SystemTokens: 1200, MessageTokens: 3200, ToolSchemaTokens: 1800, CacheablePrefixTokens: 2600, ExposedToolCount: 3, TotalToolCount: 12, FilteredToolCount: 9, HistoryPromptItems: 8, HistoryPromptRetainedItems: 4, HistoryPromptDeduplicatedItems: 2, HistoryToolItems: 10, HistoryToolRetainedItems: 6, HistoryToolCompactedOlderItems: 3, HistoryEstimatedSavedTokens: 160, PromptPrefixHash: "prefix-d"},
		},
		TokenUsages: []schema.TokenUsageSample{
			{AgentID: "planner", Mode: "plan", PromptTokens: 1000, OutputTokens: 200, CachedTokens: 100},
			{AgentID: "fixer", Mode: "fix", PromptTokens: 3000, OutputTokens: 500, CachedTokens: 400},
		},
	})
	for _, want := range []string{"Cost Diagnostics", "prompt_samples=4", "token_samples=2", "saved_est=160", "deduped=2", "compacted_old=3", "prompts=4/8", "tools=6/10", "latest", "fixer", "top agents", "tool_schema_high", "prompt_prefix_churn"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected cost output to contain %q, got %q", want, output)
		}
	}
}

func TestStyleHeaderAddsAnsiColor(t *testing.T) {
	styled := styleHeader("Runtime")
	if !strings.Contains(styled, "\x1b[") {
		t.Fatalf("expected ansi escape sequence, got %q", styled)
	}
	if !strings.Contains(styled, "Runtime") {
		t.Fatalf("expected original text, got %q", styled)
	}
}

func TestStyleStatusUsesDistinctColors(t *testing.T) {
	errorText := styleStatus("failed", "failed")
	successText := styleStatus("done", "done")
	if errorText == successText {
		t.Fatalf("expected distinct styling for different statuses")
	}
	if !strings.Contains(errorText, "\x1b[") || !strings.Contains(successText, "\x1b[") {
		t.Fatalf("expected ansi styling, got error=%q success=%q", errorText, successText)
	}
}

func TestStyleLabelAddsAnsiColor(t *testing.T) {
	styled := styleLabel("mode")
	if !strings.Contains(styled, "\x1b[") {
		t.Fatalf("expected ansi escape sequence, got %q", styled)
	}
	if !strings.Contains(styled, "mode") {
		t.Fatalf("expected original text, got %q", styled)
	}
}

func TestFormatHelpOutputAppliesColorStyling(t *testing.T) {
	help := formatHelpOutput()
	if !strings.Contains(help, "\x1b[") {
		t.Fatalf("expected colored help output, got %q", help)
	}
}

func TestFormatAgentsOutputAppliesColorStyling(t *testing.T) {
	output := formatAgentsOutput([]agentDisplayRow{{Name: "planner", Description: "Plans", Provider: "primary", Mode: "plan", ToolPolicy: "allow", AllowedToolKinds: []string{"read"}, Active: true}, {Name: "fixer", Description: "Fixes", Provider: "backup", Mode: "fix", ToolPolicy: "confirm", AllowedToolKinds: []string{"read", "write"}}})
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected colored agents output, got %q", output)
	}
}

func TestFormatToolsOutputIncludesStructuredMetadata(t *testing.T) {
	output := formatToolsOutput([]toolDisplayRow{{Name: "file_tools/read_file", Description: "Reads a file", Server: "file_tools", Kind: "read", Health: "ready", InputSchemaSummary: "object(path, content)"}, {Name: "python_notes/write_note", Description: "Writes a note", Server: "python_notes", Kind: "write", Health: "cooldown", InputSchemaSummary: "object(path, body)"}})
	if !strings.Contains(output, "file_tools/read_file") || !strings.Contains(output, "python_notes/write_note") {
		t.Fatalf("expected tool names in output, got %q", output)
	}
	if !strings.Contains(output, "Reads a file") || !strings.Contains(output, "kind") || !strings.Contains(output, "read") {
		t.Fatalf("expected structured tool metadata, got %q", output)
	}
	if !strings.Contains(output, "server") || !strings.Contains(output, "file_tools") {
		t.Fatalf("expected server grouping, got %q", output)
	}
	if !strings.Contains(output, "health") || !strings.Contains(output, "ready") || !strings.Contains(output, "schema") || !strings.Contains(output, "object(path, content)") {
		t.Fatalf("expected health and schema summary, got %q", output)
	}
	if !strings.Contains(output, "summary") || !strings.Contains(output, "total=2") || !strings.Contains(output, "servers=2") || !strings.Contains(output, "kinds=read=1,write=1") {
		t.Fatalf("expected compact tools summary, got %q", output)
	}
}

func TestFormatToolsOutputAppliesColorStyling(t *testing.T) {
	output := formatToolsOutput([]toolDisplayRow{{Name: "file_tools/read_file", Description: "Reads", Server: "file_tools", Kind: "read", Health: "ready", InputSchemaSummary: "object(path)"}})
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected colored tools output, got %q", output)
	}
}

func TestBuildToolDiagnosticsReportsSchemaAndShortNameIssues(t *testing.T) {
	tools := []schema.Tool{
		{Name: "read_file", Server: "alpha", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
		{Name: "read_file", Server: "beta", InputSchema: json.RawMessage(`{"type":"object","required":["path"]}`)},
	}
	diagnostics := buildToolDiagnosticsByQualifiedName(tools)
	alpha := strings.Join(diagnostics["alpha/read_file"], "\n")
	if !strings.Contains(alpha, "ambiguous") || !strings.Contains(alpha, "beta/read_file") {
		t.Fatalf("expected duplicate short-name diagnostic for alpha tool, got %#v", diagnostics)
	}
	beta := strings.Join(diagnostics["beta/read_file"], "\n")
	if !strings.Contains(beta, "ambiguous") || !strings.Contains(beta, "missing properties") || !strings.Contains(beta, "required field") {
		t.Fatalf("expected schema diagnostics for beta tool, got %#v", diagnostics)
	}
}

func TestFormatToolsOutputShowsDiagnostics(t *testing.T) {
	output := formatToolsOutput([]toolDisplayRow{{
		Name:               "beta/read_file",
		Description:        "Reads",
		Server:             "beta",
		Kind:               "read",
		Health:             "ready",
		InputSchemaSummary: "object",
		Diagnostics:        []string{"short name \"read_file\" is ambiguous; use beta/read_file"},
	}})
	if !strings.Contains(output, "warning") || !strings.Contains(output, "ambiguous") {
		t.Fatalf("expected tool diagnostics in output, got %q", output)
	}
	if !strings.Contains(output, "warnings=1") {
		t.Fatalf("expected warning count in tool output, got %q", output)
	}
}

func TestFormatToolsOutputShowsRiskProfile(t *testing.T) {
	output := formatToolsOutput([]toolDisplayRow{{
		Name:                   "file_tools/write_file",
		Description:            "Writes",
		Server:                 "file_tools",
		Kind:                   "write",
		Health:                 "ready",
		InputSchemaSummary:     "object(path, content)",
		RiskLevel:              "high",
		IsolationLevel:         "lifecycle",
		RequiresSandbox:        true,
		SandboxFeatures:        []string{"windows_job_object_lifecycle"},
		MissingSandboxFeatures: []string{"windows_restricted_token", "windows_appcontainer"},
		WindowsIsolation: &schema.WindowsIsolationProfile{
			JobObject:       true,
			RestrictedToken: false,
			AppContainer:    false,
			LifecycleOnly:   true,
		},
	}})
	if !strings.Contains(output, "risk") || !strings.Contains(output, "high") || !strings.Contains(output, "isolation") || !strings.Contains(output, "lifecycle") {
		t.Fatalf("expected tool risk and isolation in output, got %q", output)
	}
	if !strings.Contains(output, "sandbox") || !strings.Contains(output, "recommended") {
		t.Fatalf("expected sandbox recommendation in output, got %q", output)
	}
	if !strings.Contains(output, "sandbox_features") || !strings.Contains(output, "windows_job_object_lifecycle") ||
		!strings.Contains(output, "missing_sandbox") || !strings.Contains(output, "windows_restricted_token") ||
		!strings.Contains(output, "restricted token and AppContainer are not enabled") {
		t.Fatalf("expected Windows sandbox feature details in output, got %q", output)
	}
}

func TestFormatToolsOutputShowsSensitiveEnvRisk(t *testing.T) {
	output := formatToolsOutput([]toolDisplayRow{{
		Name:               "web_tools/search",
		Description:        "Searches the web",
		Server:             "web_tools",
		Kind:               "network",
		Health:             "ready",
		InputSchemaSummary: "object(query)",
		RiskLevel:          "medium",
		IsolationLevel:     "none",
		RequiresSandbox:    true,
		EnvAllowlistSet:    true,
		EnvAllowlist:       []string{"PATH", "WEB_API_KEY"},
		SensitiveEnv:       []string{"WEB_API_KEY"},
	}})
	if !strings.Contains(output, "sensitive_env") || !strings.Contains(output, "WEB_API_KEY") || !strings.Contains(output, "env_allowlist") {
		t.Fatalf("expected sensitive env risk in tools output, got %q", output)
	}
	if strings.Contains(output, "super-secret-value") {
		t.Fatalf("expected tools output to omit env values, got %q", output)
	}
}

func TestBuildCLIToolRiskProfileUsesServerIsolation(t *testing.T) {
	servers := buildCLIMCPServerRiskMap([]config.MCPServerRef{{Name: "file_tools", Isolation: "process_group"}})
	risk := buildCLIToolRiskProfile(schema.Tool{Name: "write_file", Server: "file_tools", Kind: "write"}, servers["file_tools"])
	if risk.RiskLevel != "high" || risk.IsolationLevel != "lifecycle" || risk.Sandboxed || !risk.RequiresSandbox {
		t.Fatalf("expected high-risk lifecycle write tool, got %#v", risk)
	}
	if strings.Join(risk.SandboxFeatures, ",") != "process_group_lifecycle" || !slices.Contains(risk.MissingSandboxFeatures, "filesystem_policy") {
		t.Fatalf("expected lifecycle sandbox feature metadata, got %#v", risk)
	}
	readRisk := buildCLIToolRiskProfile(schema.Tool{Name: "read_file", Server: "file_tools", Kind: "read"}, servers["file_tools"])
	if readRisk.RiskLevel != "low" || readRisk.RequiresSandbox {
		t.Fatalf("expected read tool to stay low risk without sandbox requirement, got %#v", readRisk)
	}

	winServers := buildCLIMCPServerRiskMap([]config.MCPServerRef{{Name: "win_tools", Isolation: "windows_job"}})
	winRisk := buildCLIToolRiskProfile(schema.Tool{Name: "write_file", Server: "win_tools", Kind: "write"}, winServers["win_tools"])
	if winRisk.WindowsIsolation == nil || !winRisk.WindowsIsolation.JobObject || winRisk.WindowsIsolation.RestrictedToken || winRisk.WindowsIsolation.AppContainer ||
		!slices.Contains(winRisk.MissingSandboxFeatures, "windows_restricted_token") ||
		!slices.Contains(winRisk.MissingSandboxFeatures, "windows_appcontainer") {
		t.Fatalf("expected Windows lifecycle-only risk metadata, got %#v", winRisk)
	}
}

func TestBuildCLIToolRiskProfileCarriesEnvRisk(t *testing.T) {
	servers := buildCLIMCPServerRiskMap([]config.MCPServerRef{{
		Name:         "web_tools",
		Isolation:    "none",
		EnvAllowlist: []string{"PATH", "WEB_API_KEY", "WEB_API_KEY"},
	}})
	risk := buildCLIToolRiskProfile(schema.Tool{Name: "search", Server: "web_tools", Kind: "network"}, servers["web_tools"])
	if !risk.EnvAllowlistSet || len(risk.EnvAllowlist) != 2 || strings.Join(risk.SensitiveEnv, ",") != "WEB_API_KEY" {
		t.Fatalf("expected sanitized sensitive env risk, got %#v", risk)
	}
}

func TestFormatSessionOutputAppliesColorStyling(t *testing.T) {
	output := formatSessionOutput("planner", "fix", "code-review", []string{"prompt one"}, []string{"write_file: suspended"}, workflowDisplayRow{}, handoffDisplayRow{}, routingDisplayRow{}, nil)
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected colored session output, got %q", output)
	}
}

func TestCLIStreamRendererStylesApprovalAndErrorOutput(t *testing.T) {
	renderer := newCLIStreamRenderer(false)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventApproval, ToolName: "write_file"})
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventError, Content: "tool call failed"})
	})
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected colored renderer output, got %q", output)
	}
}

func TestFormatStatusLinesAppliesColorStyling(t *testing.T) {
	output := formatStatusLines([]string{
		"active agent: fixer",
		"mode: fix",
		"trace: false",
		"approval call-1: write_file (fixer)",
		"mcp file_tools: ready",
	})
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected colored status output, got %q", output)
	}
}

func TestCLIStreamRendererShowsStatusWhenTraceEnabled(t *testing.T) {
	renderer := newCLIStreamRenderer(true)
	output := captureStdout(t, func() {
		_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "agent=planner mode=audit iteration=1 fallback=true"})
	})
	if !strings.Contains(output, "agent=planner mode=audit iteration=1 fallback=true") {
		t.Fatalf("expected status output, got %q", output)
	}
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("expected ansi styling, got %q", output)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	type captureResult struct {
		output string
		err    error
	}
	done := make(chan captureResult, 1)
	go func() {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, r)
		done <- captureResult{output: buf.String(), err: err}
	}()

	fn()
	_ = w.Close()
	result := <-done
	_ = r.Close()
	if result.err != nil {
		t.Fatalf("io.Copy: %v", result.err)
	}
	return result.output
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	fn()
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	_ = r.Close()
	return buf.String()
}

type workflowStubLLMClient struct {
	responses []schema.ChatResponse
	calls     int
}

func (s *workflowStubLLMClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	panic("unused")
}

func (s *workflowStubLLMClient) StreamChat(_ context.Context, _ schema.ChatRequest, _ interfaces.StreamHandler) (schema.ChatResponse, error) {
	if s.calls >= len(s.responses) {
		return schema.ChatResponse{Message: schema.Message{Content: "done"}}, nil
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

func (s *workflowStubLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

type mainTestMCP struct {
	tools []schema.Tool
}

func (s *mainTestMCP) ListTools(context.Context) ([]schema.Tool, error) {
	return append([]schema.Tool(nil), s.tools...), nil
}

func (s *mainTestMCP) RefreshTools(context.Context) ([]schema.Tool, error) {
	return append([]schema.Tool(nil), s.tools...), nil
}

func (s *mainTestMCP) CallTool(context.Context, string, []byte) (schema.ToolResult, error) {
	return schema.ToolResult{Content: "ok"}, nil
}

func (s *mainTestMCP) HealthStatus(context.Context) map[string]string {
	return map[string]string{"stub": "ready"}
}

func (s *mainTestMCP) ToolNames() []string {
	names := make([]string, 0, len(s.tools))
	for _, tool := range s.tools {
		names = append(names, tool.Name)
	}
	return names
}

func writeTestSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	content := fmt.Sprintf(`---
name: %s
description: Test skill.
version: 1.0.0
author: GoFlow
activation:
  keywords: ["%s"]
---

## Workflow

1. Test.
`, name, name)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected %s to contain %q, got %q", path, want, string(data))
	}
}

func newMainTestRuntimeForHome(t *testing.T, runtimeHome string) *agent.Runtime {
	t.Helper()
	runtimeRef, err := agent.NewRuntime(&config.Config{
		RuntimeHome:   runtimeHome,
		Audit:         config.AuditConfig{},
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		Providers:     map[string]config.LLMConfig{"primary": {Provider: "openai-compatible", Model: "test"}},
		WorkspaceRoot: filepath.Join(t.TempDir(), "workspace"),
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:       "Chat",
				Provider:   "primary",
				Mode:       "chat",
				ToolPolicy: config.ToolPolicyAllow,
			},
		},
	}, map[string]interfaces.LLMClient{
		"primary": &workflowStubLLMClient{},
	}, nil, &mainTestMCP{}, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}

func quoteYAMLPath(path string) string {
	data, err := yaml.Marshal(path)
	if err != nil {
		return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"`
	}
	return strings.TrimSpace(string(data))
}

type mainTestSkillManager struct{}

func (mainTestSkillManager) Match(string) (*schema.Skill, bool) {
	return nil, false
}

func (mainTestSkillManager) MatchWithDiagnostics(string) (*schema.Skill, schema.SkillMatchDiagnostic, bool) {
	return nil, schema.SkillMatchDiagnostic{}, false
}

func (mainTestSkillManager) List() []schema.Skill {
	return nil
}

func (mainTestSkillManager) Reload() error {
	return nil
}

func newMainWorkflowTestRuntimeWithSuspendedFixTool() *agent.Runtime {
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "planner",
		Agents: map[string]config.AgentProfile{
			"planner": {
				Name:             "Planner",
				Provider:         "planner",
				Mode:             "plan",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
			"fixer": {
				Name:             "Fixer",
				Provider:         "fixer",
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
				ToolPolicy:       config.ToolPolicyConfirm,
			},
			"auditor": {
				Name:             "Auditor",
				Provider:         "auditor",
				Mode:             "audit",
				Model:            "test-model",
				MaxIterations:    2,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
				ToolPolicy:       config.ToolPolicyAllow,
			},
		},
	}

	plannerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "plan ready"},
	}}}
	fixerLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "need to write files"},
		ToolCalls: []schema.ToolCall{{
			ID:        "call-1",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
		}},
	}, {
		Message: schema.Message{Content: "fix completed"},
	}}}
	auditorLLM := &workflowStubLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "audit done"},
	}}}
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &mainTestMCP{tools: []schema.Tool{writeTool}}
	clients := map[string]interfaces.LLMClient{
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, mainTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		panic(err)
	}
	return runtimeRef
}
