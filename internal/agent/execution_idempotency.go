package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/commandpolicy"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/executionfingerprint"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type workflowToolOperationReservation struct {
	key           string
	workspaceHash string
	reusable      bool
	replay        *schema.ToolResult
}

type ordinaryAgentReadCache struct {
	mu      sync.Mutex
	results map[string]ordinaryAgentReadCacheEntry
}

const (
	ordinaryAgentReadCacheMaxEntries     = 64
	ordinaryAgentReadCacheMaxResultBytes = 256 << 10
	ordinaryAgentReadCacheMaxFileBytes   = 4 << 20
)

type ordinaryAgentReadFingerprint struct {
	hash string
	file os.FileInfo
}

func (f ordinaryAgentReadFingerprint) matches(other ordinaryAgentReadFingerprint) bool {
	return f.hash != "" && f.hash == other.hash &&
		((f.file == nil && other.file == nil) || (f.file != nil && other.file != nil && os.SameFile(f.file, other.file)))
}

type ordinaryAgentReadCacheEntry struct {
	result      schema.ToolResult
	fingerprint ordinaryAgentReadFingerprint
}

func newOrdinaryAgentReadCache() *ordinaryAgentReadCache {
	return &ordinaryAgentReadCache{results: make(map[string]ordinaryAgentReadCacheEntry)}
}

func executeToolCallWithIdempotency(ctx context.Context, mcp executionMCP, execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool) (schema.ToolResult, bool, error) {
	readKey, readFingerprint := ordinaryAgentReadCacheState(execCtx, call, tool)
	if replay, ok := replayOrdinaryAgentRead(execCtx, call, tool, readKey, readFingerprint); ok {
		return replay, true, nil
	}
	reservation, err := reserveWorkflowToolOperation(execCtx, call, tool)
	if err != nil {
		return schema.ToolResult{}, false, err
	}
	if reservation.replay != nil {
		return *reservation.replay, true, nil
	}

	startedAt := time.Now()
	result, callErr := callExecutionMCP(ctx, mcp, execCtx, call, tool)
	result.DurationMS = time.Since(startedAt).Milliseconds()
	if callErr != nil {
		result = schema.ToolResult{
			CallID:   call.ID,
			ToolName: tool.Name,
			Content:  callErr.Error(),
			IsError:  true,
		}
	} else {
		result.CallID = call.ID
		// Keep the resolved server identity in durable evidence. MCP servers
		// return local names, which cannot satisfy a qualified validator contract.
		if tool.Server != "" && !strings.Contains(tool.Name, "/") {
			result.ToolName = tool.Server + "/" + tool.Name
		} else if strings.Contains(tool.Name, "/") || result.ToolName == "" {
			result.ToolName = tool.Name
		}
	}

	if reservation.key != "" {
		var persistErr error
		switch {
		case result.Suspended && !result.Denied:
			persistErr = execCtx.Session.ReleaseWorkflowToolOperation(execCtx.WorkflowRunID, reservation.key)
		case result.IsError && !result.Denied && retryableChangeSliceWriteFailure(tool, result):
			persistErr = execCtx.Session.ReleaseWorkflowToolOperation(execCtx.WorkflowRunID, reservation.key)
		case reservation.reusable && !reusableOperationSucceeded(result, reservation.workspaceHash, tool):
			_, persistErr = execCtx.Session.FailReusableWorkflowToolOperation(execCtx.WorkflowRunID, reservation.key, result)
		default:
			_, persistErr = execCtx.Session.CompleteWorkflowToolOperation(execCtx.WorkflowRunID, reservation.key, result)
		}
		if persistErr != nil {
			return schema.ToolResult{}, false, fmt.Errorf("workflow tool operation result is not durable; the operation will not be retried automatically: %w", persistErr)
		}
	}
	updateOrdinaryAgentReadCache(execCtx, call, tool, result, readKey, readFingerprint)
	return result, false, nil
}

