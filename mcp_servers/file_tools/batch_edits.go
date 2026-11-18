package main

import (
	"fmt"
	"os"
)

// batchEditSnapshot keeps the original bytes of a file that a batch replaced so
// a later failure can restore the whole batch instead of leaving a half-applied
// multi-file change.
type batchEditSnapshot struct {
	path string
	data []byte
	mode os.FileMode
}

func restoreBatchEditSnapshots(snapshots []batchEditSnapshot) {
	for _, snapshot := range snapshots {
		_ = os.WriteFile(snapshot.path, snapshot.data, snapshot.mode)
	}
}

// applyEditsBatch applies exact patches to several files in one tool call. The
// agent otherwise spends one model turn per file, which is the dominant cost of
// a multi-module delivery. Every file still requires expected_sha256, and a
// failure anywhere restores every file already written by this call.
func applyEditsBatch(args map[string]interface{}) (map[string]any, error) {
	rawFiles, ok := args["files"].([]interface{})
	if !ok || len(rawFiles) == 0 {
		return nil, fmt.Errorf("files must be a non-empty array")
	}
	if len(rawFiles) > 20 {
		return nil, fmt.Errorf("files exceeds the 20 file limit")
	}
	snapshots := make([]batchEditSnapshot, 0, len(rawFiles))
	applied := make([]map[string]any, 0, len(rawFiles))
	for index, raw := range rawFiles {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			restoreBatchEditSnapshots(snapshots)
			return nil, fmt.Errorf("files[%d] must be an object", index)
		}
		fileArgs := map[string]interface{}{
			"path":            entry["path"],
			"expected_sha256": entry["expected_sha256"],
			"edits":           entry["edits"],
		}
		path, err := extractMutationPath(fileArgs)
		if err != nil {
			restoreBatchEditSnapshots(snapshots)
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			restoreBatchEditSnapshots(snapshots)
			return nil, fmt.Errorf("%s", normalizeWorkspaceFileError("apply_edits", path, err))
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			restoreBatchEditSnapshots(snapshots)
			return nil, fmt.Errorf("path is a directory or symbolic link: %s", relativeWorkspacePath(path))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			restoreBatchEditSnapshots(snapshots)
			return nil, err
		}
		snapshots = append(snapshots, batchEditSnapshot{path: path, data: data, mode: info.Mode()})
		payload, err := applyExactPatchOperation(path, fileArgs, "apply_edits")
		if err != nil {
			restoreBatchEditSnapshots(snapshots)
			return nil, err
		}
		payload["path"] = relativeWorkspacePath(path)
		applied = append(applied, payload)
	}
	return map[string]any{"files": applied, "applied": len(applied)}, nil
}
