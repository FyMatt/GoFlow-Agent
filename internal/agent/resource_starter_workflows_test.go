package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

func TestResourceDefinedSecurityWorkflowsValidate(t *testing.T) {
	home := t.TempDir()
	skills := append(workflowGraphTestSkills(), schema.Skill{Name: "security-assessment"}, schema.Skill{Name: "security-incident-investigation"}, schema.Skill{Name: "security-evidence-review"}, schema.Skill{Name: "event-notification"})
	runtime := newWorkflowGraphRuntime(t, home, skills, &stubRuntimeMCP{}, workflowGraphTestClients())
	for _, name := range []string{"security-assessment", "security-incident-investigation"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "workflows", name, "workflow.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		writeWorkflowGraphYAML(t, home, name, string(data))
		doc, err := runtime.WorkflowRunner().LoadWorkflowGraphDocument(name)
		if err != nil {
			t.Fatal(err)
		}
		result := runtime.WorkflowRunner().ValidateWorkflowGraphDocument(name, doc)
		if !result.Valid {
			t.Errorf("resource %s does not validate: %#v", name, result.Issues)
		}
		if doc.Stages[1].NodeType != "input_gate" || doc.Stages[1].Params["manual"] != "true" {
			t.Errorf("resource %s must preserve manual authorization input", name)
		}
	}
}

func TestSecurityQualityIncludesExecutedNotificationFailures(t *testing.T) {
	for _, name := range []string{"security-assessment", "security-incident-investigation"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "workflows", name, "workflow.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var graph workflowGraph
			if err := yaml.Unmarshal(data, &graph); err != nil {
				t.Fatal(err)
			}
			var quality, notify workflowGraphStage
			for _, stage := range graph.Stages {
				switch stage.Name {
				case "quality":
					quality = stage
				case "notify":
					notify = stage
				}
			}
			completed := []WorkflowStageResult{{Stage: "collect", Status: "completed",
				Acceptance: []session.WorkflowRunAcceptanceSnapshot{{Name: "observed", Status: "passed"}},
				Result:     schema.AgentResult{ToolResults: []schema.ToolResult{{CallID: "read", ToolName: "file_tools/read_file", Content: "fixture evidence"}}},
			}}
			if result := evaluateWorkflowGraphQualityGate(quality, completed); !result.Passed {
				t.Fatalf("not-triggered notification must not fail the gate: %#v", result)
			}
			failed := WorkflowStageResult{Stage: "notify", Status: "completed", Result: schema.AgentResult{ToolResults: []schema.ToolResult{{CallID: "notify", ToolName: "notification_tools/publish_event", Content: `{"delivery_status":"failed"}`, IsError: true}}}}
			if result := evaluateWorkflowGraphQualityGate(quality, append(completed, failed)); result.Passed || result.ToolErrors != 1 {
				t.Fatalf("notification failure was hidden by successful collection: %#v", result)
			}
			failed.Result.ToolResults[0].IsError = false
			failed.Result.ToolResults[0].Content = `{"event_id":"fixture","delivery_status":"stored"}`
			if result := evaluateWorkflowGraphQualityGate(quality, append(completed, failed)); !result.Passed {
				t.Fatalf("stored local notification should pass: %#v", result)
			}
			claim := workflowGraphStageResult(notify, schema.AgentResult{Output: `{"event_id":"fabricated","delivery_status":"stored"}`}, nil, nil, 1)
			failure := workflowGraphStageContractFailure(notify, claim)
			if !failure.Failed || !failure.Required {
				t.Fatal("model-only notification receipt satisfied the tool evidence contract")
			}
		})
	}
}

func TestSecurityStarterDoesNotRunToolsWithoutUserAuthorization(t *testing.T) {
	for _, name := range []string{"security-assessment", "security-incident-investigation"} {
		home := t.TempDir()
		data, err := os.ReadFile(filepath.Join("..", "..", "workflows", name, "workflow.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		writeWorkflowGraphYAML(t, home, name, string(data))
		mcp := &stubRuntimeMCP{}
		skills := append(workflowGraphTestSkills(), schema.Skill{Name: "security-assessment"}, schema.Skill{Name: "security-incident-investigation"}, schema.Skill{Name: "security-evidence-review"}, schema.Skill{Name: "event-notification"})
		runtime := newWorkflowGraphRuntime(t, home, skills, mcp, workflowGraphTestClients())
		paused, err := runtime.WorkflowRunner().Run(context.Background(), name, "只读检查指定材料", true, nil)
		if err != nil || !paused.PendingInput || mcp.calls != 0 {
			t.Fatalf("manual scope cannot be bypassed by broad tool approval: %#v err=%v calls=%d", paused, err, mcp.calls)
		}
		denied, err := runtime.WorkflowRunner().ResumeInput(context.Background(), paused.RunID, map[string]string{"scope": "input/material.txt", "authorized": "false", "channel_id": "local"}, nil)
		if err != nil || denied.Status != "blocked" || mcp.calls != 0 {
			t.Fatalf("denied scope must block before any tool: %#v err=%v calls=%d", denied, err, mcp.calls)
		}
	}
}

func TestSecurityStarterBlocksIncompleteCollectorBeforeAlertRouting(t *testing.T) {
	for _, name := range []string{"security-assessment", "security-incident-investigation"} {
		for _, output := range []string{
			`{"summary":"演示材料","decision":"passed","alert_required":false,"evidence_refs":[],"findings":[],"limitations":[],"recommendations":[]}`,
			`{"summary":"演示材料","decision":"passed","severity":"info","evidence_refs":[],"findings":[],"limitations":[],"recommendations":[]}`,
			`{"summary":"演示材料","decision":"passed","alert_required":null,"severity":"info","evidence_refs":[],"findings":[],"limitations":[],"recommendations":[]}`,
			`{"summary":"演示材料","decision":"passed","alert_required":"false","severity":"info","evidence_refs":[],"findings":[],"limitations":[],"recommendations":[]}`,
			`{"summary":"演示材料","decision":"passed","alert_required":false,"severity":"safe","evidence_refs":[],"findings":[],"limitations":[],"recommendations":[]}`,
		} {
			t.Run(name+output, func(t *testing.T) {
				home := t.TempDir()
				data, err := os.ReadFile(filepath.Join("..", "..", "workflows", name, "workflow.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				writeWorkflowGraphYAML(t, home, name, string(data))
				clients := workflowGraphTestClients()
				clients["security-researcher"] = &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: output}}}}
				mcp := &stubRuntimeMCP{}
				skills := append(workflowGraphTestSkills(), schema.Skill{Name: "security-assessment"}, schema.Skill{Name: "security-incident-investigation"}, schema.Skill{Name: "security-evidence-review"}, schema.Skill{Name: "event-notification"})
				runtime := newWorkflowGraphRuntime(t, home, skills, mcp, clients)
				paused, err := runtime.WorkflowRunner().Run(t.Context(), name, "仅分析本地演示材料", true, nil)
				if err != nil || !paused.PendingInput {
					t.Fatalf("expected authorization input: %#v, %v", paused, err)
				}
				result, err := runtime.WorkflowRunner().ResumeInput(t.Context(), paused.RunID, map[string]string{"scope": "input/events.log", "authorized": "true", "channel_id": "local"}, nil)
				if err != nil || result.Status != "blocked" {
					t.Fatalf("incomplete collector must block: status=%s err=%v", result.Status, err)
				}
				if workflowStageResultNamesContain(result.CompletedStages, "alert-required") || mcp.calls != 0 {
					t.Fatal("incomplete collector reached alert routing or executed tools")
				}
			})
		}
	}
}
