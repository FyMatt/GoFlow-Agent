package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/memory"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const promptBudgetToolDiagnosticLimit = 12

type promptBudgetContext struct {
	Memory     *memory.PromptContext
	ToolPolicy *promptToolPolicyContext
	Pricing    config.PricingConfig
	Provider   string
	Model      string
}

type promptToolPolicyContext struct {
	Profile config.AgentProfile
	Tools   []schema.Tool
}

func promptBudgetContextWithTools(context promptBudgetContext, profile config.AgentProfile, tools []schema.Tool) promptBudgetContext {
	context.ToolPolicy = &promptToolPolicyContext{
		Profile: profile,
		Tools:   append([]schema.Tool(nil), tools...),
	}
	context.Pricing = profile.Pricing
	context.Provider = strings.TrimSpace(profile.Provider)
	context.Model = strings.TrimSpace(profile.Model)
	return context
}

func emitPromptBudget(handler func(event schema.StreamEvent) error, state *session.State, agentID, mode string, request schema.ChatRequest, skill *schema.Skill, totalTools int, contexts ...promptBudgetContext) {
	var snapshot session.Snapshot
	if state != nil {
		snapshot = state.Snapshot()
	}
	context := promptBudgetContext{}
	if len(contexts) > 0 {
		context = contexts[0]
	}
	budget := estimatePromptBudgetWithContext(agentID, mode, request, skill, totalTools, snapshot, context)
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
		Type:                      schema.StreamEventPromptBudget,
		Content:                   "prompt_budget",
		AgentID:                   agentID,
		Mode:                      mode,
		PromptBudget:              &budget,
		BudgetEstimatedInputCost:  budget.EstimatedInputCost,
		BudgetEstimatedOutputCost: budget.EstimatedOutputCost,
		BudgetEstimatedTotalCost:  budget.EstimatedTotalCost,
		BudgetCostCurrency:        budget.CostCurrency,
		BudgetPricingSource:       budget.PricingSource,
	})
}

func estimatePromptBudget(agentID, mode string, request schema.ChatRequest, skill *schema.Skill, totalTools int, snapshot session.Snapshot) schema.PromptBudget {
	return estimatePromptBudgetWithContext(agentID, mode, request, skill, totalTools, snapshot, promptBudgetContext{})
}

