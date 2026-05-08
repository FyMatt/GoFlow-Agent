package agent

import (
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	systemPromptRecentPromptLimit = 4
	systemPromptRecentToolLimit   = 6
	systemPromptPromptItemBytes   = 1200
	systemPromptToolItemBytes     = 900
)

// BuildSystemPrompt assembles the runtime system prompt.
func BuildSystemPrompt(profile config.AgentProfile, skill *schema.Skill, snapshot session.Snapshot) string {
	var builder strings.Builder
	builder.WriteString("You are ")
	builder.WriteString(profile.Name)
	builder.WriteString(", a local production-minded AI agent. Reason carefully, use tools when needed, and provide concise, actionable output.\n")
	if profile.Description != "" {
		builder.WriteString("Agent description: ")
		builder.WriteString(profile.Description)
		builder.WriteString("\n")
	}
	if profile.SystemPrompt != "" {
		builder.WriteString(profile.SystemPrompt)
		builder.WriteString("\n")
	}
	builder.WriteString("Current mode: ")
	builder.WriteString(profile.Mode)
	builder.WriteString("\n")
	writeCapabilityContract(&builder)
	writeReasoningLoopContract(&builder)
	writeModeContract(&builder, profile.Mode)
	if skill != nil {
		builder.WriteString("Matched skill: ")
		builder.WriteString(skill.Name)
		builder.WriteString("\nSkill description: ")
		builder.WriteString(skill.Description)
		builder.WriteString("\nSkill instructions:\n")
		builder.WriteString(skill.Instructions)
		builder.WriteString("\n")
		if len(skill.Scripts) > 0 {
			builder.WriteString("Skill declared scripts:\n")
			for _, script := range skill.Scripts {
				builder.WriteString("- ")
				builder.WriteString(script.Name)
				if strings.TrimSpace(script.Description) != "" {
					builder.WriteString(": ")
					builder.WriteString(script.Description)
				}
				builder.WriteString(" [")
				builder.WriteString(script.Path)
				if strings.TrimSpace(script.Runtime) != "" {
					builder.WriteString(", runtime=")
					builder.WriteString(script.Runtime)
				}
				if strings.TrimSpace(script.Isolation) != "" {
					builder.WriteString(", isolation=")
					builder.WriteString(script.Isolation)
				}
				if strings.TrimSpace(script.Approval) != "" {
					builder.WriteString(", approval=")
					builder.WriteString(script.Approval)
				}
				builder.WriteString("]\n")
			}
			builder.WriteString("When the skill_runner/run_script tool is exposed, run declared scripts only with {\"skill\":\"")
			builder.WriteString(skill.Name)
			builder.WriteString("\",\"script\":\"<declared-name>\",\"args\":{...}}. Do not execute scripts through any other path; normal approval, audit, and MCP isolation still apply.\n")
		}
		if len(skill.Resources) > 0 {
			builder.WriteString("Skill bundled resources available in the skill folder:\n")
			for _, resource := range skill.Resources {
				builder.WriteString("- ")
				builder.WriteString(resource.Path)
				if strings.TrimSpace(resource.Kind) != "" {
					builder.WriteString(" (")
					builder.WriteString(resource.Kind)
					builder.WriteString(")")
				}
				builder.WriteString("\n")
			}
			builder.WriteString("Use these resource paths as authoring context; do not assume they are workspace files unless the user copies or references them explicitly.\n")
		}
	}
	if snapshot.ActiveAgent != "" || snapshot.Mode != "" || snapshot.LastSkill != "" || len(snapshot.RecentPrompts) > 0 || len(snapshot.RecentTools) > 0 {
		builder.WriteString("Session context:\n")
		if snapshot.ActiveAgent != "" {
			builder.WriteString("- Active agent: ")
			builder.WriteString(snapshot.ActiveAgent)
			builder.WriteString("\n")
		}
		if snapshot.Mode != "" {
			builder.WriteString("- Session mode: ")
			builder.WriteString(snapshot.Mode)
			builder.WriteString("\n")
		}
		if snapshot.LastSkill != "" {
			builder.WriteString("- Last matched skill: ")
			builder.WriteString(snapshot.LastSkill)
			builder.WriteString("\n")
		}
		if len(snapshot.RecentPrompts) > 0 {
			builder.WriteString("- Recent prompts:\n")
			for _, prompt := range compactSessionHistoryForPrompt(snapshot.RecentPrompts, systemPromptRecentPromptLimit, systemPromptPromptItemBytes, "prompts") {
				builder.WriteString(fmt.Sprintf("  - %s\n", prompt))
			}
		}
		if len(snapshot.RecentTools) > 0 {
			builder.WriteString("- Recent tool summaries:\n")
			for _, summary := range compactSessionHistoryForPrompt(snapshot.RecentTools, systemPromptRecentToolLimit, systemPromptToolItemBytes, "tool summaries") {
				builder.WriteString(fmt.Sprintf("  - %s\n", summary))
			}
		}
	}
	builder.WriteString("When using tools, choose only the tools necessary for the current request.")
	return builder.String()
}

