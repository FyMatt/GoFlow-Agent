package taskstaging

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	schemaVersion           = "goflow.task-staging.v1"
	LiveWorkspaceStrategy   = "live_workspace"
	DefaultVersionRetention = 3
	defaultMaxFile          = int64(32 << 20)
	maxOutputFiles          = 10000
	maxOutputBytes          = int64(1 << 30)
)

const gitSnapshotTimeout = 2 * time.Minute

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type FileRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

type Snapshot struct {
	SchemaVersion string       `json:"schema_version"`
	RunID         string       `json:"run_id"`
	WorkspaceRoot string       `json:"workspace_root"`
	StagingRoot   string       `json:"staging_root"`
	CreatedAt     time.Time    `json:"created_at"`
	Files         []FileRecord `json:"files"`
	Excluded      []string     `json:"excluded,omitempty"`
	Strategy      string       `json:"strategy"`
}

type Change struct {
	Path         string      `json:"path"`
	Status       string      `json:"status"`
	Before       *FileRecord `json:"before,omitempty"`
	After        *FileRecord `json:"after,omitempty"`
	Diff         string      `json:"diff,omitempty"`
	Binary       bool        `json:"binary,omitempty"`
	BytesWritten int64       `json:"bytes_written,omitempty"`
}

type Manifest struct {
	SchemaVersion string    `json:"schema_version"`
	RunID         string    `json:"run_id"`
	WorkspaceMode string    `json:"workspace_mode,omitempty"`
	GeneratedAt   time.Time `json:"generated_at"`
	Changes       []Change  `json:"changes"`
	FilesAdded    int       `json:"files_added"`
	FilesModified int       `json:"files_modified"`
	FilesDeleted  int       `json:"files_deleted"`
	BytesWritten  int64     `json:"bytes_written"`
}

type Task struct {
	Root         string
	Input        string
	Dependencies string
	Workspace    string
	Metadata     string
	Baseline     Snapshot
}

type ApplyResult struct {
	ApplyID      string   `json:"apply_id"`
	RecoveryPath string   `json:"recovery_path"`
	Applied      []string `json:"applied"`
}

type ApplyState struct {
	ApplyID      string     `json:"apply_id"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	RestoredAt   *time.Time `json:"restored_at,omitempty"`
	RecoveryPath string     `json:"recovery_path"`
}

type Version struct {
	RunID       string    `json:"run_id"`
	VersionID   string    `json:"version_id"`
	CreatedAt   time.Time `json:"created_at"`
	Status      string    `json:"status"`
	ChangeCount int       `json:"change_count"`
	CanRestore  bool      `json:"can_restore"`
	Reason      string    `json:"reason,omitempty"`
}

type recoveryRecord struct {
	Path         string `json:"path"`
	BeforeExists bool   `json:"before_exists"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	BeforeMode   uint32 `json:"before_mode,omitempty"`
	AfterExists  bool   `json:"after_exists"`
	AfterSHA256  string `json:"after_sha256,omitempty"`
}

type recoveryManifest struct {
	SchemaVersion string           `json:"schema_version"`
	ApplyID       string           `json:"apply_id"`
	RunID         string           `json:"run_id"`
	WorkspaceRoot string           `json:"workspace_root"`
	Status        string           `json:"status"`
	CreatedAt     time.Time        `json:"created_at"`
	RestoredAt    *time.Time       `json:"restored_at,omitempty"`
	Records       []recoveryRecord `json:"records"`
}

