package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var toolDiagnosticMu sync.Mutex

// recordToolRejection appends a rejected tool call to a diagnostics file.
//
// Run events are a bounded window that reasoning deltas flood, so rejections
// were invisible exactly when a stage kept retrying. This log is append-only
// and cheap; it is written under the OS temp directory, never the workspace.
func recordToolRejection(execCtx ExecutionContext, toolName, reason string) {
	dir := filepath.Join(os.TempDir(), "goflow-diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	reason = strings.TrimSpace(strings.ReplaceAll(reason, "\n", " "))
	if len(reason) > 300 {
		reason = reason[:300]
	}
	line := fmt.Sprintf("%s\trun=%s\tstage=%s\ttool=%s\treason=%s\n",
		time.Now().Format(time.RFC3339), strings.TrimSpace(execCtx.WorkflowRunID), strings.TrimSpace(execCtx.WorkflowStage), strings.TrimSpace(toolName), reason)

	toolDiagnosticMu.Lock()
	defer toolDiagnosticMu.Unlock()
	file, err := os.OpenFile(filepath.Join(dir, "tool-rejections.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = file.WriteString(line)
	_ = file.Close()
}

// recordStageCapDiagnostic logs the cap check inputs so a budget that silently
// does not fire can be diagnosed from the same diagnostics file as rejections.
func recordStageCapDiagnostic(execCtx ExecutionContext, total, baseline, perEntry, totalCap int) {
	dir := filepath.Join(os.TempDir(), "goflow-diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	line := fmt.Sprintf("%s\trun=%s\tstage=%s\ttool=budget\treason=total=%d baseline=%d per_entry=%d total_cap=%d\n",
		time.Now().Format(time.RFC3339), strings.TrimSpace(execCtx.WorkflowRunID), strings.TrimSpace(execCtx.WorkflowStage), total, baseline, perEntry, totalCap)
	toolDiagnosticMu.Lock()
	defer toolDiagnosticMu.Unlock()
	file, err := os.OpenFile(filepath.Join(dir, "tool-rejections.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = file.WriteString(line)
	_ = file.Close()
}
