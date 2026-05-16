package session

import "strings"

const maxRunEventContentInlineBytes = maxWorkflowRunText

func (s *State) normalizeAgentRunEventLocked(runID string, event AgentRunEventSnapshot) AgentRunEventSnapshot {
	event.Type = strings.TrimSpace(event.Type)
	event.ToolName = strings.TrimSpace(event.ToolName)
	event.ToolCallID = strings.TrimSpace(event.ToolCallID)
	event.AgentID = strings.TrimSpace(event.AgentID)
	event.Mode = strings.TrimSpace(event.Mode)
	event.Content, event.ContentArtifactRef, event.ContentHash, event.ContentBytes, event.ContentStoredBytes, event.ContentExternalized = normalizeRunEventContentFields(
		s.artifactStore,
		event.Content,
		event.ContentArtifactRef,
		event.ContentHash,
		event.ContentBytes,
		event.ContentStoredBytes,
		event.ContentExternalized,
		"agent_run_event_content",
		"Agent run event content",
		runEventContentSummary(event.Content),
		map[string]string{
			"source":       "agent_run_event",
			"agent_run_id": strings.TrimSpace(runID),
			"event_type":   event.Type,
			"tool_name":    event.ToolName,
			"tool_call_id": event.ToolCallID,
			"agent_id":     event.AgentID,
			"mode":         event.Mode,
		},
	)
	return event
}

func (s *State) normalizeWorkflowRunEventLocked(runID, workflow string, event WorkflowRunEventSnapshot) WorkflowRunEventSnapshot {
	event.Stage = strings.TrimSpace(event.Stage)
	event.Type = strings.TrimSpace(event.Type)
	event.ToolName = strings.TrimSpace(event.ToolName)
	event.ToolCallID = strings.TrimSpace(event.ToolCallID)
	event.AgentID = strings.TrimSpace(event.AgentID)
	event.Mode = strings.TrimSpace(event.Mode)
	event.WorkflowName = firstPendingArgumentValue(strings.TrimSpace(event.WorkflowName), strings.TrimSpace(workflow))
	event.Content, event.ContentArtifactRef, event.ContentHash, event.ContentBytes, event.ContentStoredBytes, event.ContentExternalized = normalizeRunEventContentFields(
		s.artifactStore,
		event.Content,
		event.ContentArtifactRef,
		event.ContentHash,
		event.ContentBytes,
		event.ContentStoredBytes,
		event.ContentExternalized,
		"workflow_run_event_content",
		"Workflow run event content",
		runEventContentSummary(event.Content),
		map[string]string{
			"source":          "workflow_run_event",
			"workflow_run_id": strings.TrimSpace(runID),
			"workflow_name":   event.WorkflowName,
			"event_type":      event.Type,
			"stage":           event.Stage,
			"tool_name":       event.ToolName,
			"tool_call_id":    event.ToolCallID,
			"agent_id":        event.AgentID,
			"mode":            event.Mode,
		},
	)
	return event
}

// HydrateAgentRunEvents restores event content stored by ref.
func (s *State) HydrateAgentRunEvents(events []AgentRunEventSnapshot) []AgentRunEventSnapshot {
	if len(events) == 0 {
		return nil
	}
	out := append([]AgentRunEventSnapshot(nil), events...)
	for i := range out {
		out[i].Content, out[i].ContentArtifactRef, out[i].ContentHash, out[i].ContentBytes, out[i].ContentStoredBytes, out[i].ContentExternalized = hydrateRunEventContentFields(
			s.ArtifactObjectStore(),
			out[i].Content,
			out[i].ContentArtifactRef,
			out[i].ContentHash,
			out[i].ContentBytes,
			out[i].ContentStoredBytes,
			out[i].ContentExternalized,
		)
	}
	return out
}

// HydrateWorkflowRunEvents restores workflow event content stored by ref.
func (s *State) HydrateWorkflowRunEvents(events []WorkflowRunEventSnapshot) []WorkflowRunEventSnapshot {
	if len(events) == 0 {
		return nil
	}
	out := append([]WorkflowRunEventSnapshot(nil), events...)
	for i := range out {
		out[i].Content, out[i].ContentArtifactRef, out[i].ContentHash, out[i].ContentBytes, out[i].ContentStoredBytes, out[i].ContentExternalized = hydrateRunEventContentFields(
			s.ArtifactObjectStore(),
			out[i].Content,
			out[i].ContentArtifactRef,
			out[i].ContentHash,
			out[i].ContentBytes,
			out[i].ContentStoredBytes,
			out[i].ContentExternalized,
		)
	}
	return out
}

// HydrateAgentRun restores ordinary agent-run fields that may be stored by ref.
func (s *State) HydrateAgentRun(run AgentRunSnapshot) AgentRunSnapshot {
	if s == nil {
		return run
	}
	run.Events = s.HydrateAgentRunEvents(run.Events)
	if len(run.PendingApprovals) > 0 {
		hydrated := make([]PendingApprovalSnapshot, len(run.PendingApprovals))
		for i, approval := range run.PendingApprovals {
			hydrated[i] = s.HydratePendingApproval(approval)
		}
		run.PendingApprovals = hydrated
	}
	if run.ResumeContext != nil {
		context := s.HydrateAgentRunResumeContext(*run.ResumeContext)
		run.ResumeContext = &context
	}
	return run
}