func Prepare(workspaceRoot, stagingRoot, runID string) (*Task, error) {
	if !runIDPattern.MatchString(strings.TrimSpace(runID)) {
		return nil, fmt.Errorf("invalid task staging run id")
	}
	workspace, err := canonicalDirectory(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	stagingBase, err := filepath.Abs(filepath.Clean(stagingRoot))
	if err != nil {
		return nil, fmt.Errorf("resolve staging root: %w", err)
	}
	taskRoot := filepath.Join(stagingBase, runID)
	if _, err := os.Lstat(taskRoot); err == nil {
		return nil, fmt.Errorf("task staging directory already exists: %s", runID)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	inputCopy := filepath.Join(taskRoot, "input")
	dependencies := filepath.Join(taskRoot, "dependencies")
	metadata := filepath.Join(taskRoot, "metadata")
	if err := os.MkdirAll(inputCopy, 0o700); err != nil {
		return nil, fmt.Errorf("create task staging workspace: %w", err)
	}
	if err := os.MkdirAll(dependencies, 0o700); err != nil {
		_ = os.RemoveAll(taskRoot)
		return nil, fmt.Errorf("create task dependency directory: %w", err)
	}
	if err := os.MkdirAll(metadata, 0o700); err != nil {
		_ = os.RemoveAll(taskRoot)
		return nil, fmt.Errorf("create task staging metadata: %w", err)
	}
	excluded, err := copyWorkspace(workspace, inputCopy)
	if err != nil {
		_ = os.RemoveAll(taskRoot)
		return nil, err
	}
	files, err := scanFiles(inputCopy)
	if err != nil {
		_ = os.RemoveAll(taskRoot)
		return nil, err
	}
	if err := sealReadOnlyTree(inputCopy); err != nil {
		_ = os.RemoveAll(taskRoot)
		return nil, fmt.Errorf("seal task input snapshot: %w", err)
	}
	snapshot := Snapshot{
		SchemaVersion: schemaVersion,
		RunID:         runID,
		WorkspaceRoot: workspace,
		StagingRoot:   workspace,
		CreatedAt:     time.Now().UTC(),
		Files:         files,
		Excluded:      excluded,
		Strategy:      LiveWorkspaceStrategy,
	}
	if err := writeJSONAtomic(filepath.Join(metadata, "baseline.json"), snapshot); err != nil {
		_ = os.RemoveAll(taskRoot)
		return nil, err
	}
	return &Task{Root: taskRoot, Input: inputCopy, Dependencies: dependencies, Workspace: workspace, Metadata: metadata, Baseline: snapshot}, nil
}

func (t *Task) LiveWorkspace() bool {
	return t != nil && strings.EqualFold(strings.TrimSpace(t.Baseline.Strategy), LiveWorkspaceStrategy)
}

func prepareWorkspaceCopies(source, inputCopy, workCopy string) ([]string, string, error) {
	if gitWorkspaceRoot(source) {
		if err := os.RemoveAll(inputCopy); err != nil {
			return nil, "", err
		}
		if err := cloneLocalGitWorkspace(source, inputCopy); err == nil {
			excluded, copyErr := copyWorkspace(source, inputCopy)
			if copyErr != nil {
				return nil, "", copyErr
			}
			if err := os.RemoveAll(workCopy); err != nil {
				return nil, "", err
			}
			if err := cloneLocalGitWorkspace(inputCopy, workCopy); err != nil {
				return nil, "", fmt.Errorf("clone task Git workspace: %w", err)
			}
			if _, err := copyWorkspace(inputCopy, workCopy); err != nil {
				return nil, "", err
			}
			return excluded, "git_clone", nil
		}
		if err := os.MkdirAll(inputCopy, 0o700); err != nil {
			return nil, "", err
		}
	}
	excluded, err := copyWorkspace(source, inputCopy)
	if err != nil {
		return nil, "", err
	}
	if _, err := copyWorkspace(inputCopy, workCopy); err != nil {
		return nil, "", err
	}
	return excluded, "copy", nil
}

func gitWorkspaceRoot(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		return false
	}
	root, err := filepath.Abs(filepath.Clean(strings.TrimSpace(string(output))))
	if err != nil {
		return false
	}
	workspace, err := filepath.Abs(filepath.Clean(path))
	return err == nil && samePath(root, workspace)
}

func cloneLocalGitWorkspace(source, destination string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitSnapshotTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "clone", "--local", "--no-hardlinks", "--quiet", source, destination)
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("git clone: %w: %s", err, strings.TrimSpace(string(output)))
	}
	removeRemote := exec.CommandContext(ctx, "git", "-C", destination, "remote", "remove", "origin")
	if output, err := removeRemote.CombinedOutput(); err != nil {
		return fmt.Errorf("remove task Git remote: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func Load(taskRoot string) (*Task, error) {
	root, err := filepath.Abs(filepath.Clean(taskRoot))
	if err != nil {
		return nil, err
	}
	metadata := filepath.Join(root, "metadata")
	var snapshot Snapshot
	if err := readJSON(filepath.Join(metadata, "baseline.json"), &snapshot); err != nil {
		return nil, err
	}
	if snapshot.SchemaVersion != schemaVersion || !runIDPattern.MatchString(snapshot.RunID) {
		return nil, fmt.Errorf("invalid task staging baseline")
	}
	dependencies := filepath.Join(root, "dependencies")
	if err := os.MkdirAll(dependencies, 0o700); err != nil {
		return nil, fmt.Errorf("restore task dependency directory: %w", err)
	}
	workspace := filepath.Join(root, "workspace")
	if strings.EqualFold(strings.TrimSpace(snapshot.Strategy), LiveWorkspaceStrategy) {
		workspace = snapshot.WorkspaceRoot
	}
	return &Task{Root: root, Input: filepath.Join(root, "input"), Dependencies: dependencies, Workspace: workspace, Metadata: metadata, Baseline: snapshot}, nil
}

func (t *Task) BuildManifest() (Manifest, error) {
	if t == nil {
		return Manifest{}, fmt.Errorf("task staging is required")
	}
	afterFiles, err := scanFiles(t.Workspace)
	if err != nil {
		return Manifest{}, err
	}
	if len(afterFiles) > maxOutputFiles {
		return Manifest{}, fmt.Errorf("staged output exceeds file limit: %d > %d", len(afterFiles), maxOutputFiles)
	}
	var outputBytes int64
	for _, record := range afterFiles {
		outputBytes += record.Size
		if outputBytes > maxOutputBytes {
			return Manifest{}, fmt.Errorf("staged output exceeds size limit: %d > %d bytes", outputBytes, maxOutputBytes)
		}
	}
	before := deliveryRecordsByPath(t.Baseline.Files)
	after := deliveryRecordsByPath(afterFiles)
	paths := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		paths[path] = struct{}{}
	}
	for path := range after {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	workspaceMode := "staged"
	if t.LiveWorkspace() {
		workspaceMode = "live"
	}
	manifest := Manifest{SchemaVersion: schemaVersion, RunID: t.Baseline.RunID, WorkspaceMode: workspaceMode, GeneratedAt: time.Now().UTC()}
	for _, path := range ordered {
		oldRecord, oldOK := before[path]
		newRecord, newOK := after[path]
		if oldOK && newOK && oldRecord.SHA256 == newRecord.SHA256 && oldRecord.Mode == newRecord.Mode {
			continue
		}
		change := Change{Path: path}
		switch {
		case !oldOK && newOK:
			change.Status = "added"
			change.After = recordPointer(newRecord)
			change.BytesWritten = newRecord.Size
			manifest.FilesAdded++
		case oldOK && !newOK:
			change.Status = "deleted"
			change.Before = recordPointer(oldRecord)
			manifest.FilesDeleted++
		default:
			change.Status = "modified"
			change.Before = recordPointer(oldRecord)
			change.After = recordPointer(newRecord)
			change.BytesWritten = newRecord.Size
			manifest.FilesModified++
		}
		manifest.BytesWritten += change.BytesWritten
		change.Diff, change.Binary = t.changeDiff(change)
		manifest.Changes = append(manifest.Changes, change)
	}
	if err := writeJSONAtomic(filepath.Join(t.Metadata, "manifest.json"), manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (t *Task) StoredManifest() (Manifest, bool, error) {
	if t == nil {
		return Manifest{}, false, fmt.Errorf("task staging is required")
	}
	var manifest Manifest
	err := readJSON(filepath.Join(t.Metadata, "manifest.json"), &manifest)
	if os.IsNotExist(err) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	if manifest.SchemaVersion != schemaVersion || manifest.RunID != t.Baseline.RunID {
		return Manifest{}, false, fmt.Errorf("invalid stored task staging manifest")
	}
	return manifest, true, nil
}

func (t *Task) Apply(manifest Manifest) (ApplyResult, error) {
	if t == nil || manifest.RunID != t.Baseline.RunID || manifest.SchemaVersion != schemaVersion {
		return ApplyResult{}, fmt.Errorf("task staging manifest does not match the task")
	}
	if state, ok, err := t.CurrentApply(); err != nil {
		return ApplyResult{}, err
	} else if ok && (state.Status == "prepared" || state.Status == "applying" || state.Status == "applied") {
		return ApplyResult{}, fmt.Errorf("task staging changes were already applied by %s", state.ApplyID)
	}
	if err := t.validateManifest(manifest); err != nil {
		return ApplyResult{}, err
	}
	applyID, err := randomID()
	if err != nil {
		return ApplyResult{}, err
	}
	recoveryRoot := filepath.Join(t.Metadata, "recovery", applyID)
	if err := os.MkdirAll(filepath.Join(recoveryRoot, "before"), 0o700); err != nil {
		return ApplyResult{}, err
	}
	recovery := recoveryManifest{
		SchemaVersion: schemaVersion,
		ApplyID:       applyID,
		RunID:         manifest.RunID,
		WorkspaceRoot: t.Baseline.WorkspaceRoot,
		Status:        "prepared",
		CreatedAt:     time.Now().UTC(),
	}
	for _, change := range manifest.Changes {
		record, err := t.captureRecoveryRecord(recoveryRoot, change)
		if err != nil {
			return ApplyResult{}, err
		}
		recovery.Records = append(recovery.Records, record)
	}
	manifestPath := filepath.Join(recoveryRoot, "manifest.json")
	if err := writeJSONAtomic(manifestPath, recovery); err != nil {
		return ApplyResult{}, err
	}
	recovery.Status = "applying"
	if err := writeJSONAtomic(manifestPath, recovery); err != nil {
		return ApplyResult{}, err
	}
	applied := make([]string, 0, len(manifest.Changes))
	for _, change := range manifest.Changes {
		if !t.LiveWorkspace() {
			if err := t.applyChange(change); err != nil {
				_ = restoreRecovery(recoveryRoot, &recovery, false)
				return ApplyResult{}, fmt.Errorf("apply %s: %w", change.Path, err)
			}
		}
		applied = append(applied, change.Path)
	}
	recovery.Status = "applied"
	if err := writeJSONAtomic(manifestPath, recovery); err != nil {
		_ = restoreRecovery(recoveryRoot, &recovery, false)
		return ApplyResult{}, err
	}
	return ApplyResult{ApplyID: applyID, RecoveryPath: recoveryRoot, Applied: applied}, nil
}

func (t *Task) CurrentApply() (ApplyState, bool, error) {
	if t == nil {
		return ApplyState{}, false, fmt.Errorf("task staging is required")
	}
	root := filepath.Join(t.Metadata, "recovery")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return ApplyState{}, false, nil
	}
	if err != nil {
		return ApplyState{}, false, err
	}
	var latest ApplyState
	found := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		recoveryPath := filepath.Join(root, entry.Name())
		var manifest recoveryManifest
		if err := readJSON(filepath.Join(recoveryPath, "manifest.json"), &manifest); err != nil {
			return ApplyState{}, false, err
		}
		if manifest.SchemaVersion != schemaVersion || manifest.ApplyID == "" {
			return ApplyState{}, false, fmt.Errorf("invalid task staging recovery manifest: %s", recoveryPath)
		}
		if !found || manifest.CreatedAt.After(latest.CreatedAt) {
			latest = ApplyState{
				ApplyID:      manifest.ApplyID,
				Status:       manifest.Status,
				CreatedAt:    manifest.CreatedAt,
				RestoredAt:   manifest.RestoredAt,
				RecoveryPath: recoveryPath,
			}
			found = true
		}
	}
	return latest, found, nil
}

func Restore(recoveryRoot string) error {
	var recovery recoveryManifest
	if err := readJSON(filepath.Join(recoveryRoot, "manifest.json"), &recovery); err != nil {
		return err
	}
	if recovery.SchemaVersion != schemaVersion || recovery.ApplyID == "" {
		return fmt.Errorf("invalid task staging recovery manifest")
	}
	if recovery.Status == "restored" {
		return fmt.Errorf("task staging apply %s was already restored", recovery.ApplyID)
	}
	return restoreRecovery(recoveryRoot, &recovery, true)
}

func CanRestore(recoveryRoot string) error {
	var recovery recoveryManifest
	if err := readJSON(filepath.Join(recoveryRoot, "manifest.json"), &recovery); err != nil {
		return err
	}
	if recovery.SchemaVersion != schemaVersion || recovery.ApplyID == "" {
		return fmt.Errorf("invalid task staging recovery manifest")
	}
	if recovery.Status != "applied" {
		return fmt.Errorf("workspace version is not active")
	}
	for _, record := range recovery.Records {
		path, err := resolveRelativeFile(recovery.WorkspaceRoot, record.Path)
		if err != nil {
			return err
		}
		if err := verifyRecoveryCurrent(path, record); err != nil {
			return err
		}
	}
	return nil
}

func ListVersions(stagingRoot, workspaceRoot string, limit int) ([]Version, error) {
	base, err := filepath.Abs(filepath.Clean(stagingRoot))
	if err != nil {
		return nil, err
	}
	workspace, err := canonicalDirectory(workspaceRoot)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return []Version{}, nil
	}
	if err != nil {
		return nil, err
	}
	versions := make([]Version, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !runIDPattern.MatchString(entry.Name()) {
			continue
		}
		task, loadErr := Load(filepath.Join(base, entry.Name()))
		if loadErr != nil || !task.LiveWorkspace() || !samePath(task.Baseline.WorkspaceRoot, workspace) {
			continue
		}
		state, exists, stateErr := task.CurrentApply()
		if stateErr != nil || !exists || (state.Status != "applied" && state.Status != "restored") {
			continue
		}
		manifest, found, manifestErr := task.StoredManifest()
		if manifestErr != nil || !found {
			continue
		}
		version := Version{
			RunID:       task.Baseline.RunID,
			VersionID:   state.ApplyID,
			CreatedAt:   state.CreatedAt,
			Status:      state.Status,
			ChangeCount: len(manifest.Changes),
		}
		if state.Status == "applied" {
			if restoreErr := CanRestore(state.RecoveryPath); restoreErr == nil {
				version.CanRestore = true
			} else {
				version.Reason = restoreErr.Error()
			}
		} else {
			version.Reason = "该版本已经回退"
		}
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].CreatedAt.After(versions[j].CreatedAt) })
	if limit > 0 && len(versions) > limit {
		versions = versions[:limit]
	}
	return versions, nil
}

