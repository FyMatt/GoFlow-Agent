package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type workspaceLifecycle struct {
	root      string
	explicit  bool
	confirmed bool
}

func newWorkspaceLifecycle(root string, explicit bool) *workspaceLifecycle {
	root = strings.TrimSpace(root)
	return &workspaceLifecycle{
		root:      root,
		explicit:  explicit,
		confirmed: explicit,
	}
}

func (w *workspaceLifecycle) Root() string {
	if w == nil {
		return ""
	}
	return strings.TrimSpace(w.root)
}

func (w *workspaceLifecycle) Confirmed() bool {
	return w == nil || strings.TrimSpace(w.root) == "" || w.confirmed
}

func (w *workspaceLifecycle) Confirm() {
	if w == nil {
		return
	}
	w.confirmed = true
	w.explicit = true
}

func (w *workspaceLifecycle) ClearConfirmation() {
	if w == nil {
		return
	}
	w.confirmed = false
	w.explicit = false
}

func (w *workspaceLifecycle) DisplayRoot() string {
	root := w.Root()
	if root == "" {
		return "(none)"
	}
	if w.Confirmed() {
		return root
	}
	return root + " (default, unconfirmed)"
}

func (w *workspaceLifecycle) StatusLine() string {
	if w == nil || strings.TrimSpace(w.root) == "" {
		return "workspace: none"
	}
	status := "confirmed"
	if !w.confirmed {
		status = "default, unconfirmed"
	}
	source := "explicit"
	if !w.explicit {
		source = "current directory default"
	}
	return fmt.Sprintf("workspace: %s (%s; %s)", w.root, status, source)
}

type workspaceRequirement struct {
	Required bool
	Reason   string
}

func workspaceRequirementForInput(input string) workspaceRequirement {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return workspaceRequirement{}
	}
	if strings.Contains(text, "@") {
		return workspaceRequirement{Required: true, Reason: "@file reference reads workspace files"}
	}
	terms := []struct {
		term   string
		reason string
	}{
		{"write_file", "file write requested"},
		{"read_file", "file read requested"},
		{"delete_file", "file delete requested"},
		{"run tests", "command/test execution requested"},
		{"run the tests", "command/test execution requested"},
		{"execute", "command execution requested"},
		{"create project", "project generation requested"},
		{"generate project", "project generation requested"},
		{"write code", "code generation requested"},
		{"modify", "workspace modification requested"},
		{"edit", "workspace modification requested"},
		{"refactor", "workspace modification requested"},
		{"implement", "workspace implementation requested"},
		{"fix", "workspace fix requested"},
		{"extend", "workspace extension requested"},
		{"expand", "workspace extension requested"},
		{"optimize", "workspace optimization requested"},
		{"readme", "document generation requested"},
		{"document", "document generation requested"},
		{"docs", "document generation requested"},
		{"workflow", "workflow execution requested"},
		{"写个", "代码或文件生成请求"},
		{"写一个", "代码或文件生成请求"},
		{"编写代码", "代码生成请求"},
		{"创建", "文件或项目创建请求"},
		{"生成", "文件或项目生成请求"},
		{"新增", "工作区修改请求"},
		{"添加", "工作区修改请求"},
		{"修改", "工作区修改请求"},
		{"修复", "工作区修复请求"},
		{"优化", "工作区优化请求"},
		{"拓展", "工作区拓展请求"},
		{"扩展", "工作区拓展请求"},
		{"完善", "工作区完善请求"},
		{"重构", "工作区重构请求"},
		{"删除", "文件删除请求"},
		{"读取文件", "文件读取请求"},
		{"查看文件", "文件读取请求"},
		{"运行测试", "命令或测试执行请求"},
		{"执行测试", "命令或测试执行请求"},
		{"生成文档", "文档生成请求"},
		{"写文档", "文档生成请求"},
		{"项目", "项目文件操作请求"},
	}
	for _, item := range terms {
		if strings.Contains(text, item.term) {
			return workspaceRequirement{Required: true, Reason: item.reason}
		}
	}
	return workspaceRequirement{}
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

func handleWorkspaceCommand(fields []string, workspace *workspaceLifecycle) bool {
	if workspace == nil {
		fmt.Println(formatCommandWarning("workspace lifecycle state is not available"))
		return true
	}
	if len(fields) == 1 || strings.EqualFold(fields[1], "status") {
		fmt.Println(styleHeader("Workspace"))
		fmt.Printf("- %s\n", workspace.StatusLine())
		if !workspace.Confirmed() {
			fmt.Printf("- %s\n", "file reads/writes, command execution, @file references, and workflows require confirmation first")
			fmt.Printf("- %s %s\n", "next:", styleStatus("/workspace confirm", "ready"))
		}
		return true
	}
	switch strings.ToLower(fields[1]) {
	case "confirm":
		workspace.Confirm()
		fmt.Println(formatCommandSuccess("workspace", workspace.Root()))
		return true
	case "clear":
		workspace.ClearConfirmation()
		fmt.Println(formatCommandWarning("workspace confirmation cleared; pure chat still works"))
		return true
	case "use":
		if len(fields) < 3 {
			fmt.Fprintln(os.Stderr, "usage: /workspace use <path>")
			return true
		}
		path := strings.TrimSpace(strings.Join(fields[2:], " "))
		if !filepath.IsAbs(path) {
			abs, err := filepath.Abs(path)
			if err == nil {
				path = abs
			}
		}
		path = filepath.Clean(path)
		if samePath(path, workspace.Root()) {
			workspace.Confirm()
			fmt.Println(formatCommandSuccess("workspace", workspace.Root()))
			return true
		}
		fmt.Println(formatCommandWarning(fmt.Sprintf("workspace switching requires a restart for now; run with --workspace %s", path)))
		return true
	default:
		fmt.Fprintln(os.Stderr, "usage: /workspace [status|confirm|clear|use <path>]")
		return true
	}
}

func samePath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == "" || right == "" {
		return left == right
	}
	return strings.EqualFold(left, right)
}
