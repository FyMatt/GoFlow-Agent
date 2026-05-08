package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/app"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
)

type workspaceLifecycle = workspace.State

func newWorkspaceLifecycle(root string, explicit bool) *workspaceLifecycle {
	return workspace.New(root, explicit)
}

type workspaceRequirement = workspace.Requirement

func workspaceRequirementForInput(input string) workspaceRequirement {
	return workspace.RequirementForInput(input)
}

func workspaceSamePath(left, right string) bool {
	return workspace.SamePath(left, right)
}

func formatWorkspaceRequiredMessage(workspaceRoot, reason string) string {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = "(none)"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "workspace-scoped action requested"
	}
	var b strings.Builder
	b.WriteString(styleStatus("[workspace]", "approval"))
	b.WriteString(" This request needs a confirmed workspace: ")
	b.WriteString(reason)
	b.WriteString("\n")
	b.WriteString("  current default: ")
	b.WriteString(workspaceRoot)
	b.WriteString("\n")
	b.WriteString("  run ")
	b.WriteString(styleStatus("/workspace confirm", "ready"))
	b.WriteString(" to use it, or restart with ")
	b.WriteString(styleStatus("--workspace <path>", "ready"))
	b.WriteString(" to choose another folder.\n")
	b.WriteString("  Pure chat can continue without confirming a workspace.\n")
	return b.String()
}

func ensureWorkspaceConfirmed(workspace *workspaceLifecycle, reason string) bool {
	if workspace == nil || workspace.Confirmed() {
		return true
	}
	fmt.Print(formatWorkspaceRequiredMessage(workspace.Root(), reason))
	return false
}

func handleWorkspaceCommand(fields []string, workspaceState *workspaceLifecycle) bool {
	if workspaceState == nil {
		fmt.Println(formatCommandWarning("workspace lifecycle state is not available"))
		return true
	}
	if len(fields) == 1 || strings.EqualFold(fields[1], "status") {
		fmt.Println(styleHeader("Workspace"))
		fmt.Printf("- %s\n", workspaceState.StatusLine())
		if !workspaceState.Confirmed() {
			fmt.Printf("- %s\n", "file reads/writes, command execution, @file references, and workflows require confirmation first")
			fmt.Printf("- %s %s\n", "next:", styleStatus("/workspace confirm", "ready"))
		}
		return true
	}
	switch strings.ToLower(fields[1]) {
	case "confirm":
		workspaceState.Confirm()
		fmt.Println(formatCommandSuccess("workspace", workspaceState.Root()))
		return true
	case "clear":
		workspaceState.ClearConfirmation()
		fmt.Println(formatCommandWarning("workspace confirmation cleared; pure chat still works"))
		return true
	case "use":
		if len(fields) < 3 {
			fmt.Fprintln(os.Stderr, "usage: /workspace use <path>")
			return true
		}
		path := workspace.NormalizePath(strings.TrimSpace(strings.Join(fields[2:], " ")))
		if workspace.SamePath(path, workspaceState.Root()) {
			workspaceState.Confirm()
			fmt.Println(formatCommandSuccess("workspace", workspaceState.Root()))
			return true
		}
		fmt.Println(formatCommandWarning(fmt.Sprintf("workspace switching is handled by the CLI runtime; run /workspace use %s from the interactive prompt", path)))
		return true
	case "choose", "pick":
		capability := workspace.HostFolderPickerCapability()
		if capability.Available {
			fmt.Println(formatCommandWarning("host folder picker is handled by the CLI runtime; run /workspace choose from the interactive prompt"))
			return true
		}
		fmt.Println(formatCommandWarning("host folder picker unavailable: " + capability.Reason))
		fmt.Printf("- enable with %s=1 on a local desktop session, or use %s\n", capability.OptInEnv, styleStatus("/workspace use <path>", "ready"))
		return true
	default:
		fmt.Fprintln(os.Stderr, "usage: /workspace [status|confirm|clear|use <path>|choose]")
		return true
	}
}

func parseWorkspaceChooseInput(input string) bool {
	fields := strings.Fields(strings.TrimSpace(input))
	return len(fields) >= 2 &&
		strings.EqualFold(fields[0], "/workspace") &&
		(strings.EqualFold(fields[1], "choose") || strings.EqualFold(fields[1], "pick"))
}

