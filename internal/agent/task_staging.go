package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/taskstaging"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func (r *Runtime) prepareExecutionWorkspace(execCtx *ExecutionContext, call schema.ToolCall, tool schema.Tool) error {
	if r == nil || !r.taskStagingEnabled || execCtx == nil || normalizeExecutionTargetID(execCtx.TargetID) != session.LocalTargetID || !workspaceScopedExecutionTool(call, tool) {
		return nil
	}
	runID := strings.TrimSpace(execCtx.AgentRunID)
	if runID == "" {
		runID = strings.TrimSpace(execCtx.WorkflowRunID)
	}
	if runID == "" {
		return nil
	}
	r.taskStagingMu.Lock()
	existing := r.taskStaging[runID]
	r.taskStagingMu.Unlock()
	if existing == nil && strings.TrimSpace(execCtx.AgentRunID) != "" {
		loaded, found, err := r.loadTaskStaging(runID)
		if err != nil {
			return fmt.Errorf("restore task workspace: %w", err)
		}
		if found {
			existing = loaded
		}
	}
	// Ordinary agent reads can stay on the live workspace until a write
	// creates a task copy. Workflow runs must stage before the first read so
	// later commands resolve the same relative paths the file tools returned.
	if existing == nil && policy.KindForTool(tool) == config.ToolKindRead && !thirdPartyExecutionTool(call, tool) && strings.TrimSpace(execCtx.WorkflowRunID) == "" {
		return nil
	}
	task := existing
	var err error
	if task == nil {
		task, err = r.prepareTaskStagingWithWorkspace(runID, execCtx.WorkspaceRoot)
		if err != nil {
			return fmt.Errorf("prepare task workspace: %w", err)
		}
	}
	if task == nil {
		return fmt.Errorf("prepare task workspace: runtime returned no task workspace for %s", runID)
	}
	execCtx.ToolWorkspaceRoot = task.Workspace
	scope := schema.TaskWorkspaceScope{
		RunID:        runID,
		Root:         task.Root,
		Input:        task.Input,
		Dependencies: task.Dependencies,
		Workspace:    task.Workspace,
	}
	if task.LiveWorkspace() {
		scope.Root = task.Workspace
		scope.Input = ""
		scope.Dependencies = ""
	}
	execCtx.ToolWorkspaceScope = scope
	return nil
}

func workspaceScopedExecutionTool(call schema.ToolCall, tool schema.Tool) bool {
	server := strings.ToLower(strings.TrimSpace(executionToolServerName(call, tool)))
	switch server {
	case "file_tools", "command_tools", "office_tools":
		return true
	case "host_tools", "network_tools", "web_tools", "ssh_tools", "skill_runner", "python_notes", "notification_tools":
		return false
	default:
		return server != ""
	}
}

func thirdPartyExecutionTool(call schema.ToolCall, tool schema.Tool) bool {
	server := strings.ToLower(strings.TrimSpace(executionToolServerName(call, tool)))
	if server == "" {
		return false
	}
	switch server {
	case "file_tools", "command_tools", "office_tools", "host_tools", "network_tools", "web_tools", "ssh_tools", "skill_runner", "python_notes", "notification_tools":
		return false
	default:
		return true
	}
}

func (r *Runtime) prepareTaskStaging(runID string) (*taskstaging.Task, error) {
	workspaceRoot := ""
	if r != nil && r.cfg != nil {
		workspaceRoot = r.cfg.WorkspaceRoot
	}
	return r.prepareTaskStagingWithWorkspace(runID, workspaceRoot)
}

