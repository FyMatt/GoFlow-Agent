package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	managedConfigJournalVersion = 1
	maxManagedConfigBytes       = 1024 * 1024
)

var managedChangeIDPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)

var verifyManagedJSONStateCall = verifyManagedJSONState

type managedConfigChange struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	TargetID        string `json:"target_id"`
	Path            string `json:"path"`
	JSONPointer     string `json:"json_pointer"`
	ValueSHA256     string `json:"value_sha256"`
	BeforeSHA256    string `json:"before_sha256"`
	AfterSHA256     string `json:"after_sha256"`
	BeforeMode      uint32 `json:"before_mode"`
	Status          string `json:"status"`
	ChangeReason    string `json:"change_reason"`
	RecoveryReason  string `json:"recovery_reason,omitempty"`
	CreatedAt       string `json:"created_at"`
	RestoredAt      string `json:"restored_at,omitempty"`
	RecoveryStarted string `json:"recovery_started_at,omitempty"`
	BackupStored    bool   `json:"backup_stored"`
	PostCheckPassed bool   `json:"post_check_passed"`
	RecoveryChecked bool   `json:"recovery_post_check_passed,omitempty"`
	RollbackStatus  string `json:"rollback_status,omitempty"`
	EvidenceError   string `json:"evidence_error,omitempty"`
	RecoveryScope   string `json:"recovery_scope"`
}

func inspectManagedJSON(input diagnosticInput) (map[string]any, error) {
	path, data, info, err := readManagedJSONFile(input.Path)
	if err != nil {
		return nil, err
	}
	value, err := decodeManagedJSON(data)
	if err != nil {
		return nil, err
	}
	payload := baseEnvelope("managed_json_inspection", "ok")
	payload["path"] = path
	payload["sha256"] = managedSHA256(data)
	payload["size_bytes"] = len(data)
	payload["mode"] = fmt.Sprintf("%#o", info.Mode().Perm())
	payload["structure"] = managedJSONStructure(value, 0)
	payload["values_redacted"] = true
	payload["managed_root"] = true
	return payload, nil
}

func setManagedJSONValue(input diagnosticInput) (map[string]any, error) {
	if !input.BackupRequired || !input.PostCheckRequired {
		return nil, fmt.Errorf("backup_required and post_check_required must both be true")
	}
	if reason := strings.TrimSpace(input.ChangeReason); reason == "" || len(reason) > 500 {
		return nil, fmt.Errorf("change_reason is required and must not exceed 500 characters")
	}
	expected := strings.ToLower(strings.TrimSpace(input.ExpectedSHA))
	if !isManagedSHA256(expected) {
		return nil, fmt.Errorf("expected_sha256 must contain 64 hexadecimal characters")
	}
	path, before, info, err := readManagedJSONFile(input.Path)
	if err != nil {
		return nil, err
	}
	beforeSHA := managedSHA256(before)
	if beforeSHA != expected {
		return nil, fmt.Errorf("managed configuration changed since inspection: expected_sha256 does not match")
	}
	root, err := managedConfigBackupRoot()
	if err != nil {
		return nil, err
	}
	document, err := decodeManagedJSON(before)
	if err != nil {
		return nil, err
	}
	if err := setExistingJSONPointer(document, input.JSONPointer, input.Value); err != nil {
		return nil, err
	}
	after, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode managed JSON: %w", err)
	}
	after = append(after, '\n')
	if len(after) > maxManagedConfigBytes {
		return nil, fmt.Errorf("managed JSON exceeds %d bytes after the change", maxManagedConfigBytes)
	}
	if bytes.Equal(before, after) {
		return nil, fmt.Errorf("managed JSON value is unchanged")
	}
	changeID, err := newManagedChangeID()
	if err != nil {
		return nil, err
	}
	valueJSON, _ := json.Marshal(input.Value)
	record := managedConfigChange{
		Version: managedConfigJournalVersion, ID: changeID, TargetID: localTargetID,
		Path: path, JSONPointer: strings.TrimSpace(input.JSONPointer), ValueSHA256: managedSHA256(valueJSON),
		BeforeSHA256: beforeSHA, AfterSHA256: managedSHA256(after), BeforeMode: uint32(info.Mode().Perm()),
		Status: "prepared", ChangeReason: strings.TrimSpace(input.ChangeReason), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		BackupStored: true, RecoveryScope: "managed_json_file",
	}
	if err := storeManagedChange(root, record, before); err != nil {
		return nil, err
	}
	journalRetained := false
	defer func() {
		if !journalRetained {
			_ = os.RemoveAll(managedChangeDirectory(root, changeID))
		}
	}()
	currentPath, current, currentInfo, err := readManagedJSONFile(path)
	if err != nil {
		return nil, fmt.Errorf("recheck managed JSON before change: %w", err)
	}
	if currentPath != path || managedSHA256(current) != record.BeforeSHA256 || currentInfo.Mode().Perm() != info.Mode().Perm() {
		return nil, fmt.Errorf("managed configuration changed after backup; refusing to overwrite it")
	}
	if err := atomicWriteManagedFile(path, after, currentInfo.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("apply managed JSON change: %w", err)
	}
	journalRetained = true
	if err := verifyManagedJSONStateCall(path, record.AfterSHA256, record.JSONPointer, input.Value); err != nil {
		rollbackErr := rollbackManagedJSONChange(root, &record, path, before, currentInfo.Mode().Perm(), err)
		return nil, fmt.Errorf("post-change verification failed for change %s: %w; %v", record.ID, err, rollbackErr)
	}
	record.Status = "committed"
	record.PostCheckPassed = true
	if err := writeManagedChangeManifest(root, record); err != nil {
		rollbackErr := rollbackManagedJSONChange(root, &record, path, before, currentInfo.Mode().Perm(), err)
		return nil, fmt.Errorf("persist change evidence failed for change %s: %w; %v", record.ID, err, rollbackErr)
	}
	payload := baseEnvelope("managed_json_change", "committed")
	payload["read_only"] = false
	applyManagedChangeEvidence(payload, record)
	payload["backup_verified"] = true
	payload["post_check"] = "passed"
	payload["recovery_tool"] = "host_tools/restore_managed_json_change"
	return payload, nil
}

