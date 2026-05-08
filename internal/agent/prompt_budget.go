package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func emitPromptBudget(handler func(event schema.StreamEvent) error, state *session.State, agentID, mode string, request schema.ChatRequest, skill *schema.Skill, totalTools int) {
	var snapshot session.Snapshot
	if state != nil {
		snapshot = state.Snapshot()
	}
	budget := estimatePromptBudget(agentID, mode, request, skill, totalTools, snapshot)
	if state != nil {
		budget.TaskStage = snapshot.TaskStage.Stage
		budget.WorkflowName = snapshot.Workflow.Name
	}
	if state != nil {
		state.SetPromptBudget(budget)
	}
	if handler == nil {
		return
	}
	_ = handler(schema.StreamEvent{
		Type:         schema.StreamEventPromptBudget,
		Content:      "prompt_budget",
		AgentID:      agentID,
		Mode:         mode,
		PromptBudget: &budget,
	})
}

func estimatePromptBudget(agentID, mode string, request schema.ChatRequest, skill *schema.Skill, totalTools int, snapshot session.Snapshot) schema.PromptBudget {
	systemBytes := len([]byte(request.System))
	messageBytes := 0
	for _, message := range request.Messages {
		messageBytes += len([]byte(message.Role))
		messageBytes += len([]byte(message.Name))
		messageBytes += len([]byte(message.ToolCallID))
		messageBytes += len([]byte(message.Content))
		for _, call := range message.ToolCalls {
			messageBytes += len([]byte(call.ID))
			messageBytes += len([]byte(call.Name))
			messageBytes += len(call.Arguments)
		}
	}
	toolBytes := 0
	for _, tool := range request.Tools {
		toolBytes += len([]byte(tool.Name))
		toolBytes += len([]byte(tool.Description))
		toolBytes += len([]byte(tool.Server))
		toolBytes += len([]byte(tool.Kind))
		toolBytes += len(tool.InputSchema)
	}
	skillTokens := 0
	skillName := ""
	skillBudgetText := ""
	if skill != nil {
		skillName = skill.Name
		skillBudgetText = skill.Name + "\n" + skill.Description + "\n" + skill.Instructions + "\n" + skillResourcesBudgetText(skill.Resources)
		skillTokens = estimateTextTokens(skillBudgetText)
	}
	systemTokens := estimateByteTokens(systemBytes)
	messageTokens := estimateByteTokens(messageBytes)
	toolTokens := estimateByteTokens(toolBytes)
	systemHash := stablePromptHash(request.System)
	toolSchemaHash := stableToolSchemaHash(request.Tools)
	skillHash := stablePromptHash(skillBudgetText)
	prefixHash := stablePromptHash(strings.Join([]string{request.Model, systemHash, toolSchemaHash, skillHash}, "\x00"))
	historyPromptStats := sessionHistoryCompactionStats(snapshot.RecentPrompts, systemPromptRecentPromptLimit)
	historyToolStats := sessionHistoryCompactionStats(snapshot.RecentTools, systemPromptRecentToolLimit)
	return schema.PromptBudget{
		EstimatedPromptTokens:            systemTokens + messageTokens + toolTokens,
		SystemTokens:                     systemTokens,
		MessageTokens:                    messageTokens,
		ToolSchemaTokens:                 toolTokens,
		SkillTokens:                      skillTokens,
		CacheablePrefixTokens:            systemTokens + toolTokens,
		SystemBytes:                      systemBytes,
		MessageBytes:                     messageBytes,
		ToolSchemaBytes:                  toolBytes,
		MessageCount:                     len(request.Messages),
		ExposedToolCount:                 len(request.Tools),
		TotalToolCount:                   totalTools,
		FilteredToolCount:                maxInt(totalTools-len(request.Tools), 0),
		HistoryPromptItems:               historyPromptStats.OriginalItems,
		HistoryPromptRetainedItems:       len(historyPromptStats.RetainedItems),
		HistoryPromptDeduplicatedItems:   historyPromptStats.Deduplicated,
		HistoryPromptCompactedOlderItems: historyPromptStats.OlderCompacted,
		HistoryToolItems:                 historyToolStats.OriginalItems,
		HistoryToolRetainedItems:         len(historyToolStats.RetainedItems),
		HistoryToolDeduplicatedItems:     historyToolStats.Deduplicated,
		HistoryToolCompactedOlderItems:   historyToolStats.OlderCompacted,
		HistoryEstimatedSavedTokens:      historyPromptStats.SavedItemTokens + historyToolStats.SavedItemTokens,
		AgentID:                          agentID,
		Mode:                             mode,
		SkillName:                        skillName,
		SystemHash:                       systemHash,
		ToolSchemaHash:                   toolSchemaHash,
		SkillHash:                        skillHash,
		PromptPrefixHash:                 prefixHash,
	}
}

func estimateTextTokens(text string) int {
	return estimateByteTokens(len([]byte(text)))
}

func estimateByteTokens(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return int(math.Ceil(float64(bytes) / 4.0))
}

func skillResourcesBudgetText(resources []schema.SkillResource) string {
	if len(resources) == 0 {
		return ""
	}
	data, err := json.Marshal(resources)
	if err != nil {
		parts := make([]string, 0, len(resources))
		for _, resource := range resources {
			parts = append(parts, resource.Path)
		}
		return strings.Join(parts, "\n")
	}
	return string(data)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func stablePromptHash(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:16]
}

func stableToolSchemaHash(tools []schema.Tool) string {
	if len(tools) == 0 {
		return ""
	}
	type promptToolFingerprint struct {
		Server      string `json:"server,omitempty"`
		Name        string `json:"name"`
		Kind        string `json:"kind,omitempty"`
		Description string `json:"description,omitempty"`
		InputSchema string `json:"input_schema,omitempty"`
	}
	fingerprints := make([]promptToolFingerprint, 0, len(tools))
	for _, tool := range tools {
		fingerprints = append(fingerprints, promptToolFingerprint{
			Server:      strings.TrimSpace(tool.Server),
			Name:        strings.TrimSpace(tool.Name),
			Kind:        strings.TrimSpace(tool.Kind),
			Description: strings.TrimSpace(tool.Description),
			InputSchema: strings.TrimSpace(string(tool.InputSchema)),
		})
	}
	sort.Slice(fingerprints, func(i, j int) bool {
		left := fingerprints[i].Server + "/" + fingerprints[i].Name
		right := fingerprints[j].Server + "/" + fingerprints[j].Name
		if left == right {
			return fingerprints[i].Kind < fingerprints[j].Kind
		}
		return left < right
	})
	data, err := json.Marshal(fingerprints)
	if err != nil {
		return ""
	}
	return stablePromptHash(string(data))
}
