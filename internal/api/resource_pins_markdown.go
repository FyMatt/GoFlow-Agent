package api

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func writeResourcePinsMarkdown(builder *strings.Builder, pins []session.ResourcePin) {
	if builder == nil || len(pins) == 0 {
		return
	}
	builder.WriteString("\n## Resource Pins\n\n")
	for _, pin := range session.NormalizeResourcePins(pins) {
		builder.WriteString("- `")
		builder.WriteString(workflowRunMarkdownInline(pin.Kind + "/" + pin.Name))
		builder.WriteString("`: `")
		builder.WriteString(workflowRunMarkdownInline(pin.Version))
		builder.WriteString("`\n")
	}
}
