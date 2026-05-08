package api

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

type workflowRunEvidenceResponse struct {
	RunID     string                    `json:"run_id"`
	Workflow  string                    `json:"workflow,omitempty"`
	Status    string                    `json:"status,omitempty"`
	Quality   workflowRunQualitySummary `json:"quality"`
	Filters   workflowRunQuery          `json:"filters,omitempty"`
	Counts    workflowRunEvidenceCounts `json:"counts"`
	Stages    []workflowRunEvidenceNode `json:"stages,omitempty"`
	Artifacts []workflowRunEvidenceNode `json:"artifacts,omitempty"`
	Checks    []workflowRunEvidenceNode `json:"checks,omitempty"`
	Files     []workflowRunEvidenceNode `json:"files,omitempty"`
	Refs      []workflowRunEvidenceNode `json:"refs,omitempty"`
	Edges     []workflowRunEvidenceEdge `json:"edges,omitempty"`
	Issues    []workflowRunQualityIssue `json:"issues,omitempty"`
	Warnings  []string                  `json:"warnings,omitempty"`
}

type workflowRunEvidenceCounts struct {
	Stages              int `json:"stages"`
	Artifacts           int `json:"artifacts"`
	Checks              int `json:"checks"`
	Files               int `json:"files"`
	Refs                int `json:"refs"`
	Edges               int `json:"edges"`
	EvidenceArtifacts   int `json:"evidence_artifacts"`
	FailedArtifacts     int `json:"failed_artifacts,omitempty"`
	FailedChecks        int `json:"failed_checks,omitempty"`
	WarningChecks       int `json:"warning_checks,omitempty"`
	MissingProvenance   int `json:"missing_provenance,omitempty"`
	MissingEvidenceRefs int `json:"missing_evidence_refs,omitempty"`
}

type workflowRunEvidenceNode struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	Label            string            `json:"label,omitempty"`
	Stage            string            `json:"stage,omitempty"`
	Status           string            `json:"status,omitempty"`
	Kind             string            `json:"kind,omitempty"`
	AgentID          string            `json:"agent_id,omitempty"`
	NodeType         string            `json:"node_type,omitempty"`
	Skill            string            `json:"skill,omitempty"`
	Tool             string            `json:"tool,omitempty"`
	ToolName         string            `json:"tool_name,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	Summary          string            `json:"summary,omitempty"`
	EvidenceCategory string            `json:"evidence_category,omitempty"`
	ValidationStatus string            `json:"validation_status,omitempty"`
	RelatedFiles     []string          `json:"related_files,omitempty"`
	Refs             []string          `json:"refs,omitempty"`
	Expected         string            `json:"expected,omitempty"`
	Actual           string            `json:"actual,omitempty"`
	Detail           string            `json:"detail,omitempty"`
	IsError          bool              `json:"is_error,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	DetailPath       string            `json:"detail_path,omitempty"`
	ArtifactsPath    string            `json:"artifacts_path,omitempty"`
	ReplayPath       string            `json:"replay_path,omitempty"`
}

type workflowRunEvidenceEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