func PruneVersions(stagingRoot, workspaceRoot string, keep int) error {
	if keep < 1 {
		keep = DefaultVersionRetention
	}
	versions, err := ListVersions(stagingRoot, workspaceRoot, 0)
	if err != nil {
		return err
	}
	base, err := filepath.Abs(filepath.Clean(stagingRoot))
	if err != nil {
		return err
	}
	for _, version := range versions[minimum(keep, len(versions)):] {
		root := filepath.Join(base, version.RunID)
		if filepath.Dir(root) != base || !runIDPattern.MatchString(filepath.Base(root)) {
			return fmt.Errorf("refusing to prune an invalid workspace version directory")
		}
		if err := makeTreeOwnerWritable(root); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.RemoveAll(root); err != nil {
			return err
		}
	}
	return nil
}

func minimum(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (t *Task) validateManifest(manifest Manifest) error {
	if t.LiveWorkspace() {
		return t.validateLiveManifest(manifest)
	}
	baseline := recordsByPath(t.Baseline.Files)
	seen := make(map[string]struct{}, len(manifest.Changes))
	for _, change := range manifest.Changes {
		if sensitiveOutputPath(change.Path) {
			return fmt.Errorf("staged output policy rejects sensitive path: %s", change.Path)
		}
		path, err := resolveRelativeFile(t.Baseline.WorkspaceRoot, change.Path)
		if err != nil {
			return err
		}
		if _, exists := seen[change.Path]; exists {
			return fmt.Errorf("duplicate staged change path: %s", change.Path)
		}
		seen[change.Path] = struct{}{}
		expected, existed := baseline[change.Path]
		info, statErr := os.Lstat(path)
		switch change.Status {
		case "added":
			if existed || statErr == nil {
				return fmt.Errorf("workspace conflict for %s: path already exists", change.Path)
			}
			if !os.IsNotExist(statErr) {
				return statErr
			}
		case "modified", "deleted":
			if !existed || change.Before == nil || change.Before.SHA256 != expected.SHA256 {
				return fmt.Errorf("invalid baseline for staged change %s", change.Path)
			}
			if statErr != nil {
				return fmt.Errorf("workspace conflict for %s: %w", change.Path, statErr)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("workspace conflict for %s: current path is not a regular file", change.Path)
			}
			current, err := hashFile(path)
			if err != nil {
				return err
			}
			if current.SHA256 != expected.SHA256 {
				return fmt.Errorf("workspace conflict for %s: source SHA-256 changed", change.Path)
			}
		default:
			return fmt.Errorf("unsupported staged change status %q", change.Status)
		}
		if change.Status != "deleted" {
			staged, err := resolveRelativeFile(t.Workspace, change.Path)
			if err != nil {
				return err
			}
			record, err := hashFile(staged)
			if err != nil {
				return err
			}
			if change.After == nil || record.SHA256 != change.After.SHA256 {
				return fmt.Errorf("staged output changed after manifest generation: %s", change.Path)
			}
		}
	}
	return nil
}

func (t *Task) validateLiveManifest(manifest Manifest) error {
	baseline := recordsByPath(t.Baseline.Files)
	seen := make(map[string]struct{}, len(manifest.Changes))
	for _, change := range manifest.Changes {
		if sensitiveOutputPath(change.Path) {
			return fmt.Errorf("workspace version policy rejects sensitive path: %s", change.Path)
		}
		if _, exists := seen[change.Path]; exists {
			return fmt.Errorf("duplicate workspace change path: %s", change.Path)
		}
		seen[change.Path] = struct{}{}
		expected, existed := baseline[change.Path]
		switch change.Status {
		case "added":
			if existed || change.Before != nil {
				return fmt.Errorf("invalid baseline for workspace change %s", change.Path)
			}
		case "modified", "deleted":
			if !existed || change.Before == nil || change.Before.SHA256 != expected.SHA256 {
				return fmt.Errorf("invalid baseline for workspace change %s", change.Path)
			}
		default:
			return fmt.Errorf("unsupported workspace change status %q", change.Status)
		}
		path, err := resolveRelativeFile(t.Workspace, change.Path)
		if err != nil {
			return err
		}
		info, statErr := os.Lstat(path)
		if change.Status == "deleted" {
			if statErr == nil {
				return fmt.Errorf("workspace changed after manifest generation: %s was recreated", change.Path)
			}
			if !os.IsNotExist(statErr) {
				return statErr
			}
			continue
		}
		if statErr != nil {
			return fmt.Errorf("workspace changed after manifest generation: %s: %w", change.Path, statErr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("workspace change is not a regular file: %s", change.Path)
		}
		current, err := hashFile(path)
		if err != nil {
			return err
		}
		if change.After == nil || current.SHA256 != change.After.SHA256 {
			return fmt.Errorf("workspace changed after manifest generation: %s", change.Path)
		}
	}
	return nil
}

func (t *Task) captureRecoveryRecord(recoveryRoot string, change Change) (recoveryRecord, error) {
	record := recoveryRecord{Path: change.Path, AfterExists: change.Status != "deleted"}
	if change.After != nil {
		record.AfterSHA256 = change.After.SHA256
	}
	if change.Status == "added" {
		return record, nil
	}
	sourceRoot := t.Baseline.WorkspaceRoot
	if t.LiveWorkspace() {
		sourceRoot = t.Input
	}
	source, err := resolveRelativeFile(sourceRoot, change.Path)
	if err != nil {
		return recoveryRecord{}, err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return recoveryRecord{}, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return recoveryRecord{}, err
	}
	record.BeforeExists = true
	record.BeforeSHA256 = sha256Bytes(data)
	record.BeforeMode = uint32(info.Mode().Perm())
	backup := filepath.Join(recoveryRoot, "before", filepath.FromSlash(change.Path)+".bin")
	if err := writeFileAtomic(backup, data, 0o600); err != nil {
		return recoveryRecord{}, err
	}
	return record, nil
}

func (t *Task) applyChange(change Change) error {
	destination, err := resolveRelativeFile(t.Baseline.WorkspaceRoot, change.Path)
	if err != nil {
		return err
	}
	if change.Status == "deleted" {
		return os.Remove(destination)
	}
	source, err := resolveRelativeFile(t.Workspace, change.Path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if change.After != nil {
		mode = os.FileMode(change.After.Mode)
	}
	return writeFileAtomic(destination, data, mode)
}

func restoreRecovery(recoveryRoot string, recovery *recoveryManifest, verifyAfter bool) error {
	if recovery == nil {
		return fmt.Errorf("recovery manifest is required")
	}
	for index := len(recovery.Records) - 1; index >= 0; index-- {
		record := recovery.Records[index]
		path, err := resolveRelativeFile(recovery.WorkspaceRoot, record.Path)
		if err != nil {
			return err
		}
		if verifyAfter {
			if err := verifyRecoveryCurrent(path, record); err != nil {
				return err
			}
		}
		if !record.BeforeExists {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		backup := filepath.Join(recoveryRoot, "before", filepath.FromSlash(record.Path)+".bin")
		data, err := os.ReadFile(backup)
		if err != nil {
			return err
		}
		if sha256Bytes(data) != record.BeforeSHA256 {
			return fmt.Errorf("recovery before image failed integrity verification: %s", record.Path)
		}
		if err := writeFileAtomic(path, data, os.FileMode(record.BeforeMode)); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	recovery.Status = "restored"
	recovery.RestoredAt = &now
	return writeJSONAtomic(filepath.Join(recoveryRoot, "manifest.json"), recovery)
}

func verifyRecoveryCurrent(path string, record recoveryRecord) error {
	info, err := os.Lstat(path)
	if !record.AfterExists {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("recovery conflict for %s: deleted path was recreated", record.Path)
	}
	if err != nil {
		return fmt.Errorf("recovery conflict for %s: %w", record.Path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("recovery conflict for %s: current path is not a regular file", record.Path)
	}
	current, err := hashFile(path)
	if err != nil {
		return err
	}
	if current.SHA256 != record.AfterSHA256 {
		return fmt.Errorf("recovery conflict for %s: applied SHA-256 changed", record.Path)
	}
	return nil
}

func (t *Task) changeDiff(change Change) (string, bool) {
	var oldData, newData []byte
	var err error
	if change.Before != nil {
		oldData, err = os.ReadFile(filepath.Join(t.Input, filepath.FromSlash(change.Path)))
		if err != nil {
			return "", false
		}
	}
	if change.After != nil {
		newData, err = os.ReadFile(filepath.Join(t.Workspace, filepath.FromSlash(change.Path)))
		if err != nil {
			return "", false
		}
	}
	if isBinary(oldData) || isBinary(newData) {
		return "", true
	}
	return unifiedReplacementDiff(change.Path, oldData, newData), false
}

func copyWorkspace(source, destination string) ([]string, error) {
	excluded := make([]string, 0)
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if shouldExclude(rel, entry) {
			excluded = append(excluded, rel)
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
			excluded = append(excluded, rel)
			return nil
		}
		target := filepath.Join(destination, filepath.FromSlash(rel))
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if info.Size() > defaultMaxFile {
			excluded = append(excluded, rel)
			return nil
		}
		return copyRegularFile(path, target, info.Mode().Perm())
	})
	sort.Strings(excluded)
	return excluded, err
}

func shouldExclude(rel string, entry fs.DirEntry) bool {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(rel)))
	if entry.IsDir() {
		switch base {
		case ".git", ".goflow", ".cache", "node_modules", ".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "交付证据", "人工验收", "backups", "task-staging":
			return true
		}
	}
	if base == ".env.example" || base == ".env.sample" || base == ".env.template" {
		return false
	}
	return sensitiveFileBase(base)
}

func sensitiveOutputPath(path string) bool {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(path)))
	if base == ".env.example" || base == ".env.sample" || base == ".env.template" {
		return false
	}
	return sensitiveFileBase(base)
}