func (r *Runtime) prepareTaskStagingWithWorkspace(runID, workspaceRoot string) (*taskstaging.Task, error) {
	if r == nil || r.cfg == nil || r.session == nil {
		return nil, fmt.Errorf("task staging requires a configured runtime")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("task staging requires a run id")
	}
	if _, agentOK := r.session.AgentRun(runID); !agentOK {
		if _, workflowOK := r.session.WorkflowRun(runID); !workflowOK {
			return nil, fmt.Errorf("task staging run not found: %s", runID)
		}
	}
	r.taskStagingMu.Lock()
	defer r.taskStagingMu.Unlock()
	if task := r.taskStaging[runID]; task != nil {
		return task, nil
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		workspaceRoot = r.cfg.WorkspaceRoot
	}
	base, err := r.taskStagingBaseRootForWorkspace(workspaceRoot)
	if err != nil {
		return nil, err
	}
	taskRoot := filepath.Join(base, runID)
	var task *taskstaging.Task
	_, statErr := os.Stat(filepath.Join(taskRoot, "metadata", "baseline.json"))
	if statErr == nil {
		task, err = taskstaging.Load(taskRoot)
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	} else {
		task, err = taskstaging.Prepare(workspaceRoot, base, runID)
	}
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task staging backend returned no task for %s", runID)
	}
	if r.taskStaging == nil {
		r.taskStaging = make(map[string]*taskstaging.Task)
	}
	r.taskStaging[runID] = task
	return task, nil
}

func (r *Runtime) taskStagingManifest(runID string) (taskstaging.Manifest, error) {
	task, err := r.prepareTaskStaging(runID)
	if err != nil {
		return taskstaging.Manifest{}, err
	}
	return task.BuildManifest()
}

func (r *Runtime) applyTaskStaging(runID string) (taskstaging.Manifest, taskstaging.ApplyResult, error) {
	task, err := r.prepareTaskStaging(runID)
	if err != nil {
		return taskstaging.Manifest{}, taskstaging.ApplyResult{}, err
	}
	manifest, err := task.BuildManifest()
	if err != nil {
		return taskstaging.Manifest{}, taskstaging.ApplyResult{}, err
	}
	result, err := task.Apply(manifest)
	if err == nil && task.LiveWorkspace() {
		_ = taskstaging.PruneVersions(filepath.Dir(task.Root), task.Baseline.WorkspaceRoot, taskstaging.DefaultVersionRetention)
		r.forgetPrunedTaskStaging()
	}
	return manifest, result, err
}

func (r *Runtime) forgetPrunedTaskStaging() {
	if r == nil {
		return
	}
	r.taskStagingMu.Lock()
	defer r.taskStagingMu.Unlock()
	for runID, task := range r.taskStaging {
		if task == nil {
			delete(r.taskStaging, runID)
			continue
		}
		if _, err := os.Stat(filepath.Join(task.Metadata, "baseline.json")); os.IsNotExist(err) {
			delete(r.taskStaging, runID)
		}
	}
}

func (r *Runtime) TaskStagingManifest(runID string) (taskstaging.Manifest, taskstaging.ApplyState, bool, error) {
	task, ok, err := r.loadTaskStaging(runID)
	if err != nil || !ok {
		return taskstaging.Manifest{}, taskstaging.ApplyState{}, false, err
	}
	state, _, err := task.CurrentApply()
	if err != nil {
		return taskstaging.Manifest{}, taskstaging.ApplyState{}, true, err
	}
	workspaceExists := true
	if _, statErr := os.Stat(task.Workspace); os.IsNotExist(statErr) {
		workspaceExists = false
		if strings.TrimSpace(state.Status) == "" {
			state.Status = "discarded"
		}
	} else if statErr != nil {
		return taskstaging.Manifest{}, taskstaging.ApplyState{}, true, statErr
	}
	useStoredLiveManifest := task.LiveWorkspace() && (strings.EqualFold(strings.TrimSpace(state.Status), "applied") || strings.EqualFold(strings.TrimSpace(state.Status), "restored"))
	if workspaceExists && !stagedTaskApplyActive(state.Status) && !useStoredLiveManifest {
		manifest, buildErr := task.BuildManifest()
		return manifest, state, true, buildErr
	}
	manifest, _, err := task.StoredManifest()
	if err != nil {
		return taskstaging.Manifest{}, taskstaging.ApplyState{}, true, err
	}
	return manifest, state, true, nil
}

