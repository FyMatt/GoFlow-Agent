package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const maxAtReferenceBytes = 256 * 1024
const maxAtReferenceTotalBytes = 768 * 1024

type atReference struct {
	RawPath      string
	ResolvedPath string
	RelativePath string
	Content      string
	Size         int
}

func expandAtFileReferences(input, workspaceRoot string) (string, []atReference, error) {
	paths := parseAtReferencePaths(input)
	if len(paths) == 0 {
		return input, nil, nil
	}
	refs := make([]atReference, 0, len(paths))
	seen := make(map[string]struct{})
	total := 0
	for _, rawPath := range paths {
		resolved, rel, err := resolveWorkspaceReferencePath(rawPath, workspaceRoot)
		if err != nil {
			return "", nil, fmt.Errorf("@%s: %w", rawPath, err)
		}
		if _, exists := seen[resolved]; exists {
			continue
		}
		seen[resolved] = struct{}{}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", nil, fmt.Errorf("@%s: %w", rawPath, err)
		}
		if info.IsDir() {
			return "", nil, fmt.Errorf("@%s: path is a directory; reference a file", rawPath)
		}
		if info.Size() > maxAtReferenceBytes {
			return "", nil, fmt.Errorf("@%s: file too large: %d bytes exceeds %d", rawPath, info.Size(), maxAtReferenceBytes)
		}
		if total+int(info.Size()) > maxAtReferenceTotalBytes {
			return "", nil, fmt.Errorf("@%s: referenced files exceed total limit %d bytes", rawPath, maxAtReferenceTotalBytes)
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", nil, fmt.Errorf("@%s: %w", rawPath, err)
		}
		content, err := decodeAtReferenceUTF8(rawPath, data)
		if err != nil {
			return "", nil, err
		}
		total += len(data)
		refs = append(refs, atReference{RawPath: rawPath, ResolvedPath: resolved, RelativePath: rel, Content: content, Size: len(data)})
	}
	if len(refs) == 0 {
		return input, nil, nil
	}
	return buildAtReferencePrompt(input, refs), refs, nil
}

func decodeAtReferenceUTF8(rawPath string, data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", fmt.Errorf("@%s: file is not valid UTF-8 text; use a binary inspection tool for non-text files", rawPath)
	}
	return strings.TrimPrefix(string(data), "\ufeff"), nil
}

