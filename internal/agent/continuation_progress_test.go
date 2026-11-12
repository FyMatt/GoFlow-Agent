package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestContinuationKeepsSuccessfulWritesBeyondRecentReadWindow(t *testing.T) {
	results := []schema.ToolResult{
		{CallID: "write", ToolName: "file_tools/apply_patch", Content: `{"relative_path":"web/app.js","status":"modified"}`},
		{CallID: "failed", ToolName: "file_tools/write_file", IsError: true, Content: `{"relative_path":"failed.txt"}`},
	}
	for i := 0; i < 12; i++ {
		results = append(results, schema.ToolResult{CallID: fmt.Sprint(i), ToolName: "file_tools/read_file", Content: `{"relative_path":"read-only.txt","content":"already read"}`})
	}
	prompt := workflowGraphContinueOutputPrompt("base", WorkflowStageResult{Result: schema.AgentResult{
		Output: "未修改任何文件", Incomplete: true, ToolResults: results,
	}})
	writeIndex := strings.Index(prompt, "web/app.js")
	partialIndex := strings.Index(prompt, "Partial output from previous attempt")
	if writeIndex < 0 || writeIndex >= partialIndex {
		t.Fatalf("successful writes must survive recent reads and precede the stale model summary: %s", prompt)
	}
	if strings.Contains(prompt[:partialIndex], "failed.txt") {
		t.Fatal("failed write was represented as completed progress")
	}
}

func TestPlanningContinuationRetainsReadIndexWithinPromptBudget(t *testing.T) {
	var results []schema.ToolResult
	for i := 0; i < 16; i++ {
		body, _ := json.Marshal(map[string]string{"relative_path": fmt.Sprintf("module/file-%02d.go", i), "sha256": strings.Repeat("a", 64), "content": strings.Repeat("source line\n", 700)})
		results = append(results, schema.ToolResult{ToolName: "file_tools/read_file", Content: string(body)})
	}
	text := workflowGraphPreservedToolObservations(results)
	if len(text) > 8192 {
		t.Fatalf("continuation tool context exceeds existing budget: %d", len(text))
	}
	for i := 0; i < 16; i++ {
		if !strings.Contains(text, fmt.Sprintf("module/file-%02d.go", i)) {
			t.Fatalf("completed read %d was lost", i)
		}
	}
}