func sensitiveFileBase(base string) bool {
	return base == ".env" || strings.HasPrefix(base, ".env.") || base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".key")
}

func sealReadOnlyTree(root string) error {
	directories := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()&^0o222)
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		info, err := os.Stat(directories[index])
		if err != nil {
			return err
		}
		if err := os.Chmod(directories[index], info.Mode().Perm()&^0o222); err != nil {
			return err
		}
	}
	return nil
}

func scanFiles(root string) ([]FileRecord, error) {
	files := make([]FileRecord, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relForFilter, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relForFilter = filepath.ToSlash(relForFilter)
		if shouldExcludeManifestPath(relForFilter) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("staged path is not a regular file: %s", path)
		}
		record, err := hashFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		record.Path = filepath.ToSlash(rel)
		files = append(files, record)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

func deliveryRecordsByPath(records []FileRecord) map[string]FileRecord {
	filtered := make([]FileRecord, 0, len(records))
	for _, record := range records {
		if !shouldExcludeManifestPath(record.Path) {
			filtered = append(filtered, record)
		}
	}
	return recordsByPath(filtered)
}

func shouldExcludeManifestPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	for _, part := range strings.Split(strings.ToLower(path), "/") {
		switch part {
		case ".git", ".goflow", ".cache", "node_modules", ".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "交付证据", "人工验收", "backups", "task-staging":
			return true
		}
	}
	base := strings.ToLower(filepath.Base(filepath.FromSlash(path)))
	ext := strings.ToLower(filepath.Ext(base))
	return sensitiveFileBase(base) || strings.HasSuffix(base, ".pyc") || strings.HasSuffix(base, ".pyo") ||
		ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" ||
		strings.HasSuffix(base, "-wal") || strings.HasSuffix(base, "-shm") || strings.HasSuffix(base, "-journal")
}