func estimatePromptBudgetWithContext(agentID, mode string, request schema.ChatRequest, skill *schema.Skill, totalTools int, snapshot session.Snapshot, context promptBudgetContext) schema.PromptBudget {
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
	skillInjectedTokens := 0
	skillSourceTokens := 0
	skillOmittedTokens := 0
	skillInjectedBytes := 0
	skillSourceBytes := 0
	skillOmittedBytes := 0
	skillName := ""
	skillInstructionMode := ""
	skillInjectedBudgetText := ""
	skillSourceBudgetText := ""
	if skill != nil {
		skillName = skill.Name
		skillInjectedBudgetText, skillInstructionMode = skillPromptInjectedBudgetText(skill)
		skillSourceBudgetText = skillPromptSourceBudgetText(skill)
		skillInjectedBytes = len([]byte(skillInjectedBudgetText))
		skillSourceBytes = len([]byte(skillSourceBudgetText))
		skillInjectedTokens = estimateTextTokens(skillInjectedBudgetText)
		skillSourceTokens = estimateTextTokens(skillSourceBudgetText)
		skillOmittedBytes = maxInt(skillSourceBytes-skillInjectedBytes, 0)
		skillOmittedTokens = maxInt(skillSourceTokens-skillInjectedTokens, 0)
	}
	systemTokens := estimateByteTokens(systemBytes)
	messageTokens := estimateByteTokens(messageBytes)
	toolTokens := estimateByteTokens(toolBytes)
	memoryBlocks, memoryOmitted, memoryTokens, memorySavedTokens := promptBudgetMemoryDiagnostics(context.Memory)
	artifactRefs, compactedToolResults, artifactOmittedTokens, artifactOmitted := promptBudgetArtifactDiagnostics(request.Messages)
	messageCompactedCount, messageOmittedTokens, messageOmitted := promptBudgetCompactedMessageDiagnostics(request.Messages)
	injectedToolSchemas, filteredToolSchemas, omittedToolDiagnostics, toolSelection, toolSchemaEstimatedSavedTokens := promptBudgetToolSchemaDiagnostics(request.Tools, context.ToolPolicy)
	systemHash := stablePromptHash(request.System)
	toolSchemaHash := stableToolSchemaHash(request.Tools)
	skillHash := stablePromptHash(skillInjectedBudgetText)
	skillSourceHash := stablePromptHash(skillSourceBudgetText)
	prefixHash := stablePromptHash(strings.Join([]string{request.Model, systemHash, toolSchemaHash, skillHash}, "\x00"))
	historyPromptStats := sessionHistoryCompactionStats(snapshot.RecentPrompts, systemPromptRecentPromptLimit)
	historyToolStats := sessionHistoryCompactionStats(snapshot.RecentTools, systemPromptRecentToolLimit)
	omittedContext := make([]string, 0, len(memoryOmitted)+len(artifactOmitted)+len(messageOmitted)+1)
	omittedContext = append(omittedContext, memoryOmitted...)
	if skillOmittedTokens > 0 {
		detail := fmt.Sprintf("skill %s full instructions omitted from prompt summary; estimated_saved_tokens=%d", fallbackPromptBudgetName(skillName, "skill"), skillOmittedTokens)
		if ref := skillRefForBudget(skill); ref != "" {
			detail += " ref=" + ref
		}
		omittedContext = append(omittedContext, detail)
	}
	omittedContext = append(omittedContext, artifactOmitted...)
	omittedContext = append(omittedContext, messageOmitted...)
	budget := schema.PromptBudget{
		EstimatedPromptTokens:            systemTokens + messageTokens + toolTokens,
		SystemTokens:                     systemTokens,
		MessageTokens:                    messageTokens,
		ToolSchemaTokens:                 toolTokens,
		SkillTokens:                      skillInjectedTokens,
		SkillSourceTokens:                skillSourceTokens,
		SkillInjectedTokens:              skillInjectedTokens,
		SkillOmittedTokens:               skillOmittedTokens,
		MemoryTokens:                     memoryTokens,
		DynamicContextTokens:             messageTokens + memoryTokens,
		CacheablePrefixTokens:            systemTokens + toolTokens,
		SystemBytes:                      systemBytes,
		MessageBytes:                     messageBytes,
		ToolSchemaBytes:                  toolBytes,
		SkillSourceBytes:                 skillSourceBytes,
		SkillInjectedBytes:               skillInjectedBytes,
		SkillOmittedBytes:                skillOmittedBytes,
		MessageCount:                     len(request.Messages),
		ExposedToolCount:                 len(request.Tools),
		TotalToolCount:                   totalTools,
		FilteredToolCount:                maxInt(totalTools-len(request.Tools), 0),
		ToolSchemaEstimatedSavedTokens:   toolSchemaEstimatedSavedTokens,
		ToolSchemaDiagnosticCount:        len(injectedToolSchemas) + len(filteredToolSchemas),
		ToolSchemaDiagnosticOmitted:      omittedToolDiagnostics,
		MemoryBlockCount:                 len(memoryBlocks),
		MemoryOmittedCount:               len(memoryOmitted),
		MemoryEstimatedSavedTokens:       memorySavedTokens,
		ArtifactRefCount:                 len(artifactRefs),
		CompactedToolResultCount:         compactedToolResults,
		ArtifactOmittedTokens:            artifactOmittedTokens,
		HistoryPromptItems:               historyPromptStats.OriginalItems,
		HistoryPromptRetainedItems:       len(historyPromptStats.RetainedItems),
		HistoryPromptDeduplicatedItems:   historyPromptStats.Deduplicated,
		HistoryPromptCompactedOlderItems: historyPromptStats.OlderCompacted,
		HistoryToolItems:                 historyToolStats.OriginalItems,
		HistoryToolRetainedItems:         len(historyToolStats.RetainedItems),
		HistoryToolDeduplicatedItems:     historyToolStats.Deduplicated,
		HistoryToolCompactedOlderItems:   historyToolStats.OlderCompacted + messageCompactedCount,
		HistoryEstimatedSavedTokens:      historyPromptStats.SavedItemTokens + historyToolStats.SavedItemTokens + messageOmittedTokens,
		AgentID:                          agentID,
		Mode:                             mode,
		SkillName:                        skillName,
		SkillInstructionMode:             skillInstructionMode,
		ToolSchemaSelection:              toolSelection,
		SystemHash:                       systemHash,
		ToolSchemaHash:                   toolSchemaHash,
		SkillHash:                        skillHash,
		SkillSourceHash:                  skillSourceHash,
		PromptPrefixHash:                 prefixHash,
		MemoryBlocks:                     memoryBlocks,
		OmittedContext:                   limitStrings(omittedContext, 12),
		ArtifactRefs:                     limitStrings(artifactRefs, 12),
		InjectedToolSchemas:              injectedToolSchemas,
		FilteredToolSchemas:              filteredToolSchemas,
	}
	applyPromptBudgetCostEstimate(&budget, context)
	return budget
}

