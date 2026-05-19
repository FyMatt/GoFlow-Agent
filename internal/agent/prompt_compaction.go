package agent

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	toolResultPromptCompactThresholdBytes = 16 * 1024
	toolResultPromptHeadBytes             = 6 * 1024
	toolResultPromptTailBytes             = 3 * 1024
	toolResultPromptFocusedHeadBytes      = 4 * 1024
	toolResultPromptFocusedTailBytes      = 2 * 1024
	toolResultPromptCompactionMarker      = "[GoFlow compacted tool result for LLM context:"
)

func compactToolResultForPrompt(result schema.ToolResult) string {
	return compactToolResultForPromptWithArtifact(result, "")
}

func compactToolResultForPromptWithArtifact(result schema.ToolResult, artifactRef string) string {
	content := result.Content
	originalBytes := len([]byte(content))
	classification := classifyToolResultPromptContent(result)
	if originalBytes <= toolResultPromptCompactThresholdBytes && !classification.ForceCompact {
		return content
	}
	headBytes := toolResultPromptHeadBytes
	tailBytes := toolResultPromptTailBytes
	if classification.Focused {
		headBytes = toolResultPromptFocusedHeadBytes
		tailBytes = toolResultPromptFocusedTailBytes
	}
	head := trimStringToMaxBytes(content, headBytes)
	tail := trimStringToLastBytes(content, tailBytes)
	shownBytes := len([]byte(head)) + len([]byte(tail))
	omittedBytes := originalBytes - shownBytes
	if omittedBytes < 0 {
		omittedBytes = 0
	}
	originalLines := countLines(content)
	detail := classification.Detail
	if strings.TrimSpace(detail) == "" {
		detail = "large_text"
	}
	refText := ""
	if strings.TrimSpace(artifactRef) != "" {
		refText = " artifact_ref=" + strings.TrimSpace(artifactRef)
	}
	return fmt.Sprintf("%s tool=%s kind=%s original_bytes=%d original_lines=%d shown_bytes=%d omitted_bytes=%d%s. Full result is kept as an artifact; include artifact_ref in a later request if exact omitted content is needed.]\n\n--- head ---\n%s\n\n--- omitted %d bytes ---\n\n--- tail ---\n%s",
		toolResultPromptCompactionMarker,
		fallbackToolResultName(result.ToolName),
		detail,
		originalBytes,
		originalLines,
		shownBytes,
		omittedBytes,
		refText,
		head,
		omittedBytes,
		tail,
	)
}

func storeCompactedToolResultArtifact(state *session.State, result schema.ToolResult, agentID, mode string) string {
	if state == nil || !shouldCompactToolResultForPrompt(result) {
		return ""
	}
	artifact := state.AddArtifact(session.SessionArtifactSnapshot{
		Kind:         "tool_result",
		Title:        fallbackToolResultName(result.ToolName),
		Summary:      truncateSummary(result.Content),
		Content:      result.Content,
		ToolName:     result.ToolName,
		ToolCallID:   result.CallID,
		AgentID:      agentID,
		Mode:         mode,
		ContentBytes: len([]byte(result.Content)),
		Metadata: map[string]string{
			"is_error":  strconv.FormatBool(result.IsError),
			"denied":    strconv.FormatBool(result.Denied),
			"suspended": strconv.FormatBool(result.Suspended),
		},
	})
	return artifact.Ref
}

func shouldCompactToolResultForPrompt(result schema.ToolResult) bool {
	return len([]byte(result.Content)) > toolResultPromptCompactThresholdBytes || classifyToolResultPromptContent(result).ForceCompact
}

func fallbackToolResultName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "tool"
	}
	return name
}

func toolResultPromptWasCompacted(content string) bool {
	return strings.HasPrefix(content, toolResultPromptCompactionMarker)
}

type toolResultPromptClassification struct {
	ForceCompact bool
	Focused      bool
	Detail       string
}