func workflowRunEvidenceView(run session.WorkflowRunSnapshot, query workflowRunQuery) workflowRunEvidenceResponse {
	quality := workflowRunQualitySummaryFor(run)
	response := workflowRunEvidenceResponse{
		RunID:    run.ID,
		Workflow: run.Name,
		Status:   run.Status,
		Quality:  quality,
		Filters:  query,
		Issues:   append([]workflowRunQualityIssue{}, quality.UnmetCriteria...),
		Warnings: append([]string{}, quality.Warnings...),
	}
	response.Issues = append(response.Issues, quality.FailedValidations...)

	fileNodes := map[string]workflowRunEvidenceNode{}
	refNodes := map[string]workflowRunEvidenceNode{}
	edgeSeen := map[string]struct{}{}
	artifactByStageKind := map[string]string{}

	for _, stage := range workflowRunEvidenceStagesForQuery(run.CompletedStages, query) {
		stageNode := workflowRunEvidenceStageNode(run.ID, stage)
		response.Stages = append(response.Stages, stageNode)
		if workflowRunStageMissingProvenance(stage) {
			response.Counts.MissingProvenance++
		}
		for index, acceptance := range stage.Acceptance {
			checkNode := workflowRunAcceptanceEvidenceNode(stage.Stage, index, acceptance)
			if !workflowRunEvidenceCheckMatches(checkNode, query) {
				continue
			}
			response.Checks = append(response.Checks, checkNode)
			workflowRunEvidenceAddEdge(&response.Edges, edgeSeen, stageNode.ID, checkNode.ID, "checks", "checks")
			if workflowRunQualityStatusFailed(checkNode.Status) {
				response.Counts.FailedChecks++
			} else if !workflowRunQualityStatusPassed(checkNode.Status) {
				response.Counts.WarningChecks++
			}
		}
		for index, verification := range stage.Result.Verification {
			checkNode := workflowRunVerificationEvidenceNode(stage.Stage, index, verification.Kind, verification.Status, verification.Detail)
			if !workflowRunEvidenceCheckMatches(checkNode, query) {
				continue
			}
			response.Checks = append(response.Checks, checkNode)
			workflowRunEvidenceAddEdge(&response.Edges, edgeSeen, stageNode.ID, checkNode.ID, "checks", "checks")
			if workflowRunQualityStatusFailed(checkNode.Status) {
				response.Counts.FailedChecks++
			} else if !workflowRunQualityStatusPassed(checkNode.Status) {
				response.Counts.WarningChecks++
			}
		}
	}

	for _, artifact := range workflowRunEvidenceArtifactsForQuery(run.Artifacts, query) {
		node := workflowRunArtifactEvidenceNode(artifact)
		response.Artifacts = append(response.Artifacts, node)
		artifactByStageKind[workflowRunEvidenceStageKindKey(node.Stage, node.Kind, node.Label)] = node.ID
		if workflowRunArtifactHasQualityEvidence(artifact) {
			response.Counts.EvidenceArtifacts++
		}
		if artifact.IsError || workflowRunQualityStatusFailed(node.ValidationStatus) {
			response.Counts.FailedArtifacts++
		}
		stageID := workflowRunEvidenceStageID(node.Stage)
		if strings.TrimSpace(node.Stage) != "" {
			workflowRunEvidenceAddEdge(&response.Edges, edgeSeen, stageID, node.ID, "produced", "produced")
		} else {
			response.Counts.MissingProvenance++
		}
		if len(node.RelatedFiles) == 0 && len(node.Refs) == 0 && workflowRunArtifactHasQualityEvidence(artifact) {
			response.Counts.MissingEvidenceRefs++
		}
		for _, file := range node.RelatedFiles {
			fileNode := workflowRunFileEvidenceNode(file)
			fileNodes[fileNode.ID] = fileNode
			workflowRunEvidenceAddEdge(&response.Edges, edgeSeen, node.ID, fileNode.ID, "related_file", "file")
		}
		for _, ref := range node.Refs {
			refNode := workflowRunRefEvidenceNode(ref)
			refNodes[refNode.ID] = refNode
			workflowRunEvidenceAddEdge(&response.Edges, edgeSeen, node.ID, refNode.ID, "references", "ref")
		}
	}

	for _, check := range response.Checks {
		if artifactID := artifactByStageKind[workflowRunEvidenceStageKindKey(check.Stage, check.Kind, check.Label)]; artifactID != "" {
			workflowRunEvidenceAddEdge(&response.Edges, edgeSeen, check.ID, artifactID, "evidenced_by", "evidence")
		}
	}

	response.Files = workflowRunEvidenceNodeMapValues(fileNodes)
	response.Refs = workflowRunEvidenceNodeMapValues(refNodes)
	response.Counts.Stages = len(response.Stages)
	response.Counts.Artifacts = len(response.Artifacts)
	response.Counts.Checks = len(response.Checks)
	response.Counts.Files = len(response.Files)
	response.Counts.Refs = len(response.Refs)
	response.Counts.Edges = len(response.Edges)
	return response
}

func workflowRunEvidenceStageNode(runID string, stage session.WorkflowRunStageSnapshot) workflowRunEvidenceNode {
	escapedRun := url.PathEscape(strings.TrimSpace(runID))
	escapedStage := url.PathEscape(strings.TrimSpace(stage.Stage))
	base := "/api/workflow-runs/" + escapedRun
	return workflowRunEvidenceNode{
		ID:            workflowRunEvidenceStageID(stage.Stage),
		Type:          "stage",
		Label:         firstWorkflowRunQueryValue(stage.Stage, "stage"),
		Stage:         stage.Stage,
		Status:        stage.Status,
		AgentID:       stage.AgentID,
		NodeType:      stage.NodeType,
		Skill:         stage.Skill,
		Tool:          stage.Tool,
		Summary:       stage.Summary,
		Metadata:      workflowRunEvidenceMetadata(stage.Metadata),
		DetailPath:    base + "/stages/" + escapedStage,
		ArtifactsPath: base + "/stages/" + escapedStage + "/artifacts",
		ReplayPath:    base + "/replay?stage=" + url.QueryEscape(strings.TrimSpace(stage.Stage)),
	}
}

