package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/scheduler"
)

type scheduleCollectionResponse struct {
	Items          []scheduler.Schedule `json:"items"`
	Jobs           []scheduler.Job      `json:"jobs,omitempty"`
	SchedulerError string               `json:"scheduler_error,omitempty"`
}

func (s *Server) handleScheduleCollection(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.scheduleStore == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "scheduler is not available"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.scheduleStore.ListSchedules()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		visible := items[:0]
		for _, item := range items {
			if !item.Archived || r.URL.Query().Get("archived") == "true" {
				visible = append(visible, item)
			}
		}
		jobs, err := s.scheduleStore.ListJobs("")
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, scheduleCollectionResponse{Items: visible, Jobs: jobs, SchedulerError: s.currentScheduleError()})
	case http.MethodPost:
		var item scheduler.Schedule
		if err := decodeJSONBody(r, &item); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if item.ID == "" {
			item.ID = newScheduleID(item.Name)
		}
		item.Task.ReadOnly = true
		item.Task.TargetID = "local"
		if !s.requireSchedulePreflight(w, r, item) {
			return
		}
		saved, err := s.scheduleStore.PutSchedule(item)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if s.scheduleEngine != nil {
			_ = s.scheduleEngine.Tick()
		}
		writeJSONStatus(w, http.StatusCreated, saved)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleScheduleItem(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.scheduleStore == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "scheduler is not available"})
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/schedules/"), "/"), "/")
	id := strings.TrimSpace(parts[0])
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "jobs" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		jobs, err := s.scheduleStore.ListJobs(id)
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"items": jobs})
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, ok, err := s.scheduleStore.GetSchedule(id)
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, item)
	case http.MethodPut:
		var item scheduler.Schedule
		if err := decodeJSONBody(r, &item); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		item.ID = id
		item.Task.ReadOnly = true
		item.Task.TargetID = "local"
		if !s.requireSchedulePreflight(w, r, item) {
			return
		}
		saved, err := s.scheduleStore.PutSchedule(item)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if s.scheduleEngine != nil {
			_ = s.scheduleEngine.Tick()
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		deleted, err := s.scheduleStore.DeleteSchedule(id)
		if err != nil {
			writeJSONStatus(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if !deleted {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) requireSchedulePreflight(w http.ResponseWriter, r *http.Request, item scheduler.Schedule) bool {
	if !item.Enabled {
		return true
	}
	result := s.taskPreflight(r.Context(), taskPreflightRequest{
		Type: item.Task.Kind, Agent: item.Task.AgentID, Workflow: item.Task.WorkflowName,
		Skill: item.Task.SkillName, Tool: item.Task.ToolName, Input: item.Task.Request, TargetID: "local",
	})
	if result.Valid {
		return true
	}
	writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]any{"error": firstTaskPreflightError(result.Issues), "preflight": result})
	return false
}

func (s *Server) currentScheduleError() string {
	s.scheduleErrorMu.RLock()
	defer s.scheduleErrorMu.RUnlock()
	return s.scheduleError
}

func newScheduleID(name string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	var builder strings.Builder
	for _, char := range base {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			builder.WriteRune(char)
		} else if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "-") {
			builder.WriteByte('-')
		}
		if builder.Len() >= 40 {
			break
		}
	}
	base = strings.Trim(builder.String(), "-")
	if base == "" {
		base = "schedule"
	}
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("%s-%d", base, time.Now().UTC().UnixNano())
	}
	return base + "-" + hex.EncodeToString(random)
}