func restoreManagedJSONChange(input diagnosticInput) (map[string]any, error) {
	if !input.PostCheckRequired {
		return nil, fmt.Errorf("post_check_required must be true")
	}
	if reason := strings.TrimSpace(input.RecoveryReason); reason == "" || len(reason) > 500 {
		return nil, fmt.Errorf("recovery_reason is required and must not exceed 500 characters")
	}
	changeID := strings.ToLower(strings.TrimSpace(input.ChangeID))
	if !managedChangeIDPattern.MatchString(changeID) {
		return nil, fmt.Errorf("change_id is invalid")
	}
	expected := strings.ToLower(strings.TrimSpace(input.ExpectedSHA))
	if !isManagedSHA256(expected) {
		return nil, fmt.Errorf("expected_sha256 must contain 64 hexadecimal characters")
	}
	root, err := managedConfigBackupRoot()
	if err != nil {
		return nil, err
	}
	record, err := readManagedChangeManifest(root, changeID)
	if err != nil {
		return nil, err
	}
	if record.Status != "committed" && record.Status != "prepared" && record.Status != "restore_prepared" {
		return nil, fmt.Errorf("managed configuration change %s is not committed", changeID)
	}
	if expected != record.AfterSHA256 {
		return nil, fmt.Errorf("expected_sha256 must match the recorded after SHA")
	}
	path, current, info, err := readManagedJSONFile(record.Path)
	if err != nil {
		return nil, err
	}
	currentSHA := managedSHA256(current)
	alreadyRestored := (record.Status == "prepared" || record.Status == "restore_prepared") && currentSHA == record.BeforeSHA256
	if !sameManagedPath(path, record.Path) || currentSHA != record.AfterSHA256 && !alreadyRestored || uint32(info.Mode().Perm()) != record.BeforeMode {
		return nil, fmt.Errorf("restore conflict: current file differs from the Agent-managed after image")
	}
	backup, err := os.ReadFile(managedChangeBackupPath(root, changeID))
	if err != nil {
		return nil, fmt.Errorf("read managed configuration backup: %w", err)
	}
	if managedSHA256(backup) != record.BeforeSHA256 {
		return nil, fmt.Errorf("managed configuration backup failed integrity verification")
	}
	if _, err := decodeManagedJSON(backup); err != nil {
		return nil, fmt.Errorf("managed configuration backup is invalid: %w", err)
	}
	record.Status = "restore_prepared"
	record.RecoveryReason = strings.TrimSpace(input.RecoveryReason)
	record.RecoveryStarted = time.Now().UTC().Format(time.RFC3339Nano)
	record.EvidenceError = ""
	if err := writeManagedChangeManifest(root, record); err != nil {
		return nil, fmt.Errorf("persist recovery preparation for change %s: %w", changeID, err)
	}
	if !alreadyRestored {
		if err := atomicWriteManagedFile(path, backup, os.FileMode(record.BeforeMode)); err != nil {
			record.Status = "committed"
			record.RecoveryReason = ""
			record.RecoveryStarted = ""
			record.EvidenceError = err.Error()
			_ = writeManagedChangeManifest(root, record)
			return nil, fmt.Errorf("restore managed configuration change %s: %w", changeID, err)
		}
	}
	if err := verifyManagedJSONStateCall(path, record.BeforeSHA256, "", nil); err != nil {
		rollbackErr := rollbackManagedJSONRecovery(root, &record, path, current, info.Mode().Perm(), err)
		return nil, fmt.Errorf("recovery post-check failed for change %s: %w; %v", changeID, err, rollbackErr)
	}
	record.Status = "restored"
	record.RestoredAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.RecoveryChecked = true
	record.RollbackStatus = ""
	record.EvidenceError = ""
	if err := writeManagedChangeManifest(root, record); err != nil {
		rollbackErr := rollbackManagedJSONRecovery(root, &record, path, current, info.Mode().Perm(), err)
		return nil, fmt.Errorf("persist recovery evidence failed for change %s: %w; %v", changeID, err, rollbackErr)
	}
	payload := baseEnvelope("managed_json_recovery", "restored")
	payload["read_only"] = false
	applyManagedChangeEvidence(payload, record)
	payload["restored_sha256"] = record.BeforeSHA256
	payload["post_check"] = "passed"
	payload["recovery_post_check_passed"] = record.RecoveryChecked
	payload["restored_at"] = record.RestoredAt
	return payload, nil
}

