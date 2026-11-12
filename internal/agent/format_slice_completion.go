package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/commandpolicy"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func formattingInvocationKey(raw []byte) string {
	normalized := commandpolicy.NormalizeRunCommandArguments(raw)
	classification := commandpolicy.ClassifyArguments(normalized)
	if classification.Class != commandpolicy.ClassFormat || !classification.RequiresExplicitApproval {
		return ""
	}
	var invocation struct {
		Program string   `json:"program"`
		Args    []string `json:"args"`
	}
	if json.Unmarshal(normalized, &invocation) != nil {
		return ""
	}
	invocation.Program = strings.TrimSuffix(strings.ToLower(filepath.Base(invocation.Program)), ".exe")
	writes := invocation.Program == "go" && len(invocation.Args) > 0 && invocation.Args[0] == "fmt"
	for _, arg := range invocation.Args {
		writes = writes || arg == "-w" || arg == "--write" || arg == "--fix" || arg == "-i"
	}
	if !writes {
		return ""
	}
	return workflowGraphJSON(invocation)
}

// A successful approved formatter completes its action even when the files
// were already formatted. Merely listing dirty files never qualifies.
func completeObservedFormattingSlices(slices []session.WorkflowChangeSliceSnapshot, results []schema.ToolResult, stage string) bool {
	observed := map[string]bool{}
	for _, result := range results {
		if workflowGraphQualitySuccessfulVerificationCommand(result) {
			if payload, ok := workflowGraphQualityToolResultPayload(result); ok {
				if key := formattingInvocationKey([]byte(workflowGraphJSON(payload))); key != "" {
					observed[key] = true
				}
			}
		}
	}
	changed := false
	for i := range slices {
		item := &slices[i]
		if item.ExecutionMode != "write" || item.Status == "implemented" || item.Status == "verified" || item.Status == "blocked" {
			continue
		}
		ready := true
		for _, dependency := range item.DependsOn {
			found := false
			for _, parent := range slices {
				if parent.ID == dependency && (parent.Status == "implemented" || parent.Status == "verified") {
					found = true
				}
			}
			ready = ready && found
		}
		seen, complete := false, true
		for _, command := range item.VerificationCommands {
			if key := formattingInvocationKey([]byte(workflowGraphJSON(map[string]string{"command": command}))); key != "" {
				seen = true
				complete = complete && observed[key]
			}
		}
		if ready && seen && complete {
			item.Status = "implemented"
			item.EvidenceStages = append(item.EvidenceStages, stage)
			changed = true
		}
	}
	return changed
}
