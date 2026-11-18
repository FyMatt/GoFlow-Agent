package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// batchReadFileLimit caps how many files one read_files call may return.
const batchReadFileLimit = 10

// readFilesBatch reads several workspace files in one tool call. Reading is the
// most common single-file round trip; batching it removes one model turn per
// extra file, and every turn costs a full prompt plus fresh output tokens.
func readFilesBatch(args map[string]interface{}) (map[string]any, error) {
	rawFiles, ok := args["files"].([]interface{})
	if !ok || len(rawFiles) == 0 {
		return nil, fmt.Errorf("files must be a non-empty array")
	}
	if len(rawFiles) > batchReadFileLimit {
		return nil, fmt.Errorf("files exceeds the %d file limit", batchReadFileLimit)
	}
	results := make([]map[string]any, 0, len(rawFiles))
	// A total batch budget was tried at 600 lines: it forced extra reads and
	// raised the turn count instead of lowering cost. Keep the per-file window
	// only; the diff-preview cap and read dedupe handle the rest.
	for index, raw := range rawFiles {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("files[%d] must be an object", index)
		}
		path, err := extractPath(entry)
		if err != nil {
			results = append(results, map[string]any{"error": err.Error()})
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			results = append(results, map[string]any{"path": relativeWorkspacePath(path), "error": normalizeWorkspaceFileError("read_files", path, err)})
			continue
		}
		if info.IsDir() {
			results = append(results, map[string]any{"path": relativeWorkspacePath(path), "error": "path is a directory"})
			continue
		}
		if info.Size() > maxReadBytes {
			results = append(results, map[string]any{"path": relativeWorkspacePath(path), "error": fmt.Sprintf("file too large: %d bytes exceeds %d", info.Size(), maxReadBytes)})
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			results = append(results, map[string]any{"path": relativeWorkspacePath(path), "error": err.Error()})
			continue
		}
		content, err := decodeUTF8Text(data)
		if err != nil {
			results = append(results, map[string]any{"path": relativeWorkspacePath(path), "error": err.Error()})
			continue
		}
		window, truncated, nextOffset, totalLines := boundedReadWindow(content, map[string]interface{}{"limit": float64(200)})
		fileEntry := map[string]any{
			"path":      relativeWorkspacePath(path),
			"size":      info.Size(),
			"sha256":    sha256Hex(data),
			"lines":     totalLines,
			"content":   window,
			"truncated": truncated,
		}
		if truncated {
			fileEntry["next_offset"] = nextOffset
		}
		results = append(results, fileEntry)
	}
	return map[string]any{"files": results, "count": len(results)}, nil
}

var _ = json.Marshal