func hashFile(path string) (FileRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return FileRecord{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return FileRecord{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return FileRecord{}, err
	}
	return FileRecord{SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size, Mode: uint32(info.Mode().Perm())}, nil
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory")
	}
	return resolved, nil
}

func resolveRelativeFile(root, relative string) (string, error) {
	relative = filepath.ToSlash(strings.TrimSpace(relative))
	if relative == "" || filepath.IsAbs(filepath.FromSlash(relative)) || relative == ".." || strings.HasPrefix(relative, "../") {
		return "", fmt.Errorf("invalid staged relative path %q", relative)
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	path := filepath.Join(rootAbs, filepath.FromSlash(relative))
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("staged path escapes workspace: %s", relative)
	}
	if err := rejectSymlinkAncestors(rootAbs, path); err != nil {
		return "", err
	}
	return path, nil
}

func rejectSymlinkAncestors(root, path string) error {
	parent := filepath.Dir(path)
	relative, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, segment := range strings.Split(relative, string(os.PathSeparator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("staged path has an unsafe ancestor: %s", current)
		}
	}
	return nil
}

func copyRegularFile(source, destination string, mode os.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writeFileAtomic(destination, data, mode)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".goflow-stage-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode.Perm()); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFileAtomic(temporaryPath, path)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func recordsByPath(records []FileRecord) map[string]FileRecord {
	out := make(map[string]FileRecord, len(records))
	for _, record := range records {
		out[record.Path] = record
	}
	return out
}

