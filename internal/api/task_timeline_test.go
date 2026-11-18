package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestUnifiedAgentTimelineUsesTaskContract(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"input":"hello","background":true}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("start Agent task: status=%d body=%s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	waitAgentRunStatus(t, runtimeRef, accepted.RunID, "completed")
	timelineRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+accepted.RunID+"/timeline?include_content=1", nil)
	timelineResponse := httptest.NewRecorder()
	server.ServeHTTP(timelineResponse, timelineRequest)
	if timelineResponse.Code != http.StatusOK {
		t.Fatalf("load unified timeline: status=%d body=%s", timelineResponse.Code, timelineResponse.Body.String())
	}
	var items []TaskTimelineItem
	if err := json.NewDecoder(timelineResponse.Body).Decode(&items); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected unified timeline items")
	}
	for _, item := range items {
		if item.RunID != accepted.RunID || item.RunType != "agent" || item.Kind == "" {
			t.Fatalf("unexpected common timeline item: %#v", item)
		}
	}
}

func TestWorkflowTaskTimelineIncludesPinnedPlanCurrentStepAndParallelGroup(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	doc := agent.WorkflowGraphDocument{Name: "parallel-plan", Stages: []agent.WorkflowGraphStageDocument{
		{Name: "plan", Agent: "planner", Skill: "execution-plan", Next: []string{"implement"}},
		{Name: "implement", Agent: "fixer", Skill: "code-writing"},
	}}
	if err := runtimeRef.WorkflowRunner().SaveWorkflowGraphDocument(doc.Name, doc); err != nil {
		t.Fatalf("save Workflow: %v", err)
	}
	run := session.WorkflowRunSnapshot{
		ID:           "run-workflow-timeline",
		Name:         doc.Name,
		Status:       "running",
		StartedAt:    "2026-07-21T01:00:00Z",
		UpdatedAt:    "2026-07-21T01:01:00Z",
		NextStage:    "implement",
		Attempt:      1,
		ResourcePins: server.workflowTaskResourcePins(doc.Name),
		Events: []session.WorkflowRunEventSnapshot{{
			Seq:       2,
			At:        "2026-07-21T01:00:30Z",
			Type:      "task_stage",
			Stage:     "implement-a",
			TaskStage: "implement-a",
			Reason:    "parallel_branch",
			SourceRef: "parallel-work",
			Content:   "parallel branch stage implement-a started",
		}},
		CompletedStages: []session.WorkflowRunStageSnapshot{{
			Stage:       "implement-a",
			Status:      "completed",
			StartedAt:   "2026-07-21T01:00:30Z",
			CompletedAt: "2026-07-21T01:00:50Z",
			Attempts:    2,
			Metadata: map[string]string{
				"parallel_branch": "true",
				"parallel_parent": "parallel-work",
			},
		}},
	}
	items := server.taskTimelineForWorkflow(run, workflowRunQuery{})
	if !taskTimelineKindExists(items, "plan") {
		t.Fatalf("expected pinned Workflow plan, got %#v", items)
	}
	if !taskTimelineParallelGroupExists(items, "parallel-work") {
		t.Fatalf("expected parallel grouping metadata, got %#v", items)
	}
	if !taskTimelineCurrentStageExists(items, "implement") {
		t.Fatalf("expected current step, got %#v", items)
	}
}

func TestInterruptedWorkflowTaskTimelineAndActionsRecommendRetry(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	run := session.WorkflowRunSnapshot{
		ID:        "run-workflow-interrupted",
		Name:      "plan-fix-audit",
		Status:    "interrupted",
		Request:   "finish the task",
		NextStage: "fix",
		Events: []session.WorkflowRunEventSnapshot{{
			Seq:            2,
			Type:           "workflow_interrupted",
			Stage:          "fix",
			Content:        "workflow interrupted by process restart; retry from the saved request",
			WorkflowStatus: "interrupted",
			NextStage:      "fix",
			Reason:         "process_restart",
			Severity:       "warning",
			NeedsAction:    true,
			Suspended:      true,
		}},
	}
	items := server.taskTimelineForWorkflow(run, workflowRunQuery{})
	foundRetry := false
	for _, item := range items {
		if item.Type == "workflow_interrupted" && item.Action == "retry" && item.NeedsAction {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Fatalf("expected interrupted timeline to recommend retry, got %#v", items)
	}
	actions := server.workflowRunActions(run)
	if !workflowRunActionExists(actions, "retry") || workflowRunActionExists(actions, "cancel") {
		t.Fatalf("expected retry without cancel for interrupted workflow, got %#v", actions)
	}
}

func TestLimitAndSortTaskTimelineKeepsNewestItems(t *testing.T) {
	items := []TaskTimelineItem{
		{Seq: 1, At: "2026-07-21T01:00:00Z", Kind: "event", Type: "oldest"},
		{Seq: 2, At: "2026-07-21T01:01:00Z", Kind: "event", Type: "middle"},
		{Seq: 3, At: "2026-07-21T01:02:00Z", Kind: "event", Type: "latest"},
	}

	limited := limitAndSortTaskTimeline(items, 2)
	if len(limited) != 2 || limited[0].Type != "middle" || limited[1].Type != "latest" {
		t.Fatalf("limited timeline must retain the newest items in chronological order, got %#v", limited)
	}
}

func taskTimelineKindExists(items []TaskTimelineItem, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func taskTimelineParallelGroupExists(items []TaskTimelineItem, groupID string) bool {
	for _, item := range items {
		if item.GroupKind == "parallel" && item.GroupID == groupID {
			return true
		}
	}
	return false
}

func taskTimelineCurrentStageExists(items []TaskTimelineItem, stage string) bool {
	for _, item := range items {
		if item.Current && item.Stage == stage {
			return true
		}
	}
	return false
}
