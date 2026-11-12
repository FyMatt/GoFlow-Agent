package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxInstructionFileBytes  = 32 * 1024
	maxInstructionTotalBytes = 64 * 1024
)

var repositoryInstructionCandidates = []string{
	"AGENTS.md",
	"CLAUDE.md",
	filepath.Join(".codex", "instructions.md"),
	filepath.Join(".claude", "CLAUDE.md"),
	filepath.Join(".github", "copilot-instructions.md"),
}

// RepositoryInstruction is one trusted instruction file loaded from the
// active workspace. Path is always relative to the workspace root.
type RepositoryInstruction struct {
	Path    string
	Content string
}

// LoadRepositoryInstructions loads common Codex, Claude Code, and repository
// instruction files before task planning. Only fixed paths inside the active
// workspace are considered so discovery remains bounded and deterministic.
func LoadRepositoryInstructions(root string) ([]RepositoryInstruction, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	absoluteRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absoluteRoot); resolveErr == nil {
		absoluteRoot = filepath.Clean(resolved)
	}
	var total int
	loaded := make([]RepositoryInstruction, 0, len(repositoryInstructionCandidates))
	for _, relative := range repositoryInstructionCandidates {
		path := filepath.Join(absoluteRoot, relative)
		info, statErr := os.Lstat(path)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return nil, fmt.Errorf("inspect repository instructions %s: %w", filepath.ToSlash(relative), statErr)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.Size() > maxInstructionFileBytes {
			return nil, fmt.Errorf("repository instruction file %s exceeds %d bytes", filepath.ToSlash(relative), maxInstructionFileBytes)
		}
		if total+int(info.Size()) > maxInstructionTotalBytes {
			return nil, fmt.Errorf("repository instruction files exceed %d bytes", maxInstructionTotalBytes)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read repository instructions %s: %w", filepath.ToSlash(relative), readErr)
		}
		if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			return nil, fmt.Errorf("repository instruction file %s must be UTF-8 text", filepath.ToSlash(relative))
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		total += len(data)
		loaded = append(loaded, RepositoryInstruction{Path: filepath.ToSlash(relative), Content: content})
	}
	return loaded, nil
}

// RepositoryInstructionPrompt renders bounded instructions for the stable
// system prompt while preserving each source path for conflict diagnosis.
func RepositoryInstructionPrompt(items []RepositoryInstruction) string {
	if len(items) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Repository instructions loaded from the active workspace before planning:\n")
	for _, item := range items {
		path := strings.TrimSpace(item.Path)
		content := strings.TrimSpace(item.Content)
		if path == "" || content == "" {
			continue
		}
		builder.WriteString("\n--- ")
		builder.WriteString(path)
		builder.WriteString(" ---\n")
		builder.WriteString(content)
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}
