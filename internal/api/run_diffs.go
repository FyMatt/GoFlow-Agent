package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type runDiffResponse struct {
	RunID   string        `json:"run_id"`
	RunKind string        `json:"run_kind"`
	Total   int           `json:"total"`
	Diffs   []runDiffItem `json:"diffs"`
}

type runDiffItem struct {
	ID            string `json:"id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	RunKind       string `json:"run_kind,omitempty"`
	Stage         string `json:"stage,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Source        string `json:"source,omitempty"`
	ToolName      string `json:"tool_name,omitempty"`
	ToolCallID    string `json:"tool_call_id,omitempty"`
	Path          string `json:"path,omitempty"`
	Status        string `json:"status,omitempty"`
	StatusCode    string `json:"status_code,omitempty"`
	Summary       string `json:"summary,omitempty"`
	OldRange      string `json:"old_range,omitempty"`
	NewRange      string `json:"new_range,omitempty"`
	LineRange     string `json:"line_range,omitempty"`
	AddedLines    int    `json:"added_lines,omitempty"`
	DeletedLines  int    `json:"deleted_lines,omitempty"`
	BytesWritten  int    `json:"bytes_written,omitempty"`
	OldLineCount  int    `json:"old_line_count,omitempty"`
	NewLineCount  int    `json:"new_line_count,omitempty"`
	DiffPreview   string `json:"diff_preview,omitempty"`
	Patch         string `json:"patch,omitempty"`
	IsError       bool   `json:"is_error,omitempty"`
	NeedsAction   bool   `json:"needs_action,omitempty"`
	ArgumentsHint string `json:"arguments_hint,omitempty"`
}

func agentRunDiffResponse(run session.AgentRunSnapshot, query agentRunQuery) runDiffResponse {
	diffs := agentRunDiffs(run, query)
	return runDiffResponse{RunID: run.ID, RunKind: "agent", Total: len(diffs), Diffs: diffs}
}

func workflowRunDiffResponse(run session.WorkflowRunSnapshot, query workflowRunQuery) runDiffResponse {
	diffs := workflowRunDiffs(run, query)
	return runDiffResponse{RunID: run.ID, RunKind: "workflow", Total: len(diffs), Diffs: diffs}
}

