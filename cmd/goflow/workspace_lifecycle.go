package main

import (
	"fmt"
	"os"
	"strings"

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
		fmt.Println(formatCommandWarning(fmt.Sprintf("workspace switching requires a restart for now; run with --workspace %s", path)))
		return true
	default:
		fmt.Fprintln(os.Stderr, "usage: /workspace [status|confirm|clear|use <path>]")
		return true
	}
}
