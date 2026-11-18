package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestServerAgentTaskPinsSelectedResourcesAndRetryInheritsSnapshot(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", []schema.ChatResponse{
		{Message: schema.Message{Content: "first result"}},
		{Message: schema.Message{Content: "retry result"}},
	}, []config.MCPServerRef{{Name: "file_tools", Command: "unused", Enabled: true}})
	server := NewServer(runtimeRef)

	request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{
		"input":"inspect selected resources",
		"agent":"planner",
		"skill":"execution-plan",
		"tool":"file_tools/read_file",
		"background":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected background Agent run 202, got %d body=%s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted run: %v", err)
	}
	first := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "completed")
	if first.AgentID != "planner" {
		t.Fatalf("expected selected Agent to execute, got %#v", first)
	}
	if first.Result == nil || first.Result.MatchedSkill == nil || first.Result.MatchedSkill.Name != "execution-plan" {
		t.Fatalf("expected selected Skill to execute, got %#v", first.Result)
	}
	assertResourcePinKinds(t, first.ResourcePins, []string{"agent", "mcp", "provider", "skill"})
	for _, pin := range first.ResourcePins {
		if !strings.HasPrefix(pin.Version, "sha256:") || len(pin.Version) != len("sha256:")+64 {
			t.Fatalf("expected SHA-256 resource pin, got %#v", pin)
		}
	}
	task := server.runCollectionItemForAgent(first)
	if task.TargetID != session.LocalTargetID {
		t.Fatalf("expected TaskRecord target %q, got %#v", session.LocalTargetID, task)
	}
	if !reflect.DeepEqual(task.ResourcePins, first.ResourcePins) {
		t.Fatalf("expected TaskRecord resource pins, task=%#v run=%#v", task.ResourcePins, first.ResourcePins)
	}

	retryRequest := httptest.NewRequest(http.MethodPost, "/api/runs/"+first.ID+"/actions/retry", nil)
	retryResponse := httptest.NewRecorder()
	server.ServeHTTP(retryResponse, retryRequest)
	if retryResponse.Code != http.StatusAccepted {
		t.Fatalf("expected retry 202, got %d body=%s", retryResponse.Code, retryResponse.Body.String())
	}
	var retryAccepted agentRunBackgroundResponse
	if err := json.NewDecoder(retryResponse.Body).Decode(&retryAccepted); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	retried := waitAgentRunStatus(t, runtimeRef, retryAccepted.RunID, "completed")
	if retried.RetryOf != first.ID || !reflect.DeepEqual(retried.ResourcePins, first.ResourcePins) {
		t.Fatalf("expected retry to inherit exact resource pins, first=%#v retry=%#v", first.ResourcePins, retried.ResourcePins)
	}
}

func TestServerBuiltInWorkflowPinsWorkflowAgentsProvidersAndSkills(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	pins := server.workflowTaskResourcePins("plan-fix-audit")
	assertResourcePinKinds(t, pins, []string{
		"agent", "agent", "agent",
		"provider",
		"skill", "skill", "skill",
		"workflow",
	})
	if pin := resourcePinByKindAndName(pins, "workflow", "plan-fix-audit"); pin == nil {
		t.Fatalf("expected built-in workflow pin, got %#v", pins)
	}
}

func assertResourcePinKinds(t *testing.T, pins []session.ResourcePin, want []string) {
	t.Helper()
	got := make([]string, len(pins))
	for i, pin := range pins {
		got[i] = pin.Kind
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected resource pin kinds: got=%#v want=%#v pins=%#v", got, want, pins)
	}
}

func resourcePinByKindAndName(pins []session.ResourcePin, kind, name string) *session.ResourcePin {
	for i := range pins {
		if pins[i].Kind == kind && pins[i].Name == name {
			return &pins[i]
		}
	}
	return nil
}