func applyPromptBudgetCostEstimate(budget *schema.PromptBudget, context promptBudgetContext) {
	if budget == nil || !pricingHasRates(context.Pricing) {
		return
	}
	budget.EstimatedInputCost = estimateInputTokenCost(budget.EstimatedPromptTokens, 0, context.Pricing)
	budget.EstimatedTotalCost = budget.EstimatedInputCost
	budget.CostCurrency = context.Pricing.Currency
	budget.PricingSource = context.Pricing.Source
	budget.PricingProvider = context.Provider
	budget.PricingModel = context.Model
}

func pricingHasRates(pricing config.PricingConfig) bool {
	return pricing.InputPerMillionTokens > 0 ||
		pricing.OutputPerMillionTokens > 0 ||
		pricing.CachedInputPerMillionTokens > 0
}

func estimateInputTokenCost(promptTokens, cachedTokens int, pricing config.PricingConfig) float64 {
	if promptTokens <= 0 {
		return 0
	}
	cached := cachedTokens
	if cached < 0 {
		cached = 0
	}
	if cached > promptTokens {
		cached = promptTokens
	}
	regular := promptTokens - cached
	cachedRate := pricing.CachedInputPerMillionTokens
	if cachedRate <= 0 {
		cachedRate = pricing.InputPerMillionTokens
	}
	return roundEstimatedCost(estimateTokenCost(regular, pricing.InputPerMillionTokens) + estimateTokenCost(cached, cachedRate))
}

func estimateOutputTokenCost(outputTokens int, pricing config.PricingConfig) float64 {
	if outputTokens <= 0 {
		return 0
	}
	return roundEstimatedCost(estimateTokenCost(outputTokens, pricing.OutputPerMillionTokens))
}

func estimateTokenCost(tokens int, perMillion float64) float64 {
	if tokens <= 0 || perMillion <= 0 {
		return 0
	}
	return float64(tokens) * perMillion / 1000000.0
}