func (r *Runtime) PrepareTaskStagingForRun(runID string) (*taskstaging.Task, error) {
	return r.prepareTaskStaging(runID)
}

func (r *Runtime) ApplyTaskStaging(runID string) (taskstaging.Manifest, taskstaging.ApplyResult, error) {
	return r.applyTaskStaging(runID)
}

func (r *Runtime) RestoreTaskStaging(runID string) (taskstaging.ApplyState, error) {
	task, ok, err := r.loadTaskStaging(runID)
	if err != nil {
		return taskstaging.ApplyState{}, err
	}
	if !ok {
		return taskstaging.ApplyState{}, fmt.Errorf("task staging run not found: %s", strings.TrimSpace(runID))
	}
	state, exists, err := task.CurrentApply()
	if err != nil {
		return taskstaging.ApplyState{}, err
	}
	if !exists || !strings.EqualFold(strings.TrimSpace(state.Status), "applied") {
		return taskstaging.ApplyState{}, fmt.Errorf("task staging changes are not in an applied state")
	}
	if err := taskstaging.Restore(state.RecoveryPath); err != nil {
		return taskstaging.ApplyState{}, err
	}
	if task.LiveWorkspace() {
		_ = r.ReleaseTaskStagingProcesses(runID)
	}
	restored, exists, err := task.CurrentApply()
	if err != nil {
		return taskstaging.ApplyState{}, err
	}
	if !exists || !strings.EqualFold(strings.TrimSpace(restored.Status), "restored") {
		return taskstaging.ApplyState{}, fmt.Errorf("task staging recovery state was not persisted")
	}
	return restored, nil
}

func (r *Runtime) DiscardTaskStaging(runID string) (taskstaging.Manifest, error) {
	task, ok, err := r.loadTaskStaging(runID)
	if err != nil {
		return taskstaging.Manifest{}, err
	}
	if !ok {
		return taskstaging.Manifest{}, fmt.Errorf("task staging run not found: %s", strings.TrimSpace(runID))
	}
	if _, err := os.Stat(task.Workspace); os.IsNotExist(err) {
		return taskstaging.Manifest{}, fmt.Errorf("task staging changes were already discarded")
	} else if err != nil {
		return taskstaging.Manifest{}, err
	}
	state, _, err := task.CurrentApply()
	if err != nil {
		return taskstaging.Manifest{}, err
	}
	if stagedTaskApplyActive(state.Status) {
		return taskstaging.Manifest{}, fmt.Errorf("applied task staging changes must be restored before discard")
	}
	if task.LiveWorkspace() && strings.EqualFold(strings.TrimSpace(state.Status), "restored") {
		return taskstaging.Manifest{}, fmt.Errorf("workspace changes were already discarded")
	}
	manifest, err := task.BuildManifest()
	if err != nil {
		return taskstaging.Manifest{}, err
	}
	if task.LiveWorkspace() {
		result, applyErr := task.Apply(manifest)
		if applyErr != nil {
			return taskstaging.Manifest{}, applyErr
		}
		if restoreErr := taskstaging.Restore(result.RecoveryPath); restoreErr != nil {
			return taskstaging.Manifest{}, restoreErr
		}
		_ = r.ReleaseTaskStagingProcesses(runID)
		return manifest, nil
	}
	if err := r.ReleaseTaskStaging(runID); err != nil {
		return taskstaging.Manifest{}, err
	}
	return manifest, nil
}

func (r *Runtime) WorkspaceVersions() ([]taskstaging.Version, error) {
	if r == nil || r.cfg == nil {
		return nil, fmt.Errorf("workspace versions require a configured runtime")
	}
	base, err := r.taskStagingBaseRoot()
	if err != nil {
		return nil, err
	}
	return taskstaging.ListVersions(base, r.cfg.WorkspaceRoot, taskstaging.DefaultVersionRetention)
}

func stagedTaskApplyActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "prepared", "applying", "applied":
		return true
	default:
		return false
	}
}

func (r *Runtime) loadTaskStaging(runID string) (*taskstaging.Task, bool, error) {
	if r == nil || r.cfg == nil || r.session == nil {
		return nil, false, fmt.Errorf("task staging requires a configured runtime")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, false, nil
	}
	r.taskStagingMu.Lock()
	task := r.taskStaging[runID]
	r.taskStagingMu.Unlock()
	if task != nil {
		return task, true, nil
	}
	base, err := r.taskStagingBaseRoot()
	if err != nil {
		return nil, false, err
	}
	taskRoot := filepath.Join(base, runID)
	if _, err := os.Stat(filepath.Join(taskRoot, "metadata", "baseline.json")); os.IsNotExist(err) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	task, err = taskstaging.Load(taskRoot)
	if err != nil {
		return nil, false, err
	}
	r.taskStagingMu.Lock()
	if r.taskStaging == nil {
		r.taskStaging = make(map[string]*taskstaging.Task)
	}
	r.taskStaging[runID] = task
	r.taskStagingMu.Unlock()
	return task, true, nil
}

func (r *Runtime) ReleaseTaskStaging(runID string) error {
	task, ok, err := r.loadTaskStaging(runID)
	if err != nil || !ok {
		return err
	}
	if err := r.ReleaseTaskStagingProcesses(runID); err != nil {
		return err
	}
	return taskstaging.Cleanup(task)
}

func (r *Runtime) ReleaseTaskStagingProcesses(runID string) error {
	task, ok, err := r.loadTaskStaging(runID)
	if err != nil || !ok {
		return err
	}
	if releaser, ok := r.mcp.(interface{ ReleaseWorkspace(string) error }); ok {
		return releaser.ReleaseWorkspace(task.Workspace)
	}
	return nil
}

func (r *Runtime) releaseUnchangedTaskStaging(runID, status string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "success", "succeeded", "blocked", "failed", "error", "denied", "cancelled", "canceled":
	default:
		return
	}
	manifest, _, exists, err := r.TaskStagingManifest(runID)
	if err != nil || !exists || len(manifest.Changes) != 0 {
		return
	}
	if task, ok, loadErr := r.loadTaskStaging(runID); loadErr == nil && ok && task.LiveWorkspace() {
		_ = taskstaging.Cleanup(task)
		return
	}
	_ = r.ReleaseTaskStaging(runID)
}

func (r *Runtime) taskStagingBaseRoot() (string, error) {
	workspaceRoot := ""
	if r != nil && r.cfg != nil {
		workspaceRoot = r.cfg.WorkspaceRoot
	}
	return r.taskStagingBaseRootForWorkspace(workspaceRoot)
}

func (r *Runtime) taskStagingBaseRootForWorkspace(workspaceRoot string) (string, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return "", fmt.Errorf("resolve task staging workspace: workspace root is required")
	}
	workspace, err := filepath.Abs(filepath.Clean(strings.TrimSpace(workspaceRoot)))
	if err != nil {
		return "", fmt.Errorf("resolve task staging workspace: %w", err)
	}
	dataRoot := strings.TrimSpace(r.cfg.DataRoot)
	if dataRoot == "" {
		dataRoot = strings.TrimSpace(r.cfg.RuntimeHome)
	}
	if dataRoot != "" {
		dataRoot, err = filepath.Abs(filepath.Clean(dataRoot))
		if err != nil {
			return "", fmt.Errorf("resolve task staging data root: %w", err)
		}
	}
	base := filepath.Join(dataRoot, "task-staging")
	if dataRoot == "" || pathWithin(workspace, base) {
		digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(workspace))))
		base = filepath.Join(os.TempDir(), "goflow-task-staging", hex.EncodeToString(digest[:8]))
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("create task staging root: %w", err)
	}
	return base, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}
