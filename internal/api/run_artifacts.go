package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

type runArtifactsResponse struct {
	RunID        string            `json:"run_id"`
	RunType      string            `json:"run_type"`
	Status       string            `json:"status,omitempty"`
	AgentID      string            `json:"agent_id,omitempty"`
	Mode         string            `json:"mode,omitempty"`
	WorkflowName string            `json:"workflow_name,omitempty"`
	Items        []runArtifactItem `json:"items"`
	Filters      workflowRunQuery  `json:"filters,omitempty"`
	Counts       runArtifactCounts `json:"counts"`
}

type runArtifactCounts struct {
	Total           int `json:"total"`
	Filtered        int `json:"filtered"`
	Returned        int `json:"returned"`
	WithContent     int `json:"with_content,omitempty"`
	ContentOmitted  int `json:"content_omitted,omitempty"`
	ExternalRefs    int `json:"external_refs,omitempty"`
	ToolResults     int `json:"tool_results,omitempty"`
	StructuredItems int `json:"structured_items,omitempty"`
}

type runArtifactItem struct {
	ID           string            `json:"id"`
	RunID        string            `json:"run_id,omitempty"`
	RunType      string            `json:"run_type,omitempty"`
	Source       string            `json:"source,omitempty"`
	Stage        string            `json:"stage,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	Title        string            `json:"title,omitempty"`
	Summary      string            `json:"summary,omitempty"`
	Content      string            `json:"content,omitempty"`
	Mime         string            `json:"mime,omitempty"`
	Ref          string            `json:"ref,omitempty"`
	ArtifactRef  string            `json:"artifact_ref,omitempty"`
	Hash         string            `json:"hash,omitempty"`
	Size         int               `json:"size,omitempty"`
	ContentBytes int               `json:"content_bytes,omitempty"`
	StoredBytes  int               `json:"stored_bytes,omitempty"`
	ToolName     string            `json:"tool_name,omitempty"`
	ToolCallID   string            `json:"tool_call_id,omitempty"`
	AgentID      string            `json:"agent_id,omitempty"`
	Mode         string            `json:"mode,omitempty"`
	CreatedAt    string            `json:"created_at,omitempty"`
	IsError      bool              `json:"is_error,omitempty"`
	Denied       bool              `json:"denied,omitempty"`
	Suspended    bool              `json:"suspended,omitempty"`
	Truncated    bool              `json:"truncated,omitempty"`
	Externalized bool              `json:"externalized,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func runArtifactsWantsEnvelope(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	values := r.URL.Query()
	return workflowRunQueryBool(firstWorkflowRunQueryValue(values.Get("envelope"), values.Get("meta"), values.Get("metadata")))
}

func (s *Server) agentRunArtifactsResponse(run session.AgentRunSnapshot, query workflowRunQuery) runArtifactsResponse {
	total := agentRunArtifacts(run, workflowRunQuery{IncludeContent: query.IncludeContent})
	items := agentRunArtifactsForQuery(run, query)
	resp := runArtifactsResponse{
		RunID:   strings.TrimSpace(run.ID),
		RunType: "agent",
		Status:  strings.TrimSpace(run.Status),
		AgentID: strings.TrimSpace(run.AgentID),
		Mode:    strings.TrimSpace(run.Mode),
		Items:   items,
		Filters: query,
		Counts:  runArtifactCounts{Total: len(total), Filtered: len(items), Returned: len(items)},
	}
	resp.Items = s.hydrateRunArtifactItems(resp.Items, query)
	resp.Counts = countRunArtifacts(resp.Counts, resp.Items, query)
	return resp
}

func (s *Server) workflowRunArtifactsResponse(run session.WorkflowRunSnapshot, query workflowRunQuery) runArtifactsResponse {
	total := workflowRunArtifactsAsRunItems(run, workflowRunQuery{IncludeContent: query.IncludeContent})
	items := workflowRunArtifactsAsRunItems(run, query)
	resp := runArtifactsResponse{
		RunID:        strings.TrimSpace(run.ID),
		RunType:      "workflow",
		Status:       strings.TrimSpace(run.Status),
		AgentID:      runCollectionWorkflowPrimaryAgent(run),
		Mode:         runCollectionWorkflowPrimaryMode(run),
		WorkflowName: strings.TrimSpace(run.Name),
		Items:        items,
		Filters:      query,
		Counts:       runArtifactCounts{Total: len(total), Filtered: len(items), Returned: len(items)},
	}
	resp.Items = s.hydrateRunArtifactItems(resp.Items, query)
	resp.Counts = countRunArtifacts(resp.Counts, resp.Items, query)
	return resp
}