func roundEstimatedCost(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return math.Round(value*100000000) / 100000000
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

func skillScriptsBudgetText(scripts []schema.SkillScript) string {
	if len(scripts) == 0 {
		return ""
	}
	data, err := json.Marshal(scripts)
	if err != nil {
		parts := make([]string, 0, len(scripts))
		for _, script := range scripts {
			parts = append(parts, script.Name+" "+script.Path)
		}
		return strings.Join(parts, "\n")
	}
	return string(data)
}

func skillPromptInjectedBudgetText(skill *schema.Skill) (string, string) {
	if skill == nil {
		return "", ""
	}
	var builder strings.Builder
	writeSkillPromptBlock(&builder, skill)
	mode, _, _ := compactSkillInstructionsForPrompt(skill.Instructions)
	return builder.String(), mode
}

func skillPromptSourceBudgetText(skill *schema.Skill) string {
	if skill == nil {
		return ""
	}
	return strings.Join([]string{
		skill.Name,
		skill.Description,
		skill.Instructions,
		skillScriptsBudgetText(skill.Scripts),
		skillResourcesBudgetText(skill.Resources),
	}, "\n")
}

func skillRefForBudget(skill *schema.Skill) string {
	if skill == nil {
		return ""
	}
	if strings.TrimSpace(skill.Path) != "" {
		return strings.TrimSpace(skill.Path)
	}
	if strings.TrimSpace(skill.Name) != "" {
		return "skill:" + strings.TrimSpace(skill.Name)
	}
	return ""
}

func promptBudgetMemoryDiagnostics(ctx *memory.PromptContext) ([]schema.PromptContextBlock, []string, int, int) {
	if ctx == nil {
		return nil, nil, 0, 0
	}
	blocks := make([]schema.PromptContextBlock, 0, len(ctx.Blocks))
	tokens := 0
	for _, block := range ctx.Blocks {
		summaryTokens := estimateTextTokens(block.Summary)
		tokens += summaryTokens
		blocks = append(blocks, schema.PromptContextBlock{
			Kind:                 block.Kind,
			Title:                block.Title,
			Ref:                  block.Ref,
			Tokens:               summaryTokens,
			Score:                block.Score,
			Hash:                 block.Hash,
			Language:             block.Language,
			Size:                 block.Size,
			MTime:                block.MTime,
			ContentMode:          block.ContentMode,
			EstimatedSavedTokens: block.EstimatedSavedTokens,
		})
	}
	omitted := make([]string, 0, len(ctx.Omitted))
	for _, item := range ctx.Omitted {
		item = strings.TrimSpace(item)
		if item != "" {
			omitted = append(omitted, "memory: "+item)
		}
	}
	return blocks, omitted, tokens, ctx.EstimatedSavedTokens
}

func promptBudgetArtifactDiagnostics(messages []schema.Message) ([]string, int, int, []string) {
	refs := make([]string, 0)
	seenRefs := make(map[string]struct{})
	compacted := 0
	omittedTokens := 0
	omitted := make([]string, 0)
	for _, message := range messages {
		content := message.Content
		for _, ref := range artifactRefsInText(content) {
			if _, ok := seenRefs[ref]; ok {
				continue
			}
			seenRefs[ref] = struct{}{}
			refs = append(refs, ref)
		}
		if !toolResultPromptWasCompacted(content) {
			continue
		}
		compacted++
		omittedBytes := markerIntValue(content, "omitted_bytes")
		tokens := estimateByteTokens(omittedBytes)
		omittedTokens += tokens
		detail := fmt.Sprintf("tool result %s compacted; estimated_omitted_tokens=%d", fallbackPromptBudgetName(message.Name, "tool"), tokens)
		if ref := firstArtifactRefInText(content); ref != "" {
			detail += " ref=" + ref
		}
		omitted = append(omitted, detail)
	}
	return refs, compacted, omittedTokens, omitted
}

func promptBudgetCompactedMessageDiagnostics(messages []schema.Message) (int, int, []string) {
	compacted := 0
	omittedTokens := 0
	omitted := make([]string, 0)
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if !strings.HasPrefix(content, "[GoFlow compacted ") {
			continue
		}
		count := markerIntAfterPrefix(content, "[GoFlow compacted ")
		if count <= 0 {
			continue
		}
		compacted += count
		tokens := count * 180
		omittedTokens += tokens
		omitted = append(omitted, fmt.Sprintf("conversation history compacted; messages=%d estimated_saved_tokens=%d", count, tokens))
	}
	return compacted, omittedTokens, omitted
}

