package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxReadBytes = 1024 * 1024
const maxSearchFiles = 2000

var workspaceRoot = mustWorkspaceRoot()

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Kind        string          `json:"kind,omitempty"`
}

func main() {
	if err := serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}

		switch req.Method {
		case "tools/list":
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": builtinTools()}})
		case "tools/call":
			result := callTool(req.Params)
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Result: result})
		default:
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	return scanner.Err()
}

func builtinTools() []tool {
	return []tool{
		{
			Name:        "read_file",
			Description: "Read a local UTF-8 text file from disk.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "write_file",
			Description: "Write UTF-8 text content to a local file.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "delete_file",
			Description: "Delete a file or, when recursive is true, a directory inside the workspace.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"recursive":{"type":"boolean"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "list_dir",
			Description: "List files in a local directory.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "list_tree",
			Description: "List a bounded recursive file tree inside the workspace.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"max_depth":{"type":"integer","minimum":0,"maximum":10},"max_entries":{"type":"integer","minimum":1,"maximum":1000}},"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "file_info",
			Description: "Return metadata for a file or directory inside the workspace.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "search_files",
			Description: "Search file names and UTF-8 text content under a workspace directory.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"query":{"type":"string","minLength":1},"max_results":{"type":"integer","minimum":1,"maximum":200},"include_content":{"type":"boolean"},"case_sensitive":{"type":"boolean"}},"required":["query"],"additionalProperties":false}`),
			Kind:        "read",
		},
	}
}