func (s *Server) hydrateRunArtifactItems(items []runArtifactItem, query workflowRunQuery) []runArtifactItem {
	if len(items) == 0 || !query.IncludeContent || s == nil || s.runtime == nil {
		return items
	}
	out := make([]runArtifactItem, len(items))
	for i, item := range items {
		out[i] = s.hydrateRunArtifactItem(item)
	}
	return out
}

func (s *Server) hydrateRunArtifactItem(item runArtifactItem) runArtifactItem {
	if strings.TrimSpace(item.Content) != "" || s == nil || s.runtime == nil {
		return item
	}
	for _, ref := range []string{item.Ref, item.ArtifactRef, item.Hash} {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if artifact, ok := s.runtime.SessionArtifact(ref); ok {
			return mergeSessionArtifactIntoRunArtifact(item, artifact)
		}
		if object, ok, err := s.runtime.ArtifactObject(ref); err == nil && ok {
			return mergeArtifactObjectIntoRunArtifact(item, object)
		}
	}
	return item
}

func mergeSessionArtifactIntoRunArtifact(item runArtifactItem, artifact session.SessionArtifactSnapshot) runArtifactItem {
	item.Ref = firstWorkflowRunQueryValue(item.Ref, artifact.Ref)
	item.ArtifactRef = firstWorkflowRunQueryValue(item.ArtifactRef, artifact.ArtifactRef)
	item.Hash = firstWorkflowRunQueryValue(item.Hash, artifact.Hash)
	item.Kind = firstWorkflowRunQueryValue(item.Kind, artifact.Kind)
	item.Title = firstWorkflowRunQueryValue(item.Title, artifact.Title)
	item.Summary = firstWorkflowRunQueryValue(item.Summary, artifact.Summary)
	item.Content = firstWorkflowRunQueryValue(item.Content, artifact.Content)
	item.Mime = firstWorkflowRunQueryValue(item.Mime, artifact.Mime)
	item.ToolName = firstWorkflowRunQueryValue(item.ToolName, artifact.ToolName)
	item.ToolCallID = firstWorkflowRunQueryValue(item.ToolCallID, artifact.ToolCallID)
	item.AgentID = firstWorkflowRunQueryValue(item.AgentID, artifact.AgentID)
	item.Mode = firstWorkflowRunQueryValue(item.Mode, artifact.Mode)
	item.CreatedAt = firstWorkflowRunQueryValue(item.CreatedAt, artifact.CreatedAt)
	item.Size = firstPositiveInt(item.Size, artifact.ContentBytes, len([]byte(artifact.Content)), len([]byte(artifact.Summary)))
	item.ContentBytes = firstPositiveInt(item.ContentBytes, artifact.ContentBytes, len([]byte(artifact.Content)))
	item.StoredBytes = firstPositiveInt(item.StoredBytes, artifact.StoredBytes)
	item.Truncated = item.Truncated || artifact.Truncated
	item.Externalized = item.Externalized || strings.TrimSpace(artifact.ArtifactRef) != "" || strings.TrimSpace(artifact.Hash) != ""
	item.Metadata = mergeRunArtifactMetadata(item.Metadata, artifact.Metadata)
	return item
}

func mergeArtifactObjectIntoRunArtifact(item runArtifactItem, object session.ArtifactObject) runArtifactItem {
	item.Ref = firstWorkflowRunQueryValue(item.Ref, object.Ref)
	item.ArtifactRef = firstWorkflowRunQueryValue(item.ArtifactRef, object.Ref)
	item.Hash = firstWorkflowRunQueryValue(item.Hash, object.Hash)
	item.Kind = firstWorkflowRunQueryValue(item.Kind, object.Kind)
	item.Title = firstWorkflowRunQueryValue(item.Title, object.Title)
	item.Summary = firstWorkflowRunQueryValue(item.Summary, object.Summary)
	item.Content = firstWorkflowRunQueryValue(item.Content, object.Content)
	item.Mime = firstWorkflowRunQueryValue(item.Mime, object.Mime)
	item.CreatedAt = firstWorkflowRunQueryValue(item.CreatedAt, object.CreatedAt)
	item.Size = firstPositiveInt(item.Size, object.Size, len([]byte(object.Content)), len([]byte(object.Summary)))
	item.ContentBytes = firstPositiveInt(item.ContentBytes, object.Size, len([]byte(object.Content)))
	item.StoredBytes = firstPositiveInt(item.StoredBytes, int(object.StoredBytes))
	item.Externalized = true
	item.Metadata = mergeRunArtifactMetadata(item.Metadata, object.Metadata)
	return item
}

