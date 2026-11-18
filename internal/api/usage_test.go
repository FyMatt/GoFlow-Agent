package api

import (
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestUsageLedgerIsIndependentOfReplayWindow(t *testing.T) {
	now := time.Now()
	s := session.New(8)
	id := s.StartWorkflowRun("long", "task")
	for i := 0; i < 220; i++ {
		s.AppendWorkflowRunEvent(id, session.WorkflowRunEventSnapshot{At: now.Add(-time.Minute).Format(time.RFC3339Nano), Type: "token_usage", PromptTokens: 100, OutputTokens: 10, CachedTokens: 50})
	}
	for i := 0; i < 300; i++ {
		s.AppendWorkflowRunEvent(id, session.WorkflowRunEventSnapshot{Type: "status", Content: "progress"})
	}
	response := buildUsageResponse(s.Snapshot(), "24h", now)
	run, _ := s.WorkflowRun(id)
	totals := retainedWorkflowRunUsage(run)
	if response.Lifetime.PromptTokens != 22000 || response.Period.OutputTokens != 2200 || response.Period.CachedTokens != 11000 || totals.ModelCalls != 220 || totals.TotalTokens != 24200 {
		t.Fatalf("usage lost after event eviction: response=%#v totals=%#v", response, totals)
	}
}

func TestBuildUsageResponseAggregatesRetainedTimelines(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 10, 8, 18, 30, 0, 0, location)
	at := func(offset time.Duration) string { return now.Add(offset).UTC().Format(time.RFC3339Nano) }
	snapshot := session.Snapshot{
		AgentRuns: []session.AgentRunSnapshot{{
			ID: "agent-1", AgentID: "software-engineer", Events: []session.AgentRunEventSnapshot{
				{At: at(-2 * time.Hour), Type: "token_usage", PromptTokens: 600_000, OutputTokens: 40_000, CachedTokens: 300_000, AgentID: "software-engineer"},
				{At: at(-time.Hour), Type: "token_usage", PromptTokens: 500_000, OutputTokens: 60_000, CachedTokens: 250_000, AgentID: "software-engineer"},
			},
		}},
		WorkflowRuns: []session.WorkflowRunSnapshot{{
			ID: "workflow-1", Events: []session.WorkflowRunEventSnapshot{
				{At: at(-30 * time.Minute), Type: "token_usage", PromptTokens: 100_000, OutputTokens: 10_000, CachedTokens: 50_000, AgentID: "auditor"},
			},
		}},
	}

	response := buildUsageResponse(snapshot, "24h", now)
	if response.Range != "24h" || response.BucketUnit != "hour" || len(response.Buckets) != 24 {
		t.Fatalf("unexpected range: %#v", response)
	}
	if response.Lifetime.TotalTokens != 1_310_000 || response.Lifetime.PromptTokens != 1_200_000 || response.Lifetime.OutputTokens != 110_000 || response.Lifetime.CachedTokens != 600_000 || response.Lifetime.ModelCalls != 3 || response.Lifetime.Runs != 2 {
		t.Fatalf("unexpected lifetime totals: %#v", response.Lifetime)
	}
	if response.Period != response.Lifetime {
		t.Fatalf("24h period should include every sample: period=%#v lifetime=%#v", response.Period, response.Lifetime)
	}
	if len(response.Agents) != 2 || response.Agents[0].AgentID != "software-engineer" || response.Agents[0].Runs != 1 {
		t.Fatalf("unexpected agent breakdown: %#v", response.Agents)
	}
	if response.Coverage.Samples != 3 || response.Coverage.RetainedRuns != 2 {
		t.Fatalf("unexpected coverage: %#v", response.Coverage)
	}
	var nonEmpty int
	for _, bucket := range response.Buckets {
		if bucket.ModelCalls > 0 {
			nonEmpty++
		}
	}
	if nonEmpty != 3 {
		t.Fatalf("expected three hourly buckets, got %d: %#v", nonEmpty, response.Buckets)
	}
}

func TestNormalizeUsageRangeDefaultsToSevenDays(t *testing.T) {
	if spec := normalizeUsageRange("unknown"); spec.Name != "7d" || spec.BucketUnit != "day" || spec.Count != 7 {
		t.Fatalf("unexpected default range: %#v", spec)
	}
	if spec := normalizeUsageRange("30d"); spec.Count != 30 || spec.BucketUnit != "day" {
		t.Fatalf("unexpected 30d range: %#v", spec)
	}
}

func TestRetainedRunUsageUsesOnlyTheCurrentRunTimeline(t *testing.T) {
	at := time.Now().UTC().Format(time.RFC3339Nano)
	workflow := session.WorkflowRunSnapshot{ID: "workflow-current", Events: []session.WorkflowRunEventSnapshot{
		{At: at, Type: "token_usage", PromptTokens: 600, OutputTokens: 80, CachedTokens: 240},
		{At: at, Type: "token_usage", PromptTokens: 400, OutputTokens: 120, CachedTokens: 160},
	}}
	totals := retainedWorkflowRunUsage(workflow)
	if totals.PromptTokens != 1000 || totals.OutputTokens != 200 || totals.CachedTokens != 400 || totals.TotalTokens != 1200 || totals.ModelCalls != 2 || totals.CacheHitRate != 0.4 {
		t.Fatalf("unexpected retained workflow totals: %#v", totals)
	}
}
