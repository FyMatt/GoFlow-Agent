// Package slicescheduler provides deterministic scheduling for durable change
// slices. It contains no model or tool execution logic.
package slicescheduler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

const (
	ModeRead  = "read"
	ModeWrite = "write"

	StatusPlanned     = "planned"
	StatusPending     = "pending"
	StatusRunning     = "running"
	StatusImplemented = "implemented"
	StatusVerified    = "verified"
	StatusBlocked     = "blocked"
)

type Batch struct {
	IDs      []string `json:"ids"`
	Parallel bool     `json:"parallel"`
	Mode     string   `json:"mode"`
	Reason   string   `json:"reason,omitempty"`
}

type Plan struct {
	Slices []session.WorkflowChangeSliceSnapshot
}

func New(slices []session.WorkflowChangeSliceSnapshot) (Plan, error) {
	plan := Plan{Slices: copySlices(slices)}
	if err := Validate(plan.Slices); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func Validate(slices []session.WorkflowChangeSliceSnapshot) error {
	if len(slices) == 0 {
		return fmt.Errorf("at least one change slice is required")
	}
	byID := make(map[string]session.WorkflowChangeSliceSnapshot, len(slices))
	for _, slice := range slices {
		id := strings.TrimSpace(slice.ID)
		if id == "" {
			return fmt.Errorf("change slice id is required")
		}
		if _, exists := byID[id]; exists {
			return fmt.Errorf("duplicate change slice id %q", id)
		}
		mode := normalizeMode(slice.ExecutionMode)
		if mode != ModeRead && mode != ModeWrite {
			return fmt.Errorf("change slice %q has unsupported execution mode %q", id, slice.ExecutionMode)
		}
		byID[id] = slice
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("change slice dependency cycle includes %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range byID[id].DependsOn {
			dependency = strings.TrimSpace(dependency)
			if _, exists := byID[dependency]; !exists {
				return fmt.Errorf("change slice %q depends on unknown slice %q", id, dependency)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	ids := sortedIDs(byID)
	for _, id := range ids {
		if err := visit(id); err != nil {
			return err
		}
	}
	if err := validateWriteConflicts(byID); err != nil {
		return err
	}
	return nil
}

func validateWriteConflicts(slices map[string]session.WorkflowChangeSliceSnapshot) error {
	ids := sortedIDs(slices)
	for index, leftID := range ids {
		left := slices[leftID]
		if normalizeMode(left.ExecutionMode) != ModeWrite {
			continue
		}
		for _, rightID := range ids[index+1:] {
			right := slices[rightID]
			if normalizeMode(right.ExecutionMode) != ModeWrite || !targetFilesOverlap(left.TargetFiles, right.TargetFiles) {
				continue
			}
			if dependsOn(slices, leftID, rightID) || dependsOn(slices, rightID, leftID) {
				continue
			}
			return fmt.Errorf("write slices %q and %q overlap without an explicit dependency", leftID, rightID)
		}
	}
	return nil
}

func targetFilesOverlap(left, right []string) bool {
	return session.ChangeSliceTargetsOverlap(left, right)
}

func dependsOn(slices map[string]session.WorkflowChangeSliceSnapshot, sliceID, dependencyID string) bool {
	visited := make(map[string]bool)
	var visit func(string) bool
	visit = func(id string) bool {
		if id == dependencyID {
			return true
		}
		if visited[id] {
			return false
		}
		visited[id] = true
		for _, dependency := range slices[id].DependsOn {
			if visit(dependency) {
				return true
			}
		}
		return false
	}
	return visit(sliceID)
}

// ReadyBatches returns deterministic work that can be started now. Read-only
// slices form one parallel batch; write slices are emitted one at a time.
func (p Plan) ReadyBatches() ([]Batch, error) {
	if err := Validate(p.Slices); err != nil {
		return nil, err
	}
	completed := make(map[string]bool)
	blocked := make(map[string]bool)
	for _, slice := range p.Slices {
		switch strings.ToLower(strings.TrimSpace(slice.Status)) {
		case StatusImplemented, StatusVerified:
			completed[slice.ID] = true
		case StatusBlocked:
			blocked[slice.ID] = true
		}
	}
	readyReads := make([]string, 0)
	readyWrites := make([]string, 0)
	byID := make(map[string]session.WorkflowChangeSliceSnapshot, len(p.Slices))
	for _, slice := range p.Slices {
		byID[slice.ID] = slice
	}
	for _, id := range sortedIDs(byID) {
		slice := byID[id]
		if completed[id] || blocked[id] || strings.EqualFold(strings.TrimSpace(slice.Status), StatusRunning) {
			continue
		}
		depsReady := true
		for _, dependency := range slice.DependsOn {
			if blocked[dependency] {
				depsReady = false
				break
			}
			if !completed[dependency] {
				depsReady = false
				break
			}
		}
		if !depsReady {
			continue
		}
		if normalizeMode(slice.ExecutionMode) == ModeRead {
			readyReads = append(readyReads, id)
		} else {
			readyWrites = append(readyWrites, id)
		}
	}
	batches := make([]Batch, 0, len(readyWrites)+1)
	if len(readyReads) > 0 {
		batches = append(batches, Batch{IDs: readyReads, Parallel: true, Mode: ModeRead, Reason: "independent read-only slices"})
	}
	for _, id := range readyWrites {
		batches = append(batches, Batch{IDs: []string{id}, Parallel: false, Mode: ModeWrite, Reason: "write slices execute in dependency order"})
	}
	return batches, nil
}

func (p Plan) Start(id string) (Plan, error) {
	return p.transition(id, StatusRunning)
}

func (p Plan) Complete(id, status string) (Plan, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != StatusImplemented && status != StatusVerified && status != StatusBlocked {
		return Plan{}, fmt.Errorf("unsupported completion status %q", status)
	}
	return p.transition(id, status)
}

// RecoverInterrupted converts in-flight slices to pending. It deliberately
// does not convert implemented or verified slices, preventing side-effect
// replay after a process restart.
func (p Plan) RecoverInterrupted() (Plan, int, error) {
	if err := Validate(p.Slices); err != nil {
		return Plan{}, 0, err
	}
	out := copySlices(p.Slices)
	recovered := 0
	for i := range out {
		if strings.EqualFold(strings.TrimSpace(out[i].Status), StatusRunning) {
			out[i].Status = StatusPending
			out[i].BlockedReason = ""
			recovered++
		}
	}
	return Plan{Slices: out}, recovered, nil
}

func (p Plan) transition(id, status string) (Plan, error) {
	if err := Validate(p.Slices); err != nil {
		return Plan{}, err
	}
	id = strings.TrimSpace(id)
	out := copySlices(p.Slices)
	found := false
	for i := range out {
		if out[i].ID != id {
			continue
		}
		found = true
		current := strings.ToLower(strings.TrimSpace(out[i].Status))
		if current == StatusVerified && status != StatusVerified {
			return Plan{}, fmt.Errorf("verified slice %q cannot move backward", id)
		}
		if current == StatusBlocked && status != StatusPending {
			return Plan{}, fmt.Errorf("blocked slice %q requires explicit pending reset", id)
		}
		if status == StatusRunning && !dependenciesCompleted(out, out[i].DependsOn) {
			return Plan{}, fmt.Errorf("dependencies for slice %q are not complete", id)
		}
		if status == StatusRunning {
			out[i].Attempt++
		}
		out[i].Status = status
		if status == StatusBlocked {
			out[i].BlockedReason = "execution or verification failed"
		}
		break
	}
	if !found {
		return Plan{}, fmt.Errorf("change slice %q was not found", id)
	}
	return Plan{Slices: out}, nil
}

func dependenciesCompleted(slices []session.WorkflowChangeSliceSnapshot, dependencies []string) bool {
	completed := make(map[string]bool, len(slices))
	for _, slice := range slices {
		if slice.Status == StatusImplemented || slice.Status == StatusVerified {
			completed[slice.ID] = true
		}
	}
	for _, dependency := range dependencies {
		if !completed[dependency] {
			return false
		}
	}
	return true
}

func normalizeMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return ModeWrite
	}
	return mode
}

func sortedIDs(slices map[string]session.WorkflowChangeSliceSnapshot) []string {
	ids := make([]string, 0, len(slices))
	for id := range slices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func copySlices(slices []session.WorkflowChangeSliceSnapshot) []session.WorkflowChangeSliceSnapshot {
	out := make([]session.WorkflowChangeSliceSnapshot, len(slices))
	for i, slice := range slices {
		out[i] = slice
		out[i].DependsOn = append([]string(nil), slice.DependsOn...)
		out[i].InputFiles = append([]string(nil), slice.InputFiles...)
		out[i].TargetFiles = append([]string(nil), slice.TargetFiles...)
		out[i].VerificationCommands = append([]string(nil), slice.VerificationCommands...)
		out[i].EvidenceStages = append([]string(nil), slice.EvidenceStages...)
		out[i].ChangedFiles = append([]string(nil), slice.ChangedFiles...)
		if slice.InputHashes != nil {
			out[i].InputHashes = make(map[string]string, len(slice.InputHashes))
			for key, value := range slice.InputHashes {
				out[i].InputHashes[key] = value
			}
		}
	}
	return out
}