// HydrateSnapshot restores explicit full-session content stored by ref.
func (s *State) HydrateSnapshot(snapshot Snapshot) Snapshot {
	if s == nil {
		return snapshot
	}
	if len(snapshot.PendingApprovals) > 0 {
		hydrated := make([]PendingApprovalSnapshot, len(snapshot.PendingApprovals))
		for i, approval := range snapshot.PendingApprovals {
			hydrated[i] = s.HydratePendingApproval(approval)
		}
		snapshot.PendingApprovals = hydrated
	}
	snapshot.Workflow = s.HydrateWorkflowPendingArguments(snapshot.Workflow)
	if len(snapshot.AgentRuns) > 0 {
		hydrated := make([]AgentRunSnapshot, len(snapshot.AgentRuns))
		for i, run := range snapshot.AgentRuns {
			hydrated[i] = s.HydrateAgentRun(run)
		}
		snapshot.AgentRuns = hydrated
	}
	if len(snapshot.WorkflowRuns) > 0 {
		hydrated := make([]WorkflowRunSnapshot, len(snapshot.WorkflowRuns))
		for i, run := range snapshot.WorkflowRuns {
			hydrated[i] = s.HydrateWorkflowRun(run)
		}
		snapshot.WorkflowRuns = hydrated
	}
	if len(snapshot.Messages) > 0 {
		hydrated := make([]CollaborationMessageSnapshot, len(snapshot.Messages))
		for i, message := range snapshot.Messages {
			hydrated[i] = s.hydrateCollaborationMessageLocked(message, true)
		}
		snapshot.Messages = hydrated
	}
	if len(snapshot.Blackboard) > 0 {
		hydrated := make([]BlackboardEntrySnapshot, len(snapshot.Blackboard))
		for i, entry := range snapshot.Blackboard {
			hydrated[i] = s.hydrateBlackboardEntryLocked(entry, true)
		}
		snapshot.Blackboard = hydrated
	}
	return snapshot
}

func normalizeRunEventContentFields(store *ArtifactObjectStore, content, ref, hash string, bytes, stored int, externalized bool, kind, title, summary string, metadata map[string]string) (string, string, string, int, int, bool) {
	ref, hash, externalized = normalizePendingArgumentReferenceMetadata(ref, hash, externalized)
	if strings.TrimSpace(content) == "" {
		return content, ref, hash, bytes, stored, externalized
	}
	bytes = len([]byte(content))
	if stored == 0 {
		stored = bytes
	}
	if store == nil || bytes <= maxRunEventContentInlineBytes {
		return content, ref, hash, bytes, stored, externalized
	}
	object, _, err := store.Put(ArtifactObject{
		Mime:     "text/plain",
		Summary:  summary,
		Content:  content,
		Kind:     kind,
		Title:    title,
		Metadata: copyStringMapForArtifact(metadata),
	})
	if err != nil {
		return content, ref, hash, bytes, stored, externalized
	}
	return firstPendingArgumentValue(summary, object.Summary), object.Ref, object.Hash, object.Size, int(object.StoredBytes), true
}

func hydrateRunEventContentFields(store *ArtifactObjectStore, content, ref, hash string, bytes, stored int, externalized bool) (string, string, string, int, int, bool) {
	ref, hash, externalized = normalizePendingArgumentReferenceMetadata(ref, hash, externalized)
	if !externalized && strings.TrimSpace(ref) == "" && strings.TrimSpace(hash) == "" {
		if bytes == 0 && strings.TrimSpace(content) != "" {
			bytes = len([]byte(content))
		}
		if stored == 0 {
			stored = bytes
		}
		return content, ref, hash, bytes, stored, externalized
	}
	if store == nil {
		if bytes == 0 && strings.TrimSpace(content) != "" {
			bytes = len([]byte(content))
		}
		if stored == 0 {
			stored = bytes
		}
		return content, ref, hash, bytes, stored, externalized
	}
	lookup := firstArtifactObjectLookup(ref, hash)
	if lookup == "" {
		return content, ref, hash, bytes, stored, externalized
	}
	object, ok, err := store.Get(lookup)
	if err != nil || !ok {
		return content, ref, hash, bytes, stored, externalized
	}
	return object.Content, firstPendingArgumentValue(ref, object.Ref), firstPendingArgumentValue(hash, object.Hash), firstPositiveInt(bytes, object.Size, len([]byte(object.Content))), firstPositiveInt(stored, int(object.StoredBytes)), true
}

func runEventContentSummary(content string) string {
	if len([]byte(content)) <= maxSessionArtifactSummaryBytes {
		return content
	}
	return trimSessionArtifactBytes(content, maxSessionArtifactSummaryBytes)
}
