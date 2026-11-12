package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Keep the read index visible before sharing the remaining budget across bodies.
// Repeated successful reads of one path use its latest observation.
func compactContinuationObservations(results []schema.ToolResult) string {
	type observation struct{ header, body string }
	var selected []observation
	seen := map[string]bool{}
	headerBytes := 0
	for i := len(results) - 1; i >= 0 && len(selected) < 32; i-- {
		result := results[i]
		status := "ok"
		if result.Suspended {
			status = "suspended"
		} else if result.Denied {
			status = "denied"
		} else if result.IsError {
			status = "error"
		}
		header := fmt.Sprintf("- %s (%s)", fallbackToolResultName(result.ToolName), status)
		body := result.Content
		name := workflowGraphQualityToolName(result.ToolName)
		if name == "read_file" || name == "read_file_range" {
			var file struct {
				Path, Content, SHA256 string
				RelativePath          string `json:"relative_path"`
			}
			if json.Unmarshal([]byte(body), &file) == nil && (file.RelativePath != "" || file.Path != "") {
				path := firstWorkflowGraphValue(file.RelativePath, file.Path)
				// Ranges can contain different parts of the same file.
				if status == "ok" && name == "read_file" {
					if seen[path] {
						continue
					}
					seen[path] = true
				}
				header += " " + trimWorkflowGraphContextValue(path, 200)
				if file.SHA256 != "" {
					header += " SHA256=" + trimWorkflowGraphContextValue(file.SHA256, 64)
				}
				body = file.Content
			}
		}
		if headerBytes+len(header)+1 > 4096 {
			break
		}
		headerBytes += len(header) + 1
		selected = append(selected, observation{header, body})
	}
	if len(selected) == 0 {
		return ""
	}
	const instruction = "Reuse completed observations below. Bodies may be abbreviated; request only missing line ranges instead of repeating full reads or repository discovery.\n"
	perBody := (8192 - headerBytes - len(instruction) - len(selected)) / len(selected)
	var builder strings.Builder
	builder.WriteString(instruction)
	for i := len(selected) - 1; i >= 0; i-- {
		builder.WriteString(selected[i].header)
		builder.WriteByte('\n')
		const omitted = "\n[remaining tool content omitted; request only missing line ranges]"
		if len(selected[i].body) > perBody {
			builder.WriteString(trimWorkflowGraphContextValue(selected[i].body, perBody-len(omitted)))
			builder.WriteString(omitted)
		} else {
			builder.WriteString(selected[i].body)
		}
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}
