package session

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const maxWorkflowSchemaRuns = 20

// WorkflowSchemaSnapshot stores observed output shapes for one workflow.
type WorkflowSchemaSnapshot struct {
	Workflow  string                                 `json:"workflow"`
	UpdatedAt string                                 `json:"updated_at,omitempty"`
	RunIDs    []string                               `json:"run_ids,omitempty"`
	Stages    map[string]WorkflowStageSchemaSnapshot `json:"stages,omitempty"`
}

// WorkflowStageSchemaSnapshot stores observed output shapes for one stage.
type WorkflowStageSchemaSnapshot struct {
	Stage     string                                 `json:"stage"`
	NodeType  string                                 `json:"node_type,omitempty"`
	AgentID   string                                 `json:"agent_id,omitempty"`
	Skill     string                                 `json:"skill,omitempty"`
	Tool      string                                 `json:"tool,omitempty"`
	UpdatedAt string                                 `json:"updated_at,omitempty"`
	LastRunID string                                 `json:"last_run_id,omitempty"`
	Outputs   map[string]WorkflowValueSchemaSnapshot `json:"outputs,omitempty"`
}

// WorkflowValueSchemaSnapshot stores an observed JSON-like value shape.
type WorkflowValueSchemaSnapshot struct {
	Type      string                                 `json:"type,omitempty"`
	Observed  int                                    `json:"observed,omitempty"`
	UpdatedAt string                                 `json:"updated_at,omitempty"`
	LastRunID string                                 `json:"last_run_id,omitempty"`
	Fields    map[string]WorkflowValueSchemaSnapshot `json:"fields,omitempty"`
	Items     *WorkflowValueSchemaSnapshot           `json:"items,omitempty"`
	Example   any                                    `json:"example,omitempty"`
}

// WorkflowSchemas returns a stable, sorted copy of all workflow schemas.
func (s *State) WorkflowSchemas() []WorkflowSchemaSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return workflowSchemaCatalogList(s.workflowSchemas)
}

// WorkflowSchema returns one observed workflow schema by workflow name.
func (s *State) WorkflowSchema(name string) (WorkflowSchemaSnapshot, bool) {
	if s == nil {
		return WorkflowSchemaSnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	schema, ok := s.workflowSchemas[workflowSchemaKey(name)]
	if !ok {
		return WorkflowSchemaSnapshot{}, false
	}
	return copyWorkflowSchemaSnapshot(schema), true
}

// ClearWorkflowSchema removes one observed workflow schema.
func (s *State) ClearWorkflowSchema(name string) bool {
	if s == nil {
		return false
	}
	key := workflowSchemaKey(name)
	if key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workflowSchemas[key]; !ok {
		return false
	}
	delete(s.workflowSchemas, key)
	return true
}

// ClearWorkflowSchemas removes all observed workflow schemas.
func (s *State) ClearWorkflowSchemas() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.workflowSchemas)
	s.workflowSchemas = nil
	return count
}

// ImportWorkflowSchema imports one workflow schema into the session catalog.
func (s *State) ImportWorkflowSchema(schema WorkflowSchemaSnapshot, merge bool) (WorkflowSchemaSnapshot, error) {
	if s == nil {
		return WorkflowSchemaSnapshot{}, fmt.Errorf("session state is not configured")
	}
	normalized, err := normalizeWorkflowSchemaSnapshot(schema, workflowRunTimestamp(time.Now().UTC()))
	if err != nil {
		return WorkflowSchemaSnapshot{}, err
	}
	key := workflowSchemaKey(normalized.Workflow)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workflowSchemas == nil {
		s.workflowSchemas = make(map[string]WorkflowSchemaSnapshot)
	}
	if merge {
		normalized = mergeWorkflowSchemaSnapshot(s.workflowSchemas[key], normalized)
	}
	s.workflowSchemas[key] = normalized
	return copyWorkflowSchemaSnapshot(normalized), nil
}