func workflowRunArtifactEvidenceNode(artifact session.WorkflowRunArtifact) workflowRunEvidenceNode {
	metadata := workflowRunEvidenceMetadata(artifact.Metadata)
	return workflowRunEvidenceNode{
		ID:               workflowRunEvidenceArtifactID(artifact.ID),
		Type:             "artifact",
		Label:            firstWorkflowRunQueryValue(artifact.Title, artifact.Kind, artifact.ID),
		Stage:            artifact.Stage,
		Kind:             artifact.Kind,
		ToolName:         artifact.ToolName,
		ToolCallID:       artifact.ToolCallID,
		Summary:          artifact.Summary,
		EvidenceCategory: firstWorkflowRunQueryValue(metadata["evidence_category"], workflowRunEvidenceCategoryFallback(artifact)),
		ValidationStatus: firstWorkflowRunQueryValue(metadata["validation_status"], workflowRunArtifactValidationFallback(artifact)),
		RelatedFiles:     workflowRunEvidenceSplitList(firstWorkflowRunQueryValue(metadata["related_files"], metadata["files"])),
		Refs:             workflowRunEvidenceSplitList(firstWorkflowRunQueryValue(metadata["refs"], metadata["ref"])),
		IsError:          artifact.IsError,
		Metadata:         metadata,
	}
}

func workflowRunAcceptanceEvidenceNode(stage string, index int, acceptance session.WorkflowRunAcceptanceSnapshot) workflowRunEvidenceNode {
	label := firstWorkflowRunQueryValue(acceptance.Name, acceptance.Ref, "acceptance")
	return workflowRunEvidenceNode{
		ID:       workflowRunEvidenceCheckID(stage, "acceptance", label, index),
		Type:     "check",
		Label:    label,
		Stage:    stage,
		Kind:     "acceptance",
		Status:   normalizeWorkflowRunQualityStatus(acceptance.Status),
		Expected: acceptance.Expected,
		Actual:   acceptance.Actual,
		Detail:   firstWorkflowRunQueryValue(acceptance.Reason, acceptance.Description),
		Refs:     workflowRunEvidenceSplitList(acceptance.Ref),
	}
}

func workflowRunVerificationEvidenceNode(stage string, index int, kind, status, detail string) workflowRunEvidenceNode {
	label := firstWorkflowRunQueryValue(kind, "verification")
	return workflowRunEvidenceNode{
		ID:     workflowRunEvidenceCheckID(stage, "verification", label, index),
		Type:   "check",
		Label:  label,
		Stage:  stage,
		Kind:   "verification",
		Status: normalizeWorkflowRunQualityStatus(status),
		Detail: detail,
	}
}

func workflowRunFileEvidenceNode(file string) workflowRunEvidenceNode {
	file = strings.TrimSpace(file)
	return workflowRunEvidenceNode{
		ID:    "file:" + file,
		Type:  "file",
		Label: file,
		Kind:  "file",
	}
}

func workflowRunRefEvidenceNode(ref string) workflowRunEvidenceNode {
	ref = strings.TrimSpace(ref)
	return workflowRunEvidenceNode{
		ID:    "ref:" + ref,
		Type:  "ref",
		Label: ref,
		Kind:  "ref",
	}
}

func workflowRunEvidenceCheckMatches(node workflowRunEvidenceNode, query workflowRunQuery) bool {
	if query.Stage != "" && !workflowRunStageEqual(node.Stage, query.Stage) {
		return false
	}
	if query.Status != "" && normalizeWorkflowRunQueryToken(node.Status) != query.Status {
		return false
	}
	if query.Query != "" && !workflowRunSearchMatch(query.Query, node.ID, node.Label, node.Stage, node.Kind, node.Status, node.Detail, node.Expected, node.Actual) {
		return false
	}
	if query.ItemKind != "" && query.ItemKind != "check" && query.ItemKind != "evidence" {
		return false
	}
	return true
}

