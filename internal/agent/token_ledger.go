package agent

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func classifyLLMOutput(resp schema.ChatResponse) (kind string, toolNames []string) {
	toolNames = llmOutputToolNames(resp.ToolCalls)
	hasTools := len(toolNames) > 0 || resp.StopReason == schema.StopReasonToolCalls
	hasText := strings.TrimSpace(resp.Message.Content) != ""
	switch {
	case hasTools && hasText:
		return schema.LLMOutputKindMixed, toolNames
	case hasTools:
		return schema.LLMOutputKindToolCalls, toolNames
	case hasText:
		return schema.LLMOutputKindText, toolNames
	default:
		return schema.LLMOutputKindEmpty, toolNames
	}
}

func llmOutputToolNames(calls []schema.ToolCall) []string {
	if len(calls) == 0 {
		return nil
	}
	names := make([]string, 0, len(calls))
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func cacheHitRate(usage schema.TokenUsage) float64 {
	if usage.PromptTokens <= 0 || usage.CachedTokens <= 0 {
		return 0
	}
	rate := float64(usage.CachedTokens) / float64(usage.PromptTokens)
	if rate > 1 {
		return 1
	}
	return rate
}

func shouldRecordLLMCall(resp schema.ChatResponse) bool {
	return hasTokenUsage(resp.Usage) ||
		strings.TrimSpace(resp.StopReason) != "" ||
		len(resp.ToolCalls) > 0 ||
		strings.TrimSpace(resp.Message.Content) != ""
}