func replayOrdinaryAgentRead(execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool, key string, fingerprint ordinaryAgentReadFingerprint) (schema.ToolResult, bool) {
	cache := execCtx.readResultCache
	if key == "" {
		return schema.ToolResult{}, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.results[key]
	if !ok || !entry.fingerprint.matches(fingerprint) {
		delete(cache.results, key)
		return schema.ToolResult{}, false
	}
	result := entry.result
	result.CallID = call.ID
	if strings.TrimSpace(result.ToolName) == "" {
		result.ToolName = tool.Name
	}
	if policy.KindForTool(tool) == config.ToolKindRead {
		result.Summary = "reused unchanged read result"
	} else {
		result.Summary = "reused unchanged validation result"
	}
	return result, true
}

func updateOrdinaryAgentReadCache(execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool, result schema.ToolResult, key string, before ordinaryAgentReadFingerprint) {
	cache := execCtx.readResultCache
	if cache == nil || strings.TrimSpace(execCtx.AgentRunID) == "" || strings.TrimSpace(execCtx.WorkflowRunID) != "" || result.Denied || result.Suspended {
		return
	}
	if ordinaryAgentToolMayChangeWorkspace(tool, result) {
		cache.mu.Lock()
		clear(cache.results)
		cache.mu.Unlock()
		return
	}
	if key == "" || !reusableOperationSucceeded(result, before.hash, tool) || len(result.Content)+len(result.Summary) > ordinaryAgentReadCacheMaxResultBytes {
		return
	}
	// A post-call snapshot alone could bind an old result to a concurrent edit.
	// Require both snapshots to agree before publishing the entry.
	afterKey, after := ordinaryAgentReadCacheState(execCtx, call, tool)
	if key != afterKey || !before.matches(after) {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.results) >= ordinaryAgentReadCacheMaxEntries {
		// A small per-run cache does not need an LRU or background eviction.
		clear(cache.results)
	}
	cache.results[key] = ordinaryAgentReadCacheEntry{result: result, fingerprint: after}
}

func ordinaryAgentToolCacheable(call schema.ToolCall, tool schema.Tool) bool {
	if managedProcessLifecycleTool(call, tool) {
		return false
	}
	var request struct {
		Refresh bool  `json:"refresh"`
		Reuse   *bool `json:"reuse_if_unchanged"`
	}
	if json.Unmarshal(call.Arguments, &request) != nil || request.Refresh || (request.Reuse != nil && !*request.Reuse) {
		return false
	}
	if policy.KindForTool(tool) == config.ToolKindRead {
		if !strings.EqualFold(executionToolServerName(call, tool), "file_tools") {
			return false
		}
		// Workspace fingerprints skip directories that searches/listings can see.
		// Only single-file reads have a complete, inexpensive dependency set.
		for _, name := range []string{"read_file", "read_file_range"} {
			if toolNameEquivalent(call.Name, name) || toolNameEquivalent(tool.Name, name) {
				return true
			}
		}
		return false
	}
	if !taskScopedCommandTool(call, tool) || request.Reuse == nil || !*request.Reuse {
		return false
	}
	classification := commandpolicy.ClassifyArguments(call.Arguments)
	if classification.RequiresExplicitApproval {
		return false
	}
	switch classification.Class {
	case commandpolicy.ClassTest, commandpolicy.ClassBuild, commandpolicy.ClassLint:
		return true
	default:
		return false
	}
}

func ordinaryAgentReadCacheKey(execCtx ExecutionContext, root string, call schema.ToolCall, tool schema.Tool) (string, error) {
	canonical, err := canonicalToolArguments(call.Arguments)
	if err != nil {
		return "", err
	}
	name := strings.ToLower(strings.TrimSpace(tool.Name))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(call.Name))
	}
	scope, err := json.Marshal(execCtx.ToolWorkspaceScope)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{execCtx.AgentRunID, execCtx.TargetID, root, string(scope), strings.ToLower(executionToolServerName(call, tool)), name, string(canonical)}, "\x00")))
	return hex.EncodeToString(digest[:]), nil
}