func countRunArtifacts(counts runArtifactCounts, items []runArtifactItem, query workflowRunQuery) runArtifactCounts {
	counts.Filtered = len(items)
	counts.Returned = len(items)
	for _, item := range items {
		if strings.TrimSpace(item.Content) != "" {
			counts.WithContent++
		} else if item.Size > 0 || item.ContentBytes > 0 || strings.TrimSpace(item.Summary) != "" {
			counts.ContentOmitted++
		}
		if strings.TrimSpace(item.Ref) != "" || strings.TrimSpace(item.ArtifactRef) != "" || strings.TrimSpace(item.Hash) != "" {
			counts.ExternalRefs++
		}
		switch normalizeWorkflowRunQueryToken(item.Kind) {
		case "tool_result":
			counts.ToolResults++
		case "structured", "finding", "change", "verification", "audit":
			counts.StructuredItems++
		}
	}
	if !query.IncludeContent {
		counts.WithContent = 0
	}
	return counts
}

func agentRunArtifactsForQuery(run session.AgentRunSnapshot, query workflowRunQuery) []runArtifactItem {
	if query.ItemKind != "" && query.ItemKind != "artifact" && query.ItemKind != "artifacts" {
		return nil
	}
	all := agentRunArtifacts(run, query)
	filtered := make([]runArtifactItem, 0, len(all))
	for _, item := range all {
		if !runArtifactMatchesQuery(item, query) {
			continue
		}
		if !query.IncludeContent {
			item.Content = ""
		}
		filtered = append(filtered, item)
		if query.Limit > 0 && len(filtered) >= query.Limit {
			break
		}
	}
	return filtered
}

func agentRunArtifacts(run session.AgentRunSnapshot, query workflowRunQuery) []runArtifactItem {
	items := agentRunPersistedArtifacts(run, query)
	if len(items) > 0 {
		items = append(items, agentRunEventArtifactRefs(run, query)...)
		return dedupeRunArtifacts(items)
	}
	return agentRunDerivedArtifacts(run, query)
}

func agentRunPersistedArtifacts(run session.AgentRunSnapshot, query workflowRunQuery) []runArtifactItem {
	items := make([]runArtifactItem, 0, len(run.Artifacts))
	for _, artifact := range run.Artifacts {
		item := agentRunArtifactAsRunItem(run, artifact, query)
		if !runArtifactMatchesQuery(item, query) {
			continue
		}
		items = append(items, item)
	}
	return items
}

