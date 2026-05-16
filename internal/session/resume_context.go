package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func (s *State) normalizeAgentRunResumeContextLocked(runID string, context AgentRunResumeContextSnapshot) AgentRunResumeContextSnapshot {
	context = copyAgentRunResumeContext(context)
	context.AgentID = strings.TrimSpace(context.AgentID)
	context.Mode = strings.TrimSpace(context.Mode)
	context.SystemPrompt = trimAgentRunText(context.SystemPrompt)
	context.Messages, context.MessagesArtifactRef, context.MessagesHash, context.MessagesCount, context.MessagesBytes, context.MessagesStoredBytes, context.MessagesExternalized = normalizeResumeContextPayload(
		s.artifactStore,
		context.Messages,
		context.MessagesArtifactRef,
		context.MessagesHash,
		context.MessagesCount,
		context.MessagesBytes,
		context.MessagesStoredBytes,
		context.MessagesExternalized,
		"agent_resume_messages",
		"Ordinary approval resume messages",
		fmt.Sprintf("%d resume messages", len(context.Messages)),
		map[string]string{
			"source":       "agent_resume_context",
			"segment":      "messages",
			"agent_run_id": strings.TrimSpace(runID),
			"agent_id":     context.AgentID,
			"mode":         context.Mode,
		},
	)
	context.SuspendedCalls, context.SuspendedCallsArtifactRef, context.SuspendedCallsHash, context.SuspendedCallCount, context.SuspendedCallsBytes, context.SuspendedCallsStoredBytes, context.SuspendedCallsExternalized = normalizeResumeContextPayload(
		s.artifactStore,
		context.SuspendedCalls,
		context.SuspendedCallsArtifactRef,
		context.SuspendedCallsHash,
		context.SuspendedCallCount,
		context.SuspendedCallsBytes,
		context.SuspendedCallsStoredBytes,
		context.SuspendedCallsExternalized,
		"agent_resume_suspended_calls",
		"Ordinary approval suspended calls",
		fmt.Sprintf("%d suspended calls", len(context.SuspendedCalls)),
		map[string]string{
			"source":       "agent_resume_context",
			"segment":      "suspended_calls",
			"agent_run_id": strings.TrimSpace(runID),
			"agent_id":     context.AgentID,
			"mode":         context.Mode,
		},
	)
	context.CollectedResults, context.CollectedResultsArtifactRef, context.CollectedResultsHash, context.CollectedResultCount, context.CollectedResultsBytes, context.CollectedResultsStoredBytes, context.CollectedResultsExternalized = normalizeResumeContextPayload(
		s.artifactStore,
		context.CollectedResults,
		context.CollectedResultsArtifactRef,
		context.CollectedResultsHash,
		context.CollectedResultCount,
		context.CollectedResultsBytes,
		context.CollectedResultsStoredBytes,
		context.CollectedResultsExternalized,
		"agent_resume_collected_results",
		"Ordinary approval collected results",
		fmt.Sprintf("%d collected results", len(context.CollectedResults)),
		map[string]string{
			"source":       "agent_resume_context",
			"segment":      "collected_results",
			"agent_run_id": strings.TrimSpace(runID),
			"agent_id":     context.AgentID,
			"mode":         context.Mode,
		},
	)
	return context
}