func classifyToolResultPromptContent(result schema.ToolResult) toolResultPromptClassification {
	content := strings.TrimSpace(result.Content)
	if content == "" {
		return toolResultPromptClassification{}
	}
	lowerName := strings.ToLower(strings.TrimSpace(result.ToolName))
	lineCount := countLines(content)
	contentBytes := len([]byte(content))
	classification := toolResultPromptClassification{}
	if strings.Contains(lowerName, "diff") || strings.Contains(lowerName, "patch") || looksLikeDiffContent(content) {
		classification.Detail = "diff"
		classification.Focused = true
		if contentBytes > 8*1024 || (contentBytes > toolResultPromptFocusedHeadBytes+toolResultPromptFocusedTailBytes && lineCount > 160) {
			classification.ForceCompact = true
		}
		return classification
	}
	if strings.Contains(lowerName, "log") || looksLikeLogContent(content) {
		classification.Detail = "log"
		classification.Focused = true
		if contentBytes > 8*1024 || (contentBytes > toolResultPromptFocusedHeadBytes+toolResultPromptFocusedTailBytes && lineCount > 160) {
			classification.ForceCompact = true
		}
		return classification
	}
	if strings.Contains(lowerName, "web") || strings.Contains(lowerName, "search") || looksLikeWebContent(content) {
		classification.Detail = "web"
		classification.Focused = true
		if contentBytes > 10*1024 || (contentBytes > toolResultPromptFocusedHeadBytes+toolResultPromptFocusedTailBytes && lineCount > 180) {
			classification.ForceCompact = true
		}
		return classification
	}
	if strings.Contains(lowerName, "list") || strings.Contains(lowerName, "grep") || strings.Contains(lowerName, "search") || looksLikeRepeatedStructuredLines(content) {
		classification.Detail = "structured_lines"
		classification.Focused = true
		if contentBytes > 10*1024 || (contentBytes > toolResultPromptFocusedHeadBytes+toolResultPromptFocusedTailBytes && lineCount > 220) {
			classification.ForceCompact = true
		}
	}
	return classification
}

func looksLikeDiffContent(content string) bool {
	lines := strings.Split(content, "\n")
	hits := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ") {
			hits++
		}
		if hits >= 2 {
			return true
		}
	}
	return false
}

func looksLikeLogContent(content string) bool {
	lines := strings.Split(content, "\n")
	hits := 0
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(lower, " error ") || strings.Contains(lower, " warn") || strings.Contains(lower, "failed") || strings.Contains(lower, "exception") || strings.Contains(lower, "stack trace") {
			hits++
		}
		if strings.HasPrefix(lower, "[error]") || strings.HasPrefix(lower, "error:") || strings.HasPrefix(lower, "warning:") || strings.HasPrefix(lower, "panic:") {
			hits += 2
		}
		if hits >= 4 {
			return true
		}
	}
	return false
}

func looksLikeWebContent(content string) bool {
	lower := strings.ToLower(content)
	if strings.Count(lower, "http://")+strings.Count(lower, "https://") >= 8 {
		return true
	}
	return strings.Count(lower, "<a ") >= 8 || strings.Count(lower, "<p") >= 8 || strings.Count(lower, "search result") >= 4
}

func looksLikeRepeatedStructuredLines(content string) bool {
	lines := strings.Split(content, "\n")
	if len(lines) < 80 {
		return false
	}
	withSeparators := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Count(line, ":") >= 2 || strings.Count(line, "\t") >= 2 || strings.Count(line, "|") >= 2 {
			withSeparators++
		}
	}
	return withSeparators >= len(lines)/2
}

func countLines(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

func trimStringToMaxBytes(content string, maxBytes int) string {
	if maxBytes <= 0 || content == "" {
		return ""
	}
	if len([]byte(content)) <= maxBytes {
		return content
	}
	trimmed := content[:maxBytes]
	for !utf8.ValidString(trimmed) && len(trimmed) > 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

func trimStringToLastBytes(content string, maxBytes int) string {
	if maxBytes <= 0 || content == "" {
		return ""
	}
	if len([]byte(content)) <= maxBytes {
		return content
	}
	start := len(content) - maxBytes
	if start < 0 {
		start = 0
	}
	trimmed := content[start:]
	for !utf8.ValidString(trimmed) && len(trimmed) > 0 {
		trimmed = trimmed[1:]
	}
	return trimmed
}