func agentRunArtifactAsRunItem(run session.AgentRunSnapshot, artifact session.AgentRunArtifactSnapshot, query workflowRunQuery) runArtifactItem {
	source := firstWorkflowRunQueryValue(artifact.Metadata["source"], artifact.Metadata["kind"])
	item := runArtifactItem{
		ID:           strings.TrimSpace(artifact.ID),
		RunID:        strings.TrimSpace(run.ID),
		RunType:      "agent",
		Source:       source,
		Kind:         strings.TrimSpace(artifact.Kind),
		Title:        strings.TrimSpace(artifact.Title),
		Summary:      summaryText(artifact.Summary),
		Content:      artifact.Content,
		Mime:         firstWorkflowRunQueryValue(artifact.Mime, artifact.Metadata["mime"], runArtifactMimeForKind(artifact.Kind)),
		Ref:          strings.TrimSpace(artifact.Ref),
		ArtifactRef:  strings.TrimSpace(artifact.ArtifactRef),
		Hash:         strings.TrimSpace(artifact.Hash),
		Size:         firstPositiveInt(artifact.ContentBytes, len([]byte(artifact.Content)), len([]byte(artifact.Summary))),
		ContentBytes: artifact.ContentBytes,
		StoredBytes:  artifact.StoredBytes,
		ToolName:     strings.TrimSpace(artifact.ToolName),
		ToolCallID:   strings.TrimSpace(artifact.ToolCallID),
		AgentID:      firstWorkflowRunQueryValue(artifact.AgentID, run.AgentID),
		Mode:         firstWorkflowRunQueryValue(artifact.Mode, run.Mode),
		CreatedAt:    strings.TrimSpace(artifact.CreatedAt),
		IsError:      workflowRunQueryBool(artifact.Metadata["is_error"]) || strings.EqualFold(artifact.Kind, "error"),
		Denied:       workflowRunQueryBool(artifact.Metadata["denied"]),
		Suspended:    workflowRunQueryBool(artifact.Metadata["suspended"]),
		Truncated:    artifact.Truncated,
		Externalized: strings.TrimSpace(artifact.ArtifactRef) != "" || strings.TrimSpace(artifact.Hash) != "",
		Metadata:     mergeRunArtifactMetadata(nil, artifact.Metadata),
	}
	if item.Source == "" {
		item.Source = "agent_artifact"
	}
	if item.Hash == "" {
		item.Hash = firstWorkflowRunQueryValue(artifact.Metadata["hash"], runArtifactHashFromRef(item.ArtifactRef))
	}
	if item.ArtifactRef == "" {
		item.ArtifactRef = firstWorkflowRunQueryValue(artifact.Metadata["artifact_ref"], artifact.Metadata["ref"])
	}
	return normalizeRunArtifactItem(item, query)
}