// ImportWorkflowSchemas imports multiple workflow schemas into the session catalog.
func (s *State) ImportWorkflowSchemas(schemas []WorkflowSchemaSnapshot, merge bool) ([]WorkflowSchemaSnapshot, error) {
	if s == nil {
		return nil, fmt.Errorf("session state is not configured")
	}
	if len(schemas) == 0 {
		return nil, fmt.Errorf("workflow schema import is empty")
	}
	imported := make([]WorkflowSchemaSnapshot, 0, len(schemas))
	for _, schema := range schemas {
		next, err := s.ImportWorkflowSchema(schema, merge)
		if err != nil {
			return imported, err
		}
		imported = append(imported, next)
	}
	return imported, nil
}

// RebuildWorkflowSchemas rebuilds observed schemas from retained workflow runs.
func (s *State) RebuildWorkflowSchemas() []WorkflowSchemaSnapshot {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workflowSchemas = nil
	for i := len(s.workflowRuns) - 1; i >= 0; i-- {
		s.mergeWorkflowSchemaFromRunLocked(s.workflowRuns[i])
	}
	return workflowSchemaCatalogList(s.workflowSchemas)
}

func (s *State) mergeWorkflowSchemaFromRunLocked(run WorkflowRunSnapshot) {
	workflow := strings.TrimSpace(run.Name)
	key := workflowSchemaKey(workflow)
	if key == "" || len(run.CompletedStages) == 0 {
		return
	}
	updatedAt := firstWorkflowSchemaValue(run.UpdatedAt, run.CompletedAt, workflowRunTimestamp(time.Now().UTC()))
	catalog := s.workflowSchemas
	if catalog == nil {
		catalog = make(map[string]WorkflowSchemaSnapshot)
		s.workflowSchemas = catalog
	}
	current := catalog[key]
	if strings.TrimSpace(current.Workflow) == "" {
		current.Workflow = workflow
	}
	current.UpdatedAt = updatedAt
	current.RunIDs = prependWorkflowSchemaRunID(current.RunIDs, run.ID)
	if current.Stages == nil {
		current.Stages = make(map[string]WorkflowStageSchemaSnapshot)
	}
	for _, stage := range run.CompletedStages {
		stageKey := workflowSchemaKey(stage.Stage)
		if stageKey == "" || (len(stage.OutputValues) == 0 && len(stage.Outputs) == 0) {
			continue
		}
		stageSchema := current.Stages[stageKey]
		if strings.TrimSpace(stageSchema.Stage) == "" {
			stageSchema.Stage = strings.TrimSpace(stage.Stage)
		}
		stageSchema.NodeType = firstWorkflowSchemaValue(stage.NodeType, stageSchema.NodeType)
		stageSchema.AgentID = firstWorkflowSchemaValue(stage.AgentID, stageSchema.AgentID)
		stageSchema.Skill = firstWorkflowSchemaValue(stage.Skill, stageSchema.Skill)
		stageSchema.Tool = firstWorkflowSchemaValue(stage.Tool, stageSchema.Tool)
		stageSchema.UpdatedAt = updatedAt
		stageSchema.LastRunID = run.ID
		if stageSchema.Outputs == nil {
			stageSchema.Outputs = make(map[string]WorkflowValueSchemaSnapshot)
		}
		for name, value := range stage.OutputValues {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			next := workflowValueSchemaFromValue(value, run.ID, updatedAt)
			stageSchema.Outputs[name] = mergeWorkflowValueSchema(stageSchema.Outputs[name], next)
		}
		for name, value := range stage.Outputs {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := stage.OutputValues[name]; ok {
				continue
			}
			next := workflowValueSchemaFromValue(value, run.ID, updatedAt)
			stageSchema.Outputs[name] = mergeWorkflowValueSchema(stageSchema.Outputs[name], next)
		}
		current.Stages[stageKey] = stageSchema
	}
	catalog[key] = current
}