func ordinaryAgentReadCacheState(execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool) (string, ordinaryAgentReadFingerprint) {
	if execCtx.readResultCache == nil || strings.TrimSpace(execCtx.AgentRunID) == "" || strings.TrimSpace(execCtx.WorkflowRunID) != "" || !ordinaryAgentToolCacheable(call, tool) || normalizeExecutionTargetID(execCtx.TargetID) != session.LocalTargetID {
		return "", ordinaryAgentReadFingerprint{}
	}
	root := strings.TrimSpace(execCtx.ToolWorkspaceRoot)
	if root == "" {
		root = strings.TrimSpace(execCtx.WorkspaceRoot)
	} else if strings.TrimSpace(execCtx.ToolWorkspaceScope.Root) != "" {
		root = strings.TrimSpace(execCtx.ToolWorkspaceScope.Workspace)
	}
	if root == "" {
		return "", ordinaryAgentReadFingerprint{}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", ordinaryAgentReadFingerprint{}
	}
	key, err := ordinaryAgentReadCacheKey(execCtx, root, call, tool)
	if err != nil {
		return "", ordinaryAgentReadFingerprint{}
	}
	if policy.KindForTool(tool) != config.ToolKindRead {
		snapshot, err := executionfingerprint.Workspace(root)
		if err != nil {
			return "", ordinaryAgentReadFingerprint{}
		}
		return key, ordinaryAgentReadFingerprint{hash: snapshot.Hash}
	}
	var request struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(call.Arguments, &request) != nil || strings.TrimSpace(request.Path) == "" {
		return "", ordinaryAgentReadFingerprint{}
	}
	path := request.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ordinaryAgentReadFingerprint{}
	}
	// Root.Open checks symlink containment while opening the file, avoiding a
	// separate ancestor walk and the check/open race of EvalSymlinks + Open.
	directory, err := os.OpenRoot(root)
	if err != nil {
		return "", ordinaryAgentReadFingerprint{}
	}
	defer directory.Close()
	file, err := directory.Open(relative)
	if err != nil {
		return "", ordinaryAgentReadFingerprint{}
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > ordinaryAgentReadCacheMaxFileBytes {
		return "", ordinaryAgentReadFingerprint{}
	}
	// Hash content, not just size/mtime: editors can preserve both on replacement.
	data := make([]byte, info.Size()+1)
	n, err := io.ReadFull(file, data)
	if (err != io.EOF && err != io.ErrUnexpectedEOF) || int64(n) != info.Size() {
		return "", ordinaryAgentReadFingerprint{}
	}
	digest := sha256.Sum256(data[:n])
	return key, ordinaryAgentReadFingerprint{hash: fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%x", path, info.Size(), info.ModTime().UnixNano(), info.Mode(), digest), file: info}
}

func ordinaryAgentToolMayChangeWorkspace(tool schema.Tool, result schema.ToolResult) bool {
	if policy.KindForTool(tool) == config.ToolKindWrite {
		return true
	}
	if policy.KindForTool(tool) != config.ToolKindExec {
		return false
	}
	var payload struct {
		WorkspaceChanged bool `json:"workspace_changed"`
	}
	return json.Unmarshal([]byte(result.Content), &payload) == nil && payload.WorkspaceChanged
}

type workspaceExecutionMCP interface {
	CallToolInWorkspace(ctx context.Context, name string, arguments []byte, workspaceRoot string) (schema.ToolResult, error)
}

type taskWorkspaceExecutionMCP interface {
	CallToolInTaskWorkspace(ctx context.Context, name string, arguments []byte, scope schema.TaskWorkspaceScope) (schema.ToolResult, error)
}

func callExecutionMCP(ctx context.Context, mcp executionMCP, execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool) (schema.ToolResult, error) {
	workspaceRoot := strings.TrimSpace(execCtx.ToolWorkspaceRoot)
	if workspaceRoot == "" || !workspaceScopedExecutionTool(call, tool) {
		return mcp.CallTool(ctx, call.Name, call.Arguments)
	}
	if scoped, ok := mcp.(taskWorkspaceExecutionMCP); ok && strings.TrimSpace(execCtx.ToolWorkspaceScope.Root) != "" {
		return scoped.CallToolInTaskWorkspace(ctx, call.Name, call.Arguments, execCtx.ToolWorkspaceScope)
	}
	scoped, ok := mcp.(workspaceExecutionMCP)
	if !ok {
		return schema.ToolResult{}, fmt.Errorf("task workspace is prepared but MCP does not support workspace-scoped execution; refusing to fall back to the real workspace")
	}
	return scoped.CallToolInWorkspace(ctx, call.Name, call.Arguments, workspaceRoot)
}

func reserveWorkflowToolOperation(execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool) (workflowToolOperationReservation, error) {
	if !execCtx.IdempotentToolCalls || managedProcessLifecycleTool(call, tool) {
		return workflowToolOperationReservation{}, nil
	}
	operation, err := workflowToolOperationForCall(execCtx, call, tool)
	if err != nil {
		return workflowToolOperationReservation{}, err
	}
	if policy.KindForTool(tool) == config.ToolKindRead && !operation.Reusable {
		return workflowToolOperationReservation{}, nil
	}
	existing, created, err := execCtx.Session.ReserveWorkflowToolOperation(operation)
	if err != nil {
		return workflowToolOperationReservation{}, fmt.Errorf("reserve workflow tool operation: %w", err)
	}
	if created {
		return workflowToolOperationReservation{key: operation.Key, workspaceHash: operation.WorkspaceHash, reusable: operation.Reusable}, nil
	}
	switch existing.Status {
	case session.WorkflowToolOperationCompleted:
		if existing.Reusable && !reusableOperationSucceeded(existing.Result, existing.WorkspaceHash, tool) {
			return workflowToolOperationReservation{}, fmt.Errorf("reusable workflow validation %s has invalid success evidence", existing.Key)
		}
		result := existing.Result
		result.CallID = call.ID
		if result.ToolName == "" {
			result.ToolName = tool.Name
		}
		return workflowToolOperationReservation{key: existing.Key, workspaceHash: existing.WorkspaceHash, reusable: existing.Reusable, replay: &result}, nil
	case session.WorkflowToolOperationStarted:
		return workflowToolOperationReservation{}, fmt.Errorf("workflow tool operation outcome is uncertain for run %s stage %s tool %s; refusing to execute the side effect again", execCtx.WorkflowRunID, execCtx.WorkflowStage, tool.Name)
	case session.WorkflowToolOperationFailed:
		return workflowToolOperationReservation{}, fmt.Errorf("failed reusable workflow validation %s was not reset for retry", existing.Key)
	default:
		return workflowToolOperationReservation{}, fmt.Errorf("workflow tool operation %s has unsupported status %q", existing.Key, existing.Status)
	}
}

// Process state is external to the workspace fingerprint and changes across
// stages and server restarts. Never replay a previous PID or running flag.
func managedProcessLifecycleTool(call schema.ToolCall, tool schema.Tool) bool {
	if !strings.EqualFold(executionToolServerName(call, tool), "command_tools") {
		return false
	}
	for _, name := range []string{"start_process", "process_status", "stop_process"} {
		if toolNameEquivalent(call.Name, name) || toolNameEquivalent(tool.Name, name) {
			return true
		}
	}
	return false
}

func workflowToolOperationForCall(execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool) (session.WorkflowToolOperationSnapshot, error) {
	if execCtx.Session == nil {
		return session.WorkflowToolOperationSnapshot{}, fmt.Errorf("session state is required for retryable side-effecting workflow tools")
	}
	runID := strings.TrimSpace(execCtx.WorkflowRunID)
	stage := strings.TrimSpace(execCtx.WorkflowStage)
	toolName := strings.TrimSpace(tool.Name)
	if toolName == "" {
		toolName = strings.TrimSpace(call.Name)
	}
	if runID == "" || stage == "" || toolName == "" {
		return session.WorkflowToolOperationSnapshot{}, fmt.Errorf("retryable side-effecting workflow tool requires run, stage, and tool identity")
	}
	canonical, err := canonicalToolArguments(call.Arguments)
	if err != nil {
		return session.WorkflowToolOperationSnapshot{}, fmt.Errorf("normalize workflow tool arguments: %w", err)
	}
	argumentsDigest := sha256.Sum256(canonical)
	argumentsHash := hex.EncodeToString(argumentsDigest[:])
	workflow := strings.TrimSpace(execCtx.WorkflowName)
	reusable, workspaceHash := reusableValidationForCall(execCtx, call, tool)
	if !reusable {
		reusable, workspaceHash = reusableReadForCall(execCtx, call, tool)
	}
	keyParts := []string{"goflow-workflow-tool-operation-v1", runID, workflow, stage, toolName, argumentsHash}
	if reusable {
		keyParts = []string{"goflow-workflow-validation-v1", runID, workflow, toolName, argumentsHash, workspaceHash}
	}
	keyDigest := sha256.Sum256([]byte(strings.Join(keyParts, "\x00")))
	return session.WorkflowToolOperationSnapshot{
		Key:           hex.EncodeToString(keyDigest[:]),
		RunID:         runID,
		Workflow:      workflow,
		Stage:         stage,
		ToolName:      toolName,
		ArgumentsHash: argumentsHash,
		WorkspaceHash: workspaceHash,
		Reusable:      reusable,
	}, nil
}

func reusableValidationForCall(execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool) (bool, string) {
	if !taskScopedCommandTool(call, tool) {
		return false, ""
	}
	var request map[string]any
	if json.Unmarshal(call.Arguments, &request) != nil {
		return false, ""
	}
	classification := commandpolicy.ClassifyArguments(call.Arguments)
	if classification.RequiresExplicitApproval {
		return false, ""
	}
	switch classification.Class {
	case commandpolicy.ClassInspection, commandpolicy.ClassTest, commandpolicy.ClassBuild, commandpolicy.ClassLint, commandpolicy.ClassFormat:
	default:
		return false, ""
	}
	workspaceRoot := strings.TrimSpace(execCtx.ToolWorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = strings.TrimSpace(execCtx.WorkspaceRoot)
	}
	snapshot, err := executionfingerprint.Workspace(workspaceRoot)
	if err != nil || strings.TrimSpace(snapshot.Hash) == "" {
		return false, ""
	}
	return true, snapshot.Hash
}

func reusableReadForCall(execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool) (bool, string) {
	if policy.KindForTool(tool) != config.ToolKindRead || !strings.EqualFold(executionToolServerName(call, tool), "file_tools") || execCtx.Session == nil || strings.TrimSpace(execCtx.WorkflowRunID) == "" {
		return false, ""
	}
	name := strings.ToLower(strings.TrimSpace(tool.Name))
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	switch name {
	case "read_file", "read_file_range", "search_files", "list_tree":
	default:
		return false, ""
	}
	root := strings.TrimSpace(execCtx.ToolWorkspaceRoot)
	if root == "" {
		root = strings.TrimSpace(execCtx.WorkspaceRoot)
	}
	snapshot, err := executionfingerprint.Workspace(root)
	if err != nil || strings.TrimSpace(snapshot.Hash) == "" {
		return false, ""
	}
	return true, snapshot.Hash
}

func reusableOperationSucceeded(result schema.ToolResult, workspaceHash string, tool schema.Tool) bool {
	if policy.KindForTool(tool) == config.ToolKindRead {
		return !result.IsError && !result.Denied && !result.Suspended && strings.TrimSpace(workspaceHash) != ""
	}
	return reusableValidationSucceeded(result, workspaceHash)
}

func reusableValidationSucceeded(result schema.ToolResult, workspaceHash string) bool {
	if result.IsError || result.Denied || result.Suspended || strings.TrimSpace(workspaceHash) == "" {
		return false
	}
	var payload struct {
		ExitCode                       *int   `json:"exit_code"`
		TimedOut                       bool   `json:"timed_out"`
		Cancelled                      bool   `json:"cancelled"`
		ProcessError                   string `json:"process_error"`
		WorkspaceFingerprintBeforeHash string `json:"workspace_fingerprint_before_hash"`
		WorkspaceFingerprintAfterHash  string `json:"workspace_fingerprint_after_hash"`
	}
	if json.Unmarshal([]byte(result.Content), &payload) != nil || payload.ExitCode == nil || *payload.ExitCode != 0 || payload.TimedOut || payload.Cancelled || strings.TrimSpace(payload.ProcessError) != "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(payload.WorkspaceFingerprintBeforeHash), workspaceHash) &&
		strings.EqualFold(strings.TrimSpace(payload.WorkspaceFingerprintAfterHash), workspaceHash)
}

func canonicalToolArguments(arguments []byte) ([]byte, error) {
	if len(bytes.TrimSpace(arguments)) == 0 {
		return []byte("{}"), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return json.Marshal(value)
}
