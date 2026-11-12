package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	WorkflowToolOperationStarted   = "started"
	WorkflowToolOperationCompleted = "completed"
	WorkflowToolOperationFailed    = "failed"
)

// ReserveWorkflowToolOperation writes a started record before an MCP call.
// An existing record is returned without modification so callers can replay a
// known result or block an invocation whose outcome is uncertain.
func (s *State) ReserveWorkflowToolOperation(operation WorkflowToolOperationSnapshot) (WorkflowToolOperationSnapshot, bool, error) {
	if s == nil {
		return WorkflowToolOperationSnapshot{}, false, fmt.Errorf("session state is not configured")
	}
	operation.Key = strings.TrimSpace(operation.Key)
	operation.RunID = strings.TrimSpace(operation.RunID)
	operation.Workflow = strings.TrimSpace(operation.Workflow)
	operation.Stage = strings.TrimSpace(operation.Stage)
	operation.ToolName = strings.TrimSpace(operation.ToolName)
	operation.ArgumentsHash = strings.TrimSpace(operation.ArgumentsHash)
	operation.WorkspaceHash = strings.TrimSpace(operation.WorkspaceHash)
	if operation.Key == "" || operation.RunID == "" || operation.Stage == "" || operation.ToolName == "" || operation.ArgumentsHash == "" {
		return WorkflowToolOperationSnapshot{}, false, fmt.Errorf("workflow tool operation requires key, run, stage, tool, and arguments hash")
	}
	if operation.Reusable && operation.WorkspaceHash == "" {
		return WorkflowToolOperationSnapshot{}, false, fmt.Errorf("reusable workflow tool operation requires a workspace hash")
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()

	s.mu.Lock()
	index := s.workflowRunIndexLocked(operation.RunID)
	if index < 0 {
		s.mu.Unlock()
		return WorkflowToolOperationSnapshot{}, false, fmt.Errorf("workflow run not found: %s", operation.RunID)
	}
	run := s.workflowRuns[index]
	for operationIndex, existing := range run.ToolOperations {
		if existing.Key == operation.Key {
			if existing.Status == WorkflowToolOperationFailed && existing.Reusable {
				previous := existing
				now := workflowRunTimestamp(time.Now().UTC())
				existing.Stage = operation.Stage
				existing.Status = WorkflowToolOperationStarted
				existing.StartedAt = now
				existing.UpdatedAt = now
				existing.Result = schema.ToolResult{}
				run.ToolOperations[operationIndex] = existing
				run.UpdatedAt = now
				s.workflowRuns[index] = run
				s.mu.Unlock()
				if err := s.PersistConfigured(); err != nil {
					s.mu.Lock()
					if currentIndex := s.workflowRunIndexLocked(operation.RunID); currentIndex >= 0 {
						currentRun := s.workflowRuns[currentIndex]
						for i := range currentRun.ToolOperations {
							if currentRun.ToolOperations[i].Key == operation.Key && currentRun.ToolOperations[i].Status == WorkflowToolOperationStarted {
								currentRun.ToolOperations[i] = previous
								s.workflowRuns[currentIndex] = currentRun
								break
							}
						}
					}
					s.mu.Unlock()
					return existing, true, fmt.Errorf("persist workflow tool operation retry: %w", err)
				}
				return existing, true, nil
			}
			s.mu.Unlock()
			return existing, false, nil
		}
	}
	if len(run.ToolOperations) >= maxWorkflowToolOperations {
		s.mu.Unlock()
		return WorkflowToolOperationSnapshot{}, false, fmt.Errorf("workflow tool operation limit reached for run %s: %d", operation.RunID, maxWorkflowToolOperations)
	}
	now := workflowRunTimestamp(time.Now().UTC())
	operation.Status = WorkflowToolOperationStarted
	operation.StartedAt = now
	operation.UpdatedAt = now
	operation.Result = schema.ToolResult{}
	run.ToolOperations = append(run.ToolOperations, operation)
	run.UpdatedAt = now
	s.workflowRuns[index] = run
	s.mu.Unlock()
	if err := s.PersistConfigured(); err != nil {
		return operation, true, fmt.Errorf("persist workflow tool operation start: %w", err)
	}
	return operation, true, nil
}

// CompleteWorkflowToolOperation stores the normalized result after the MCP
// call. A completed record is immutable and can be safely replayed.
func (s *State) CompleteWorkflowToolOperation(runID, key string, result schema.ToolResult) (WorkflowToolOperationSnapshot, error) {
	return s.finishWorkflowToolOperation(runID, key, result, WorkflowToolOperationCompleted)
}

// FailReusableWorkflowToolOperation records a deterministic validation failure.
// Reusable failures are never replayed and may be attempted again safely.
func (s *State) FailReusableWorkflowToolOperation(runID, key string, result schema.ToolResult) (WorkflowToolOperationSnapshot, error) {
	return s.finishWorkflowToolOperation(runID, key, result, WorkflowToolOperationFailed)
}

// ReleaseWorkflowToolOperation drops a started record so a later approval can
// execute the same arguments instead of replaying a control placeholder.
func (s *State) ReleaseWorkflowToolOperation(runID, key string) error {
	if s == nil {
		return fmt.Errorf("session state is not configured")
	}
	runID = strings.TrimSpace(runID)
	key = strings.TrimSpace(key)
	if runID == "" || key == "" {
		return fmt.Errorf("workflow tool operation release requires run and key")
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()
	s.mu.Lock()
	index := s.workflowRunIndexLocked(runID)
	if index < 0 {
		s.mu.Unlock()
		return fmt.Errorf("workflow run not found: %s", runID)
	}
	run := s.workflowRuns[index]
	kept := run.ToolOperations[:0]
	found := false
	for _, item := range run.ToolOperations {
		if item.Key == key {
			found = true
			if item.Status != WorkflowToolOperationStarted {
				s.mu.Unlock()
				return fmt.Errorf("workflow tool operation %s cannot be released from %s", key, item.Status)
			}
			continue
		}
		kept = append(kept, item)
	}
	if !found {
		s.mu.Unlock()
		return fmt.Errorf("workflow tool operation not found: %s", key)
	}
	run.ToolOperations = kept
	run.UpdatedAt = workflowRunTimestamp(time.Now().UTC())
	s.workflowRuns[index] = run
	s.mu.Unlock()
	return s.PersistConfigured()
}

func (s *State) finishWorkflowToolOperation(runID, key string, result schema.ToolResult, status string) (WorkflowToolOperationSnapshot, error) {
	if s == nil {
		return WorkflowToolOperationSnapshot{}, fmt.Errorf("session state is not configured")
	}
	runID = strings.TrimSpace(runID)
	key = strings.TrimSpace(key)
	if runID == "" || key == "" {
		return WorkflowToolOperationSnapshot{}, fmt.Errorf("workflow tool operation completion requires run and key")
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()

	s.mu.Lock()
	index := s.workflowRunIndexLocked(runID)
	if index < 0 {
		s.mu.Unlock()
		return WorkflowToolOperationSnapshot{}, fmt.Errorf("workflow run not found: %s", runID)
	}
	run := s.workflowRuns[index]
	operationIndex := -1
	for i := range run.ToolOperations {
		if run.ToolOperations[i].Key == key {
			operationIndex = i
			break
		}
	}
	if operationIndex < 0 {
		s.mu.Unlock()
		return WorkflowToolOperationSnapshot{}, fmt.Errorf("workflow tool operation not found: %s", key)
	}
	operation := run.ToolOperations[operationIndex]
	if operation.Status == status {
		s.mu.Unlock()
		return operation, nil
	}
	if status == WorkflowToolOperationFailed && !operation.Reusable {
		s.mu.Unlock()
		return WorkflowToolOperationSnapshot{}, fmt.Errorf("only reusable workflow tool operations may be marked failed")
	}
	if operation.Status != WorkflowToolOperationStarted {
		s.mu.Unlock()
		return WorkflowToolOperationSnapshot{}, fmt.Errorf("workflow tool operation %s cannot transition from %s to %s", key, operation.Status, status)
	}
	previous := operation
	operation.Status = status
	operation.UpdatedAt = workflowRunTimestamp(time.Now().UTC())
	operation.Result = result
	run.ToolOperations[operationIndex] = operation
	run.UpdatedAt = operation.UpdatedAt
	s.workflowRuns[index] = run
	s.mu.Unlock()
	if err := s.PersistConfigured(); err != nil {
		s.mu.Lock()
		if currentIndex := s.workflowRunIndexLocked(runID); currentIndex >= 0 {
			currentRun := s.workflowRuns[currentIndex]
			for i := range currentRun.ToolOperations {
				if currentRun.ToolOperations[i].Key == key && currentRun.ToolOperations[i].Status == status {
					currentRun.ToolOperations[i] = previous
					s.workflowRuns[currentIndex] = currentRun
					break
				}
			}
		}
		s.mu.Unlock()
		return operation, fmt.Errorf("persist workflow tool operation result: %w", err)
	}
	return operation, nil
}