func compactSessionHistoryForPrompt(items []string, recentLimit, itemBytes int, label string) []string {
	stats := sessionHistoryCompactionStats(items, recentLimit)
	if len(stats.RetainedItems) == 0 {
		return nil
	}
	out := make([]string, 0, len(stats.RetainedItems)+1)
	if stats.OlderCompacted > 0 || stats.Deduplicated > 0 {
		out = append(out, sessionHistoryCompactionMarker(stats.OlderCompacted, stats.Deduplicated, len(stats.RetainedItems), label))
	}
	for _, item := range stats.RetainedItems {
		out = append(out, compactHistoryItemForPrompt(item, itemBytes))
	}
	return out
}

type sessionHistoryCompaction struct {
	OriginalItems   int
	RetainedItems   []string
	Deduplicated    int
	OlderCompacted  int
	SavedItemTokens int
}

func sessionHistoryCompactionStats(items []string, recentLimit int) sessionHistoryCompaction {
	if len(items) == 0 {
		return sessionHistoryCompaction{}
	}
	cleaned := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		cleaned = append(cleaned, item)
	}
	if len(cleaned) == 0 {
		return sessionHistoryCompaction{}
	}
	deduped, duplicateCount := dedupeHistoryItemsNewestFirst(cleaned)
	if recentLimit <= 0 || recentLimit > len(deduped) {
		recentLimit = len(deduped)
	}
	olderCount := len(deduped) - recentLimit
	retained := append([]string(nil), deduped[len(deduped)-recentLimit:]...)
	savedTokens := 0
	if duplicateCount > 0 || olderCount > 0 {
		retainedKeys := make(map[string]int, len(retained))
		for _, item := range retained {
			retainedKeys[normalizeHistoryDedupeKey(item)]++
		}
		for _, item := range cleaned {
			key := normalizeHistoryDedupeKey(item)
			if retainedKeys[key] > 0 {
				retainedKeys[key]--
				continue
			}
			savedTokens += estimateTextTokens(item)
		}
	}
	return sessionHistoryCompaction{
		OriginalItems:   len(cleaned),
		RetainedItems:   retained,
		Deduplicated:    duplicateCount,
		OlderCompacted:  olderCount,
		SavedItemTokens: savedTokens,
	}
}

func dedupeHistoryItemsNewestFirst(items []string) ([]string, int) {
	if len(items) == 0 {
		return nil, 0
	}
	seen := make(map[string]struct{}, len(items))
	reversed := make([]string, 0, len(items))
	duplicates := 0
	for i := len(items) - 1; i >= 0; i-- {
		item := strings.TrimSpace(items[i])
		if item == "" {
			continue
		}
		key := normalizeHistoryDedupeKey(item)
		if _, ok := seen[key]; ok {
			duplicates++
			continue
		}
		seen[key] = struct{}{}
		reversed = append(reversed, item)
	}
	out := make([]string, len(reversed))
	for i := range reversed {
		out[len(reversed)-1-i] = reversed[i]
	}
	return out, duplicates
}

func normalizeHistoryDedupeKey(item string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(item)), " ")
}