func agentRunDerivedArtifacts(run session.AgentRunSnapshot, query workflowRunQuery) []runArtifactItem {
	items := make([]runArtifactItem, 0)
	runID := strings.TrimSpace(run.ID)
	agentID := strings.TrimSpace(run.AgentID)
	mode := strings.TrimSpace(run.Mode)
	if strings.TrimSpace(run.Output) != "" {
		items = append(items, normalizeRunArtifactItem(runArtifactItem{
			ID:      "final-output",
			RunID:   runID,
			RunType: "agent",
			Source:  "agent_output",
			Kind:    "output",
			Title:   "Final output",
			Summary: summaryText(run.Output),
			Content: run.Output,
			Mime:    "text/markdown",
			AgentID: agentID,
			Mode:    mode,
		}, query))
	}
	if strings.TrimSpace(run.Error) != "" {
		items = append(items, normalizeRunArtifactItem(runArtifactItem{
			ID:      "error",
			RunID:   runID,
			RunType: "agent",
			Source:  "agent_error",
			Kind:    "error",
			Title:   "Run error",
			Summary: summaryText(run.Error),
			Content: run.Error,
			Mime:    "text/plain",
			AgentID: agentID,
			Mode:    mode,
			IsError: true,
		}, query))
	}
	if run.Result != nil {
		result := *run.Result
		agentID = firstWorkflowRunQueryValue(agentID, result.AgentID)
		mode = firstWorkflowRunQueryValue(mode, result.Mode)
		for i, toolResult := range result.ToolResults {
			title := firstWorkflowRunQueryValue(toolResult.ToolName, toolResult.CallID, "Tool result")
			items = append(items, normalizeRunArtifactItem(runArtifactItem{
				ID:         runArtifactID("tool-result", i+1, toolResult.CallID, toolResult.ToolName),
				RunID:      runID,
				RunType:    "agent",
				Source:     "tool_result",
				Kind:       "tool_result",
				Title:      title,
				Summary:    summaryText(toolResult.Content),
				Content:    toolResult.Content,
				Mime:       "text/plain",
				ToolName:   toolResult.ToolName,
				ToolCallID: toolResult.CallID,
				AgentID:    agentID,
				Mode:       mode,
				IsError:    toolResult.IsError,
				Denied:     toolResult.Denied,
				Suspended:  toolResult.Suspended,
				Metadata: map[string]string{
					"denied":    strconv.FormatBool(toolResult.Denied),
					"suspended": strconv.FormatBool(toolResult.Suspended),
				},
			}, query))
		}
		for i, section := range result.Structured {
			content := runArtifactJSON(section)
			summary := firstWorkflowRunQueryValue(section.Summary, strings.Join(section.Items, "\n"))
			items = append(items, normalizeRunArtifactItem(runArtifactItem{
				ID:      runArtifactID("structured", i+1, section.Title, section.Kind),
				RunID:   runID,
				RunType: "agent",
				Source:  "structured",
				Kind:    firstWorkflowRunQueryValue(section.Kind, "structured"),
				Title:   firstWorkflowRunQueryValue(section.Title, section.Kind, "Structured output"),
				Summary: summaryText(summary),
				Content: content,
				Mime:    "application/json",
				AgentID: agentID,
				Mode:    mode,
			}, query))
		}
		for i, finding := range result.Findings {
			items = append(items, normalizeRunArtifactItem(runArtifactItem{
				ID:      runArtifactID("finding", i+1, finding.Summary, finding.Severity),
				RunID:   runID,
				RunType: "agent",
				Source:  "finding",
				Kind:    "finding",
				Title:   firstWorkflowRunQueryValue(finding.Severity, "Finding"),
				Summary: summaryText(finding.Summary),
				Content: runArtifactJSON(finding),
				Mime:    "application/json",
				AgentID: agentID,
				Mode:    mode,
				Metadata: map[string]string{
					"severity": finding.Severity,
					"fixed":    strconv.FormatBool(finding.Fixed),
					"files":    strings.Join(finding.Files, ", "),
				},
			}, query))
		}
		for i, change := range result.Changes {
			items = append(items, normalizeRunArtifactItem(runArtifactItem{
				ID:      runArtifactID("change", i+1, change.Summary),
				RunID:   runID,
				RunType: "agent",
				Source:  "change",
				Kind:    "change",
				Title:   "Change",
				Summary: summaryText(change.Summary),
				Content: runArtifactJSON(change),
				Mime:    "application/json",
				AgentID: agentID,
				Mode:    mode,
				Metadata: map[string]string{
					"files": strings.Join(change.Files, ", "),
				},
			}, query))
		}
		for i, verification := range result.Verification {
			items = append(items, normalizeRunArtifactItem(runArtifactItem{
				ID:      runArtifactID("verification", i+1, verification.Kind, verification.Status),
				RunID:   runID,
				RunType: "agent",
				Source:  "verification",
				Kind:    "verification",
				Title:   firstWorkflowRunQueryValue(verification.Kind, "Verification"),
				Summary: summaryText(firstWorkflowRunQueryValue(verification.Detail, verification.Status)),
				Content: runArtifactJSON(verification),
				Mime:    "application/json",
				AgentID: agentID,
				Mode:    mode,
				IsError: strings.EqualFold(verification.Status, "failed") || strings.EqualFold(verification.Status, "fail"),
				Metadata: map[string]string{
					"status": verification.Status,
				},
			}, query))
		}
		for i, audit := range result.AuditTrail {
			items = append(items, normalizeRunArtifactItem(runArtifactItem{
				ID:       runArtifactID("audit", i+1, audit.Type, audit.ToolName, audit.Outcome),
				RunID:    runID,
				RunType:  "agent",
				Source:   "audit",
				Kind:     "audit",
				Title:    firstWorkflowRunQueryValue(audit.Type, "Audit entry"),
				Summary:  summaryText(firstWorkflowRunQueryValue(audit.Detail, audit.Outcome)),
				Content:  runArtifactJSON(audit),
				Mime:     "application/json",
				ToolName: audit.ToolName,
				AgentID:  firstWorkflowRunQueryValue(audit.AgentID, agentID),
				Mode:     mode,
				IsError:  strings.Contains(strings.ToLower(audit.Outcome), "error") || strings.Contains(strings.ToLower(audit.Outcome), "fail"),
				Metadata: map[string]string{
					"outcome":     audit.Outcome,
					"skill_name":  audit.SkillName,
					"duration_ms": strconv.FormatInt(audit.DurationMs, 10),
				},
			}, query))
		}
	}
	items = append(items, agentRunEventArtifactRefs(run, query)...)
	return dedupeRunArtifacts(items)
}