func expandAtFileReferencesWithTools(ctx context.Context, input, workspaceRoot string, mcpClient interfaces.MCPClient, renderer *cliStreamRenderer) (string, []atReference, error) {
	if mcpClient == nil {
		return expandAtFileReferences(input, workspaceRoot)
	}
	paths := parseAtReferencePaths(input)
	if len(paths) == 0 {
		return input, nil, nil
	}
	refs := make([]atReference, 0, len(paths))
	seen := make(map[string]struct{})
	total := 0
	for index, rawPath := range paths {
		resolved, rel, err := resolveWorkspaceReferencePath(rawPath, workspaceRoot)
		if err != nil {
			return "", nil, fmt.Errorf("@%s: %w", rawPath, err)
		}
		if _, exists := seen[resolved]; exists {
			continue
		}
		seen[resolved] = struct{}{}
		callID := fmt.Sprintf("at-ref-%d", index+1)
		args, _ := json.Marshal(map[string]any{"path": rawPath})
		if renderer != nil {
			_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "read_file", ToolCallID: callID, Content: "read", ArgumentsSummary: "path=" + rawPath})
		}
		result, err := mcpClient.CallTool(ctx, "file_tools/read_file", args)
		if err != nil {
			return "", nil, fmt.Errorf("@%s: %w", rawPath, err)
		}
		result.CallID = callID
		if renderer != nil {
			_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "read_file", ToolCallID: callID, ArgumentsSummary: "path=" + rawPath, Content: result.Content, IsError: result.IsError})
		}
		if result.IsError {
			return "", nil, fmt.Errorf("@%s: %s", rawPath, result.Content)
		}
		var payload struct {
			Path    string `json:"path"`
			Size    int    `json:"size"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
			return "", nil, fmt.Errorf("@%s: decode read_file result: %w", rawPath, err)
		}
		if payload.Size > maxAtReferenceBytes {
			return "", nil, fmt.Errorf("@%s: file too large: %d bytes exceeds %d", rawPath, payload.Size, maxAtReferenceBytes)
		}
		if total+payload.Size > maxAtReferenceTotalBytes {
			return "", nil, fmt.Errorf("@%s: referenced files exceed total limit %d bytes", rawPath, maxAtReferenceTotalBytes)
		}
		total += payload.Size
		refs = append(refs, atReference{RawPath: rawPath, ResolvedPath: payload.Path, RelativePath: rel, Content: payload.Content, Size: payload.Size})
	}
	return buildAtReferencePrompt(input, refs), refs, nil
}

func buildAtReferencePrompt(input string, refs []atReference) string {
	if len(refs) == 0 {
		return input
	}
	var b strings.Builder
	b.WriteString(input)
	b.WriteString("\n\nReferenced workspace files:\n")
	for _, ref := range refs {
		fmt.Fprintf(&b, "\n--- @%s (%d bytes) ---\n", ref.RelativePath, ref.Size)
		b.WriteString(ref.Content)
		if !strings.HasSuffix(ref.Content, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func expandSessionArtifactReferencesForCLI(input string, runtimeRef *agent.Runtime, renderer *cliStreamRenderer) (string, []string, error) {
	refs := parseSessionArtifactRefs(input)
	if len(refs) == 0 {
		return input, nil, nil
	}
	var b strings.Builder
	b.WriteString(input)
	b.WriteString("\n\nReferenced GoFlow artifacts:\n")
	attached := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for index, ref := range refs {
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		artifact, ok := runtimeRef.SessionArtifact(ref)
		if !ok {
			return "", nil, fmt.Errorf("%s: session artifact not found", ref)
		}
		callID := fmt.Sprintf("artifact-ref-%d", index+1)
		if renderer != nil {
			_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "session_artifact", ToolCallID: callID, Content: "read", ArgumentsSummary: "ref=" + ref})
			_ = renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "session_artifact", ToolCallID: callID, Content: artifact.Summary, ArgumentsSummary: "ref=" + ref})
		}
		fmt.Fprintf(&b, "\n--- %s (%s, %d bytes) ---\n", artifact.Ref, artifact.Title, artifact.ContentBytes)
		b.WriteString(artifact.Content)
		if !strings.HasSuffix(artifact.Content, "\n") {
			b.WriteString("\n")
		}
		attached = append(attached, artifact.Ref)
	}
	return b.String(), attached, nil
}

func parseSessionArtifactRefs(input string) []string {
	var refs []string
	for _, field := range strings.Fields(input) {
		field = strings.TrimRight(strings.TrimSpace(field), ".,;:!?)]}")
		if strings.HasPrefix(field, "goflow://session-artifacts/") {
			refs = append(refs, field)
		}
	}
	return refs
}

func parseAtReferencePaths(input string) []string {
	var paths []string
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '@' {
			continue
		}
		if i > 0 && !unicode.IsSpace(runes[i-1]) {
			continue
		}
		if i+1 >= len(runes) || unicode.IsSpace(runes[i+1]) {
			continue
		}
		start := i + 1
		if runes[start] == '"' || runes[start] == '\'' {
			quote := runes[start]
			start++
			end := start
			for end < len(runes) && runes[end] != quote {
				end++
			}
			if end > start {
				paths = append(paths, string(runes[start:end]))
			}
			i = end
			continue
		}
		end := start
		for end < len(runes) && !unicode.IsSpace(runes[end]) {
			end++
		}
		if end > start {
			paths = append(paths, strings.TrimRight(string(runes[start:end]), ".,;:!?)]}"))
		}
		i = end
	}
	return paths
}

func shouldShowAtReferenceSuggestions(input string) bool {
	input = strings.TrimSpace(input)
	return strings.HasPrefix(input, "@") && !strings.ContainsAny(input, " \t\r\n")
}

func formatAtReferenceSuggestions(input, workspaceRoot string) (string, bool, error) {
	if !shouldShowAtReferenceSuggestions(input) {
		return "", false, nil
	}
	prefix := strings.TrimPrefix(strings.TrimSpace(input), "@")
	matches, err := suggestAtReferencePaths(prefix, workspaceRoot, 20)
	if err != nil {
		return "", true, err
	}
	var b strings.Builder
	if prefix == "" {
		b.WriteString(formatCommandWarning("Type @path to reference a workspace file. Matching files:"))
	} else {
		b.WriteString(formatCommandWarning(fmt.Sprintf("Matching workspace files for @%s:", prefix)))
	}
	b.WriteString("\n")
	if len(matches) == 0 {
		b.WriteString("  no matches\n")
		return b.String(), true, nil
	}
	for _, match := range matches {
		b.WriteString("  @")
		b.WriteString(match)
		b.WriteString("\n")
	}
	return b.String(), true, nil
}

func suggestAtReferencePaths(prefix, workspaceRoot string, limit int) ([]string, error) {
	root, err := canonicalReferenceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	normalizedPrefix := filepath.ToSlash(strings.TrimPrefix(prefix, "./"))
	candidates := make([]string, 0, limit)
	visited := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != root && shouldSkipAtReferenceSuggestionDir(entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > 3000 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		real, err := filepath.EvalSymlinks(path)
		if err == nil {
			real, _ = filepath.Abs(filepath.Clean(real))
			if !referenceWithinBase(root, real) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		candidate := rel
		if entry.IsDir() {
			candidate += "/"
		}
		candidates = append(candidates, candidate)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matchAtReferenceCandidates(candidates, normalizedPrefix, limit), nil
}

func shouldSkipAtReferenceSuggestionDir(entry os.DirEntry) bool {
	if entry == nil || !entry.IsDir() {
		return false
	}
	switch strings.ToLower(entry.Name()) {
	case ".git", ".hg", ".svn", ".goflow",
		".venv", "venv", "env",
		"node_modules", "__pycache__", "vendor",
		"dist", "build", "target", "coverage",
		".next", ".nuxt", ".cache", ".pytest_cache", ".mypy_cache":
		return true
	default:
		return false
	}
}

func matchAtReferenceCandidates(candidates []string, normalizedPrefix string, limit int) []string {
	if limit <= 0 {
		limit = len(candidates)
	}
	prefixMatches := make([]string, 0, limit)
	for _, candidate := range candidates {
		if normalizedPrefix == "" || strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(normalizedPrefix)) {
			prefixMatches = append(prefixMatches, candidate)
		}
	}
	if len(prefixMatches) > 0 {
		sort.Strings(prefixMatches)
		if len(prefixMatches) > limit {
			return prefixMatches[:limit]
		}
		return prefixMatches
	}
	return fuzzyFilterStrings(candidates, normalizedPrefix, limit)
}

func resolveWorkspaceReferencePath(raw, root string) (string, string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", "", fmt.Errorf("path is required")
	}
	cleanRoot, err := canonicalReferenceRoot(root)
	if err != nil {
		return "", "", err
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(cleanRoot, candidate)
	}
	cleanCandidate, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", "", fmt.Errorf("resolve path: %w", err)
	}
	if !referenceWithinBase(cleanRoot, cleanCandidate) {
		return "", "", fmt.Errorf("path escapes workspace root")
	}
	realPath, err := resolveReferenceRealPath(cleanRoot, cleanCandidate)
	if err != nil {
		return "", "", err
	}
	realPath, err = filepath.Abs(filepath.Clean(realPath))
	if err != nil {
		return "", "", fmt.Errorf("resolve real path: %w", err)
	}
	if !referenceWithinBase(cleanRoot, realPath) {
		return "", "", fmt.Errorf("path resolves outside workspace root")
	}
	rel, err := filepath.Rel(cleanRoot, realPath)
	if err != nil {
		return "", "", fmt.Errorf("relative path: %w", err)
	}
	return realPath, filepath.ToSlash(rel), nil
}

func resolveReferenceRealPath(cleanRoot, cleanCandidate string) (string, error) {
	realPath, err := filepath.EvalSymlinks(cleanCandidate)
	if err == nil {
		return realPath, nil
	}
	if _, statErr := os.Stat(cleanCandidate); statErr != nil {
		return "", fmt.Errorf("resolve real path: %w", err)
	}
	hasLink, linkErr := referencePathHasSymlink(cleanRoot, cleanCandidate)
	if linkErr != nil {
		return "", linkErr
	}
	if hasLink {
		return "", fmt.Errorf("resolve real path: %w", err)
	}
	return cleanCandidate, nil
}

func referencePathHasSymlink(cleanRoot, cleanCandidate string) (bool, error) {
	rel, err := filepath.Rel(cleanRoot, cleanCandidate)
	if err != nil {
		return false, fmt.Errorf("relative path: %w", err)
	}
	if rel == "." {
		return false, nil
	}
	current := cleanRoot
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return false, fmt.Errorf("resolve real path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}

func canonicalReferenceRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	cleanRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err == nil {
		cleanRoot, err = filepath.Abs(filepath.Clean(realRoot))
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}
	}
	return cleanRoot, nil
}

func referenceWithinBase(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
