package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/scheduler"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func (s *Server) StartScheduledTask(schedule scheduler.Schedule, job scheduler.Job) (string, error) {
	if s == nil || s.runtime == nil {
		return "", fmt.Errorf("runtime is not configured")
	}
	if !schedule.Task.ReadOnly {
		return "", fmt.Errorf("scheduled task must be read-only")
	}
	preflight := s.taskPreflight(context.Background(), taskPreflightRequest{
		Type: schedule.Task.Kind, Agent: schedule.Task.AgentID, Workflow: schedule.Task.WorkflowName,
		Skill: schedule.Task.SkillName, Tool: schedule.Task.ToolName, Input: schedule.Task.Request,
		TargetID: schedule.Task.TargetID,
	})
	if !preflight.Valid {
		return "", fmt.Errorf("scheduled task preflight failed: %s", firstTaskPreflightError(preflight.Issues))
	}
	switch schedule.Task.Kind {
	case scheduler.TaskAgent:
		return s.startScheduledAgentTask(schedule, job)
	case scheduler.TaskWorkflow:
		return s.startScheduledWorkflowTask(schedule, job)
	default:
		return "", fmt.Errorf("unsupported scheduled task kind %q", schedule.Task.Kind)
	}
}

func firstTaskPreflightError(issues []TaskPreflightIssue) string {
	for _, issue := range issues {
		if strings.EqualFold(strings.TrimSpace(issue.Severity), "error") {
			return strings.TrimSpace(issue.Message)
		}
	}
	return "task prerequisites are not satisfied"
}

func (s *Server) startScheduledAgentTask(schedule scheduler.Schedule, job scheduler.Job) (string, error) {
	if !s.agentExecMu.TryLock() {
		return "", fmt.Errorf("another agent run is already active")
	}
	agentID := strings.TrimSpace(schedule.Task.AgentID)
	if agentID == "" {
		agentID = s.runtime.ActiveAgent()
	}
	if err := s.runtime.SetActiveAgent(agentID); err != nil {
		s.agentExecMu.Unlock()
		return "", err
	}
	pins := s.agentTaskResourcePins(agentID, schedule.Task.SkillName, schedule.Task.ToolName)
	runID, err := s.runtime.StartAgentRunWithOptionsDurable(schedule.Task.Request, session.AgentRunStartOptions{
		AgentID: agentID, TargetID: schedule.Task.TargetID, ScheduleID: schedule.ID,
		ScheduleJobID: job.ID, ResourcePins: pins,
	})
	if err != nil || strings.TrimSpace(runID) == "" {
		s.agentExecMu.Unlock()
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("scheduled agent task could not allocate a durable run")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = agent.WithReadOnlyTools(ctx)
	ctx = agent.WithAgentRunID(ctx, runID)
	ctx = agent.WithAgentTarget(ctx, agentID)
	ctx = agent.WithAgentSkill(ctx, schedule.Task.SkillName)
	ctx = agent.WithExecutionTarget(ctx, schedule.Task.TargetID)
	activeToken := s.registerActiveAgentRun(runID, cancel)
	go func() {
		defer s.agentExecMu.Unlock()
		defer s.unregisterActiveAgentRun(runID, activeToken)
		defer cancel()
		record := s.recordAgentRunEvents(runID, nil)
		expanded, err := s.expandAtReferences(ctx, schedule.Task.Request, record)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.runtime.CancelAgentRun(runID, "scheduled agent task cancelled")
				return
			}
			s.runtime.FailAgentRun(runID, err.Error())
			return
		}
		result, err := s.runtime.RunStream(ctx, expanded, record)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.runtime.CancelAgentRun(runID, "scheduled agent task cancelled")
				return
			}
			s.runtime.FailAgentRun(runID, err.Error())
			return
		}
		s.runtime.CompleteAgentRun(runID, agentRunStatusForResult(result), result)
	}()
	return runID, nil
}