func rollbackManagedJSONChange(root string, record *managedConfigChange, path string, before []byte, mode os.FileMode, cause error) error {
	record.PostCheckPassed = false
	record.EvidenceError = cause.Error()
	if err := atomicWriteManagedFile(path, before, mode); err != nil {
		record.Status = "rollback_failed"
		record.RollbackStatus = "failed"
		record.EvidenceError += "; rollback: " + err.Error()
		_ = writeManagedChangeManifest(root, *record)
		return fmt.Errorf("backup retained but rollback failed: %w", err)
	}
	if err := verifyManagedJSONStateCall(path, record.BeforeSHA256, "", nil); err != nil {
		record.Status = "rollback_failed"
		record.RollbackStatus = "failed"
		record.EvidenceError += "; rollback verification: " + err.Error()
		_ = writeManagedChangeManifest(root, *record)
		return fmt.Errorf("backup retained but rollback verification failed: %w", err)
	}
	record.Status = "rolled_back"
	record.RollbackStatus = "passed"
	if err := writeManagedChangeManifest(root, *record); err != nil {
		return fmt.Errorf("rollback passed and backup was retained, but rollback evidence update failed: %w", err)
	}
	return fmt.Errorf("backup was restored and verified; evidence was retained")
}

func rollbackManagedJSONRecovery(root string, record *managedConfigChange, path string, after []byte, mode os.FileMode, cause error) error {
	record.RecoveryChecked = false
	record.EvidenceError = cause.Error()
	if err := atomicWriteManagedFile(path, after, mode); err != nil {
		record.Status = "recovery_rollback_failed"
		record.RollbackStatus = "failed"
		record.EvidenceError += "; recovery rollback: " + err.Error()
		_ = writeManagedChangeManifest(root, *record)
		return fmt.Errorf("backup remains available but recovery rollback failed: %w", err)
	}
	if err := verifyManagedJSONStateCall(path, record.AfterSHA256, "", nil); err != nil {
		record.Status = "recovery_rollback_failed"
		record.RollbackStatus = "failed"
		record.EvidenceError += "; recovery rollback verification: " + err.Error()
		_ = writeManagedChangeManifest(root, *record)
		return fmt.Errorf("backup remains available but recovery rollback verification failed: %w", err)
	}
	record.Status = "committed"
	record.RecoveryReason = ""
	record.RecoveryStarted = ""
	record.RestoredAt = ""
	record.RollbackStatus = "passed"
	if err := writeManagedChangeManifest(root, *record); err != nil {
		return fmt.Errorf("changed image was restored and verified, but recovery rollback evidence update failed: %w", err)
	}
	return fmt.Errorf("changed image was restored and verified; recovery may be retried")
}

// sameManagedPath compares paths after resolving absolute form, symlinks and
// Windows 8.3 short names, which CI temp directories frequently use.
func sameManagedPath(left, right string) bool {
	canonical := func(value string) string {
		resolved, err := filepath.Abs(filepath.Clean(strings.TrimSpace(value)))
		if err != nil {
			return value
		}
		if real, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
			resolved = real
		}
		return resolved
	}
	leftPath, rightPath := canonical(left), canonical(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftPath, rightPath)
	}
	return leftPath == rightPath
}

