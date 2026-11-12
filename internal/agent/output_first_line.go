package agent

import "strings"

func workflowGraphFirstOutputLine(output string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(output), "\n", 2)[0])
}
