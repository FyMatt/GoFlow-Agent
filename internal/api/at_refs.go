package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/memory"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const maxHTTPAtReferenceBytes = 256 * 1024
const maxHTTPAtReferenceTotalBytes = 768 * 1024
const httpAtReferenceSummaryThresholdBytes = 64 * 1024

type httpAtReference struct {
	RawPath      string
	ResolvedPath string
	RelativePath string
	Content      string
	Size         int
	Summary      string
	Hash         string
	Language     string
	SummaryOnly  bool
}

func (s *Server) expandAtReferences(ctx context.Context, input string, handler func(schema.StreamEvent) error) (string, error) {
	paths := parseHTTPAtReferencePaths(input)
	artifactRefs := parseSessionArtifactRefs(input)
	expanded := input
	if len(paths) > 0 {
		if s == nil || s.workspace == nil || strings.TrimSpace(s.workspace.Root()) == "" {
			return "", fmt.Errorf("workspace is required before resolving @file references")
		}
		if !s.workspace.Confirmed() {
			return "", fmt.Errorf("workspace confirmation required before resolving @file references")
		}
		var store *memory.Store
		if s.runtime != nil {
			store = s.runtime.MemoryStore()
		}
		next, err := expandHTTPAtFileReferences(ctx, expanded, s.workspace.Root(), store, handler)
		if err != nil {
			return "", err
		}
		expanded = next
	}
	if len(artifactRefs) > 0 {
		next, err := s.expandSessionArtifactReferences(expanded, artifactRefs, handler)
		if err != nil {
			return "", err
		}
		expanded = next
	}
	return expanded, nil
}

func expandHTTPAtFileReferences(ctx context.Context, input, workspaceRoot string, store *memory.Store, handler func(schema.StreamEvent) error) (string, error) {
	paths := parseHTTPAtReferencePaths(input)
	if len(paths) == 0 {
		return input, nil
	}
	refs := make([]httpAtReference, 0, len(paths))
	seen := make(map[string]struct{})
	total := 0
	for index, rawPath := range paths {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		resolved, rel, err := resolveWorkspaceReferencePath(rawPath, workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("@%s: %w", rawPath, err)
		}
		if _, exists := seen[resolved]; exists {
			continue
		}
		seen[resolved] = struct{}{}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", fmt.Errorf("@%s: %w", rawPath, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("@%s: path is a directory; reference a file", rawPath)
		}
		if info.Size() > maxHTTPAtReferenceBytes && store == nil {
			return "", fmt.Errorf("@%s: file too large: %d bytes exceeds %d", rawPath, info.Size(), maxHTTPAtReferenceBytes)
		}
		if store == nil && total+int(info.Size()) > maxHTTPAtReferenceTotalBytes {
			return "", fmt.Errorf("@%s: referenced files exceed total limit %d bytes", rawPath, maxHTTPAtReferenceTotalBytes)
		}
		callID := fmt.Sprintf("http-at-ref-%d", index+1)
		if handler != nil {
			_ = handler(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "read_file", ToolCallID: callID, Content: "read", ArgumentsSummary: "path=" + rawPath})
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", fmt.Errorf("@%s: %w", rawPath, err)
		}
		content, err := decodeHTTPAtReferenceUTF8(rawPath, data)
		if err != nil {
			return "", err
		}
		ref := httpAtReference{RawPath: rawPath, ResolvedPath: resolved, RelativePath: rel, Content: content, Size: len(data)}
		if shouldSummarizeHTTPAtReference(ref, store) {
			ref = summarizeHTTPAtReference(ctx, ref, data, store)
		} else {
			total += len(data)
			if total > maxHTTPAtReferenceTotalBytes {
				return "", fmt.Errorf("@%s: referenced files exceed total limit %d bytes", rawPath, maxHTTPAtReferenceTotalBytes)
			}
		}
		refs = append(refs, ref)
		if store != nil {
			_ = store.MarkFilePathsUsedByTask(ctx, []string{rel}, "")
		}
		if handler != nil {
			payload, _ := json.Marshal(map[string]any{
				"path": rel,
				"size": ref.Size,
			})
			_ = handler(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "read_file", ToolCallID: callID, Content: string(payload), ArgumentsSummary: "path=" + rawPath})
		}
	}
	return buildHTTPAtReferencePrompt(input, refs), nil
}

func shouldSummarizeHTTPAtReference(ref httpAtReference, store *memory.Store) bool {
	if store == nil {
		return false
	}
	return ref.Size > httpAtReferenceSummaryThresholdBytes
}