func readManagedJSONFile(raw string) (string, []byte, os.FileInfo, error) {
	path, err := allowedManagedConfigPath(raw)
	if err != nil {
		return "", nil, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, nil, fmt.Errorf("inspect managed configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, nil, fmt.Errorf("managed configuration must be a regular non-symlink file")
	}
	if info.Size() > maxManagedConfigBytes {
		return "", nil, nil, fmt.Errorf("managed configuration exceeds %d bytes", maxManagedConfigBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, fmt.Errorf("read managed configuration: %w", err)
	}
	if _, err := decodeManagedJSON(data); err != nil {
		return "", nil, nil, err
	}
	return path, data, info, nil
}

func allowedManagedConfigPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !filepath.IsAbs(raw) {
		return "", fmt.Errorf("managed configuration path must be an existing absolute JSON file")
	}
	if !strings.EqualFold(filepath.Ext(raw), ".json") {
		return "", fmt.Errorf("managed configuration path must use the .json extension")
	}
	resolved, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("resolve managed configuration path: %w", err)
	}
	roots := managedConfigRoots()
	if len(roots) == 0 {
		return "", fmt.Errorf("managed configuration changes are disabled; set GOFLOW_HOST_MANAGED_CONFIG_ROOTS")
	}
	for _, root := range roots {
		rootResolved, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		candidateReal, candidateErr := filepath.EvalSymlinks(resolved)
		rootReal, rootErr := filepath.EvalSymlinks(rootResolved)
		if candidateErr == nil && rootErr == nil && pathWithinRoot(rootReal, candidateReal) {
			return candidateReal, nil
		}
		if pathWithinRoot(rootResolved, resolved) && pathHasNoSymlinkFromRoot(rootResolved, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("managed configuration path is outside GOFLOW_HOST_MANAGED_CONFIG_ROOTS")
}

func managedConfigRoots() []string {
	roots := make([]string, 0)
	for _, root := range filepath.SplitList(os.Getenv("GOFLOW_HOST_MANAGED_CONFIG_ROOTS")) {
		if root = strings.TrimSpace(root); root != "" && filepath.IsAbs(root) {
			roots = append(roots, filepath.Clean(root))
		}
	}
	return roots
}

func managedConfigBackupRoot() (string, error) {
	raw := strings.TrimSpace(os.Getenv("GOFLOW_HOST_CHANGE_BACKUP_ROOT"))
	if raw == "" || !filepath.IsAbs(raw) {
		return "", fmt.Errorf("managed configuration backup is disabled; set GOFLOW_HOST_CHANGE_BACKUP_ROOT to an absolute path")
	}
	root, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("resolve managed configuration backup root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create managed configuration backup root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("managed configuration backup root must be a non-symlink directory")
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve managed configuration backup root: %w", err)
	}
	for _, managedRoot := range managedConfigRoots() {
		managedReal, resolveErr := filepath.EvalSymlinks(managedRoot)
		if resolveErr != nil {
			continue
		}
		if pathWithinRoot(managedReal, real) || pathWithinRoot(real, managedReal) {
			return "", fmt.Errorf("managed configuration backup root must be separate from managed configuration roots")
		}
	}
	return real, nil
}

func decodeManagedJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("parse managed JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse managed JSON: multiple JSON values")
		}
		return nil, fmt.Errorf("parse managed JSON: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("managed JSON root must be an object")
	}
	return value, nil
}

func setExistingJSONPointer(document any, pointer string, value any) error {
	parts, err := managedJSONPointerParts(pointer)
	if err != nil {
		return err
	}
	current, ok := document.(map[string]any)
	if !ok {
		return fmt.Errorf("managed JSON root must be an object")
	}
	for _, part := range parts[:len(parts)-1] {
		next, exists := current[part]
		if !exists {
			return fmt.Errorf("json_pointer references a missing object key")
		}
		current, ok = next.(map[string]any)
		if !ok {
			return fmt.Errorf("json_pointer may traverse object keys only")
		}
	}
	leaf := parts[len(parts)-1]
	if _, exists := current[leaf]; !exists {
		return fmt.Errorf("json_pointer must reference an existing object key")
	}
	current[leaf] = value
	return nil
}