func callTool(params json.RawMessage) map[string]any {
	var input struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return map[string]any{"content": err.Error(), "is_error": true}
	}

	switch input.Name {
	case "read_file":
		path, err := extractPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		info, err := os.Stat(path)
		if err != nil {
			return map[string]any{"content": normalizeWorkspaceFileError("read_file", path, err), "is_error": true}
		}
		if info.IsDir() {
			return map[string]any{"content": "path is a directory", "is_error": true}
		}
		if info.Size() > maxReadBytes {
			return map[string]any{"content": fmt.Sprintf("file too large: %d bytes exceeds %d", info.Size(), maxReadBytes), "is_error": true}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		content, err := decodeUTF8Text(data)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		payload, _ := json.Marshal(map[string]any{"path": path, "size": info.Size(), "content": content})
		return map[string]any{"content": string(payload), "is_error": false}
	case "write_file":
		path, err := extractPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		content, ok := input.Arguments["content"].(string)
		if !ok {
			return map[string]any{"content": "content must be a string", "is_error": true}
		}
		oldContent := ""
		existed := false
		if data, err := os.ReadFile(path); err == nil {
			oldContent = string(data)
			existed = true
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		stats := analyzeWriteChange(oldContent, content, existed)
		payload := map[string]any{
			"path":              path,
			"bytes_written":     len(content),
			"existed":           existed,
			"status":            stats.Status,
			"line_summary":      stats.Summary(),
			"old_range":         stats.OldRange(),
			"new_range":         stats.NewRange(),
			"added_lines":       stats.AddedLines,
			"deleted_lines":     stats.DeletedLines,
			"old_line_count":    stats.OldLineCount,
			"new_line_count":    stats.NewLineCount,
			"changed_old_lines": stats.DeletedLines,
			"changed_new_lines": stats.AddedLines,
		}
		if preview := summarizeDiffPreview(oldContent, content, stats); preview != "" {
			payload["diff_preview"] = preview
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "delete_file":
		path, err := extractPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		recursive, _ := input.Arguments["recursive"].(bool)
		payload, err := deleteWorkspacePath(path, recursive)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "list_dir":
		path, err := extractPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return map[string]any{"content": normalizeWorkspaceFileError("list_dir", path, err), "is_error": true}
		}
		items := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			if shouldHideRootEntry(path, entry.Name()) {
				continue
			}
			items = append(items, map[string]any{"name": entry.Name(), "is_dir": entry.IsDir()})
		}
		encoded, _ := json.Marshal(map[string]any{"path": path, "entries": items})
		return map[string]any{"content": string(encoded), "is_error": false}
	case "list_tree":
		path, err := extractOptionalPath(input.Arguments, ".")
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		maxDepth := optionalInt(input.Arguments, "max_depth", 3, 0, 10)
		maxEntries := optionalInt(input.Arguments, "max_entries", 200, 1, 1000)
		payload, err := listWorkspaceTree(path, maxDepth, maxEntries)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "file_info":
		path, err := extractPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		payload, err := workspaceFileInfo(path)
		if err != nil {
			return map[string]any{"content": normalizeWorkspaceFileError("file_info", path, err), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "search_files":
		path, err := extractOptionalPath(input.Arguments, ".")
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		query, _ := input.Arguments["query"].(string)
		if strings.TrimSpace(query) == "" {
			return map[string]any{"content": "query is required", "is_error": true}
		}
		maxResults := optionalInt(input.Arguments, "max_results", 50, 1, 200)
		includeContent, _ := input.Arguments["include_content"].(bool)
		if _, exists := input.Arguments["include_content"]; !exists {
			includeContent = true
		}
		caseSensitive, _ := input.Arguments["case_sensitive"].(bool)
		payload, err := searchWorkspaceFiles(path, query, maxResults, includeContent, caseSensitive)
		if err != nil {
			return map[string]any{"content": normalizeWorkspaceFileError("search_files", path, err), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	default:
		return map[string]any{"content": "unknown tool", "is_error": true}
	}
}

func deleteWorkspacePath(path string, recursive bool) (map[string]any, error) {
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(path) == root {
		return nil, fmt.Errorf("refusing to delete workspace root")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	status := "deleted_file"
	if info.IsDir() {
		if !recursive {
			return nil, fmt.Errorf("path is a directory; set recursive=true to delete directories")
		}
		status = "deleted_directory"
		if err := os.RemoveAll(path); err != nil {
			return nil, err
		}
	} else if err := os.Remove(path); err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "relative_path": relativeWorkspacePath(path), "status": status, "recursive": recursive}, nil
}

func listWorkspaceTree(path string, maxDepth, maxEntries int) (map[string]any, error) {
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	rootRel := relativeWorkspacePath(path)
	items := make([]map[string]any, 0, maxEntries)
	truncated := false
	err = filepath.WalkDir(path, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current != path && shouldHideRootEntry(filepath.Dir(current), entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err := ensureResolvedPathWithinWorkspace(root, current, current); err != nil {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, depth := relativeDepth(path, current)
		if depth > maxDepth {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(items) >= maxEntries {
			truncated = true
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, statErr := entry.Info()
		item := map[string]any{"path": current, "relative_path": relativeWorkspacePath(current), "name": entry.Name(), "is_dir": entry.IsDir(), "depth": depth}
		if rel == "." && rootRel != "." {
			item["name"] = filepath.Base(rootRel)
		}
		if statErr == nil {
			item["size"] = info.Size()
			item["mode"] = info.Mode().String()
			item["modified"] = info.ModTime().Format("2006-01-02T15:04:05Z07:00")
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "relative_path": rootRel, "max_depth": maxDepth, "max_entries": maxEntries, "truncated": truncated, "entries": items}, nil
}

func workspaceFileInfo(path string) (map[string]any, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":          path,
		"relative_path": relativeWorkspacePath(path),
		"name":          filepath.Base(path),
		"is_dir":        info.IsDir(),
		"size":          info.Size(),
		"mode":          info.Mode().String(),
		"modified":      info.ModTime().Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func searchWorkspaceFiles(path, query string, maxResults int, includeContent, caseSensitive bool) (map[string]any, error) {
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	needle := query
	if !caseSensitive {
		needle = strings.ToLower(needle)
	}
	matches := make([]map[string]any, 0, maxResults)
	visited := 0
	truncated := false
	err = filepath.WalkDir(path, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current != path && shouldHideRootEntry(filepath.Dir(current), entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err := ensureResolvedPathWithinWorkspace(root, current, current); err != nil {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= maxResults {
			truncated = true
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > maxSearchFiles {
			truncated = true
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		haystack := entry.Name()
		if !caseSensitive {
			haystack = strings.ToLower(haystack)
		}
		if strings.Contains(haystack, needle) {
			matches = append(matches, map[string]any{"path": current, "relative_path": relativeWorkspacePath(current), "is_dir": entry.IsDir(), "match_type": "name", "line_number": 0, "snippet": entry.Name()})
		}
		if entry.IsDir() || !includeContent || len(matches) >= maxResults {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil || info.Size() > maxReadBytes {
			return nil
		}
		data, readErr := os.ReadFile(current)
		if readErr != nil {
			return nil
		}
		text, textErr := decodeUTF8Text(data)
		if textErr != nil {
			return nil
		}
		lines := splitLines(text)
		for i, line := range lines {
			haystackLine := line
			if !caseSensitive {
				haystackLine = strings.ToLower(haystackLine)
			}
			if strings.Contains(haystackLine, needle) {
				matches = append(matches, map[string]any{"path": current, "relative_path": relativeWorkspacePath(current), "is_dir": false, "match_type": "content", "line_number": i + 1, "snippet": truncateDiffLine(strings.TrimSpace(line))})
				if len(matches) >= maxResults {
					truncated = true
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return fmt.Sprint(matches[i]["relative_path"]) < fmt.Sprint(matches[j]["relative_path"])
	})
	return map[string]any{"path": path, "relative_path": relativeWorkspacePath(path), "query": query, "case_sensitive": caseSensitive, "include_content": includeContent, "max_results": maxResults, "truncated": truncated, "matches": matches}, nil
}

func decodeUTF8Text(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", fmt.Errorf("file is not valid UTF-8 text; use a binary inspection tool for non-text files")
	}
	return strings.TrimPrefix(string(data), "\ufeff"), nil
}

func normalizeWorkspaceFileError(toolName, path string, err error) string {
	if err == nil {
		return ""
	}
	if os.IsNotExist(err) {
		return fmt.Sprintf("%s path not found: %s", toolName, path)
	}
	if os.IsPermission(err) {
		return fmt.Sprintf("%s permission denied: %s", toolName, path)
	}
	if pe, ok := err.(*os.PathError); ok {
		switch {
		case os.IsNotExist(pe.Err):
			return fmt.Sprintf("%s path not found: %s", toolName, path)
		case os.IsPermission(pe.Err):
			return fmt.Sprintf("%s permission denied: %s", toolName, path)
		}
	}
	return fmt.Sprintf("%s failed for %s: %v", toolName, path, err)
}

type writeChangeStats struct {
	Status       string
	OldStart     int
	OldEnd       int
	NewStart     int
	NewEnd       int
	AddedLines   int
	DeletedLines int
	OldLineCount int
	NewLineCount int
}

func analyzeWriteChange(oldContent, newContent string, existed bool) writeChangeStats {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)
	stats := writeChangeStats{OldLineCount: len(oldLines), NewLineCount: len(newLines)}
	if !existed {
		stats.Status = "created"
		stats.AddedLines = len(newLines)
		if len(newLines) > 0 {
			stats.NewStart = 1
			stats.NewEnd = len(newLines)
		}
		return stats
	}
	if oldContent == newContent {
		stats.Status = "unchanged"
		if len(oldLines) > 0 {
			stats.OldStart = 1
			stats.OldEnd = len(oldLines)
			stats.NewStart = 1
			stats.NewEnd = len(newLines)
		}
		return stats
	}
	if len(newLines) == 0 {
		stats.Status = "cleared"
		stats.DeletedLines = len(oldLines)
		if len(oldLines) > 0 {
			stats.OldStart = 1
			stats.OldEnd = len(oldLines)
		}
		return stats
	}
	prefix, oldEnd, newEnd := changedLineBounds(oldLines, newLines)
	stats.Status = "modified"
	stats.DeletedLines = maxInt(0, oldEnd-prefix+1)
	stats.AddedLines = maxInt(0, newEnd-prefix+1)
	if stats.DeletedLines > 0 {
		stats.OldStart = prefix + 1
		stats.OldEnd = oldEnd + 1
	}
	if stats.AddedLines > 0 {
		stats.NewStart = prefix + 1
		stats.NewEnd = newEnd + 1
	}
	return stats
}

func (s writeChangeStats) Summary() string {
	switch s.Status {
	case "created":
		if s.NewLineCount == 0 {
			return "created empty file"
		}
		return fmt.Sprintf("created file with %d lines (+%d -0)", s.NewLineCount, s.AddedLines)
	case "unchanged":
		return fmt.Sprintf("unchanged file (%d lines)", s.NewLineCount)
	case "cleared":
		return fmt.Sprintf("cleared file old lines %s (+0 -%d)", s.OldRange(), s.DeletedLines)
	case "modified":
		switch {
		case s.AddedLines > 0 && s.DeletedLines == 0:
			return fmt.Sprintf("inserted new lines %s (+%d -0)", s.NewRange(), s.AddedLines)
		case s.AddedLines == 0 && s.DeletedLines > 0:
			return fmt.Sprintf("removed old lines %s (+0 -%d)", s.OldRange(), s.DeletedLines)
		default:
			return fmt.Sprintf("changed old lines %s -> new lines %s (+%d -%d)", s.OldRange(), s.NewRange(), s.AddedLines, s.DeletedLines)
		}
	default:
		return "changed"
	}
}

func (s writeChangeStats) OldRange() string {
	return formatLineRange(s.OldStart, s.OldEnd)
}

func (s writeChangeStats) NewRange() string {
	return formatLineRange(s.NewStart, s.NewEnd)
}

func formatLineRange(start, end int) string {
	if start <= 0 || end <= 0 || end < start {
		return "-"
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func summarizeDiffPreview(oldContent, newContent string, stats writeChangeStats) string {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)
	switch stats.Status {
	case "created":
		entries := make([]diffEntry, 0, len(newLines))
		for i, line := range newLines {
			entries = append(entries, diffEntry{prefix: "+", newLine: i + 1, line: line})
		}
		return formatDiffEntries(0, 0, 1, len(newLines), entries)
	case "unchanged":
		return ""
	case "cleared":
		entries := make([]diffEntry, 0, len(oldLines))
		for i, line := range oldLines {
			entries = append(entries, diffEntry{prefix: "-", oldLine: i + 1, line: line})
		}
		return formatDiffEntries(1, len(oldLines), 0, 0, entries)
	}
	prefix, oldEnd, newEnd := changedLineBounds(oldLines, newLines)
	contextBefore := 2
	contextAfter := 2
	oldStart := maxInt(0, prefix-contextBefore)
	newStart := maxInt(0, prefix-contextBefore)
	oldStop := minInt(len(oldLines)-1, maxInt(oldEnd, prefix)+contextAfter)
	newStop := minInt(len(newLines)-1, newEnd+contextAfter)
	entries := make([]diffEntry, 0, (oldStop-oldStart+1)+(newStop-newStart+1))
	for i := oldStart; i < prefix && i < len(oldLines); i++ {
		entries = append(entries, diffEntry{prefix: " ", oldLine: i + 1, newLine: i + 1, line: oldLines[i]})
	}
	for i := prefix; i <= oldEnd && i < len(oldLines); i++ {
		entries = append(entries, diffEntry{prefix: "-", oldLine: i + 1, line: oldLines[i]})
	}
	for i := prefix; i <= newEnd && i < len(newLines); i++ {
		entries = append(entries, diffEntry{prefix: "+", newLine: i + 1, line: newLines[i]})
	}
	for i := newEnd + 1; i <= newStop && i < len(newLines); i++ {
		oldLine := i - stats.AddedLines + stats.DeletedLines + 1
		entries = append(entries, diffEntry{prefix: " ", oldLine: oldLine, newLine: i + 1, line: newLines[i]})
	}
	return formatDiffEntries(oldStart+1, maxInt(0, oldStop-oldStart+1), newStart+1, maxInt(0, newStop-newStart+1), entries)
}

func changedLineBounds(oldLines, newLines []string) (int, int, int) {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	oldEnd := len(oldLines) - 1
	newEnd := len(newLines) - 1
	for oldEnd >= prefix && newEnd >= prefix && oldLines[oldEnd] == newLines[newEnd] {
		oldEnd--
		newEnd--
	}
	return prefix, oldEnd, newEnd
}

type diffEntry struct {
	prefix  string
	oldLine int
	newLine int
	line    string
}

func formatDiffEntries(oldStart, oldCount, newStart, newCount int, entries []diffEntry) string {
	const maxPreviewLines = 18
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount)
	written := 1
	for _, entry := range entries {
		if written >= maxPreviewLines {
			b.WriteString("\n...")
			break
		}
		b.WriteString("\n")
		b.WriteString(entry.prefix)
		b.WriteString(formatDiffLineNumbers(entry.oldLine, entry.newLine))
		b.WriteString(truncateDiffLine(entry.line))
		written++
	}
	return b.String()
}

func formatDiffLineNumbers(oldLine, newLine int) string {
	oldText := ""
	newText := ""
	if oldLine > 0 {
		oldText = fmt.Sprintf("%d", oldLine)
	}
	if newLine > 0 {
		newText = fmt.Sprintf("%d", newLine)
	}
	return fmt.Sprintf(" %4s %4s | ", oldText, newText)
}

func truncateDiffLine(line string) string {
	const maxRunes = 120
	runes := []rune(strings.ReplaceAll(line, "\t", "    "))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes-3]) + "..."
}

func splitLines(content string) []string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.TrimRight(normalized, "\n")
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "\n")
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func extractPath(args map[string]interface{}) (string, error) {
	raw, _ := args["path"].(string)
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}
	return resolveWorkspacePath(raw, workspaceRoot)
}

func extractOptionalPath(args map[string]interface{}, fallback string) (string, error) {
	raw, _ := args["path"].(string)
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	return resolveWorkspacePath(raw, workspaceRoot)
}

func optionalInt(args map[string]interface{}, key string, fallback, minimum, maximum int) int {
	value := fallback
	switch typed := args[key].(type) {
	case float64:
		value = int(typed)
	case int:
		value = typed
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			value = int(parsed)
		}
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func resolveWorkspacePath(raw, root string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("path is required")
	}
	cleanRoot, err := canonicalWorkspaceRoot(root)
	if err != nil {
		return "", err
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(cleanRoot, candidate)
	}
	cleanCandidate, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if !isWithinBase(cleanRoot, cleanCandidate) {
		return "", fmt.Errorf("path %q escapes workspace root", raw)
	}
	if err := ensureResolvedPathWithinWorkspace(cleanRoot, cleanCandidate, raw); err != nil {
		return "", err
	}
	return cleanCandidate, nil
}

func canonicalWorkspaceRoot(root string) (string, error) {
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

func ensureResolvedPathWithinWorkspace(root, candidate, original string) error {
	check := candidate
	for {
		if _, err := os.Lstat(check); err == nil {
			break
		}
		parent := filepath.Dir(check)
		if parent == check {
			return fmt.Errorf("path %q has no existing parent inside workspace", original)
		}
		check = parent
	}
	realPath, err := filepath.EvalSymlinks(check)
	if err != nil {
		return nil
	}
	realPath, err = filepath.Abs(filepath.Clean(realPath))
	if err != nil {
		return fmt.Errorf("resolve real path: %w", err)
	}
	if !isWithinBase(root, realPath) {
		return fmt.Errorf("path %q resolves outside workspace root", original)
	}
	return nil
}

func isWithinBase(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func shouldHideRootEntry(path, name string) bool {
	if name != ".goflow" {
		return false
	}
	cleanRoot, err := filepath.Abs(filepath.Clean(workspaceRoot))
	if err != nil {
		return false
	}
	cleanPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	return cleanRoot == cleanPath
}

func relativeWorkspacePath(path string) string {
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return path
	}
	cleanPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(root, cleanPath)
	if err != nil || rel == "" {
		return path
	}
	return filepath.ToSlash(rel)
}

func relativeDepth(base, path string) (string, int) {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "" {
		return ".", 0
	}
	if rel == "." {
		return rel, 0
	}
	return rel, strings.Count(filepath.ToSlash(rel), "/") + 1
}

func mustWorkspaceRoot() string {
	if root := strings.TrimSpace(os.Getenv("GOFLOW_WORKSPACE_ROOT")); root != "" {
		return root
	}
	root, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("resolve workspace root: %v", err))
	}
	return root
}

func write(writer *bufio.Writer, resp response) {
	data, _ := json.Marshal(resp)
	_, _ = writer.Write(append(data, '\n'))
	_ = writer.Flush()
}
