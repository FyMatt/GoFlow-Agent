package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const fileChangeJournalVersion = 1

var fileChangeIDPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)

type fileChangeRecord struct {
	Version        int    `json:"version"`
	ID             string `json:"id"`
	Operation      string `json:"operation"`
	RelativePath   string `json:"relative_path"`
	BeforeExists   bool   `json:"before_exists"`
	BeforeType     string `json:"before_type,omitempty"`
	BeforeMode     uint32 `json:"before_mode,omitempty"`
	BeforeSHA256   string `json:"before_sha256,omitempty"`
	AfterExists    bool   `json:"after_exists"`
	AfterSHA256    string `json:"after_sha256,omitempty"`
	GitBefore      string `json:"git_before,omitempty"`
	GitAfter       string `json:"git_after,omitempty"`
	DiffPreview    string `json:"diff_preview,omitempty"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
	RestoredAt     string `json:"restored_at,omitempty"`
	RecoveryScope  string `json:"recovery_scope"`
	SnapshotStored bool   `json:"snapshot_stored"`
}

func beginFileChange(operation, path string, beforeExists bool, beforeData []byte, beforeMode os.FileMode) (*fileChangeRecord, error) {
	id, err := newFileChangeID()
	if err != nil {
		return nil, err
	}
	record := &fileChangeRecord{
		Version:        fileChangeJournalVersion,
		ID:             id,
		Operation:      strings.TrimSpace(operation),
		RelativePath:   relativeWorkspacePath(path),
		BeforeExists:   beforeExists,
		BeforeMode:     uint32(beforeMode.Perm()),
		GitBefore:      gitStatusForJournalPath(path),
		Status:         "prepared",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		RecoveryScope:  "file_only",
		SnapshotStored: beforeExists,
	}
	if beforeExists {
		record.BeforeType = "file"
		record.BeforeSHA256 = sha256Hex(beforeData)
		if err := os.MkdirAll(fileChangeDirectory(id), 0o700); err != nil {
			return nil, fmt.Errorf("create file-change journal: %w", err)
		}
		if err := atomicWriteFile(fileChangeSnapshotPath(id), beforeData, 0o600); err != nil {
			return nil, fmt.Errorf("store file-change before image: %w", err)
		}
	}
	if err := writeFileChangeManifest(*record); err != nil {
		_ = os.RemoveAll(fileChangeDirectory(id))
		return nil, err
	}
	return record, nil
}

func commitFileChange(record *fileChangeRecord, path string, afterExists bool, afterData []byte, diffPreview string) error {
	if record == nil {
		return fmt.Errorf("file-change journal record is required")
	}
	record.AfterExists = afterExists
	if afterExists {
		record.AfterSHA256 = sha256Hex(afterData)
	}
	record.GitAfter = gitStatusForJournalPath(path)
	record.DiffPreview = strings.TrimSpace(diffPreview)
	record.Status = "committed"
	return writeFileChangeManifest(*record)
}

func rollbackPreparedFileChange(record *fileChangeRecord, path string) error {
	if record == nil {
		return nil
	}
	if record.BeforeExists {
		before, err := os.ReadFile(fileChangeSnapshotPath(record.ID))
		if err != nil {
			return err
		}
		return atomicWriteFile(path, before, os.FileMode(record.BeforeMode))
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func restoreFileChange(changeID string) (map[string]any, error) {
	changeID = strings.ToLower(strings.TrimSpace(changeID))
	if !fileChangeIDPattern.MatchString(changeID) {
		return nil, fmt.Errorf("change_id is invalid")
	}
	record, err := readFileChangeManifest(changeID)
	if err != nil {
		return nil, err
	}
	if record.Status != "committed" {
		if record.Status == "restored" {
			return nil, fmt.Errorf("file change %s was already restored", changeID)
		}
		return nil, fmt.Errorf("file change %s is not committed", changeID)
	}
	path, err := resolveWorkspacePath(record.RelativePath, workspaceRoot)
	if err != nil {
		return nil, err
	}
	if err := verifyFileChangeCurrentState(record, path); err != nil {
		return nil, err
	}
	if record.BeforeExists {
		before, err := os.ReadFile(fileChangeSnapshotPath(changeID))
		if err != nil {
			return nil, fmt.Errorf("read file-change before image: %w", err)
		}
		if sha256Hex(before) != record.BeforeSHA256 {
			return nil, fmt.Errorf("file-change before image failed integrity verification")
		}
		if err := atomicWriteFile(path, before, os.FileMode(record.BeforeMode)); err != nil {
			return nil, err
		}
	} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	record.Status = "restored"
	record.RestoredAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.GitAfter = gitStatusForJournalPath(path)
	if err := writeFileChangeManifest(record); err != nil {
		return nil, err
	}
	return map[string]any{
		"change_id":       record.ID,
		"relative_path":   record.RelativePath,
		"status":          "restored",
		"restored_sha256": record.BeforeSHA256,
		"restored_exists": record.BeforeExists,
		"recovery_scope":  record.RecoveryScope,
		"git_after":       record.GitAfter,
	}, nil
}

func verifyFileChangeCurrentState(record fileChangeRecord, path string) error {
	info, err := os.Lstat(path)
	if record.AfterExists {
		if err != nil {
			return fmt.Errorf("restore conflict: current file is missing")
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore conflict: current path is not the recorded regular file")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.EqualFold(sha256Hex(data), record.AfterSHA256) {
			return fmt.Errorf("restore conflict: current SHA-256 differs from the Agent-managed after image")
		}
		return nil
	}
	if err == nil {
		return fmt.Errorf("restore conflict: path was recreated after the Agent-managed deletion")
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func fileChangePayload(record *fileChangeRecord) map[string]any {
	if record == nil {
		return nil
	}
	return map[string]any{
		"change_id":       record.ID,
		"reversible":      true,
		"recovery_scope":  record.RecoveryScope,
		"before_type":     record.BeforeType,
		"before_mode":     fmt.Sprintf("%#o", record.BeforeMode),
		"before_sha256":   record.BeforeSHA256,
		"after_sha256":    record.AfterSHA256,
		"git_before":      record.GitBefore,
		"git_after":       record.GitAfter,
		"snapshot_stored": record.SnapshotStored,
	}
}

func newFileChangeID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("create file-change id: %w", err)
	}
	return hex.EncodeToString(random), nil
}

func fileChangeJournalRoot() string {
	return filepath.Join(workspaceRoot, ".goflow", "file-journal")
}

func fileChangeDirectory(id string) string {
	return filepath.Join(fileChangeJournalRoot(), id)
}

func fileChangeManifestPath(id string) string {
	return filepath.Join(fileChangeDirectory(id), "manifest.json")
}

func fileChangeSnapshotPath(id string) string {
	return filepath.Join(fileChangeDirectory(id), "before.bin")
}

func writeFileChangeManifest(record fileChangeRecord) error {
	if !fileChangeIDPattern.MatchString(record.ID) {
		return fmt.Errorf("file-change id is invalid")
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(fileChangeDirectory(record.ID), 0o700); err != nil {
		return fmt.Errorf("create file-change journal: %w", err)
	}
	if err := atomicWriteFile(fileChangeManifestPath(record.ID), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write file-change journal: %w", err)
	}
	return nil
}

func readFileChangeManifest(id string) (fileChangeRecord, error) {
	data, err := os.ReadFile(fileChangeManifestPath(id))
	if err != nil {
		return fileChangeRecord{}, fmt.Errorf("read file-change journal: %w", err)
	}
	var record fileChangeRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fileChangeRecord{}, fmt.Errorf("decode file-change journal: %w", err)
	}
	if record.ID != id || record.Version != fileChangeJournalVersion {
		return fileChangeRecord{}, fmt.Errorf("file-change journal identity or version mismatch")
	}
	return record, nil
}

func gitStatusForJournalPath(path string) string {
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--short", "--", rel)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	status := strings.TrimSpace(string(output))
	if len(status) > 4096 {
		status = status[:4096]
	}
	return status
}