func summarizeHTTPAtReference(ctx context.Context, ref httpAtReference, data []byte, store *memory.Store) httpAtReference {
	ref.SummaryOnly = true
	if len(data) > 0 {
		sum := sha256.Sum256(data)
		ref.Hash = hex.EncodeToString(sum[:])
	}
	if store != nil {
		if summary, ok, err := store.FileSummaryForPath(ctx, ref.RelativePath); err == nil && ok {
			ref.Summary = strings.TrimSpace(summary.Summary)
			ref.Hash = firstNonEmptyAPIString(summary.Hash, ref.Hash)
			ref.Language = strings.TrimSpace(summary.Language)
		}
	}
	if ref.Summary == "" {
		ref.Summary = fmt.Sprintf("Large workspace file (%d bytes); full content omitted from @file prompt expansion.", ref.Size)
	}
	ref.Content = ""
	return ref
}

func decodeHTTPAtReferenceUTF8(rawPath string, data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", fmt.Errorf("@%s: file is not valid UTF-8 text; use a binary inspection tool for non-text files", rawPath)
	}
	return strings.TrimPrefix(string(data), "\ufeff"), nil
}

func buildHTTPAtReferencePrompt(input string, refs []httpAtReference) string {
	if len(refs) == 0 {
		return input
	}
	var b strings.Builder
	b.WriteString(input)
	b.WriteString("\n\nReferenced workspace files:\n")
	for _, ref := range refs {
		if ref.SummaryOnly {
			fmt.Fprintf(&b, "\n--- @%s (%d bytes, summary-only) ---\n", ref.RelativePath, ref.Size)
			if ref.Hash != "" {
				fmt.Fprintf(&b, "hash=%s\n", ref.Hash)
			}
			if ref.Language != "" {
				fmt.Fprintf(&b, "language=%s\n", ref.Language)
			}
			b.WriteString("Full content omitted from prompt expansion. Use read_file if exact content is required.\n")
			if ref.Summary != "" {
				b.WriteString("summary: ")
				b.WriteString(ref.Summary)
				b.WriteString("\n")
			}
			continue
		}
		fmt.Fprintf(&b, "\n--- @%s (%d bytes) ---\n", ref.RelativePath, ref.Size)
		b.WriteString(ref.Content)
		if !strings.HasSuffix(ref.Content, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func firstNonEmptyAPIString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Server) expandSessionArtifactReferences(input string, refs []string, handler func(schema.StreamEvent) error) (string, error) {
	if len(refs) == 0 {
		return input, nil
	}
	var b strings.Builder
	b.WriteString(input)
	b.WriteString("\n\nReferenced GoFlow artifacts:\n")
	seen := make(map[string]struct{}, len(refs))
	for index, ref := range refs {
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		artifact, ok := s.runtime.SessionArtifact(ref)
		if !ok {
			return "", fmt.Errorf("%s: session artifact not found", ref)
		}
		callID := fmt.Sprintf("http-artifact-ref-%d", index+1)
		if handler != nil {
			_ = handler(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: "session_artifact", ToolCallID: callID, Content: "read", ArgumentsSummary: "ref=" + ref})
			_ = handler(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: "session_artifact", ToolCallID: callID, Content: artifact.Summary, ArgumentsSummary: "ref=" + ref})
		}
		fmt.Fprintf(&b, "\n--- %s (%s, %d bytes) ---\n", artifact.Ref, artifact.Title, artifact.ContentBytes)
		b.WriteString(artifact.Content)
		if !strings.HasSuffix(artifact.Content, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
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

func parseHTTPAtReferencePaths(input string) []string {
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

func resolveWorkspaceReferencePath(raw, root string) (string, string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", "", fmt.Errorf("path is required")
	}
	cleanRoot, err := canonicalWorkspaceReferenceRoot(root)
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
	realPath, err := resolveWorkspaceReferenceRealPath(cleanRoot, cleanCandidate)
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

func resolveWorkspaceReferenceRealPath(cleanRoot, cleanCandidate string) (string, error) {
	realPath, err := filepath.EvalSymlinks(cleanCandidate)
	if err == nil {
		return realPath, nil
	}
	if _, statErr := os.Stat(cleanCandidate); statErr != nil {
		return "", fmt.Errorf("resolve real path: %w", err)
	}
	hasLink, linkErr := workspaceReferencePathHasSymlink(cleanRoot, cleanCandidate)
	if linkErr != nil {
		return "", linkErr
	}
	if hasLink {
		return "", fmt.Errorf("resolve real path: %w", err)
	}
	return cleanCandidate, nil
}

func workspaceReferencePathHasSymlink(cleanRoot, cleanCandidate string) (bool, error) {
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

func canonicalWorkspaceReferenceRoot(root string) (string, error) {
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

func workspacePathWithinRoot(root, path string) bool {
	cleanRoot, err := canonicalWorkspaceReferenceRoot(root)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err == nil {
		target, err = filepath.Abs(filepath.Clean(realTarget))
		if err != nil {
			return false
		}
	}
	return referenceWithinBase(cleanRoot, target)
}

func referenceWithinBase(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