func workflowValueSchemaFromValue(value any, runID, updatedAt string) WorkflowValueSchemaSnapshot {
	valueType := workflowSchemaValueType(value)
	out := WorkflowValueSchemaSnapshot{
		Type:      valueType,
		Observed:  1,
		UpdatedAt: updatedAt,
		LastRunID: runID,
		Example:   workflowSchemaExample(value),
	}
	switch typed := value.(type) {
	case map[string]any:
		out.Fields = make(map[string]WorkflowValueSchemaSnapshot, len(typed))
		for _, key := range sortedWorkflowSchemaAnyKeys(typed) {
			out.Fields[key] = workflowValueSchemaFromValue(typed[key], runID, updatedAt)
		}
	case map[string]string:
		out.Fields = make(map[string]WorkflowValueSchemaSnapshot, len(typed))
		for _, key := range sortedWorkflowSchemaStringKeys(typed) {
			out.Fields[key] = workflowValueSchemaFromValue(typed[key], runID, updatedAt)
		}
	case []any:
		for _, item := range typed {
			itemSchema := workflowValueSchemaFromValue(item, runID, updatedAt)
			if out.Items == nil {
				copied := itemSchema
				out.Items = &copied
			} else {
				merged := mergeWorkflowValueSchema(*out.Items, itemSchema)
				out.Items = &merged
			}
		}
	case []string:
		itemSchema := WorkflowValueSchemaSnapshot{Type: "string", Observed: len(typed), UpdatedAt: updatedAt, LastRunID: runID}
		out.Items = &itemSchema
	}
	return out
}

func mergeWorkflowValueSchema(current, next WorkflowValueSchemaSnapshot) WorkflowValueSchemaSnapshot {
	if current.Type == "" {
		return copyWorkflowValueSchema(next)
	}
	if next.Type == "" {
		return copyWorkflowValueSchema(current)
	}
	merged := copyWorkflowValueSchema(current)
	merged.Type = mergeWorkflowSchemaType(current.Type, next.Type)
	merged.Observed += next.Observed
	if next.UpdatedAt != "" {
		merged.UpdatedAt = next.UpdatedAt
	}
	if next.LastRunID != "" {
		merged.LastRunID = next.LastRunID
	}
	if merged.Example == nil && next.Example != nil {
		merged.Example = copyWorkflowRunAnyValue(next.Example)
	}
	if len(next.Fields) > 0 {
		if merged.Fields == nil {
			merged.Fields = make(map[string]WorkflowValueSchemaSnapshot, len(next.Fields))
		}
		for key, value := range next.Fields {
			merged.Fields[key] = mergeWorkflowValueSchema(merged.Fields[key], value)
		}
	}
	if next.Items != nil {
		if merged.Items == nil {
			copied := copyWorkflowValueSchema(*next.Items)
			merged.Items = &copied
		} else {
			item := mergeWorkflowValueSchema(*merged.Items, *next.Items)
			merged.Items = &item
		}
	}
	return merged
}

func mergeWorkflowSchemaSnapshot(current, next WorkflowSchemaSnapshot) WorkflowSchemaSnapshot {
	if strings.TrimSpace(current.Workflow) == "" {
		return copyWorkflowSchemaSnapshot(next)
	}
	if strings.TrimSpace(next.Workflow) == "" {
		return copyWorkflowSchemaSnapshot(current)
	}
	merged := copyWorkflowSchemaSnapshot(current)
	merged.Workflow = firstWorkflowSchemaValue(next.Workflow, current.Workflow)
	merged.UpdatedAt = firstWorkflowSchemaValue(next.UpdatedAt, current.UpdatedAt)
	merged.RunIDs = mergeWorkflowSchemaRunIDs(next.RunIDs, current.RunIDs)
	if len(next.Stages) > 0 {
		if merged.Stages == nil {
			merged.Stages = make(map[string]WorkflowStageSchemaSnapshot, len(next.Stages))
		}
		for key, stage := range next.Stages {
			stageKey := workflowSchemaKey(firstWorkflowSchemaValue(stage.Stage, key))
			if stageKey == "" {
				continue
			}
			merged.Stages[stageKey] = mergeWorkflowStageSchema(merged.Stages[stageKey], stage)
		}
	}
	return merged
}