func (s *State) HydrateAgentRunResumeContext(context AgentRunResumeContextSnapshot) AgentRunResumeContextSnapshot {
	context.Messages, context.MessagesArtifactRef, context.MessagesHash, context.MessagesCount, context.MessagesBytes, context.MessagesStoredBytes, context.MessagesExternalized = hydrateResumeContextPayload[schema.Message](
		s.ArtifactObjectStore(),
		context.Messages,
		context.MessagesArtifactRef,
		context.MessagesHash,
		context.MessagesCount,
		context.MessagesBytes,
		context.MessagesStoredBytes,
		context.MessagesExternalized,
	)
	context.SuspendedCalls, context.SuspendedCallsArtifactRef, context.SuspendedCallsHash, context.SuspendedCallCount, context.SuspendedCallsBytes, context.SuspendedCallsStoredBytes, context.SuspendedCallsExternalized = hydrateResumeContextPayload[schema.ToolCall](
		s.ArtifactObjectStore(),
		context.SuspendedCalls,
		context.SuspendedCallsArtifactRef,
		context.SuspendedCallsHash,
		context.SuspendedCallCount,
		context.SuspendedCallsBytes,
		context.SuspendedCallsStoredBytes,
		context.SuspendedCallsExternalized,
	)
	context.CollectedResults, context.CollectedResultsArtifactRef, context.CollectedResultsHash, context.CollectedResultCount, context.CollectedResultsBytes, context.CollectedResultsStoredBytes, context.CollectedResultsExternalized = hydrateResumeContextPayload[schema.ToolResult](
		s.ArtifactObjectStore(),
		context.CollectedResults,
		context.CollectedResultsArtifactRef,
		context.CollectedResultsHash,
		context.CollectedResultCount,
		context.CollectedResultsBytes,
		context.CollectedResultsStoredBytes,
		context.CollectedResultsExternalized,
	)
	return context
}

func normalizeResumeContextPayload[T any](store *ArtifactObjectStore, values []T, ref, hash string, count, bytes, stored int, externalized bool, kind, title, summary string, metadata map[string]string) ([]T, string, string, int, int, int, bool) {
	ref, hash, externalized = normalizePendingArgumentReferenceMetadata(ref, hash, externalized)
	count = len(values)
	if count == 0 {
		return nil, "", "", 0, 0, 0, false
	}
	data, err := json.Marshal(values)
	if err != nil {
		return values, ref, hash, count, bytes, stored, externalized
	}
	bytes = len(data)
	if stored == 0 {
		stored = bytes
	}
	if store == nil || bytes <= maxPendingArgumentsInlineBytes {
		return values, ref, hash, count, bytes, stored, externalized
	}
	object, _, err := store.Put(ArtifactObject{
		Mime:     "application/json",
		Summary:  strings.TrimSpace(summary),
		Content:  string(data),
		Kind:     kind,
		Title:    title,
		Metadata: copyStringMapForArtifact(metadata),
	})
	if err != nil {
		return values, ref, hash, count, bytes, stored, externalized
	}
	return nil, object.Ref, object.Hash, count, object.Size, int(object.StoredBytes), true
}

func hydrateResumeContextPayload[T any](store *ArtifactObjectStore, values []T, ref, hash string, count, bytes, stored int, externalized bool) ([]T, string, string, int, int, int, bool) {
	ref, hash, externalized = normalizePendingArgumentReferenceMetadata(ref, hash, externalized)
	if len(values) > 0 {
		if count == 0 {
			count = len(values)
		}
		if bytes == 0 {
			if data, err := json.Marshal(values); err == nil {
				bytes = len(data)
			}
		}
		if stored == 0 {
			stored = bytes
		}
		return values, ref, hash, count, bytes, stored, externalized
	}
	if count == 0 {
		return nil, ref, hash, 0, bytes, stored, externalized
	}
	if store == nil {
		return nil, ref, hash, count, bytes, stored, externalized
	}
	lookup := firstArtifactObjectLookup(ref, hash)
	if lookup == "" {
		return nil, ref, hash, count, bytes, stored, externalized
	}
	object, ok, err := store.Get(lookup)
	if err != nil || !ok || strings.TrimSpace(object.Content) == "" {
		return nil, ref, hash, count, bytes, stored, externalized
	}
	var hydrated []T
	if err := json.Unmarshal([]byte(object.Content), &hydrated); err != nil {
		return nil, ref, hash, count, bytes, stored, externalized
	}
	return hydrated, firstPendingArgumentValue(ref, object.Ref), firstPendingArgumentValue(hash, object.Hash), firstPositiveInt(count, len(hydrated)), firstPositiveInt(bytes, object.Size), firstPositiveInt(stored, int(object.StoredBytes)), true
}