func managedJSONPointerParts(pointer string) ([]string, error) {
	pointer = strings.TrimSpace(pointer)
	if pointer == "" || !strings.HasPrefix(pointer, "/") || len(pointer) > 1024 {
		return nil, fmt.Errorf("json_pointer must be a non-root JSON pointer")
	}
	raw := strings.Split(pointer[1:], "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		decoded, err := decodeManagedJSONPointerToken(part)
		if err != nil {
			return nil, err
		}
		part = decoded
		if part == "" {
			return nil, fmt.Errorf("json_pointer must not contain empty object keys")
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("json_pointer must reference an object key")
	}
	return parts, nil
}

func decodeManagedJSONPointerToken(value string) (string, error) {
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '~' {
			builder.WriteByte(value[index])
			continue
		}
		if index+1 >= len(value) || value[index+1] != '0' && value[index+1] != '1' {
			return "", fmt.Errorf("json_pointer contains an invalid escape")
		}
		index++
		if value[index] == '0' {
			builder.WriteByte('~')
		} else {
			builder.WriteByte('/')
		}
	}
	return builder.String(), nil
}

func managedJSONStructure(value any, depth int) any {
	if depth >= 6 {
		return "nested"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = managedJSONStructure(item, depth+1)
		}
		return out
	case []any:
		return map[string]any{"type": "array", "length": len(typed)}
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

func verifyManagedJSONState(path, expectedSHA, pointer string, expectedValue any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if managedSHA256(data) != strings.ToLower(strings.TrimSpace(expectedSHA)) {
		return fmt.Errorf("SHA-256 does not match expected state")
	}
	document, err := decodeManagedJSON(data)
	if err != nil {
		return err
	}
	if strings.TrimSpace(pointer) == "" {
		return nil
	}
	actual, err := managedJSONPointerValue(document, pointer)
	if err != nil {
		return err
	}
	actualJSON, _ := json.Marshal(actual)
	expectedJSON, _ := json.Marshal(expectedValue)
	if !bytes.Equal(actualJSON, expectedJSON) {
		return fmt.Errorf("changed JSON value does not match the requested value")
	}
	return nil
}

func managedJSONPointerValue(document any, pointer string) (any, error) {
	parts, err := managedJSONPointerParts(pointer)
	if err != nil {
		return nil, err
	}
	current := document
	for _, part := range parts {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("json_pointer may traverse object keys only")
		}
		current, ok = object[part]
		if !ok {
			return nil, fmt.Errorf("json_pointer references a missing object key")
		}
	}
	return current, nil
}

func storeManagedChange(root string, record managedConfigChange, before []byte) error {
	dir := managedChangeDirectory(root, record.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create managed change journal: %w", err)
	}
	if err := atomicWriteManagedFile(managedChangeBackupPath(root, record.ID), before, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("store managed configuration backup: %w", err)
	}
	if err := writeManagedChangeManifest(root, record); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	return nil
}

func writeManagedChangeManifest(root string, record managedConfigChange) error {
	if !managedChangeIDPattern.MatchString(record.ID) {
		return fmt.Errorf("managed change id is invalid")
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteManagedFile(managedChangeManifestPath(root, record.ID), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write managed change journal: %w", err)
	}
	return nil
}

func readManagedChangeManifest(root, id string) (managedConfigChange, error) {
	data, err := os.ReadFile(managedChangeManifestPath(root, id))
	if err != nil {
		return managedConfigChange{}, fmt.Errorf("read managed change journal: %w", err)
	}
	var record managedConfigChange
	if err := json.Unmarshal(data, &record); err != nil {
		return managedConfigChange{}, fmt.Errorf("decode managed change journal: %w", err)
	}
	if record.ID != id || record.Version != managedConfigJournalVersion || record.TargetID != localTargetID {
		return managedConfigChange{}, fmt.Errorf("managed change journal identity or version mismatch")
	}
	return record, nil
}

func atomicWriteManagedFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".goflow-managed-*")
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
	if err := temp.Chmod(mode.Perm()); err != nil {
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

func applyManagedChangeEvidence(payload map[string]any, record managedConfigChange) {
	payload["change_id"] = record.ID
	payload["path"] = record.Path
	payload["json_pointer"] = record.JSONPointer
	payload["before_sha256"] = record.BeforeSHA256
	payload["after_sha256"] = record.AfterSHA256
	payload["backup_stored"] = record.BackupStored
	payload["post_check_passed"] = record.PostCheckPassed
	payload["reversible"] = true
	payload["recovery_scope"] = record.RecoveryScope
}

func managedChangeDirectory(root, id string) string { return filepath.Join(root, id) }
func managedChangeManifestPath(root, id string) string {
	return filepath.Join(managedChangeDirectory(root, id), "manifest.json")
}
func managedChangeBackupPath(root, id string) string {
	return filepath.Join(managedChangeDirectory(root, id), "before.json")
}

func newManagedChangeID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("create managed change id: %w", err)
	}
	return hex.EncodeToString(random), nil
}

func managedSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isManagedSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