func sessionHistoryCompactionMarker(olderCount, duplicateCount, retained int, label string) string {
	parts := make([]string, 0, 2)
	if olderCount > 0 {
		parts = append(parts, fmt.Sprintf("compacted %d older %s", olderCount, fallbackHistoryLabel(label)))
	}
	if duplicateCount > 0 {
		parts = append(parts, fmt.Sprintf("deduplicated %d repeated %s", duplicateCount, fallbackHistoryLabel(label)))
	}
	parts = append(parts, fmt.Sprintf("latest %d retained", retained))
	return "[" + strings.Join(parts, "; ") + "]"
}

func compactHistoryItemForPrompt(item string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(item)) <= maxBytes {
		return item
	}
	trimmed := strings.TrimSpace(trimStringToMaxBytes(item, maxBytes))
	omitted := len([]byte(item)) - len([]byte(trimmed))
	if omitted <= 0 {
		return trimmed
	}
	return fmt.Sprintf("%s ... [compacted %d bytes]", trimmed, omitted)
}

func fallbackHistoryLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "items"
	}
	return label
}

func writeCapabilityContract(builder *strings.Builder) {
	if builder == nil {
		return
	}
	builder.WriteString("Workspace and tool contract:\n")
	builder.WriteString("- Treat the configured workspace as the only project filesystem boundary.\n")
	builder.WriteString("- File reads, writes, deletes, searches, and tree listings must target workspace paths only; never try to access parent directories or absolute paths outside the workspace.\n")
	builder.WriteString("- If the user references files with @path, their contents may already appear under 'Referenced workspace files'; use that context before re-reading.\n")
	builder.WriteString("- Use web/network tools only when current external information is necessary; cite fetched URLs or search result sources in the final answer.\n")
	builder.WriteString("- For destructive actions such as delete_file, explain the target and wait for approval when policy requires it.\n")
}

func writeReasoningLoopContract(builder *strings.Builder) {
	if builder == nil {
		return
	}
	builder.WriteString("Reasoning loop:\n")
	builder.WriteString("- Plan -> Act -> Observe -> Conclude for tool-using tasks.\n")
	builder.WriteString("- Keep reasoning concise and private; expose only decisions, evidence, and results.\n")
	builder.WriteString("- Use one minimal batch of tool calls at a time, then inspect the results before continuing.\n")
	builder.WriteString("- If the next step is a tool call, call the tool directly; do not emit a prose prelude that repeats the plan.\n")
	builder.WriteString("- After tool results arrive, continue from the observation instead of restating the same plan or previous status text.\n")
	builder.WriteString("- Treat .goflow as GoFlow runtime state, not project source; do not inspect .goflow/session.json or other .goflow files unless the user explicitly asks about GoFlow session internals.\n")
	builder.WriteString("- Once you have enough source context and intend to modify files, proceed to the write action instead of spending extra reads on runtime metadata.\n")
	builder.WriteString("- For coding tasks: inspect relevant files, make focused changes, run or describe the most relevant verification, then summarize changed files and residual risk.\n")
	builder.WriteString("- If a tool fails or is denied, treat that as an observation and recover, retry with a safer step, or ask for operator input.\n")
	builder.WriteString("- Final answers should state what changed or what was found, how it was verified, and any remaining risk.\n")
}

func writeModeContract(builder *strings.Builder, mode string) {
	if builder == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		builder.WriteString("Mode contract for plan:\n")
		builder.WriteString("- Produce a scoped execution plan with objective, assumptions, ordered steps, expected files or systems touched, verification, rollback notes, and risks.\n")
		builder.WriteString("- Make the plan directly usable by a later fixer agent; include enough file/module detail for implementation without re-planning.\n")
		builder.WriteString("- Do not perform write actions while planning.\n")
	case "fix":
		builder.WriteString("Mode contract for fix:\n")
		builder.WriteString("- Make the smallest useful change that satisfies the approved request.\n")
		builder.WriteString("- Prefer existing project patterns and avoid unrelated refactors.\n")
		builder.WriteString("- Summarize changed files, verification performed, and follow-up work that remains.\n")
	case "audit":
		builder.WriteString("Mode contract for audit:\n")
		builder.WriteString("- Prioritize findings by severity and include evidence, affected areas, and concrete remediation guidance.\n")
		builder.WriteString("- Separate confirmed issues from hypotheses and hardening suggestions.\n")
		builder.WriteString("- Do not perform write actions while auditing.\n")
	}
}