func agentRunEventArtifactRefs(run session.AgentRunSnapshot, query workflowRunQuery) []runArtifactItem {
	items := make([]runArtifactItem, 0)
	seen := map[string]struct{}{}
	for _, event := range run.Events {
		if event.PromptBudget == nil {
			continue
		}
		for i, ref := range event.PromptBudget.ArtifactRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			key := strings.ToLower(ref)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, normalizeRunArtifactItem(runArtifactItem{
				ID:           runArtifactID("artifact-ref", i+1, ref),
				RunID:        strings.TrimSpace(run.ID),
				RunType:      "agent",
				Source:       "prompt_budget_ref",
				Kind:         "artifact_ref",
				Title:        "Referenced artifact",
				Summary:      "Externalized content referenced by this run context.",
				Mime:         "text/plain",
				Ref:          ref,
				ArtifactRef:  ref,
				Hash:         runArtifactHashFromRef(ref),
				AgentID:      firstWorkflowRunQueryValue(event.AgentID, run.AgentID),
				Mode:         firstWorkflowRunQueryValue(event.Mode, run.Mode),
				CreatedAt:    event.At,
				Externalized: true,
			}, query))
		}
	}
	return items
}

func workflowRunArtifactsAsRunItems(run session.WorkflowRunSnapshot, query workflowRunQuery) []runArtifactItem {
	artifacts := run.Artifacts
	if query.ItemKind != "" && query.ItemKind != "artifact" && query.ItemKind != "artifacts" {
		return nil
	}
	items := make([]runArtifactItem, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !workflowRunArtifactMatchesQuery(artifact, query) {
			continue
		}
		item := workflowRunArtifactAsRunItem(run, artifact, query)
		if !runArtifactMatchesQuery(item, query) {
			continue
		}
		items = append(items, item)
		if query.Limit > 0 && len(items) >= query.Limit {
			break
		}
	}
	return items
}

func workflowRunArtifactAsRunItem(run session.WorkflowRunSnapshot, artifact session.WorkflowRunArtifact, query workflowRunQuery) runArtifactItem {
	item := runArtifactItem{
		ID:           strings.TrimSpace(artifact.ID),
		RunID:        strings.TrimSpace(run.ID),
		RunType:      "workflow",
		Source:       "workflow_artifact",
		Stage:        strings.TrimSpace(artifact.Stage),
		Kind:         strings.TrimSpace(artifact.Kind),
		Title:        strings.TrimSpace(artifact.Title),
		Summary:      summaryText(artifact.Summary),
		Content:      artifact.Content,
		Mime:         firstWorkflowRunQueryValue(artifact.Mime, artifact.Metadata["mime"], "text/plain"),
		Ref:          strings.TrimSpace(artifact.ArtifactRef),
		ArtifactRef:  strings.TrimSpace(artifact.ArtifactRef),
		Hash:         strings.TrimSpace(artifact.Hash),
		Size:         artifact.Size,
		ContentBytes: artifact.ContentBytes,
		StoredBytes:  artifact.StoredBytes,
		ToolName:     strings.TrimSpace(artifact.ToolName),
		ToolCallID:   strings.TrimSpace(artifact.ToolCallID),
		IsError:      artifact.IsError,
		Externalized: artifact.Externalized,
		Metadata:     mergeRunArtifactMetadata(nil, artifact.Metadata),
	}
	if ref := firstWorkflowRunQueryValue(item.ArtifactRef, artifact.Metadata["artifact_ref"], artifact.Metadata["ref"], artifact.Metadata["refs"]); strings.HasPrefix(ref, "sha256:") || strings.HasPrefix(ref, "goflow://") {
		item.Ref = firstWorkflowRunQueryValue(item.Ref, ref)
		item.ArtifactRef = firstWorkflowRunQueryValue(item.ArtifactRef, ref)
		item.Hash = firstWorkflowRunQueryValue(item.Hash, artifact.Metadata["hash"], runArtifactHashFromRef(ref))
		item.Externalized = true
	}
	if item.Hash == "" {
		item.Hash = firstWorkflowRunQueryValue(artifact.Metadata["hash"], runArtifactHashFromRef(item.ArtifactRef))
	}
	return normalizeRunArtifactItem(item, query)
}

