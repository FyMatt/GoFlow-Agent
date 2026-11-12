package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/scheduler"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestScheduleAPIManagesDefinitionsAndJobHistory(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	server.scheduleStore = scheduler.NewStore(t.TempDir())
	if err := server.scheduleStore.Ensure(); err != nil {
		t.Fatal(err)
	}
	payload := scheduler.Schedule{
		Name: "Nightly review", Enabled: false,
		Trigger: scheduler.Trigger{Kind: scheduler.TriggerOnce, RunAt: "2026-07-23T10:00:00Z"},
		Task:    scheduler.TaskSpec{Kind: scheduler.TaskAgent, Request: "inspect repository", AgentID: "chat"},
	}
	body, _ := json.Marshal(payload)
	create := httptest.NewRecorder()
	server.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/schedules", bytes.NewReader(body)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var saved scheduler.Schedule
	if err := json.Unmarshal(create.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || !saved.Task.ReadOnly || saved.Task.TargetID != session.LocalTargetID || saved.Policy.TimeoutSeconds != 1800 {
		t.Fatalf("unexpected schedule defaults %#v", saved)
	}
	if _, err := server.scheduleStore.PutJob(scheduler.Job{ID: "job-api", ScheduleID: saved.ID, ExecutionKey: "api/job", ScheduledFor: "2026-07-23T10:00:00Z", Status: scheduler.JobSucceeded}); err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRecorder()
	server.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/schedules", nil))
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"job-api"`)) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	jobs := httptest.NewRecorder()
	server.ServeHTTP(jobs, httptest.NewRequest(http.MethodGet, "/api/schedules/"+saved.ID+"/jobs", nil))
	if jobs.Code != http.StatusOK || !bytes.Contains(jobs.Body.Bytes(), []byte(`"job-api"`)) {
		t.Fatalf("jobs status=%d body=%s", jobs.Code, jobs.Body.String())
	}

	remove := httptest.NewRecorder()
	server.ServeHTTP(remove, httptest.NewRequest(http.MethodDelete, "/api/schedules/"+saved.ID, nil))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", remove.Code, remove.Body.String())
	}
	archived, ok, err := server.scheduleStore.GetSchedule(saved.ID)
	if err != nil || !ok || !archived.Archived || archived.Enabled {
		t.Fatalf("schedule history was not archived %#v ok=%t err=%v", archived, ok, err)
	}

	hidden := httptest.NewRecorder()
	server.ServeHTTP(hidden, httptest.NewRequest(http.MethodGet, "/api/schedules", nil))
	var hiddenList scheduleCollectionResponse
	if err := json.Unmarshal(hidden.Body.Bytes(), &hiddenList); err != nil || len(hiddenList.Items) != 0 {
		t.Fatalf("archived schedule remained in default list: %s err=%v", hidden.Body.String(), err)
	}
	withArchived := httptest.NewRecorder()
	server.ServeHTTP(withArchived, httptest.NewRequest(http.MethodGet, "/api/schedules?archived=true", nil))
	if !bytes.Contains(withArchived.Body.Bytes(), []byte(saved.ID)) {
		t.Fatalf("archived schedule missing from explicit list: %s", withArchived.Body.String())
	}
}

func TestTaskRecordExposesScheduleSource(t *testing.T) {
	state := session.New(8)
	state.SetPersistPath(filepath.Join(t.TempDir(), "session.json"))
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	runID, err := runtimeRef.StartAgentRunWithOptionsDurable("inspect", session.AgentRunStartOptions{AgentID: "chat", ScheduleID: "daily", ScheduleJobID: "job-daily"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(runtimeRef)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"schedule_id":"daily"`)) || !bytes.Contains(response.Body.Bytes(), []byte(runID)) {
		t.Fatalf("scheduled task metadata missing: %s", response.Body.String())
	}
}