func agentRunDiffs(run session.AgentRunSnapshot, query agentRunQuery) []runDiffItem {
	seen := make(map[string]struct{})
	out := make([]runDiffItem, 0)
	add := func(item runDiffItem) {
		if !agentRunDiffMatchesQuery(item, query) {
			return
		}
		key := runDiffDedupKey(item)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	for _, event := range run.Events {
		if !strings.EqualFold(normalizeWorkflowRunQueryToken(event.Type), "tool_result") {
			continue
		}
		item, ok := runDiffFromToolPayload(run.ID, "agent", "", event.AgentID, event.Mode, "event", event.ToolName, event.ToolCallID, event.ArgumentsSummary, event.Content, event.IsError, event.NeedsAction || event.Suspended)
		if ok {
			add(item)
		}
	}
	if run.Result != nil {
		for _, result := range run.Result.ToolResults {
			item, ok := runDiffFromToolResult(run.ID, "agent", "", firstWorkflowRunQueryValue(run.Result.AgentID, run.AgentID), firstWorkflowRunQueryValue(run.Result.Mode, run.Mode), "result", result)
			if ok {
				add(item)
			}
		}
	}
	if query.Limit > 0 && len(out) > query.Limit {
		return append([]runDiffItem(nil), out[:query.Limit]...)
	}
	return out
}

func workflowRunDiffs(run session.WorkflowRunSnapshot, query workflowRunQuery) []runDiffItem {
	if query.ItemKind != "" {
		switch query.ItemKind {
		case "diff", "diffs", "change", "changes", "write", "writes":
		default:
			return nil
		}
	}
	seen := make(map[string]struct{})
	out := make([]runDiffItem, 0)
	add := func(item runDiffItem) {
		if !workflowRunDiffMatchesQuery(item, query) {
			return
		}
		key := runDiffDedupKey(item)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	for _, event := range run.Events {
		if !strings.EqualFold(normalizeWorkflowRunQueryToken(event.Type), "tool_result") {
			continue
		}
		item, ok := runDiffFromToolPayload(run.ID, "workflow", event.Stage, event.AgentID, event.Mode, "event", event.ToolName, event.ToolCallID, event.ArgumentsSummary, event.Content, event.IsError, event.NeedsAction || event.PendingApproval || event.Suspended)
		if ok {
			add(item)
		}
	}
	for _, stage := range run.CompletedStages {
		for _, result := range stage.Result.ToolResults {
			item, ok := runDiffFromToolResult(run.ID, "workflow", stage.Stage, firstWorkflowRunQueryValue(stage.Result.AgentID, stage.AgentID), firstWorkflowRunQueryValue(stage.Result.Mode, ""), "result", result)
			if ok {
				add(item)
			}
		}
		for _, artifact := range stage.Artifacts {
			item, ok := runDiffFromArtifact(run.ID, stage.Stage, artifact)
			if ok {
				add(item)
			}
		}
	}
	for _, artifact := range run.Artifacts {
		item, ok := runDiffFromArtifact(run.ID, artifact.Stage, artifact)
		if ok {
			add(item)
		}
	}
	if query.Limit > 0 && len(out) > query.Limit {
		return append([]runDiffItem(nil), out[:query.Limit]...)
	}
	return out
}

func runDiffFromToolResult(runID, runKind, stage, agentID, mode, source string, result schema.ToolResult) (runDiffItem, bool) {
	return runDiffFromToolPayload(runID, runKind, stage, agentID, mode, source, result.ToolName, result.CallID, "", result.Content, result.IsError, result.Suspended)
}

func runDiffFromArtifact(runID, stage string, artifact session.WorkflowRunArtifact) (runDiffItem, bool) {
	kind := normalizeWorkflowRunQueryToken(artifact.Kind)
	if kind != "diff" && kind != "patch" && kind != "change" {
		return runDiffItem{}, false
	}
	item := runDiffItem{
		ID:          firstWorkflowRunQueryValue(artifact.ID, runID+"-"+stage+"-artifact"),
		RunID:       runID,
		RunKind:     "workflow",
		Stage:       stage,
		Source:      "artifact",
		ToolName:    artifact.ToolName,
		ToolCallID:  artifact.ToolCallID,
		Path:        artifact.Metadata["path"],
		Status:      firstWorkflowRunQueryValue(artifact.Metadata["status"], "artifact"),
		StatusCode:  runDiffStatusCode(artifact.Metadata["status"]),
		Summary:     firstWorkflowRunQueryValue(artifact.Summary, artifact.Title),
		DiffPreview: artifact.Content,
		Patch:       artifact.Content,
		IsError:     artifact.IsError,
	}
	if strings.TrimSpace(item.Path) == "" {
		item.Path = artifact.Title
	}
	return item, strings.TrimSpace(item.DiffPreview) != "" || strings.TrimSpace(item.Summary) != ""
}

func runDiffFromToolPayload(runID, runKind, stage, agentID, mode, source, toolName, callID, argsHint, content string, isError, needsAction bool) (runDiffItem, bool) {
	if !runDiffToolName(toolName) || strings.TrimSpace(content) == "" {
		return runDiffItem{}, false
	}
	payload, ok := runDiffPayload(content)
	if !ok {
		return runDiffItem{}, false
	}
	path := runDiffString(payload, "relative_path")
	if path == "" {
		path = runDiffString(payload, "path")
	}
	status := runDiffString(payload, "status")
	summary := firstWorkflowRunQueryValue(runDiffString(payload, "line_summary"), runDiffString(payload, "summary"), status)
	diffPreview := strings.TrimRight(runDiffString(payload, "diff_preview"), "\n")
	if status == "" && summary == "" && diffPreview == "" && runDiffInt(payload, "added_lines") == 0 && runDiffInt(payload, "deleted_lines") == 0 {
		return runDiffItem{}, false
	}
	item := runDiffItem{
		ID:            runDiffID(runID, stage, source, toolName, callID, path),
		RunID:         runID,
		RunKind:       runKind,
		Stage:         stage,
		AgentID:       agentID,
		Mode:          mode,
		Source:        source,
		ToolName:      toolName,
		ToolCallID:    callID,
		Path:          path,
		Status:        status,
		StatusCode:    runDiffStatusCode(status),
		Summary:       summary,
		OldRange:      runDiffString(payload, "old_range"),
		NewRange:      runDiffString(payload, "new_range"),
		AddedLines:    runDiffInt(payload, "added_lines"),
		DeletedLines:  runDiffInt(payload, "deleted_lines"),
		BytesWritten:  runDiffInt(payload, "bytes_written"),
		OldLineCount:  runDiffInt(payload, "old_line_count"),
		NewLineCount:  runDiffInt(payload, "new_line_count"),
		DiffPreview:   diffPreview,
		IsError:       isError,
		NeedsAction:   needsAction,
		ArgumentsHint: argsHint,
	}
	item.LineRange = runDiffLineRange(item.OldRange, item.NewRange)
	item.Patch = runDiffPatch(item)
	return item, true
}

func runDiffPayload(content string) (map[string]any, bool) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || len(payload) == 0 {
		return nil, false
	}
	return payload, true
}

func runDiffToolName(toolName string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if index := strings.LastIndex(name, "/"); index >= 0 {
		name = name[index+1:]
	}
	switch name {
	case "write_file", "edit_file", "delete_file":
		return true
	default:
		return false
	}
}

func runDiffString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func runDiffInt(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return int(parsed)
		}
	}
	return 0
}