func recordPointer(record FileRecord) *FileRecord {
	copy := record
	return &copy
}

func randomID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func sha256Bytes(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func isBinary(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	return strings.IndexByte(string(data[:limit]), 0) >= 0
}

func unifiedReplacementDiff(path string, oldData, newData []byte) string {
	oldLines := splitLines(oldData)
	newLines := splitLines(newData)
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix && oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	oldChanged := oldLines[prefix : len(oldLines)-suffix]
	newChanged := newLines[prefix : len(newLines)-suffix]
	if len(oldChanged) == 0 && len(newChanged) == 0 {
		return ""
	}
	oldStart := prefix + 1
	newStart := prefix + 1
	if len(oldChanged) == 0 {
		oldStart = prefix
	}
	if len(newChanged) == 0 {
		newStart = prefix
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "--- a/%s\n+++ b/%s\n", path, path)
	fmt.Fprintf(&builder, "@@ -%d,%d +%d,%d @@\n", oldStart, len(oldChanged), newStart, len(newChanged))
	for _, line := range oldChanged {
		builder.WriteByte('-')
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	for _, line := range newChanged {
		builder.WriteByte('+')
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

func IsConflict(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "workspace conflict") || strings.Contains(err.Error(), "recovery conflict"))
}

func Cleanup(task *Task) error {
	if task == nil || strings.TrimSpace(task.Root) == "" {
		return nil
	}
	if !runIDPattern.MatchString(filepath.Base(task.Root)) {
		return errors.New("refusing to clean an invalid task staging directory")
	}
	if err := makeTreeOwnerWritable(task.Input); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(task.Input); err != nil {
		return err
	}
	if task.LiveWorkspace() {
		return nil
	}
	return os.RemoveAll(task.Workspace)
}

func makeTreeOwnerWritable(root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm() | 0o600
		if entry.IsDir() {
			mode |= 0o100
		}
		return os.Chmod(path, mode)
	})
}
