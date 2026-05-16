package session

import "strings"

const maxPendingArgumentsInlineBytes = maxWorkflowRunText

func (s *State) normalizePendingApprovalsLocked(approvals []PendingApprovalSnapshot) []PendingApprovalSnapshot {
	if len(approvals) == 0 {
		return nil
	}
	out := make([]PendingApprovalSnapshot, len(approvals))
	for i, approval := range approvals {
		out[i] = s.normalizePendingApprovalLocked(approval)
	}
	return out
}

func (s *State) normalizePendingApprovalLocked(approval PendingApprovalSnapshot) PendingApprovalSnapshot {
	approval.CallID = strings.TrimSpace(approval.CallID)
	approval.ToolName = strings.TrimSpace(approval.ToolName)
	approval.AgentID = strings.TrimSpace(approval.AgentID)
	approval.WorkflowName = strings.TrimSpace(approval.WorkflowName)
	approval.Stage = strings.TrimSpace(approval.Stage)
	approval.Request = trimAgentRunText(approval.Request)
	approval.CompletedSummary = trimAgentRunText(approval.CompletedSummary)
	approval.AgentRunID = strings.TrimSpace(approval.AgentRunID)
	approval.ArgumentsSummary, approval.Arguments, approval.ArgumentsArtifactRef, approval.ArgumentsHash, approval.ArgumentsBytes, approval.ArgumentsStoredBytes, approval.ArgumentsExternalized = normalizePendingArgumentFields(
		s.artifactStore,
		approval.ArgumentsSummary,
		approval.Arguments,
		approval.ArgumentsArtifactRef,
		approval.ArgumentsHash,
		approval.ArgumentsBytes,
		approval.ArgumentsStoredBytes,
		approval.ArgumentsExternalized,
		approval.ToolName,
		map[string]string{
			"source":        "pending_approval",
			"call_id":       approval.CallID,
			"tool_name":     approval.ToolName,
			"agent_id":      approval.AgentID,
			"workflow_name": approval.WorkflowName,
			"stage":         approval.Stage,
			"agent_run_id":  approval.AgentRunID,
		},
	)
	return approval
}

func (s *State) normalizeWorkflowSnapshotLocked(snapshot WorkflowSnapshot) WorkflowSnapshot {
	if strings.TrimSpace(snapshot.PendingCallID) == "" {
		return clearWorkflowSnapshotPendingArguments(snapshot)
	}
	snapshot.PendingArgumentsSummary, snapshot.PendingArguments, snapshot.PendingArgumentsArtifactRef, snapshot.PendingArgumentsHash, snapshot.PendingArgumentsBytes, snapshot.PendingArgumentsStoredBytes, snapshot.PendingArgumentsExternalized = normalizePendingArgumentFields(
		s.artifactStore,
		snapshot.PendingArgumentsSummary,
		snapshot.PendingArguments,
		snapshot.PendingArgumentsArtifactRef,
		snapshot.PendingArgumentsHash,
		snapshot.PendingArgumentsBytes,
		snapshot.PendingArgumentsStoredBytes,
		snapshot.PendingArgumentsExternalized,
		snapshot.PendingToolName,
		map[string]string{
			"source":          "workflow",
			"workflow_run_id": strings.TrimSpace(snapshot.RunID),
			"workflow_name":   strings.TrimSpace(snapshot.Name),
			"stage":           strings.TrimSpace(snapshot.NextStage),
			"call_id":         strings.TrimSpace(snapshot.PendingCallID),
			"tool_name":       strings.TrimSpace(snapshot.PendingToolName),
			"agent_id":        strings.TrimSpace(snapshot.PendingAgentID),
		},
	)
	return snapshot
}

