package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

type resourceIntegrityError struct{ code, kind, name, runID string }

func (e *resourceIntegrityError) Error() string {
	if e.code == "resource_in_use" {
		return fmt.Sprintf("resource_in_use: 资源 %s/%q 正被运行中的任务 %s 引用，请在任务停止后重试。", e.kind, e.name, e.runID)
	}
	return fmt.Sprintf("resource_pin_drift: 资源 %s/%q 已变更或不可用，请使用当前资源创建新任务。", e.kind, e.name)
}

func resourceErrorStatus(err error, fallback int) int {
	var conflict *resourceIntegrityError
	if errors.As(err, &conflict) {
		return http.StatusConflict
	}
	return fallback
}

func runActionNeedsCurrentResources(action string) bool {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(action)), "-", "_") {
	case "continue", "continue_output", "retry", "approve", "approve_tool", "approve_tools", "approve_task_scope", "approve_remember_tool", "approve_all_tools", "approve_remember_all_tools", "input", "resume_sub_workflow", "approve_budget", "escalate_model":
		return true
	}
	return false
}

func (s *Server) ensureResourceNotInUse(kind, name string) error {
	if s == nil || s.runtime == nil {
		return nil
	}
	name = normalizeResourceName(name)
	matches := func(pin session.ResourcePin) bool {
		return strings.EqualFold(pin.Name, name) && (pin.Kind == kind || kind == "workflow" && pin.Kind == "workflow_template")
	}
	snapshot := s.runtime.SessionSnapshot()
	for _, run := range snapshot.AgentRuns {
		if !strings.EqualFold(strings.TrimSpace(run.Status), "running") && !strings.EqualFold(strings.TrimSpace(run.Status), "cancelling") {
			continue
		}
		for _, pin := range run.ResourcePins {
			if matches(pin) {
				return &resourceIntegrityError{code: "resource_in_use", kind: kind, name: name, runID: run.ID}
			}
		}
		if kind == "agent" && strings.EqualFold(run.AgentID, name) {
			return &resourceIntegrityError{code: "resource_in_use", kind: kind, name: name, runID: run.ID}
		}
	}
	for _, run := range snapshot.WorkflowRuns {
		if !strings.EqualFold(strings.TrimSpace(run.Status), "running") && !strings.EqualFold(strings.TrimSpace(run.Status), "cancelling") {
			continue
		}
		for _, pin := range run.ResourcePins {
			if matches(pin) {
				return &resourceIntegrityError{code: "resource_in_use", kind: kind, name: name, runID: run.ID}
			}
		}
		if kind == "workflow" && strings.EqualFold(run.Name, name) {
			return &resourceIntegrityError{code: "resource_in_use", kind: kind, name: name, runID: run.ID}
		}
	}
	return nil
}

// These compare public configuration identities, not binaries, credentials or external state.
func (s *Server) ensureResourcePinsCurrent(pins []session.ResourcePin) error {
	for _, wanted := range pins {
		var candidates []session.ResourcePin
		switch wanted.Kind {
		case "agent":
			candidates = s.agentAndProviderResourcePins(wanted.Name)
		case "provider":
			candidates = s.providerAndFallbackResourcePins(wanted.Name, nil)
		case "mcp":
			if server, ok := s.runtimeMCPServerForTool(wanted.Name + "/tool"); ok {
				candidates = []session.ResourcePin{mcpServerResourcePin(server)}
			}
		case "skill":
			if skill, ok := s.runtimeSkillByName(wanted.Name); ok {
				candidates = s.skillAndMCPResourcePins(skill)
			}
		case "workflow":
			candidates = s.workflowTaskResourcePins(wanted.Name)
		case "kit":
			if doc, ok := s.kitResourceByName(wanted.Name); ok && doc.Routing != nil {
				candidates = s.kitRoutingResourcePins(doc)
			}
		case "workflow_template":
			if item, ok := s.runtime.WorkflowRunner().WorkflowTemplate(wanted.Name); ok {
				candidates = []session.ResourcePin{session.NewResourcePin(wanted.Kind, wanted.Name, item)}
			}
		case "team_template":
			if item, ok := s.runtime.WorkflowRunner().TeamTemplate(wanted.Name); ok {
				candidates = []session.ResourcePin{session.NewResourcePin(wanted.Kind, wanted.Name, item)}
			}
		case "policy_rule":
			if item, ok := s.policyRuleResourceByName(wanted.Name); ok {
				item.Path = ""
				candidates = []session.ResourcePin{session.NewResourcePin(wanted.Kind, wanted.Name, item)}
			}
		default:
			continue // Older/unknown pin kinds have no compatible API comparator.
		}
		matched := false
		for _, current := range candidates {
			if current.Kind == wanted.Kind && strings.EqualFold(current.Name, wanted.Name) && current.Version == wanted.Version {
				matched = true
				break
			}
		}
		if !matched {
			return &resourceIntegrityError{code: "resource_pin_drift", kind: wanted.Kind, name: wanted.Name}
		}
	}
	return nil
}

func (s *Server) requireResourcePinsCurrent(w http.ResponseWriter, pins []session.ResourcePin) bool {
	if err := s.ensureResourcePinsCurrent(pins); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return false
	}
	return true
}

func (s *Server) approvalResourcePins(callID string) []session.ResourcePin {
	snapshot := s.runtime.SessionSnapshot()
	for _, pending := range snapshot.PendingApprovals {
		if pending.CallID != callID {
			continue
		}
		for _, run := range snapshot.AgentRuns {
			if run.ID == pending.AgentRunID {
				return run.ResourcePins
			}
		}
		for _, run := range snapshot.WorkflowRuns {
			if run.PendingCallID == pending.CallID && run.Name == pending.WorkflowName {
				return run.ResourcePins
			}
		}
	}
	return nil
}

func (s *Server) saveWorkflowGraphResource(name string, doc agent.WorkflowGraphDocument) error {
	if err := s.ensureResourceNotInUse("workflow", name); err != nil {
		return err
	}
	return s.runtime.WorkflowRunner().SaveWorkflowGraphDocument(name, doc)
}

func (s *Server) deleteWorkflowGraphResource(name string) error {
	if err := s.ensureResourceNotInUse("workflow", name); err != nil {
		return err
	}
	return s.runtime.WorkflowRunner().DeleteWorkflowGraphDocument(name)
}

func (s *Server) saveWorkflowTemplateResource(name string, doc agent.WorkflowTemplate) (agent.WorkflowTemplate, error) {
	if err := s.ensureResourceNotInUse("workflow", name); err != nil {
		return agent.WorkflowTemplate{}, err
	}
	return s.runtime.SaveWorkflowTemplateResource(name, doc)
}
