package api

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestAgentRunDefaultQueryWindowReturnsRecentEvents(t *testing.T) {
	events := make([]session.AgentRunEventSnapshot, 150)
	for i := range events {
		events[i] = session.AgentRunEventSnapshot{Seq: i + 1, Content: fmt.Sprintf("event-%d", i+1)}
	}
	query := agentRunQueryWithDefaultWindow(httptest.NewRequest("GET", "/api/agent-runs/run/events", nil), events, 100)
	filtered := agentRunEventsForQuery(events, query)
	if len(filtered) != 100 || filtered[0].Seq != 51 || filtered[len(filtered)-1].Seq != 150 {
		t.Fatalf("expected recent 100 agent events, got query=%#v events=%#v", query, filtered)
	}
	allQuery := agentRunQueryWithDefaultWindow(httptest.NewRequest("GET", "/api/agent-runs/run/events?limit=0", nil), events, 100)
	if filtered := agentRunEventsForQuery(events, allQuery); len(filtered) != 150 {
		t.Fatalf("explicit limit=0 must preserve full-query behavior, got %d", len(filtered))
	}
}

func TestWorkflowRunDefaultQueryWindowReturnsRecentEvents(t *testing.T) {
	events := make([]session.WorkflowRunEventSnapshot, 150)
	for i := range events {
		events[i] = session.WorkflowRunEventSnapshot{Seq: i + 1, Content: fmt.Sprintf("event-%d", i+1)}
	}
	query := workflowRunQueryWithDefaultWindow(httptest.NewRequest("GET", "/api/workflow-runs/run/events", nil), events, 100)
	filtered := workflowRunEventsForQuery(events, query)
	if len(filtered) != 100 || filtered[0].Seq != 51 || filtered[len(filtered)-1].Seq != 150 {
		t.Fatalf("expected recent 100 workflow events, got query=%#v events=%#v", query, filtered)
	}
}