func parseWorkspaceUseInput(input string) (string, bool, string) {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) == 0 || !strings.EqualFold(fields[0], "/workspace") {
		return "", false, ""
	}
	if len(fields) < 2 || !strings.EqualFold(fields[1], "use") {
		return "", false, ""
	}
	if len(fields) < 3 {
		return "", true, "usage: /workspace use <path>"
	}
	path := workspace.NormalizePath(strings.TrimSpace(strings.Join(fields[2:], " ")))
	if strings.TrimSpace(path) == "" {
		return "", true, "usage: /workspace use <path>"
	}
	return path, true, ""
}

func rebindCLIWorkspace(ctx context.Context, current *app.RuntimeApp, path string) (*app.RuntimeApp, *workspaceLifecycle, error) {
	path = workspace.NormalizePath(path)
	if strings.TrimSpace(path) == "" {
		return nil, nil, errors.New("workspace path is required")
	}
	if current == nil || current.Config == nil {
		return nil, nil, errors.New("runtime app is not available")
	}
	if blockers := cliWorkspaceRebindBlockers(current); len(blockers) > 0 {
		return nil, nil, fmt.Errorf("workspace rebind blocked: %s", strings.Join(blockers, ", "))
	}
	newApp, err := app.Bootstrap(ctx, app.BootstrapOptions{
		RuntimeHome:   current.Config.RuntimeHome,
		WorkspaceRoot: path,
		ConfigPath:    current.ConfigPath,
	})
	if err != nil {
		return nil, nil, err
	}
	newWorkspace := newWorkspaceLifecycle(path, true)
	newApp.Runtime.SetWorkspaceConfirmed(true)
	if err := current.SaveSession(); err != nil {
		_ = newApp.Close()
		return nil, nil, fmt.Errorf("save current workspace session: %w", err)
	}
	if err := current.Close(); err != nil {
		_ = newApp.Close()
		return nil, nil, fmt.Errorf("close current workspace runtime: %w", err)
	}
	return newApp, newWorkspace, nil
}

func cliWorkspaceRebindBlockers(current *app.RuntimeApp) []string {
	if current == nil || current.Runtime == nil {
		return []string{"runtime_unavailable"}
	}
	snapshot := current.Runtime.SessionSnapshot()
	blockers := make([]string, 0, 4)
	if len(snapshot.PendingApprovals) > 0 {
		blockers = append(blockers, "pending_approvals")
	}
	if strings.TrimSpace(snapshot.PendingHandoff.TargetAgent) != "" || strings.TrimSpace(snapshot.PendingHandoff.ExpectedAction) != "" {
		blockers = append(blockers, "pending_handoff")
	}
	if workflowBlocksWorkspaceRebind(snapshot.Workflow) {
		blockers = append(blockers, "active_workflow")
	}
	for _, run := range snapshot.AgentRuns {
		if !agentRunFinished(run.Status) {
			blockers = append(blockers, "active_agent_run")
			break
		}
	}
	for _, run := range snapshot.WorkflowRuns {
		if !workflowRunFinished(run.Status) {
			blockers = append(blockers, "active_workflow_run")
			break
		}
	}
	return blockers
}

func workflowBlocksWorkspaceRebind(snapshot session.WorkflowSnapshot) bool {
	status := strings.ToLower(strings.TrimSpace(snapshot.Status))
	if status == "" {
		return false
	}
	switch status {
	case "completed", "failed", "cancelled", "canceled", "denied", "blocked":
		return false
	default:
		return true
	}
}

func agentRunFinished(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "completed", "failed", "cancelled", "canceled", "denied":
		return true
	default:
		return false
	}
}

func workflowRunFinished(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "completed", "failed", "cancelled", "canceled", "denied", "blocked":
		return true
	default:
		return false
	}
}

func formatCLIWorkspaceSwitchError(path string, err error) string {
	if err == nil {
		return ""
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = "<path>"
	}
	var b strings.Builder
	b.WriteString(formatCommandWarning("workspace switch failed: " + err.Error()))
	b.WriteString("\n")
	b.WriteString("  target: ")
	b.WriteString(path)
	b.WriteString("\n")
	b.WriteString("  fallback: restart GoFlow with ")
	b.WriteString(styleStatus("--workspace "+path, "ready"))
	return b.String()
}