func mergeWorkflowStageSchema(current, next WorkflowStageSchemaSnapshot) WorkflowStageSchemaSnapshot {
	if strings.TrimSpace(current.Stage) == "" {
		return copyWorkflowStageSchema(next)
	}
	if strings.TrimSpace(next.Stage) == "" {
		return copyWorkflowStageSchema(current)
	}
	merged := copyWorkflowStageSchema(current)
	merged.Stage = firstWorkflowSchemaValue(next.Stage, current.Stage)
	merged.NodeType = firstWorkflowSchemaValue(next.NodeType, current.NodeType)
	merged.AgentID = firstWorkflowSchemaValue(next.AgentID, current.AgentID)
	merged.Skill = firstWorkflowSchemaValue(next.Skill, current.Skill)
	merged.Tool = firstWorkflowSchemaValue(next.Tool, current.Tool)
	merged.UpdatedAt = firstWorkflowSchemaValue(next.UpdatedAt, current.UpdatedAt)
	merged.LastRunID = firstWorkflowSchemaValue(next.LastRunID, current.LastRunID)
	if len(next.Outputs) > 0 {
		if merged.Outputs == nil {
			merged.Outputs = make(map[string]WorkflowValueSchemaSnapshot, len(next.Outputs))
		}
		for name, output := range next.Outputs {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			merged.Outputs[name] = mergeWorkflowValueSchema(merged.Outputs[name], output)
		}
	}
	return merged
}

func mergeWorkflowSchemaType(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || left == "unknown" {
		return right
	}
	if right == "" || right == "unknown" || left == right {
		return left
	}
	if left == "mixed" || right == "mixed" {
		return "mixed"
	}
	return "mixed"
}

func workflowSchemaValueType(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return "number"
	case map[string]any, map[string]string:
		return "object"
	case []any, []string:
		return "array"
	case string:
		if strings.TrimSpace(typed) == "" {
			return "string"
		}
		return "string"
	default:
		return "string"
	}
}

func workflowSchemaExample(value any) any {
	switch typed := value.(type) {
	case nil, bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return typed
	default:
		return nil
	}
}

func workflowSchemaCatalogList(catalog map[string]WorkflowSchemaSnapshot) []WorkflowSchemaSnapshot {
	if len(catalog) == 0 {
		return nil
	}
	keys := make([]string, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]WorkflowSchemaSnapshot, 0, len(keys))
	for _, key := range keys {
		out = append(out, copyWorkflowSchemaSnapshot(catalog[key]))
	}
	return out
}

func normalizeWorkflowSchemaSnapshot(value WorkflowSchemaSnapshot, now string) (WorkflowSchemaSnapshot, error) {
	value = copyWorkflowSchemaSnapshot(value)
	value.Workflow = strings.TrimSpace(value.Workflow)
	if value.Workflow == "" {
		return WorkflowSchemaSnapshot{}, fmt.Errorf("workflow schema import requires workflow")
	}
	if strings.TrimSpace(value.UpdatedAt) == "" {
		value.UpdatedAt = now
	}
	value.RunIDs = mergeWorkflowSchemaRunIDs(value.RunIDs, nil)
	stages := make(map[string]WorkflowStageSchemaSnapshot)
	for key, stage := range value.Stages {
		stage, ok := normalizeWorkflowStageSchemaSnapshot(key, stage, value.UpdatedAt)
		if !ok {
			continue
		}
		stages[workflowSchemaKey(stage.Stage)] = stage
	}
	if len(stages) == 0 {
		return WorkflowSchemaSnapshot{}, fmt.Errorf("workflow schema import for %s requires at least one stage output", value.Workflow)
	}
	value.Stages = stages
	return value, nil
}

func normalizeWorkflowStageSchemaSnapshot(key string, value WorkflowStageSchemaSnapshot, updatedAt string) (WorkflowStageSchemaSnapshot, bool) {
	value = copyWorkflowStageSchema(value)
	value.Stage = firstWorkflowSchemaValue(value.Stage, key)
	if workflowSchemaKey(value.Stage) == "" {
		return WorkflowStageSchemaSnapshot{}, false
	}
	if strings.TrimSpace(value.UpdatedAt) == "" {
		value.UpdatedAt = updatedAt
	}
	outputs := make(map[string]WorkflowValueSchemaSnapshot)
	for name, output := range value.Outputs {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		outputs[name] = normalizeWorkflowValueSchemaSnapshot(output, value.LastRunID, value.UpdatedAt)
	}
	if len(outputs) == 0 {
		return WorkflowStageSchemaSnapshot{}, false
	}
	value.Outputs = outputs
	return value, true
}

