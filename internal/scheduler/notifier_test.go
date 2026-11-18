package scheduler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestHTTPNotifierRejectsPrivateTargetByDefault(t *testing.T) {
	notifier := NewHTTPNotifier()
	target, err := url.Parse("http://127.0.0.1:8080/hook")
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.validateURL(t.Context(), target, false); err == nil {
		t.Fatal("expected private target rejection")
	}
}

func TestHTTPNotifierSendsBoundedPayloadWithIdempotencyKey(t *testing.T) {
	var payload map[string]any
	var idempotencyKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idempotencyKey = r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	notifier := NewHTTPNotifier()
	schedule := testSchedule("notify-http")
	schedule.Notify = Notification{Kind: "webhook", URL: server.URL, AllowPrivate: true, OnFailure: true}
	job := Job{ID: "job-notify", ScheduleID: schedule.ID, ExecutionKey: "stable/execution/key", Status: JobFailed, Error: "failed"}
	if err := notifier.Notify(schedule, job); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if idempotencyKey != job.ExecutionKey || payload["job_id"] != job.ID || payload["status"] != JobFailed {
		t.Fatalf("unexpected request key=%q payload=%#v", idempotencyKey, payload)
	}
}
