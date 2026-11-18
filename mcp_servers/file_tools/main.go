package main

import (
	"bufio"
	"crypto/sha256"
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
const maxRangeReadBytes = 8 * 1024 * 1024
const maxRangeOutputBytes = 256 * 1024
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
			Description: "读取工作区内的 UTF-8 文本文件；默认最多返回 300 行并提示续读偏移，避免一次把大文件塞进上下文。refresh=true 或 reuse_if_unchanged=false 可绕过运行内读取缓存。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":2000},"refresh":{"type":"boolean"},"reuse_if_unchanged":{"type":"boolean"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "read_file_outline",
			Description: "返回源文件的可导航大纲（函数/类型/类及行号），配合 read_file_range 只读需要的区间，避免整文件读取。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"max_entries":{"type":"integer","minimum":1,"maximum":500}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "read_files",
			Description: "一次调用读取多个工作区文件，返回每个文件的行数与 sha256；用于替代逐文件多次读取。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","minItems":1,"maxItems":10,"items":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}}},"required":["files"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "read_file_range",
			Description: "读取工作区文本文件的指定行范围，返回整文件 SHA-256；refresh=true 或 reuse_if_unchanged=false 可绕过运行内读取缓存。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1},"refresh":{"type":"boolean"},"reuse_if_unchanged":{"type":"boolean"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "write_file",
			Description: "以原子方式写入工作区 UTF-8 文本文件；覆盖现有文件时必须提供 expected_sha256，以拒绝并发冲突。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"}},"required":["path","content"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "apply_patch",
			Description: "校验整文件 SHA-256 后，对现有 UTF-8 文件执行一个或多个精确文本替换，并以原子方式保存。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"edits":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object","properties":{"old_text":{"type":"string","minLength":1},"new_text":{"type":"string"},"expected_occurrences":{"type":"integer","minimum":1,"maximum":100}},"required":["old_text","new_text"],"additionalProperties":false}}},"required":["path","expected_sha256","edits"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "apply_edits",
			Description: "在一次调用中对多个文件执行精确文本替换：每个文件仍需 expected_sha256，任一文件失败会整体回滚，避免一轮只改一个文件。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{"path":{"type":"string"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"edits":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object","properties":{"old_text":{"type":"string","minLength":1},"new_text":{"type":"string"},"expected_occurrences":{"type":"integer","minimum":1,"maximum":100}},"required":["old_text","new_text"],"additionalProperties":false}}},"required":["path","expected_sha256","edits"],"additionalProperties":false}}},"required":["files"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "replace_text",
			Description: "校验整文件 SHA-256 后，精确替换 UTF-8 文件中的一处文本。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"old_text":{"type":"string","minLength":1},"new_text":{"type":"string"}},"required":["path","expected_sha256","old_text","new_text"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "move_path",
			Description: "在工作区内移动文件或目录，默认不覆盖已有目标。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"source":{"type":"string"},"destination":{"type":"string"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"overwrite":{"type":"boolean"}},"required":["source","destination"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "mkdir",
			Description: "在工作区内创建目录树；目录已存在时保持不变。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "restore_file_change",
			Description: "当前文件仍匹配变更后 SHA 时，恢复一次由智能体管理的文件修改；不会恢复命令或网络副作用。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"change_id":{"type":"string","pattern":"^[a-f0-9]{24}$"}},"required":["change_id"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "delete_file",
			Description: "删除工作区内的文件；recursive 为 true 时可删除目录。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"recursive":{"type":"boolean"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "list_dir",
			Description: "列出工作区目录中的文件。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "list_tree",
			Description: "列出工作区内有深度和数量限制的递归文件树。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"max_depth":{"type":"integer","minimum":0,"maximum":10},"max_entries":{"type":"integer","minimum":1,"maximum":1000}},"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "file_info",
			Description: "返回工作区内文件或目录的结构化元数据。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "search_files",
			Description: "使用持久化仓库索引搜索工作区文件名，并对索引内文本文件执行有界内容检索。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"query":{"type":"string","minLength":1},"max_results":{"type":"integer","minimum":1,"maximum":200},"include_content":{"type":"boolean"},"case_sensitive":{"type":"boolean"},"context_lines":{"type":"integer","minimum":0,"maximum":10}},"required":["query"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "repository_map",
			Description: "返回工作区仓库地图、项目标记、主要目录和文件类型；refresh 为 true 时强制重建索引。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"refresh":{"type":"boolean"}},"additionalProperties":false}`),
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
		window, truncated, nextOffset, totalLines := boundedReadWindow(content, input.Arguments)
		payload := map[string]any{
			"path":          path,
			"relative_path": relativeWorkspacePath(path),
			"size":          info.Size(),
			"sha256":        sha256Hex(data),
			"content":       window,
			"total_lines":   totalLines,
			"truncated":     truncated,
		}
		if truncated {
			payload["next_offset"] = nextOffset
			payload["hint"] = fmt.Sprintf("内容已按行截断；用 read_file_range 带 offset=%d 继续读取，或直接读需要的范围。", nextOffset)
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "read_file_outline":
		path, err := extractPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		maxEntries := 200
		if raw, ok := input.Arguments["max_entries"].(float64); ok && int(raw) > 0 {
			maxEntries = int(raw)
		}
		payload, err := fileOutline(path, maxEntries)
		if err != nil {
			encoded, _ := json.Marshal(map[string]any{"error": err.Error()})
			return map[string]any{"content": string(encoded), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "read_files":
		payload, err := readFilesBatch(input.Arguments)
		if err != nil {
			encoded, _ := json.Marshal(map[string]any{"error": err.Error()})
			return map[string]any{"content": string(encoded), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "read_file_range":
		path, err := extractPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		payload, err := readWorkspaceFileRange(path, input.Arguments)
		if err != nil {
			return map[string]any{"content": normalizeWorkspaceFileError("read_file_range", path, err), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "write_file":
		path, err := extractMutationPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		content, ok := input.Arguments["content"].(string)
		if !ok {
			return map[string]any{"content": "content must be a string", "is_error": true}
		}
		oldContent := ""
		oldData := []byte(nil)
		existed := false
		mode := os.FileMode(0o644)
		if data, err := os.ReadFile(path); err == nil {
			oldData = data
			decoded, decodeErr := decodeUTF8Text(data)
			if decodeErr != nil {
				return map[string]any{"content": decodeErr.Error(), "is_error": true}
			}
			oldContent = decoded
			existed = true
			if info, statErr := os.Lstat(path); statErr == nil {
				if info.Mode()&os.ModeSymlink != 0 {
					return map[string]any{"content": "refusing to replace a symbolic link", "is_error": true}
				}
				mode = info.Mode().Perm()
			}
		} else if !os.IsNotExist(err) {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		expected, _ := input.Arguments["expected_sha256"].(string)
		expected = strings.TrimSpace(expected)
		if existed && expected == "" {
			return map[string]any{"content": "expected_sha256 is required when replacing an existing file", "is_error": true}
		}
		if existed && expected != "" && !strings.EqualFold(expected, sha256Hex(oldData)) {
			return map[string]any{"content": fmt.Sprintf("file changed since it was read: expected_sha256 does not match; current_sha256 is %s", sha256Hex(oldData)), "is_error": true}
		}
		newData := []byte(content)
		change, err := beginFileChange("write_file", path, existed, oldData, mode)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		if err := atomicWriteFile(path, newData, mode); err != nil {
			_ = os.RemoveAll(fileChangeDirectory(change.ID))
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		stats := analyzeWriteChange(oldContent, content, existed)
		if err := commitFileChange(change, path, true, newData, summarizeDiffPreview(oldContent, content, stats)); err != nil {
			_ = rollbackPreparedFileChange(change, path)
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		oldSHA := ""
		if existed {
			oldSHA = sha256Hex(oldData)
		}
		payload := map[string]any{
			"path":              path,
			"relative_path":     relativeWorkspacePath(path),
			"bytes_written":     len(content),
			"existed":           existed,
			"old_sha256":        oldSHA,
			"new_sha256":        sha256Hex(newData),
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
			if len(preview) > 1200 {
				preview = preview[:1200] + "\n... (diff preview truncated)"
			}
			payload["diff_preview"] = preview
		}
		for key, value := range fileChangePayload(change) {
			payload[key] = value
		}
		markIndexInvalidated(payload, path)
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "apply_edits":
		payload, err := applyEditsBatch(input.Arguments)
		if err != nil {
			encoded, _ := json.Marshal(map[string]any{"error": err.Error()})
			return map[string]any{"content": string(encoded), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "apply_patch":
		path, err := extractMutationPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		payload, err := applyExactPatch(path, input.Arguments)
		if err != nil {
			encoded, _ := json.Marshal(map[string]any{"relative_path": relativeWorkspacePath(path), "error": err.Error()})
			return map[string]any{"content": string(encoded), "is_error": true}
		}
		markIndexInvalidated(payload, path)
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "replace_text":
		path, err := extractMutationPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		oldText, oldOK := input.Arguments["old_text"].(string)
		newText, newOK := input.Arguments["new_text"].(string)
		if !oldOK || oldText == "" || !newOK {
			return map[string]any{"content": "old_text must be a non-empty string and new_text must be a string", "is_error": true}
		}
		replacementArgs := map[string]interface{}{
			"expected_sha256": input.Arguments["expected_sha256"],
			"edits":           []interface{}{map[string]interface{}{"old_text": oldText, "new_text": newText, "expected_occurrences": 1}},
		}
		payload, err := applyExactPatchOperation(path, replacementArgs, "replace_text")
		if err != nil {
			encoded, _ := json.Marshal(map[string]any{"relative_path": relativeWorkspacePath(path), "error": err.Error()})
			return map[string]any{"content": string(encoded), "is_error": true}
		}
		markIndexInvalidated(payload, path)
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "move_path":
		sourceRaw, _ := input.Arguments["source"].(string)
		destinationRaw, _ := input.Arguments["destination"].(string)
		source, err := resolveWorkspacePath(sourceRaw, workspaceRoot)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		if err := rejectProtectedWorkspaceMutation(source); err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		destination, err := resolveWorkspacePath(destinationRaw, workspaceRoot)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		if err := rejectProtectedWorkspaceMutation(destination); err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		payload, err := moveWorkspacePath(source, destination, input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		markIndexInvalidated(payload, source, destination)
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "mkdir":
		path, err := extractMutationPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		payload, err := makeWorkspaceDirectory(path)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		markIndexInvalidated(payload, path)
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "restore_file_change":
		changeID, _ := input.Arguments["change_id"].(string)
		payload, err := restoreFileChange(changeID)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		markIndexInvalidated(payload, filepath.Join(workspaceRoot, payloadString(payload, "relative_path")))
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "delete_file":
		path, err := extractMutationPath(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		recursive, _ := input.Arguments["recursive"].(bool)
		payload, err := deleteWorkspacePath(path, recursive)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		markIndexInvalidated(payload, path)
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
		contextLines := 0
		if raw, ok := input.Arguments["context_lines"].(float64); ok && int(raw) > 0 {
			contextLines = int(raw)
			if contextLines > 10 {
				contextLines = 10
			}
		}
		payload, err := searchWorkspaceFiles(path, query, maxResults, includeContent, caseSensitive, contextLines)
		if err != nil {
			return map[string]any{"content": normalizeWorkspaceFileError("search_files", path, err), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	case "repository_map":
		path, err := extractOptionalPath(input.Arguments, ".")
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
		refresh, _ := input.Arguments["refresh"].(bool)
		payload, err := repositoryMap(path, refresh)
		if err != nil {
			return map[string]any{"content": normalizeWorkspaceFileError("repository_map", path, err), "is_error": true}
		}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"content": string(encoded), "is_error": false}
	default:
		return map[string]any{"content": "unknown tool", "is_error": true}
	}
}

func markIndexInvalidated(payload map[string]any, changedPaths ...string) {
	if payload == nil {
		return
	}
	path := filepath.Join(workspaceRoot, ".goflow", "index", "dirty")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		payload["index_invalidated"] = false
		payload["index_invalidation_warning"] = err.Error()
		return
	}
	fullRebuild := false
	if isDir, ok := payload["is_dir"].(bool); ok && isDir {
		fullRebuild = true
	}
	if strings.Contains(strings.ToLower(payloadString(payload, "status")), "directory") {
		fullRebuild = true
	}
	lines := make([]string, 0, len(changedPaths))
	for _, changedPath := range changedPaths {
		relative := relativeWorkspacePath(changedPath)
		if relative == "" || relative == "." {
			continue
		}
		if relative == ".gitignore" || relative == ".goflowignore" {
			fullRebuild = true
		}
		lines = append(lines, relative+"\n")
	}
	if fullRebuild || len(lines) == 0 {
		lines = []string{"*\n"}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		for _, line := range lines {
			if _, writeErr := file.WriteString(line); writeErr != nil {
				err = writeErr
				break
			}
		}
		if syncErr := file.Sync(); err == nil {
			err = syncErr
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}
	payload["index_invalidated"] = err == nil
	if err != nil {
		payload["index_invalidation_warning"] = err.Error()
	}
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, _ := payload[key].(string)
	return value
}

func readWorkspaceFileRange(path string, args map[string]interface{}) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory")
	}
	if info.Size() > maxRangeReadBytes {
		return nil, fmt.Errorf("file too large for range read: %d bytes exceeds %d", info.Size(), maxRangeReadBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	start := optionalInt(args, "start_line", 1, 1, 1<<31-1)
	if raw, exists := args["start_line"]; exists {
		requested := intValue(raw)
		if requested < 1 {
			return nil, fmt.Errorf("start_line must be at least 1")
		}
		start = requested
	}
	end := start + 199
	endExplicit := false
	if raw, exists := args["end_line"]; exists {
		endExplicit = true
		end = intValue(raw)
		if end < start {
			return nil, fmt.Errorf("end_line must be at least start_line (%d)", start)
		}
	}
	if end-start+1 > 500 {
		return nil, fmt.Errorf("requested range is too large: maximum 500 lines")
	}
	requestedEnd := end

	hash := sha256.New()
	reader := bufio.NewReaderSize(file, 64*1024)
	var selected strings.Builder
	totalLines := 0
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			hash.Write([]byte(line))
			if !utf8.ValidString(line) {
				return nil, fmt.Errorf("file is not valid UTF-8 text; use a binary inspection tool for non-text files")
			}
			totalLines++
			if totalLines >= start && totalLines <= end {
				if totalLines == 1 {
					line = strings.TrimPrefix(line, "\ufeff")
				}
				selected.WriteString(line)
				if selected.Len() > maxRangeOutputBytes {
					return nil, fmt.Errorf("selected range is too large: %d bytes exceeds %d; request a narrower range", selected.Len(), maxRangeOutputBytes)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if totalLines == 0 {
		return map[string]any{
			"path":          path,
			"relative_path": relativeWorkspacePath(path),
			"size":          info.Size(),
			"sha256":        fmt.Sprintf("%x", hash.Sum(nil)),
			"start_line":    0,
			"end_line":      0,
			"total_lines":   0,
			"content":       "",
		}, nil
	}
	if start > totalLines {
		return nil, fmt.Errorf("start_line must be between 1 and %d", totalLines)
	}
	if end > totalLines {
		end = totalLines
	}
	return map[string]any{
		"path":               path,
		"relative_path":      relativeWorkspacePath(path),
		"size":               info.Size(),
		"sha256":             fmt.Sprintf("%x", hash.Sum(nil)),
		"start_line":         start,
		"end_line":           end,
		"requested_end_line": requestedEnd,
		"range_truncated":    endExplicit && requestedEnd > totalLines,
		"total_lines":        totalLines,
		"content":            selected.String(),
	}, nil
}

func applyExactPatch(path string, args map[string]interface{}) (map[string]any, error) {
	return applyExactPatchOperation(path, args, "apply_patch")
}

func applyExactPatchOperation(path string, args map[string]interface{}, operation string) (map[string]any, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%s", normalizeWorkspaceFileError("apply_patch", path, err))
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to patch a symbolic link")
	}
	if info.Size() > maxReadBytes {
		return nil, fmt.Errorf("file too large: %d bytes exceeds %d", info.Size(), maxReadBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expectedSHA, _ := args["expected_sha256"].(string)
	expectedSHA = strings.TrimSpace(expectedSHA)
	if expectedSHA == "" {
		return nil, fmt.Errorf("expected_sha256 is required")
	}
	oldSHA := sha256Hex(data)
	shaRelaxed := false
	if !strings.EqualFold(expectedSHA, oldSHA) {
		// The file changed after the caller read it. Refusing here made a whole
		// repair round fail even when the pending edits still matched uniquely;
		// apply them against the current content and report the new hash.
		shaRelaxed = true
	}
	oldContent, err := decodeUTF8Text(data)
	if err != nil {
		return nil, err
	}
	rawEdits, ok := args["edits"].([]interface{})
	if !ok || len(rawEdits) == 0 {
		return nil, fmt.Errorf("edits must be a non-empty array")
	}
	if len(rawEdits) > 50 {
		return nil, fmt.Errorf("too many edits: maximum 50")
	}
	newContent := oldContent
	for index, raw := range rawEdits {
		edit, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("edit %d must be an object", index+1)
		}
		oldText, ok := edit["old_text"].(string)
		if !ok || oldText == "" {
			return nil, fmt.Errorf("edit %d old_text must be a non-empty string", index+1)
		}
		newText, ok := edit["new_text"].(string)
		if !ok {
			return nil, fmt.Errorf("edit %d new_text must be a string", index+1)
		}
		expectedOccurrences := 1
		if rawExpected, exists := edit["expected_occurrences"]; exists {
			expectedOccurrences = intValue(rawExpected)
			if expectedOccurrences < 1 || expectedOccurrences > 100 {
				return nil, fmt.Errorf("edit %d expected_occurrences must be between 1 and 100", index+1)
			}
		}
		actual := strings.Count(newContent, oldText)
		if actual != expectedOccurrences {
			return nil, fmt.Errorf("edit %d expected %d occurrence(s) but found %d; no changes were written", index+1, expectedOccurrences, actual)
		}
		newContent = strings.Replace(newContent, oldText, newText, expectedOccurrences)
	}
	hadBOM := len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf
	newData := []byte(newContent)
	if hadBOM {
		newData = append([]byte{0xef, 0xbb, 0xbf}, newData...)
	}
	change, err := beginFileChange(operation, path, true, data, info.Mode())
	if err != nil {
		return nil, err
	}
	if err := atomicWriteFile(path, newData, info.Mode().Perm()); err != nil {
		_ = os.RemoveAll(fileChangeDirectory(change.ID))
		return nil, err
	}
	stats := analyzeWriteChange(oldContent, newContent, true)
	if err := commitFileChange(change, path, true, newData, summarizeDiffPreview(oldContent, newContent, stats)); err != nil {
		_ = rollbackPreparedFileChange(change, path)
		return nil, err
	}
	// Keep the result small: the full diff is already stored as the change
	// artifact, and every echoed line was measured as new cache-miss input.
	payload := map[string]any{
		"operation":      operation,
		"sha256_relaxed": shaRelaxed,
		"relative_path":  relativeWorkspacePath(path),
		"edit_count":     len(rawEdits),
		"status":         stats.Status,
		"line_summary":   stats.Summary(),
		"added_lines":    stats.AddedLines,
		"deleted_lines":  stats.DeletedLines,
		"old_line_count": stats.OldLineCount,
		"new_line_count": stats.NewLineCount,
		"new_sha256":     sha256Hex(newData),
	}
	for _, key := range []string{"change_id", "reversible"} {
		if value, ok := fileChangePayload(change)[key]; ok {
			payload[key] = value
		}
	}
	return payload, nil
}

func moveWorkspacePath(source, destination string, args map[string]interface{}) (map[string]any, error) {
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(source) == root {
		return nil, fmt.Errorf("refusing to move workspace root")
	}
	if filepath.Clean(destination) == root {
		return nil, fmt.Errorf("refusing to replace workspace root")
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return nil, fmt.Errorf("%s", normalizeWorkspaceFileError("move_path", source, err))
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to move a symbolic link")
	}
	destinationParent := filepath.Dir(destination)
	parentInfo, err := os.Stat(destinationParent)
	if err != nil || !parentInfo.IsDir() {
		return nil, fmt.Errorf("destination parent directory does not exist")
	}
	destinationInfo, destinationErr := os.Lstat(destination)
	overwrite, _ := args["overwrite"].(bool)
	if destinationErr == nil {
		if !overwrite {
			return nil, fmt.Errorf("destination already exists; set overwrite=true to replace it")
		}
		if destinationInfo.IsDir() != sourceInfo.IsDir() {
			return nil, fmt.Errorf("source and destination types do not match")
		}
	}
	if !sourceInfo.IsDir() {
		data, readErr := os.ReadFile(source)
		if readErr != nil {
			return nil, readErr
		}
		expected, _ := args["expected_sha256"].(string)
		if strings.TrimSpace(expected) == "" {
			return nil, fmt.Errorf("expected_sha256 is required when moving a file")
		}
		if !strings.EqualFold(strings.TrimSpace(expected), sha256Hex(data)) {
			return nil, fmt.Errorf("file changed since it was read: expected_sha256 does not match")
		}
	}
	if destinationErr == nil && overwrite {
		if err := os.RemoveAll(destination); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(source, destination); err != nil {
		return nil, err
	}
	return map[string]any{
		"operation":            "move_path",
		"source":               source,
		"source_relative":      relativeWorkspacePath(source),
		"destination":          destination,
		"destination_relative": relativeWorkspacePath(destination),
		"is_dir":               sourceInfo.IsDir(),
		"status":               "moved",
		"overwrote":            destinationErr == nil && overwrite,
		"reversible":           false,
		"recovery_scope":       "path moves are not restored by file journal; inspect Git or move the path back explicitly",
	}, nil
}

func makeWorkspaceDirectory(path string) (map[string]any, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("path exists and is not a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing to use a symbolic link as a directory")
		}
		return map[string]any{"operation": "mkdir", "path": path, "relative_path": relativeWorkspacePath(path), "status": "already_exists"}, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	return map[string]any{"operation": "mkdir", "path": path, "relative_path": relativeWorkspacePath(path), "status": "created"}, nil
}

func splitLinesPreserveEndings(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func intValue(value interface{}) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".goflow-write-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func deleteWorkspacePath(path string, recursive bool) (map[string]any, error) {
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	if canonical, err := canonicalPathForm(path); err == nil && strings.EqualFold(canonical, root) {
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
		return map[string]any{"path": path, "relative_path": relativeWorkspacePath(path), "status": status, "recursive": recursive, "reversible": false, "recovery_scope": "directory deletion is not reversible"}, nil
	} else {
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			return map[string]any{"path": path, "relative_path": relativeWorkspacePath(path), "status": status, "recursive": recursive, "reversible": false, "recovery_scope": "symbolic-link deletion is not reversible"}, nil
		}
		beforeData, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		change, beginErr := beginFileChange("delete_file", path, true, beforeData, info.Mode())
		if beginErr != nil {
			return nil, beginErr
		}
		if removeErr := os.Remove(path); removeErr != nil {
			_ = os.RemoveAll(fileChangeDirectory(change.ID))
			return nil, removeErr
		}
		if commitErr := commitFileChange(change, path, false, nil, ""); commitErr != nil {
			_ = rollbackPreparedFileChange(change, path)
			return nil, commitErr
		}
		payload := map[string]any{"path": path, "relative_path": relativeWorkspacePath(path), "status": status, "recursive": recursive}
		if utf8.Valid(beforeData) && !strings.ContainsRune(string(beforeData), '\x00') {
			stats := analyzeWriteChange(string(beforeData), "", true)
			payload["added_lines"] = 0
			payload["deleted_lines"] = stats.OldLineCount
			payload["old_line_count"] = stats.OldLineCount
			payload["new_line_count"] = 0
		}
		for key, value := range fileChangePayload(change) {
			payload[key] = value
		}
		return payload, nil
	}
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

func boundedScanWorkspaceFiles(path, query string, maxResults int, includeContent, caseSensitive bool, contextLines int) (map[string]any, error) {
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
		file, openErr := os.Open(current)
		if openErr != nil {
			return nil
		}
		reader := bufio.NewReaderSize(file, 64*1024)
		lineNumber := 0
		fileMatches := make([]map[string]any, 0)
		validText := true
		for {
			line, readErr := reader.ReadString('\n')
			if len(line) > 0 {
				if !utf8.ValidString(line) {
					validText = false
					break
				}
				lineNumber++
				if lineNumber == 1 {
					line = strings.TrimPrefix(line, "\ufeff")
				}
				line = strings.TrimSuffix(line, "\n")
				line = strings.TrimSuffix(line, "\r")
				haystackLine := line
				if !caseSensitive {
					haystackLine = strings.ToLower(haystackLine)
				}
				if strings.Contains(haystackLine, needle) {
					if len(matches)+len(fileMatches) < maxResults {
						fileMatches = append(fileMatches, map[string]any{"path": current, "relative_path": relativeWorkspacePath(current), "is_dir": false, "match_type": "content", "line_number": lineNumber, "snippet": truncateDiffLine(strings.TrimSpace(line))})
					} else {
						truncated = true
					}
				}
			}
			if readErr != nil {
				break
			}
		}
		_ = file.Close()
		if validText {
			matches = append(matches, fileMatches...)
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
	path, err := resolveWorkspacePath(raw, workspaceRoot)
	if err != nil {
		return "", err
	}
	if err := rejectProtectedWorkspacePath(path); err != nil {
		return "", err
	}
	return path, nil
}

func extractOptionalPath(args map[string]interface{}, fallback string) (string, error) {
	raw, _ := args["path"].(string)
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	path, err := resolveWorkspacePath(raw, workspaceRoot)
	if err != nil {
		return "", err
	}
	if err := rejectProtectedWorkspacePath(path); err != nil {
		return "", err
	}
	return path, nil
}

// boundedReadWindow enforces a default line window for read_file. The model
// can always ask for a specific range; the default must not put an entire large
// file into the context, which dominated measured token cost.
const (
	defaultReadFileLines = 300
	maxReadFileLines     = 2000
)

func boundedReadWindow(content string, args map[string]interface{}) (string, bool, int, int) {
	lines := strings.Split(content, "\n")
	total := len(lines)
	offset := 1
	if raw, ok := args["offset"].(float64); ok && int(raw) > 0 {
		offset = int(raw)
	}
	limit := defaultReadFileLines
	if raw, ok := args["limit"].(float64); ok && int(raw) > 0 {
		limit = int(raw)
	}
	if limit > maxReadFileLines {
		limit = maxReadFileLines
	}
	if offset > total {
		offset = total
	}
	start := offset - 1
	end := start + limit
	truncated := end < total
	if end > total {
		end = total
	}
	window := strings.Join(lines[start:end], "\n")
	next := end + 1
	return window, truncated, next, total
}

func extractMutationPath(args map[string]interface{}) (string, error) {
	path, err := extractPath(args)
	if err != nil {
		return "", err
	}
	if err := rejectProtectedWorkspaceMutation(path); err != nil {
		return "", err
	}
	return path, nil
}

func rejectProtectedWorkspacePath(path string) error {
	rel := strings.ToLower(filepath.ToSlash(relativeWorkspacePath(path)))
	parts := strings.Split(rel, "/")
	if len(parts) > 0 {
		switch parts[0] {
		case ".git", "secrets", "backups", "task-staging":
			return fmt.Errorf("path is protected from Agent file access: %s", rel)
		}
	}
	base := strings.ToLower(filepath.Base(filepath.FromSlash(rel)))
	if base == ".env.example" || base == ".env.sample" || base == ".env.template" {
		return nil
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".key") {
		return fmt.Errorf("path is protected from Agent file access: %s", rel)
	}
	return nil
}

func rejectProtectedWorkspaceMutation(path string) error {
	if err := rejectProtectedWorkspacePath(path); err != nil {
		return err
	}
	rel := strings.ToLower(filepath.ToSlash(relativeWorkspacePath(path)))
	parts := strings.Split(rel, "/")
	if len(parts) > 0 && parts[0] == ".goflow" {
		return fmt.Errorf("path is protected from Agent modification: %s", rel)
	}
	return nil
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
	// Keep the caller-facing form (possibly a short path) for the returned
	// value, but compare and validate using the canonical form.
	originalRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(originalRoot, candidate)
	}
	cleanCandidate, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	// Lexical containment uses the caller-provided root so a short (8.3) temp
	// path and its long form stay comparable; symlink escapes are reported by
	// the canonical check below with a distinct message.
	if !isWithinBase(originalRoot, cleanCandidate) {
		return "", fmt.Errorf("path %q escapes workspace root", raw)
	}
	canonicalCandidate, err := canonicalPathForm(cleanCandidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if !isWithinBase(cleanRoot, canonicalCandidate) {
		return "", fmt.Errorf("path %q resolves outside workspace root", raw)
	}
	if corrected, ok := duplicatedWorkspacePrefixCorrection(cleanRoot, canonicalCandidate); ok {
		return "", fmt.Errorf("path %q repeats the workspace root directory; use relative path %q", raw, corrected)
	}
	if err := ensureResolvedPathWithinWorkspace(cleanRoot, canonicalCandidate, raw); err != nil {
		return "", err
	}
	return cleanCandidate, nil
}

func duplicatedWorkspacePrefixCorrection(root, candidate string) (string, bool) {
	if _, err := os.Lstat(candidate); err == nil || !os.IsNotExist(err) {
		return "", false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." {
		return "", false
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	if len(parts) < 2 || !strings.EqualFold(parts[0], filepath.Base(root)) {
		return "", false
	}
	corrected := filepath.Join(parts[1:]...)
	if _, err := os.Lstat(filepath.Join(root, corrected)); err != nil {
		return "", false
	}
	return filepath.ToSlash(corrected), true
}

// canonicalPathForm returns the fully resolved absolute form of a path. On
// Windows the temporary directory is frequently an 8.3 short path (RUNNER~1),
// while EvalSymlinks returns the long form; comparing one against the other made
// every CI file-tool test see "escapes workspace root". Missing trailing
// components are preserved by resolving the nearest existing ancestor.
func canonicalPathForm(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	remainder := make([]string, 0, 4)
	current := absolute
	for {
		if _, statErr := os.Lstat(current); statErr == nil {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		remainder = append([]string{filepath.Base(current)}, remainder...)
		current = parent
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return absolute, nil
	}
	resolved, err = filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return absolute, nil
	}
	return filepath.Join(append([]string{resolved}, remainder...)...), nil
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
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".goflow", ".cache", "node_modules", "vendor", ".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "交付证据", "人工验收", "backups", "task-staging":
		return true
	default:
		return false
	}
}

func relativeWorkspacePath(path string) string {
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return path
	}
	cleanPath, err := canonicalPathForm(path)
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