func workflowRunEvidenceStagesForQuery(stages []session.WorkflowRunStageSnapshot, query workflowRunQuery) []session.WorkflowRunStageSnapshot {
	stageQuery := query
	stageQuery.ItemKind = ""
	stageQuery.EventType = ""
	stageQuery.ArtifactKind = ""
	stageQuery.NeedsActionOnly = false
	stageQuery.SuspendedOnly = false
	switch query.ItemKind {
	case "check", "checks", "artifact", "artifacts", "evidence":
		stageQuery.Status = ""
	}
	filtered := make([]session.WorkflowRunStageSnapshot, 0, len(stages))
	for _, stage := range stages {
		if !workflowRunStageMatchesQuery(stage, stageQuery) {
			continue
		}
		filtered = append(filtered, stage)
		if query.Limit > 0 && query.ItemKind != "check" && query.ItemKind != "checks" && len(filtered) >= query.Limit {
			break
		}
	}
	return filtered
}

func workflowRunEvidenceArtifactsForQuery(artifacts []session.WorkflowRunArtifact, query workflowRunQuery) []session.WorkflowRunArtifact {
	if query.ItemKind != "" && query.ItemKind != "artifact" && query.ItemKind != "artifacts" && query.ItemKind != "evidence" {
		return nil
	}
	artifactQuery := query
	artifactQuery.ItemKind = ""
	artifactQuery.Status = ""
	filtered := make([]session.WorkflowRunArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !workflowRunArtifactMatchesQuery(artifact, artifactQuery) {
			continue
		}
		if query.ItemKind == "evidence" && !workflowRunArtifactHasQualityEvidence(artifact) {
			continue
		}
		if query.Status != "" && normalizeWorkflowRunQueryToken(workflowRunArtifactEvidenceNode(artifact).ValidationStatus) != query.Status {
			continue
		}
		filtered = append(filtered, artifact)
		if query.Limit > 0 && len(filtered) >= query.Limit {
			break
		}
	}
	return filtered
}

func workflowRunStageMissingProvenance(stage session.WorkflowRunStageSnapshot) bool {
	return strings.TrimSpace(stage.Stage) == "" || (strings.TrimSpace(stage.AgentID) == "" && strings.TrimSpace(stage.NodeType) == "" && strings.TrimSpace(stage.Skill) == "" && strings.TrimSpace(stage.Tool) == "")
}

func workflowRunEvidenceCategoryFallback(artifact session.WorkflowRunArtifact) string {
	switch normalizeWorkflowRunQueryToken(artifact.Kind) {
	case "acceptance":
		return "acceptance"
	case "verification":
		return "verification"
	case "finding":
		return "finding"
	case "change", "diff", "patch":
		return "change"
	case "tool_result":
		return "tool"
	default:
		if artifact.IsError {
			return "error"
		}
		return "artifact"
	}
}

func workflowRunArtifactValidationFallback(artifact session.WorkflowRunArtifact) string {
	if artifact.IsError {
		return "failed"
	}
	return "recorded"
}

func workflowRunEvidenceMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
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

func workflowRunEvidenceSplitList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key := strings.ToLower(field)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, field)
	}
	return out
}

func workflowRunEvidenceNodeMapValues(nodes map[string]workflowRunEvidenceNode) []workflowRunEvidenceNode {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]workflowRunEvidenceNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func workflowRunEvidenceAddEdge(edges *[]workflowRunEvidenceEdge, seen map[string]struct{}, from, to, kind, label string) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	kind = strings.TrimSpace(kind)
	if from == "" || to == "" || kind == "" {
		return
	}
	key := from + "\x00" + to + "\x00" + kind
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*edges = append(*edges, workflowRunEvidenceEdge{From: from, To: to, Kind: kind, Label: strings.TrimSpace(label)})
}

func workflowRunEvidenceStageID(stage string) string {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		stage = "unknown"
	}
	return "stage:" + stage
}

func workflowRunEvidenceArtifactID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "unknown"
	}
	return "artifact:" + id
}

func workflowRunEvidenceCheckID(stage, kind, label string, index int) string {
	return "check:" + strings.Join([]string{strings.TrimSpace(stage), strings.TrimSpace(kind), strings.TrimSpace(label), workflowRunIntString(index)}, ":")
}

func workflowRunEvidenceStageKindKey(stage, kind, label string) string {
	return strings.ToLower(strings.Join([]string{strings.TrimSpace(stage), strings.TrimSpace(kind), strings.TrimSpace(label)}, "\x00"))
}

func workflowRunIntString(value int) string {
	if value < 0 {
		value = 0
	}
	return strconv.Itoa(value)
}
