package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// State tracks whether a workspace was explicitly selected or only defaulted.
type State struct {
	mu        sync.RWMutex
	root      string
	explicit  bool
	confirmed bool
}

// Requirement describes whether a request needs confirmed workspace access.
type Requirement struct {
	Required bool
	Reason   string
}

// Snapshot is the API/session-facing workspace state.
type Snapshot struct {
	Root      string `json:"root"`
	Confirmed bool   `json:"confirmed"`
	Explicit  bool   `json:"explicit"`
	Source    string `json:"source"`
	Status    string `json:"status"`
	Display   string `json:"display"`
}

// New creates a workspace state. Explicit workspaces start confirmed.
func New(root string, explicit bool) *State {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." {
		root = ""
	}
	return &State{
		root:      root,
		explicit:  explicit,
		confirmed: explicit,
	}
}

func (s *State) Root() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.root)
}

func (s *State) Explicit() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.explicit
}

func (s *State) Confirmed() bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.root) == "" || s.confirmed
}

func (s *State) Confirm() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.confirmed = true
	s.explicit = true
}

func (s *State) ClearConfirmation() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.confirmed = false
	s.explicit = false
}

func (s *State) DisplayRoot() string {
	snapshot := s.Snapshot()
	if snapshot.Display != "" {
		return snapshot.Display
	}
	return "(none)"
}

func (s *State) StatusLine() string {
	snapshot := s.Snapshot()
	if snapshot.Root == "" {
		return "workspace: none"
	}
	return fmt.Sprintf("workspace: %s (%s; %s)", snapshot.Root, snapshot.Status, snapshot.Source)
}

func (s *State) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{Status: "confirmed", Source: "none", Display: "(none)"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	root := strings.TrimSpace(s.root)
	status := "confirmed"
	if !s.confirmed {
		status = "default, unconfirmed"
	}
	source := "explicit"
	if !s.explicit {
		source = "current directory default"
	}
	display := "(none)"
	if root != "" {
		display = root
		if !s.confirmed {
			display += " (default, unconfirmed)"
		}
	}
	return Snapshot{
		Root:      root,
		Confirmed: root == "" || s.confirmed,
		Explicit:  s.explicit,
		Source:    source,
		Status:    status,
		Display:   display,
	}
}

func SamePath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == "" || right == "" {
		return left == right
	}
	return strings.EqualFold(left, right)
}

func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	return filepath.Clean(path)
}

func RequirementForInput(input string) Requirement {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return Requirement{}
	}
	if strings.Contains(text, "@") {
		return Requirement{Required: true, Reason: "@file reference reads workspace files"}
	}
	for _, item := range workspaceRequirementTerms() {
		if strings.Contains(text, item.term) {
			return Requirement{Required: true, Reason: item.reason}
		}
	}
	return Requirement{}
}

type requirementTerm struct {
	term   string
	reason string
}

func workspaceRequirementTerms() []requirementTerm {
	return []requirementTerm{
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
		{"写个", "code or file generation requested"},
		{"写一个", "code or file generation requested"},
		{"编写代码", "code generation requested"},
		{"创建", "file or project creation requested"},
		{"生成", "file or project generation requested"},
		{"新增", "workspace modification requested"},
		{"添加", "workspace modification requested"},
		{"修改", "workspace modification requested"},
		{"修复", "workspace fix requested"},
		{"优化", "workspace optimization requested"},
		{"拓展", "workspace extension requested"},
		{"扩展", "workspace extension requested"},
		{"完善", "workspace improvement requested"},
		{"重构", "workspace refactor requested"},
		{"删除", "file delete requested"},
		{"读取文件", "file read requested"},
		{"查看文件", "file read requested"},
		{"运行测试", "command/test execution requested"},
		{"执行测试", "command/test execution requested"},
		{"生成文档", "document generation requested"},
		{"写文档", "document generation requested"},
		{"项目", "project file operation requested"},
	}
}