func markerIntAfterPrefix(content, prefix string) int {
	content = strings.TrimSpace(content)
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || !strings.HasPrefix(content, prefix) {
		return 0
	}
	rest := strings.TrimSpace(strings.TrimPrefix(content, prefix))
	var parsed int
	if _, err := fmt.Sscanf(rest, "%d", &parsed); err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func promptBudgetToolSchemaDiagnostics(promptTools []schema.Tool, context *promptToolPolicyContext) ([]schema.PromptToolSchema, []schema.PromptToolSchema, int, string, int) {
	injected := make([]schema.PromptToolSchema, 0, len(promptTools))
	for _, tool := range promptTools {
		injected = append(injected, promptToolSchemaDiagnostic(tool, "injected", "visible to model"))
	}
	filtered := make([]schema.PromptToolSchema, 0)
	if context != nil && len(context.Tools) > 0 {
		promptKeys := make(map[string]struct{}, len(promptTools)*2)
		for _, tool := range promptTools {
			for _, key := range promptToolSchemaKeys(tool) {
				promptKeys[key] = struct{}{}
			}
		}
		for _, tool := range context.Tools {
			visible := false
			for _, key := range promptToolSchemaKeys(tool) {
				if _, ok := promptKeys[key]; ok {
					visible = true
					break
				}
			}
			if visible {
				continue
			}
			reason := "hidden by active agent or skill policy"
			if err := policy.EnforceToolPolicy(context.Profile, tool); err != nil {
				reason = err.Error()
			}
			filtered = append(filtered, promptToolSchemaDiagnostic(tool, "filtered", reason))
		}
	}
	selection := "none"
	if len(promptTools) > 0 && context != nil && len(context.Tools) > 0 {
		switch {
		case len(filtered) > 0:
			selection = "policy_filtered"
		case len(promptTools) == len(context.Tools):
			selection = "all_visible"
		default:
			selection = "partial_visible"
		}
	} else if len(promptTools) > 0 {
		selection = "visible"
	}
	savedTokens := 0
	for _, tool := range filtered {
		savedTokens += tool.Tokens
	}
	omitted := 0
	if len(injected) > promptBudgetToolDiagnosticLimit {
		omitted += len(injected) - promptBudgetToolDiagnosticLimit
		injected = append([]schema.PromptToolSchema(nil), injected[:promptBudgetToolDiagnosticLimit]...)
	}
	if len(filtered) > promptBudgetToolDiagnosticLimit {
		omitted += len(filtered) - promptBudgetToolDiagnosticLimit
		filtered = append([]schema.PromptToolSchema(nil), filtered[:promptBudgetToolDiagnosticLimit]...)
	}
	return injected, filtered, omitted, selection, savedTokens
}

func promptToolSchemaDiagnostic(tool schema.Tool, status, reason string) schema.PromptToolSchema {
	name := strings.TrimSpace(tool.Name)
	server := strings.TrimSpace(tool.Server)
	qualified := name
	if server != "" && name != "" {
		qualified = server + "/" + name
	}
	bytes := len([]byte(tool.Name)) + len([]byte(tool.Description)) + len([]byte(tool.Server)) + len([]byte(tool.Kind)) + len(tool.InputSchema)
	return schema.PromptToolSchema{
		Name:          name,
		QualifiedName: qualified,
		Server:        server,
		Kind:          string(policy.KindForTool(tool)),
		Status:        status,
		Reason:        strings.TrimSpace(reason),
		Tokens:        estimateByteTokens(bytes),
		Bytes:         bytes,
		SchemaHash:    stableToolSchemaHash([]schema.Tool{tool}),
	}
}

func promptToolSchemaKeys(tool schema.Tool) []string {
	keys := []string{promptToolSchemaCanonicalKey(tool)}
	name := strings.ToLower(strings.TrimSpace(tool.Name))
	if name != "" {
		keys = append(keys, name)
	}
	return keys
}

func promptToolSchemaCanonicalKey(tool schema.Tool) string {
	name := strings.ToLower(strings.TrimSpace(tool.Name))
	server := strings.ToLower(strings.TrimSpace(tool.Server))
	if server == "" {
		return name
	}
	return server + "/" + name
}

func artifactRefsInText(content string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	refs := make([]string, 0)
	for _, field := range strings.Fields(content) {
		field = strings.TrimSpace(field)
		switch {
		case strings.HasPrefix(field, "artifact_ref="):
			refs = append(refs, normalizePromptBudgetRef(strings.TrimPrefix(field, "artifact_ref=")))
		case strings.HasPrefix(field, "sha256:"):
			refs = append(refs, normalizePromptBudgetRef(field))
		case strings.HasPrefix(field, "goflow://session-artifacts/"):
			refs = append(refs, normalizePromptBudgetRef(field))
		}
	}
	out := refs[:0]
	for _, ref := range refs {
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

func firstArtifactRefInText(content string) string {
	refs := artifactRefsInText(content)
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}

func normalizePromptBudgetRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.Trim(ref, "[](){}.,;\"'")
	ref = strings.TrimSuffix(ref, "]")
	return strings.TrimSpace(ref)
}

func markerIntValue(content, key string) int {
	key = strings.TrimSpace(key)
	if key == "" {
		return 0
	}
	prefix := key + "="
	for _, field := range strings.Fields(content) {
		if !strings.HasPrefix(field, prefix) {
			continue
		}
		value := strings.Trim(strings.TrimPrefix(field, prefix), "[](){}.,;\"'")
		var parsed int
		if _, err := fmt.Sscanf(value, "%d", &parsed); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

func fallbackPromptBudgetName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func limitStrings(values []string, max int) []string {
	if len(values) == 0 {
		return nil
	}
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if max > 0 && len(cleaned) > max {
		return append([]string(nil), cleaned[:max]...)
	}
	return append([]string(nil), cleaned...)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func maxFloat64(left, right float64) float64 {
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