func normalizeWorkflowValueSchemaSnapshot(value WorkflowValueSchemaSnapshot, runID, updatedAt string) WorkflowValueSchemaSnapshot {
	value = copyWorkflowValueSchema(value)
	value.Type = strings.TrimSpace(value.Type)
	if value.Type == "" {
		switch {
		case len(value.Fields) > 0:
			value.Type = "object"
		case value.Items != nil:
			value.Type = "array"
		default:
			if value.Example != nil {
				value.Type = workflowSchemaValueType(value.Example)
			}
			if value.Type == "" {
				value.Type = "unknown"
			}
		}
	}
	if value.Observed <= 0 {
		value.Observed = 1
	}
	if strings.TrimSpace(value.UpdatedAt) == "" {
		value.UpdatedAt = updatedAt
	}
	if strings.TrimSpace(value.LastRunID) == "" {
		value.LastRunID = runID
	}
	if len(value.Fields) > 0 {
		fields := make(map[string]WorkflowValueSchemaSnapshot, len(value.Fields))
		for key, field := range value.Fields {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			fields[key] = normalizeWorkflowValueSchemaSnapshot(field, value.LastRunID, value.UpdatedAt)
		}
		value.Fields = fields
	}
	if value.Items != nil {
		item := normalizeWorkflowValueSchemaSnapshot(*value.Items, value.LastRunID, value.UpdatedAt)
		value.Items = &item
	}
	return value
}

func copyWorkflowSchemaCatalog(catalog map[string]WorkflowSchemaSnapshot) map[string]WorkflowSchemaSnapshot {
	if len(catalog) == 0 {
		return nil
	}
	copied := make(map[string]WorkflowSchemaSnapshot, len(catalog))
	for key, value := range catalog {
		copied[key] = copyWorkflowSchemaSnapshot(value)
	}
	return copied
}

func copyWorkflowSchemaSnapshot(value WorkflowSchemaSnapshot) WorkflowSchemaSnapshot {
	value.RunIDs = append([]string(nil), value.RunIDs...)
	if len(value.Stages) > 0 {
		stages := make(map[string]WorkflowStageSchemaSnapshot, len(value.Stages))
		for key, stage := range value.Stages {
			stages[key] = copyWorkflowStageSchema(stage)
		}
		value.Stages = stages
	}
	return value
}

func copyWorkflowStageSchema(value WorkflowStageSchemaSnapshot) WorkflowStageSchemaSnapshot {
	if len(value.Outputs) > 0 {
		outputs := make(map[string]WorkflowValueSchemaSnapshot, len(value.Outputs))
		for key, output := range value.Outputs {
			outputs[key] = copyWorkflowValueSchema(output)
		}
		value.Outputs = outputs
	}
	return value
}

func copyWorkflowValueSchema(value WorkflowValueSchemaSnapshot) WorkflowValueSchemaSnapshot {
	value.Example = copyWorkflowRunAnyValue(value.Example)
	if len(value.Fields) > 0 {
		fields := make(map[string]WorkflowValueSchemaSnapshot, len(value.Fields))
		for key, field := range value.Fields {
			fields[key] = copyWorkflowValueSchema(field)
		}
		value.Fields = fields
	}
	if value.Items != nil {
		item := copyWorkflowValueSchema(*value.Items)
		value.Items = &item
	}
	return value
}

func prependWorkflowSchemaRunID(values []string, runID string) []string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return append([]string(nil), values...)
	}
	out := []string{runID}
	for _, value := range values {
		if value == "" || value == runID {
			continue
		}
		out = append(out, value)
		if len(out) >= maxWorkflowSchemaRuns {
			break
		}
	}
	return out
}

func mergeWorkflowSchemaRunIDs(primary, secondary []string) []string {
	out := make([]string, 0, maxWorkflowSchemaRuns)
	seen := make(map[string]struct{})
	appendValue := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || len(out) >= maxWorkflowSchemaRuns {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range primary {
		appendValue(value)
	}
	for _, value := range secondary {
		appendValue(value)
	}
	return out
}

func workflowSchemaKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func firstWorkflowSchemaValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sortedWorkflowSchemaAnyKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func sortedWorkflowSchemaStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