func normalizeRunArtifactItem(item runArtifactItem, query workflowRunQuery) runArtifactItem {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		item.ID = runArtifactID("artifact", 1, item.Title, item.Kind, item.Ref, item.Hash)
	}
	item.Kind = firstWorkflowRunQueryValue(item.Kind, "artifact")
	item.Title = firstWorkflowRunQueryValue(item.Title, item.Kind, item.ID)
	item.Mime = firstWorkflowRunQueryValue(item.Mime, runArtifactMimeForKind(item.Kind))
	if strings.TrimSpace(item.Summary) == "" {
		item.Summary = summaryText(item.Content)
	}
	if item.Size == 0 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(item.Metadata["size"])); err == nil {
			item.Size = parsed
		}
	}
	if item.ContentBytes == 0 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(item.Metadata["content_bytes"])); err == nil {
			item.ContentBytes = parsed
		}
	}
	item.Size = firstPositiveInt(item.Size, item.ContentBytes, len([]byte(item.Content)), len([]byte(item.Summary)))
	item.ContentBytes = firstPositiveInt(item.ContentBytes, len([]byte(item.Content)), item.Size)
	if item.StoredBytes == 0 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(item.Metadata["stored_bytes"])); err == nil {
			item.StoredBytes = parsed
		}
	}
	item.Externalized = item.Externalized || strings.TrimSpace(item.Ref) != "" || strings.TrimSpace(item.ArtifactRef) != "" || strings.TrimSpace(item.Hash) != ""
	item.Metadata = cleanRunArtifactMetadata(item.Metadata)
	if !query.IncludeContent {
		item.Content = ""
	}
	return item
}

func runArtifactMatchesQuery(item runArtifactItem, query workflowRunQuery) bool {
	if query.Stage != "" && !workflowRunStageEqual(item.Stage, query.Stage) {
		return false
	}
	if query.ArtifactKind != "" && normalizeWorkflowRunQueryToken(item.Kind) != query.ArtifactKind {
		return false
	}
	if query.ToolName != "" && !strings.EqualFold(strings.TrimSpace(item.ToolName), query.ToolName) {
		return false
	}
	if query.AgentID != "" && !strings.EqualFold(strings.TrimSpace(item.AgentID), query.AgentID) {
		return false
	}
	if query.ErrorsOnly && !item.IsError {
		return false
	}
	if query.NeedsActionOnly && !item.Suspended && !item.Denied {
		return false
	}
	if query.SuspendedOnly && !item.Suspended {
		return false
	}
	if query.Query != "" && !workflowRunSearchMatch(query.Query, item.ID, item.Ref, item.ArtifactRef, item.Hash, item.Source, item.Stage, item.Kind, item.Title, item.Summary, item.Content, item.ToolName, item.ToolCallID, item.AgentID, item.Mode) {
		return false
	}
	return true
}

func dedupeRunArtifacts(items []runArtifactItem) []runArtifactItem {
	seen := map[string]struct{}{}
	out := make([]runArtifactItem, 0, len(items))
	for _, item := range items {
		key := strings.ToLower(strings.Join([]string{
			item.ID,
			item.Ref,
			item.ArtifactRef,
			item.Hash,
			item.Source,
			item.ToolCallID,
			item.Kind,
			item.Title,
		}, "|"))
		if key == "" {
			key = fmt.Sprintf("%s:%d", item.Kind, len(out)+1)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func runArtifactID(prefix string, index int, values ...string) string {
	parts := []string{normalizeWorkflowRunExportName(prefix)}
	for _, value := range values {
		value = normalizeWorkflowRunExportName(value)
		if value != "" {
			parts = append(parts, value)
			break
		}
	}
	if index > 0 {
		parts = append(parts, strconv.Itoa(index))
	}
	id := strings.Join(parts, "-")
	if id == "" || id == "-" {
		return fmt.Sprintf("artifact-%d", index)
	}
	return id
}

func runArtifactJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func runArtifactMimeForKind(kind string) string {
	switch normalizeWorkflowRunQueryToken(kind) {
	case "structured", "finding", "change", "verification", "audit":
		return "application/json"
	case "output", "report", "summary":
		return "text/markdown"
	default:
		return "text/plain"
	}
}

func mergeRunArtifactMetadata(base map[string]string, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	for key, value := range extra {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	return cleanRunArtifactMetadata(out)
}

func cleanRunArtifactMetadata(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func runArtifactHashFromRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "sha256:") {
		return strings.TrimPrefix(ref, "sha256:")
	}
	return ""
}

func agentRunArtifactCount(run session.AgentRunSnapshot) int {
	return len(agentRunArtifactsForQuery(run, workflowRunQuery{}))
}