func normalizeWorkflowRunPendingArgumentsLocked(store *ArtifactObjectStore, run WorkflowRunSnapshot) WorkflowRunSnapshot {
	if strings.TrimSpace(run.PendingCallID) == "" {
		return clearWorkflowRunPendingArguments(run)
	}
	run.PendingArgsSummary, run.PendingArgs, run.PendingArgsArtifactRef, run.PendingArgsHash, run.PendingArgsBytes, run.PendingArgsStoredBytes, run.PendingArgsExternalized = normalizePendingArgumentFields(
		store,
		run.PendingArgsSummary,
		run.PendingArgs,
		run.PendingArgsArtifactRef,
		run.PendingArgsHash,
		run.PendingArgsBytes,
		run.PendingArgsStoredBytes,
		run.PendingArgsExternalized,
		run.PendingToolName,
		map[string]string{
			"source":          "workflow_run",
			"workflow_run_id": strings.TrimSpace(run.ID),
			"workflow_name":   strings.TrimSpace(run.Name),
			"stage":           strings.TrimSpace(run.NextStage),
			"call_id":         strings.TrimSpace(run.PendingCallID),
			"tool_name":       strings.TrimSpace(run.PendingToolName),
			"agent_id":        strings.TrimSpace(run.PendingAgentID),
		},
	)
	return run
}

func (s *State) HydratePendingApproval(approval PendingApprovalSnapshot) PendingApprovalSnapshot {
	approval.ArgumentsSummary, approval.Arguments, approval.ArgumentsArtifactRef, approval.ArgumentsHash, approval.ArgumentsBytes, approval.ArgumentsStoredBytes, approval.ArgumentsExternalized = hydratePendingArgumentFields(
		s.ArtifactObjectStore(),
		approval.ArgumentsSummary,
		approval.Arguments,
		approval.ArgumentsArtifactRef,
		approval.ArgumentsHash,
		approval.ArgumentsBytes,
		approval.ArgumentsStoredBytes,
		approval.ArgumentsExternalized,
	)
	return approval
}

func (s *State) HydrateWorkflowPendingArguments(snapshot WorkflowSnapshot) WorkflowSnapshot {
	if strings.TrimSpace(snapshot.PendingCallID) == "" {
		return clearWorkflowSnapshotPendingArguments(snapshot)
	}
	snapshot.PendingArgumentsSummary, snapshot.PendingArguments, snapshot.PendingArgumentsArtifactRef, snapshot.PendingArgumentsHash, snapshot.PendingArgumentsBytes, snapshot.PendingArgumentsStoredBytes, snapshot.PendingArgumentsExternalized = hydratePendingArgumentFields(
		s.ArtifactObjectStore(),
		snapshot.PendingArgumentsSummary,
		snapshot.PendingArguments,
		snapshot.PendingArgumentsArtifactRef,
		snapshot.PendingArgumentsHash,
		snapshot.PendingArgumentsBytes,
		snapshot.PendingArgumentsStoredBytes,
		snapshot.PendingArgumentsExternalized,
	)
	return snapshot
}

func (s *State) HydrateWorkflowRunPendingArguments(run WorkflowRunSnapshot) WorkflowRunSnapshot {
	if strings.TrimSpace(run.PendingCallID) == "" {
		return clearWorkflowRunPendingArguments(run)
	}
	run.PendingArgsSummary, run.PendingArgs, run.PendingArgsArtifactRef, run.PendingArgsHash, run.PendingArgsBytes, run.PendingArgsStoredBytes, run.PendingArgsExternalized = hydratePendingArgumentFields(
		s.ArtifactObjectStore(),
		run.PendingArgsSummary,
		run.PendingArgs,
		run.PendingArgsArtifactRef,
		run.PendingArgsHash,
		run.PendingArgsBytes,
		run.PendingArgsStoredBytes,
		run.PendingArgsExternalized,
	)
	return run
}

