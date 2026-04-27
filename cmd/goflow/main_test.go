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
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	skillpkg "github.com/FyMatt/GoFlow-Agent/internal/skill"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
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
	wantConfigPath := filepath.Join(runtimeHome, "configs", "agent.yaml")
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

	configPath, resolvedWorkspace, httpAddr, err := resolvePaths(runtimeHome, []string{"--config", "configs/agent.docker.yaml", "--workspace", workspaceRoot, "--http", ":8080"})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	wantConfigPath := filepath.Join(runtimeHome, "configs", "agent.docker.yaml")
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
	wantConfigPath := filepath.Join(runtimeHome, "configs", "agent.yaml")
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
	if err := os.WriteFile(filepath.Join(runtimeHome, "configs", "agent.binary.yaml"), []byte("agent: {}\n"), 0o644); err != nil {
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
	if got := defaultConfigPathForRuntime(info); got != filepath.Join(runtimeHome, "configs", "agent.binary.yaml") {
		t.Fatalf("expected binary config, got %q", got)
	}
}

func TestResolvePathSettingsWithDefaultConfig(t *testing.T) {
	runtimeHome := t.TempDir()
	customConfig := filepath.Join(runtimeHome, "configs", "agent.binary.yaml")

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

func TestWorkspaceRequirementForInputDetectsWorkspaceTasks(t *testing.T) {
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
	if !strings.Contains(output.String(), "Use �?�?to choose and Enter to confirm.") {
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
	if !strings.Contains(output.String(), "Enter to confirm �?�?�?to move") {
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
	if !strings.Contains(stdout, "Enter to confirm �?�?�?to move") {
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
	if !strings.Contains(help, "/skill-templates") || !strings.Contains(help, "/new-skill") {
		t.Fatalf("expected skill scaffold commands in help output, got %q", help)
	}
}

func TestCompleteCommandTokenCompletesUniqueCommand(t *testing.T) {
	next, changed, message := completeCommandToken([]rune("/sta"), len([]rune("/sta")))
	if !changed || message != "" || string(next) != "/status " {
		t.Fatalf("expected /status completion, changed=%t message=%q next=%q", changed, message, string(next))
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
		if handled := handleCommand(context.Background(), "/new-workflow release-check", manager, nil, nil); !handled {
			t.Fatal("expected /new-workflow to be handled")
		}
	})
	if !strings.Contains(output, "notes-helper.py") || !strings.Contains(output, "researcher.yaml") || !strings.Contains(output, "release-check") {
		t.Fatalf("expected scaffold output, got %q", output)
	}
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "notes-helper.py"), "WORKSPACE_ROOT")
	assertFileContains(t, filepath.Join(runtimeHome, "mcp_servers", "notes-helper.py"), "write_text")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "agents", "researcher.yaml"), "researcher:")
	assertFileContains(t, filepath.Join(runtimeHome, "configs", "agents", "researcher.yaml"), "allowed_tools")
	assertFileContains(t, filepath.Join(runtimeHome, "workflows", "release-check", "workflow.yaml"), "skill: execution-plan")
	assertFileContains(t, filepath.Join(runtimeHome, "workflows", "release-check", "WORKFLOW.md"), "Workflow")
}

func TestScaffoldValidationRejectsIncompleteTemplates(t *testing.T) {
	if err := validatePythonMCPScaffold("tools/list only"); err == nil {
		t.Fatal("expected incomplete python MCP scaffold to fail validation")
	}
	if err := validateAgentScaffold("worker", "worker:\n  provider: primary\n"); err == nil {
		t.Fatal("expected incomplete agent scaffold to fail validation")
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

	fn()
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	_ = r.Close()
	return buf.String()
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
