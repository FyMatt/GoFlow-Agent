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
	toolResultPromptCompactThresholdBytes = 24 * 1024
	toolResultPromptHeadBytes             = 16 * 1024
	toolResultPromptTailBytes             = 6 * 1024
	toolResultPromptCompactionMarker      = "[GoFlow compacted tool result for LLM context:"
)

func compactToolResultForPrompt(result schema.ToolResult) string {
	return compactToolResultForPromptWithArtifact(result, "")
}

func compactToolResultForPromptWithArtifact(result schema.ToolResult, artifactRef string) string {
	content := result.Content
	originalBytes := len([]byte(content))
	if originalBytes <= toolResultPromptCompactThresholdBytes {
		return content
	}
	head := trimStringToMaxBytes(content, toolResultPromptHeadBytes)
	tail := trimStringToLastBytes(content, toolResultPromptTailBytes)
	shownBytes := len([]byte(head)) + len([]byte(tail))
	omittedBytes := originalBytes - shownBytes
	if omittedBytes < 0 {
		omittedBytes = 0
	}
	originalLines := countLines(content)
	refText := ""
	if strings.TrimSpace(artifactRef) != "" {
		refText = " artifact_ref=" + strings.TrimSpace(artifactRef)
	}
	return fmt.Sprintf("%s tool=%s original_bytes=%d original_lines=%d shown_bytes=%d omitted_bytes=%d%s. Full result is kept as an artifact; include artifact_ref in a later request if exact omitted content is needed.]\n\n--- head ---\n%s\n\n--- omitted %d bytes ---\n\n--- tail ---\n%s",
		toolResultPromptCompactionMarker,
		fallbackToolResultName(result.ToolName),
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
	return len([]byte(result.Content)) > toolResultPromptCompactThresholdBytes
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