func normalizePendingArgumentFields(store *ArtifactObjectStore, summary, raw, ref, hash string, bytes, stored int, externalized bool, title string, metadata map[string]string) (string, string, string, string, int, int, bool) {
	summary = normalizePendingArgumentsSummary(summary, raw)
	ref, hash, externalized = normalizePendingArgumentReferenceMetadata(ref, hash, externalized)
	if strings.TrimSpace(raw) == "" {
		return summary, raw, ref, hash, bytes, stored, externalized
	}
	bytes = len([]byte(raw))
	if stored == 0 {
		stored = bytes
	}
	if store == nil || bytes <= maxPendingArgumentsInlineBytes {
		return summary, raw, ref, hash, bytes, stored, externalized
	}
	object, _, err := store.Put(ArtifactObject{
		Mime:     "application/json",
		Summary:  summary,
		Content:  raw,
		Kind:     "pending_tool_arguments",
		Title:    pendingArgumentsTitle(title),
		Metadata: copyStringMapForArtifact(metadata),
	})
	if err != nil {
		return summary, raw, ref, hash, bytes, stored, externalized
	}
	return firstPendingArgumentValue(summary, object.Summary), "", object.Ref, object.Hash, object.Size, int(object.StoredBytes), true
}

func hydratePendingArgumentFields(store *ArtifactObjectStore, summary, raw, ref, hash string, bytes, stored int, externalized bool) (string, string, string, string, int, int, bool) {
	summary = normalizePendingArgumentsSummary(summary, raw)
	ref, hash, externalized = normalizePendingArgumentReferenceMetadata(ref, hash, externalized)
	if strings.TrimSpace(raw) != "" {
		if bytes == 0 {
			bytes = len([]byte(raw))
		}
		if stored == 0 {
			stored = len([]byte(raw))
		}
		return summary, raw, ref, hash, bytes, stored, externalized
	}
	if store == nil {
		return summary, raw, ref, hash, bytes, stored, externalized
	}
	lookup := firstArtifactObjectLookup(ref, hash)
	if lookup == "" {
		return summary, raw, ref, hash, bytes, stored, externalized
	}
	object, ok, err := store.Get(lookup)
	if err != nil || !ok {
		return summary, raw, ref, hash, bytes, stored, externalized
	}
	return firstPendingArgumentValue(summary, object.Summary), object.Content, firstPendingArgumentValue(ref, object.Ref), firstPendingArgumentValue(hash, object.Hash), firstPositiveInt(bytes, object.Size, len([]byte(object.Content))), firstPositiveInt(stored, int(object.StoredBytes)), true
}

func normalizePendingArgumentsSummary(summary, raw string) string {
	summary = strings.TrimSpace(summary)
	if summary != "" {
		return trimWorkflowRunText(summary)
	}
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	return trimWorkflowRunText(strings.TrimSpace(raw))
}

func normalizePendingArgumentReferenceMetadata(ref, hash string, externalized bool) (string, string, bool) {
	ref = strings.TrimSpace(ref)
	hash = normalizeArtifactHash(firstPendingArgumentValue(hash, ref))
	if ref == "" && hash != "" {
		ref = "sha256:" + hash
	}
	return ref, hash, externalized || ref != "" || hash != ""
}

func clearWorkflowSnapshotPendingArguments(snapshot WorkflowSnapshot) WorkflowSnapshot {
	snapshot.PendingArguments = ""
	snapshot.PendingArgumentsSummary = ""
	snapshot.PendingArgumentsArtifactRef = ""
	snapshot.PendingArgumentsHash = ""
	snapshot.PendingArgumentsBytes = 0
	snapshot.PendingArgumentsStoredBytes = 0
	snapshot.PendingArgumentsExternalized = false
	return snapshot
}

func clearWorkflowRunPendingArguments(run WorkflowRunSnapshot) WorkflowRunSnapshot {
	run.PendingArgs = ""
	run.PendingArgsSummary = ""
	run.PendingArgsArtifactRef = ""
	run.PendingArgsHash = ""
	run.PendingArgsBytes = 0
	run.PendingArgsStoredBytes = 0
	run.PendingArgsExternalized = false
	return run
}

func pendingArgumentsTitle(toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return "Pending tool arguments"
	}
	return toolName + " pending arguments"
}

func firstPendingArgumentValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