func runDiffStatusCode(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created", "added":
		return "A"
	case "deleted", "deleted_file", "deleted_directory", "removed":
		return "D"
	case "unchanged":
		return "="
	default:
		return "M"
	}
}

func runDiffLineRange(oldRange, newRange string) string {
	oldRange = strings.TrimSpace(oldRange)
	newRange = strings.TrimSpace(newRange)
	switch {
	case oldRange != "" && oldRange != "-" && newRange != "" && newRange != "-":
		return "old " + oldRange + " -> new " + newRange
	case oldRange != "" && oldRange != "-":
		return "old " + oldRange
	case newRange != "" && newRange != "-":
		return "new " + newRange
	default:
		return ""
	}
}

func runDiffPatch(item runDiffItem) string {
	preview := strings.TrimRight(item.DiffPreview, "\n")
	if preview == "" {
		return ""
	}
	path := firstWorkflowRunQueryValue(item.Path, "unknown")
	oldPath := "a/" + path
	newPath := "b/" + path
	if item.StatusCode == "A" {
		oldPath = "/dev/null"
	}
	if item.StatusCode == "D" {
		newPath = "/dev/null"
	}
	return fmt.Sprintf("diff --goflow %s %s\n--- %s\n+++ %s\n%s\n", oldPath, newPath, oldPath, newPath, preview)
}

func runDiffID(runID, stage, source, toolName, callID, path string) string {
	parts := []string{runID, stage, source, toolName, callID, path}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, strings.TrimSpace(part))
		}
	}
	return strings.Join(filtered, ":")
}

func runDiffDedupKey(item runDiffItem) string {
	return strings.Join([]string{
		item.RunID,
		item.Stage,
		item.ToolCallID,
		item.ToolName,
		item.Path,
		item.Status,
		item.DiffPreview,
	}, "\x00")
}

func agentRunDiffMatchesQuery(item runDiffItem, query agentRunQuery) bool {
	if query.EventType != "" && query.EventType != "tool_result" && query.EventType != "diff" && query.EventType != "change" {
		return false
	}
	if query.AgentID != "" && !strings.EqualFold(strings.TrimSpace(item.AgentID), query.AgentID) {
		return false
	}
	if query.ToolName != "" && !strings.EqualFold(strings.TrimSpace(item.ToolName), query.ToolName) {
		return false
	}
	if query.ErrorsOnly && !item.IsError {
		return false
	}
	if query.NeedsActionOnly && !item.NeedsAction {
		return false
	}
	if query.SuspendedOnly && !item.NeedsAction {
		return false
	}
	if query.Query != "" && !runDiffSearchMatch(query.Query, item) {
		return false
	}
	return true
}

func workflowRunDiffMatchesQuery(item runDiffItem, query workflowRunQuery) bool {
	if query.Stage != "" && !workflowRunStageEqual(item.Stage, query.Stage) {
		return false
	}
	if query.EventType != "" && query.EventType != "tool_result" && query.EventType != "diff" && query.EventType != "change" {
		return false
	}
	if query.ArtifactKind != "" && query.ArtifactKind != "diff" && query.ArtifactKind != "patch" && query.ArtifactKind != "change" {
		return false
	}
	if query.Status != "" && normalizeWorkflowRunQueryToken(item.Status) != query.Status {
		return false
	}
	if query.AgentID != "" && !strings.EqualFold(strings.TrimSpace(item.AgentID), query.AgentID) {
		return false
	}
	if query.ToolName != "" && !strings.EqualFold(strings.TrimSpace(item.ToolName), query.ToolName) {
		return false
	}
	if query.ErrorsOnly && !item.IsError {
		return false
	}
	if query.NeedsActionOnly && !item.NeedsAction {
		return false
	}
	if query.SuspendedOnly && !item.NeedsAction {
		return false
	}
	if query.Query != "" && !runDiffSearchMatch(query.Query, item) {
		return false
	}
	return true
}

func runDiffSearchMatch(query string, item runDiffItem) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	values := []string{
		item.Stage,
		item.AgentID,
		item.Mode,
		item.ToolName,
		item.ToolCallID,
		item.Path,
		item.Status,
		item.StatusCode,
		item.Summary,
		item.LineRange,
		item.DiffPreview,
		item.ArgumentsHint,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
