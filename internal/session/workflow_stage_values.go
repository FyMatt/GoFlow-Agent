package session

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxWorkflowStageValuesInlineBytes = maxWorkflowRunText

func (s *State) normalizeWorkflowRunStagePayloadsLocked(runID, workflow string, stages []WorkflowRunStageSnapshot) []WorkflowRunStageSnapshot {
	return normalizeWorkflowRunStagePayloads(s.artifactStore, runID, workflow, stages)
}

func normalizeWorkflowRunStagePayloads(store *ArtifactObjectStore, runID, workflow string, stages []WorkflowRunStageSnapshot) []WorkflowRunStageSnapshot {
	if len(stages) == 0 {
		return nil
	}
	out := copyWorkflowRunStageSnapshots(stages)
	for i := range out {
		stage := &out[i]
		stageName := strings.TrimSpace(stage.Stage)
		agentID := strings.TrimSpace(stage.AgentID)
		nodeType := strings.TrimSpace(stage.NodeType)
		stage.InputValues, stage.InputValuesArtifactRef, stage.InputValuesHash, stage.InputValueCount, stage.InputValuesBytes, stage.InputValuesStoredBytes, stage.InputValuesExternalized = normalizeWorkflowRunStageValuePayload(
			store,
			stage.InputValues,
			stage.InputValuesArtifactRef,
			stage.InputValuesHash,
			stage.InputValueCount,
			stage.InputValuesBytes,
			stage.InputValuesStoredBytes,
			stage.InputValuesExternalized,
			"workflow_stage_input_values",
			"Workflow stage input values",
			workflowRunStageValuePayloadSummary("input", stageName, len(stage.InputValues)),
			map[string]string{
				"source":          "workflow_stage_snapshot",
				"segment":         "input_values",
				"workflow_run_id": strings.TrimSpace(runID),
				"workflow_name":   strings.TrimSpace(workflow),
				"stage":           stageName,
				"agent_id":        agentID,
				"node_type":       nodeType,
			},
		)
		stage.OutputValues, stage.OutputValuesArtifactRef, stage.OutputValuesHash, stage.OutputValueCount, stage.OutputValuesBytes, stage.OutputValuesStoredBytes, stage.OutputValuesExternalized = normalizeWorkflowRunStageValuePayload(
			store,
			stage.OutputValues,
			stage.OutputValuesArtifactRef,
			stage.OutputValuesHash,
			stage.OutputValueCount,
			stage.OutputValuesBytes,
			stage.OutputValuesStoredBytes,
			stage.OutputValuesExternalized,
			"workflow_stage_output_values",
			"Workflow stage output values",
			workflowRunStageValuePayloadSummary("output", stageName, len(stage.OutputValues)),
			map[string]string{
				"source":          "workflow_stage_snapshot",
				"segment":         "output_values",
				"workflow_run_id": strings.TrimSpace(runID),
				"workflow_name":   strings.TrimSpace(workflow),
				"stage":           stageName,
				"agent_id":        agentID,
				"node_type":       nodeType,
			},
		)
	}
	return out
}

// HydrateWorkflowRunStages restores full typed input/output values for stage
// snapshots that were externalized into the artifact object store.
func (s *State) HydrateWorkflowRunStages(stages []WorkflowRunStageSnapshot) []WorkflowRunStageSnapshot {
	if s == nil {
		return copyWorkflowRunStageSnapshots(stages)
	}
	return hydrateWorkflowRunStagePayloads(s.ArtifactObjectStore(), stages)
}

// HydrateWorkflowRun restores workflow-run fields that may be stored by ref.
func (s *State) HydrateWorkflowRun(run WorkflowRunSnapshot) WorkflowRunSnapshot {
	if s == nil {
		return run
	}
	run = s.HydrateWorkflowRunPendingArguments(run)
	run.CompletedStages = s.HydrateWorkflowRunStages(run.CompletedStages)
	run.Events = s.HydrateWorkflowRunEvents(run.Events)
	return run
}

func hydrateWorkflowRunStagePayloads(store *ArtifactObjectStore, stages []WorkflowRunStageSnapshot) []WorkflowRunStageSnapshot {
	if len(stages) == 0 {
		return nil
	}
	out := copyWorkflowRunStageSnapshots(stages)
	for i := range out {
		stage := &out[i]
		stage.InputValues, stage.InputValuesArtifactRef, stage.InputValuesHash, stage.InputValueCount, stage.InputValuesBytes, stage.InputValuesStoredBytes, stage.InputValuesExternalized = hydrateWorkflowRunStageValuePayload(
			store,
			stage.InputValues,
			stage.InputValuesArtifactRef,
			stage.InputValuesHash,
			stage.InputValueCount,
			stage.InputValuesBytes,
			stage.InputValuesStoredBytes,
			stage.InputValuesExternalized,
		)
		stage.OutputValues, stage.OutputValuesArtifactRef, stage.OutputValuesHash, stage.OutputValueCount, stage.OutputValuesBytes, stage.OutputValuesStoredBytes, stage.OutputValuesExternalized = hydrateWorkflowRunStageValuePayload(
			store,
			stage.OutputValues,
			stage.OutputValuesArtifactRef,
			stage.OutputValuesHash,
			stage.OutputValueCount,
			stage.OutputValuesBytes,
			stage.OutputValuesStoredBytes,
			stage.OutputValuesExternalized,
		)
	}
	return out
}

func normalizeWorkflowRunStageValuePayload(store *ArtifactObjectStore, values map[string]any, ref, hash string, count, bytes, stored int, externalized bool, kind, title, summary string, metadata map[string]string) (map[string]any, string, string, int, int, int, bool) {
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
	if store == nil || bytes <= maxWorkflowStageValuesInlineBytes {
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

func hydrateWorkflowRunStageValuePayload(store *ArtifactObjectStore, values map[string]any, ref, hash string, count, bytes, stored int, externalized bool) (map[string]any, string, string, int, int, int, bool) {
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
	var hydrated map[string]any
	if err := json.Unmarshal([]byte(object.Content), &hydrated); err != nil {
		return nil, ref, hash, count, bytes, stored, externalized
	}
	return hydrated, firstPendingArgumentValue(ref, object.Ref), firstPendingArgumentValue(hash, object.Hash), firstPositiveInt(count, len(hydrated)), firstPositiveInt(bytes, object.Size), firstPositiveInt(stored, int(object.StoredBytes)), true
}

func workflowRunStageValuePayloadSummary(kind, stage string, count int) string {
	kind = strings.TrimSpace(kind)
	stage = strings.TrimSpace(stage)
	switch {
	case stage != "" && kind != "":
		return fmt.Sprintf("%s stage %s values (%d keys)", stage, kind, count)
	case stage != "":
		return fmt.Sprintf("%s stage values (%d keys)", stage, count)
	case kind != "":
		return fmt.Sprintf("%s values (%d keys)", kind, count)
	default:
		return fmt.Sprintf("workflow stage values (%d keys)", count)
	}
}