func (s *Server) startScheduledWorkflowTask(schedule scheduler.Schedule, job scheduler.Job) (string, error) {
	if !s.workflowExecMu.TryLock() {
		return "", fmt.Errorf("another workflow run is already active")
	}
	started := make(chan string, 1)
	done := make(chan error, 1)
	ctx := agent.WithWorkflowSchedule(context.Background(), schedule.ID, job.ID)
	ctx = agent.WithReadOnlyTools(ctx)
	ctx = agent.WithExecutionTarget(ctx, schedule.Task.TargetID)
	go func() {
		defer s.workflowExecMu.Unlock()
		_, err := s.runWorkflowWithCancelHook(ctx, schedule.Task.WorkflowName, schedule.Task.Request, "", false, nil, func(runID string) {
			select {
			case started <- runID:
			default:
			}
		})
		done <- err
	}()
	select {
	case runID := <-started:
		return runID, nil
	case err := <-done:
		if err == nil {
			return "", fmt.Errorf("scheduled workflow completed before allocating a run id")
		}
		return "", err
	case <-time.After(5 * time.Second):
		return "", fmt.Errorf("scheduled workflow did not allocate a run id in time")
	}
}

func (s *Server) FindScheduledTask(jobID string) (scheduler.TaskRunState, bool) {
	jobID = strings.TrimSpace(jobID)
	if s == nil || s.runtime == nil || jobID == "" {
		return scheduler.TaskRunState{}, false
	}
	for _, run := range s.runtime.AgentRuns() {
		if run.ScheduleJobID == jobID {
			return scheduledAgentRunState(run), true
		}
	}
	for _, run := range s.runtime.SessionSnapshot().WorkflowRuns {
		if run.ScheduleJobID == jobID {
			return scheduledWorkflowRunState(run), true
		}
	}
	return scheduler.TaskRunState{}, false
}

func (s *Server) ScheduledTask(runID string) (scheduler.TaskRunState, bool) {
	if s == nil || s.runtime == nil {
		return scheduler.TaskRunState{}, false
	}
	if run, ok := s.runtime.AgentRun(runID); ok {
		return scheduledAgentRunState(run), true
	}
	for _, run := range s.runtime.SessionSnapshot().WorkflowRuns {
		if run.ID == strings.TrimSpace(runID) {
			return scheduledWorkflowRunState(run), true
		}
	}
	return scheduler.TaskRunState{}, false
}

func scheduledAgentRunState(run session.AgentRunSnapshot) scheduler.TaskRunState {
	terminal := agentRunCollectionRunTerminal(run)
	return scheduler.TaskRunState{
		RunID: run.ID, Status: run.Status, Error: run.Error, Terminal: terminal,
		Success:          terminal && strings.EqualFold(strings.TrimSpace(run.Status), "completed"),
		AwaitingApproval: strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_tool_approval"),
	}
}

func scheduledWorkflowRunState(run session.WorkflowRunSnapshot) scheduler.TaskRunState {
	terminal := workflowRunCollectionRunTerminal(run)
	return scheduler.TaskRunState{
		RunID: run.ID, Status: run.Status, Error: workflowRunErrorText(run), Terminal: terminal,
		Success:          terminal && strings.EqualFold(strings.TrimSpace(run.Status), "completed"),
		AwaitingApproval: workflowRunCollectionRunNeedsAction(run),
	}
}

func workflowRunErrorText(run session.WorkflowRunSnapshot) string {
	for index := len(run.Events) - 1; index >= 0; index-- {
		if run.Events[index].IsError {
			return strings.TrimSpace(run.Events[index].Content)
		}
	}
	return strings.TrimSpace(run.Summary)
}

func (s *Server) CancelScheduledTask(runID, reason string) bool {
	if s == nil || s.runtime == nil {
		return false
	}
	if run, ok := s.runtime.AgentRun(runID); ok {
		if cancel, active := s.agentRunCancelFunc(run.ID); active {
			s.runtime.RequestAgentRunCancel(run.ID, reason)
			cancel()
			return true
		}
		if agentRunCollectionRunActive(run) {
			_, ok = s.runtime.CancelAgentRunAndApprovals(run.ID, reason)
			return ok
		}
	}
	for _, run := range s.runtime.SessionSnapshot().WorkflowRuns {
		if run.ID != strings.TrimSpace(runID) {
			continue
		}
		if cancel, active := s.workflowRunCancelFunc(run.ID); active {
			s.runtime.RequestWorkflowRunCancel(run.ID, reason)
			cancel()
			return true
		}
		if workflowRunCollectionRunActive(run) {
			_, ok := s.runtime.CancelWorkflowRunAndApprovals(run.ID, reason)
			return ok
		}
	}
	return false
}
