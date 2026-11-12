package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/taskstaging"
)

type stagedChangesApplyResponse struct {
	RunID    string                  `json:"run_id"`
	Status   string                  `json:"status"`
	Manifest taskstaging.Manifest    `json:"manifest"`
	Apply    taskstaging.ApplyResult `json:"apply"`
}

type stagedChangesRestoreResponse struct {
	RunID  string                 `json:"run_id"`
	Status string                 `json:"status"`
	State  taskstaging.ApplyState `json:"recovery"`
}

type stagedChangesDiscardResponse struct {
	RunID    string               `json:"run_id"`
	Status   string               `json:"status"`
	Manifest taskstaging.Manifest `json:"manifest"`
}

func (s *Server) agentRunStagedChangesAction(run session.AgentRunSnapshot) (agentRunAction, bool) {
	if !agentRunCollectionRunTerminal(run) {
		return agentRunAction{}, false
	}
	manifest, state, exists, err := s.runtime.TaskStagingManifest(run.ID)
	if err != nil || !exists || len(manifest.Changes) == 0 {
		return agentRunAction{}, false
	}
	if manifest.WorkspaceMode == "live" && strings.EqualFold(strings.TrimSpace(state.Status), "restored") {
		return agentRunAction{}, false
	}
	if strings.EqualFold(strings.TrimSpace(state.Status), "applied") {
		label := "恢复应用前状态"
		reason := fmt.Sprintf("恢复编号 %s；恢复前会检查工作区是否又被修改", state.ApplyID)
		if manifest.WorkspaceMode == "live" {
			label = "回退到上一版本"
			reason = fmt.Sprintf("版本 %s；仅当工作区仍与该版本一致时允许回退", state.ApplyID)
		}
		return agentRunAction{
			Name:          "restore_staged_changes",
			Label:         label,
			Method:        http.MethodPost,
			Path:          agentRunActionPath(run.ID, "restore_staged_changes"),
			Available:     true,
			Durable:       true,
			Destructive:   true,
			Reason:        reason,
			ChangeCount:   len(manifest.Changes),
			RecoveryID:    state.ApplyID,
			WorkspaceMode: manifest.WorkspaceMode,
		}, true
	}
	if stagedApplyActive(state) {
		return agentRunAction{}, false
	}
	delivery := s.agentRunDeliveryManifest(run)
	available, reason := stagedChangesDeliveryAllowed(delivery)
	label := "应用已验证变更"
	if manifest.WorkspaceMode == "live" {
		label = "保存当前工作区版本"
		if available {
			reason = fmt.Sprintf("变更已在真实工作区生效；保存为可回退版本，并保留最近 %d 个版本", taskstaging.DefaultVersionRetention)
		}
	}
	return agentRunAction{
		Name:          "apply_staged_changes",
		Label:         label,
		Method:        http.MethodPost,
		Path:          agentRunActionPath(run.ID, "apply_staged_changes"),
		Available:     available,
		Durable:       available,
		Reason:        reason,
		ChangeCount:   len(manifest.Changes),
		WorkspaceMode: manifest.WorkspaceMode,
	}, true
}

func (s *Server) workflowRunStagedChangesAction(run session.WorkflowRunSnapshot) (workflowRunAction, bool) {
	if !workflowRunCollectionRunTerminal(run) {
		return workflowRunAction{}, false
	}
	manifest, state, exists, err := s.runtime.TaskStagingManifest(run.ID)
	if err != nil || !exists || len(manifest.Changes) == 0 {
		return workflowRunAction{}, false
	}
	if manifest.WorkspaceMode == "live" && strings.EqualFold(strings.TrimSpace(state.Status), "restored") {
		return workflowRunAction{}, false
	}
	if strings.EqualFold(strings.TrimSpace(state.Status), "applied") {
		label := "恢复应用前状态"
		reason := fmt.Sprintf("恢复编号 %s；恢复前会检查工作区是否又被修改", state.ApplyID)
		if manifest.WorkspaceMode == "live" {
			label = "回退到上一版本"
			reason = fmt.Sprintf("版本 %s；仅当工作区仍与该版本一致时允许回退", state.ApplyID)
		}
		return workflowRunAction{
			Name:          "restore_staged_changes",
			Label:         label,
			Method:        http.MethodPost,
			Path:          "/api/workflow-runs/" + strings.TrimSpace(run.ID) + "/restore-staged-changes",
			Available:     true,
			Durable:       true,
			Destructive:   true,
			Reason:        reason,
			ChangeCount:   len(manifest.Changes),
			RecoveryID:    state.ApplyID,
			WorkspaceMode: manifest.WorkspaceMode,
		}, true
	}
	if stagedApplyActive(state) {
		return workflowRunAction{}, false
	}
	delivery := s.workflowRunDeliveryManifest(run)
	available, reason := stagedChangesDeliveryAllowed(delivery)
	label := "应用已验证变更"
	if manifest.WorkspaceMode == "live" {
		label = "保存当前工作区版本"
		if available {
			reason = fmt.Sprintf("变更已在真实工作区生效；保存为可回退版本，并保留最近 %d 个版本", taskstaging.DefaultVersionRetention)
		}
	}
	return workflowRunAction{
		Name:          "apply_staged_changes",
		Label:         label,
		Method:        http.MethodPost,
		Path:          "/api/workflow-runs/" + strings.TrimSpace(run.ID) + "/apply-staged-changes",
		Available:     available,
		Durable:       available,
		Reason:        reason,
		ChangeCount:   len(manifest.Changes),
		WorkspaceMode: manifest.WorkspaceMode,
	}, true
}

func (s *Server) agentRunDiscardStagedChangesAction(run session.AgentRunSnapshot) (agentRunAction, bool) {
	if !agentRunCollectionRunTerminal(run) {
		return agentRunAction{}, false
	}
	manifest, state, exists, err := s.runtime.TaskStagingManifest(run.ID)
	if err != nil || !exists || len(manifest.Changes) == 0 || stagedApplyActive(state) || strings.EqualFold(strings.TrimSpace(state.Status), "discarded") {
		return agentRunAction{}, false
	}
	if manifest.WorkspaceMode == "live" && strings.EqualFold(strings.TrimSpace(state.Status), "restored") {
		return agentRunAction{}, false
	}
	label := "放弃暂存变更"
	reason := "删除任务副本并释放对应工具进程；真实工作区不会改变，审计清单仍会保留"
	if manifest.WorkspaceMode == "live" {
		label = "撤销本次工作区变更"
		reason = "把本次任务改动恢复到任务开始前；恢复前会检查文件是否又被修改"
	}
	return agentRunAction{
		Name:          "discard_staged_changes",
		Label:         label,
		Method:        http.MethodPost,
		Path:          agentRunActionPath(run.ID, "discard_staged_changes"),
		Available:     true,
		Durable:       true,
		Destructive:   true,
		Reason:        reason,
		ChangeCount:   len(manifest.Changes),
		WorkspaceMode: manifest.WorkspaceMode,
	}, true
}

func (s *Server) workflowRunDiscardStagedChangesAction(run session.WorkflowRunSnapshot) (workflowRunAction, bool) {
	if !workflowRunCollectionRunTerminal(run) {
		return workflowRunAction{}, false
	}
	manifest, state, exists, err := s.runtime.TaskStagingManifest(run.ID)
	if err != nil || !exists || len(manifest.Changes) == 0 || stagedApplyActive(state) || strings.EqualFold(strings.TrimSpace(state.Status), "discarded") {
		return workflowRunAction{}, false
	}
	if manifest.WorkspaceMode == "live" && strings.EqualFold(strings.TrimSpace(state.Status), "restored") {
		return workflowRunAction{}, false
	}
	label := "放弃暂存变更"
	reason := "删除任务副本并释放对应工具进程；真实工作区不会改变，审计清单仍会保留"
	if manifest.WorkspaceMode == "live" {
		label = "撤销本次工作区变更"
		reason = "把本次任务改动恢复到任务开始前；恢复前会检查文件是否又被修改"
	}
	return workflowRunAction{
		Name:          "discard_staged_changes",
		Label:         label,
		Method:        http.MethodPost,
		Path:          "/api/workflow-runs/" + strings.TrimSpace(run.ID) + "/discard-staged-changes",
		Available:     true,
		Durable:       true,
		Destructive:   true,
		Reason:        reason,
		ChangeCount:   len(manifest.Changes),
		WorkspaceMode: manifest.WorkspaceMode,
	}, true
}

func stagedApplyActive(state taskstaging.ApplyState) bool {
	switch strings.ToLower(strings.TrimSpace(state.Status)) {
	case "prepared", "applying", "applied":
		return true
	default:
		return false
	}
}

func stagedChangesDeliveryAllowed(delivery runDeliveryManifest) (bool, string) {
	if strings.EqualFold(strings.TrimSpace(delivery.Status), "blocked") {
		return false, "任务质量门禁未通过，不能保存为已验证版本；请先完成修复与复测"
	}
	switch strings.TrimSpace(delivery.Conclusion) {
	case "waiting_input", "running":
		return false, "任务仍在执行或等待输入，不能写回真实工作区"
	case "success":
		return true, "真实 Diff、验证证据和交付质量门均已通过，等待写回真实工作区"
	}
	if len(delivery.Changes) > 0 {
		return true, "暂存区已有可写回变更"
	}
	if len(delivery.Unverified) > 0 {
		return false, "暂存变更尚未通过质量门：" + strings.Join(delivery.Unverified, "；")
	}
	return false, "暂存变更尚未通过质量门，不能写回真实工作区"
}

func (s *Server) handleAgentRunApplyStagedChanges(w http.ResponseWriter, run session.AgentRunSnapshot) {
	delivery := s.agentRunDeliveryManifest(run)
	if allowed, reason := stagedChangesDeliveryAllowed(delivery); !allowed {
		http.Error(w, reason, http.StatusConflict)
		return
	}
	manifest, result, err := s.runtime.ApplyTaskStaging(run.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	content := fmt.Sprintf("已将 %d 个暂存变更写回真实工作区，恢复编号 %s", len(manifest.Changes), result.ApplyID)
	if manifest.WorkspaceMode == "live" {
		content = fmt.Sprintf("已保存当前工作区版本，共 %d 个文件变更，版本编号 %s；系统保留最近 %d 个版本", len(manifest.Changes), result.ApplyID, taskstaging.DefaultVersionRetention)
	}
	s.runtime.AppendAgentRunEvent(run.ID, session.AgentRunEventSnapshot{
		At:        time.Now().UTC().Format(time.RFC3339Nano),
		Type:      "staged_changes_applied",
		TaskStage: "deliver",
		Content:   content,
	})
	writeJSON(w, stagedChangesApplyResponse{RunID: run.ID, Status: "applied", Manifest: manifest, Apply: result})
	if manifest.WorkspaceMode != "live" {
		go func() { _ = s.runtime.ReleaseTaskStagingProcesses(run.ID) }()
	}
}

func (s *Server) handleWorkflowRunApplyStagedChanges(w http.ResponseWriter, run session.WorkflowRunSnapshot) {
	delivery := s.workflowRunDeliveryManifest(run)
	if allowed, reason := stagedChangesDeliveryAllowed(delivery); !allowed {
		http.Error(w, reason, http.StatusConflict)
		return
	}
	manifest, result, err := s.runtime.ApplyTaskStaging(run.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	content := fmt.Sprintf("已将 %d 个暂存变更写回真实工作区，恢复编号 %s", len(manifest.Changes), result.ApplyID)
	if manifest.WorkspaceMode == "live" {
		content = fmt.Sprintf("已保存当前工作区版本，共 %d 个文件变更，版本编号 %s；系统保留最近 %d 个版本", len(manifest.Changes), result.ApplyID, taskstaging.DefaultVersionRetention)
	}
	s.runtime.AppendWorkflowRunEvent(run.ID, session.WorkflowRunEventSnapshot{
		At:             time.Now().UTC().Format(time.RFC3339Nano),
		Type:           "staged_changes_applied",
		TaskStage:      "deliver",
		WorkflowName:   run.Name,
		WorkflowStatus: run.Status,
		Content:        content,
	})
	writeJSON(w, stagedChangesApplyResponse{RunID: run.ID, Status: "applied", Manifest: manifest, Apply: result})
	if manifest.WorkspaceMode != "live" {
		go func() { _ = s.runtime.ReleaseTaskStagingProcesses(run.ID) }()
	}
}

func (s *Server) handleAgentRunRestoreStagedChanges(w http.ResponseWriter, run session.AgentRunSnapshot) {
	manifest, _, _, _ := s.runtime.TaskStagingManifest(run.ID)
	state, err := s.runtime.RestoreTaskStaging(run.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	content := fmt.Sprintf("已恢复应用前状态，恢复编号 %s", state.ApplyID)
	if manifest.WorkspaceMode == "live" {
		content = fmt.Sprintf("已回退到上一工作区版本，版本编号 %s", state.ApplyID)
	}
	s.runtime.AppendAgentRunEvent(run.ID, session.AgentRunEventSnapshot{
		At:        time.Now().UTC().Format(time.RFC3339Nano),
		Type:      "staged_changes_restored",
		TaskStage: "deliver",
		Content:   content,
	})
	writeJSON(w, stagedChangesRestoreResponse{RunID: run.ID, Status: "restored", State: state})
}

func (s *Server) handleWorkflowRunRestoreStagedChanges(w http.ResponseWriter, run session.WorkflowRunSnapshot) {
	manifest, _, _, _ := s.runtime.TaskStagingManifest(run.ID)
	state, err := s.runtime.RestoreTaskStaging(run.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	content := fmt.Sprintf("已恢复应用前状态，恢复编号 %s", state.ApplyID)
	if manifest.WorkspaceMode == "live" {
		content = fmt.Sprintf("已回退到上一工作区版本，版本编号 %s", state.ApplyID)
	}
	s.runtime.AppendWorkflowRunEvent(run.ID, session.WorkflowRunEventSnapshot{
		At:             time.Now().UTC().Format(time.RFC3339Nano),
		Type:           "staged_changes_restored",
		TaskStage:      "deliver",
		WorkflowName:   run.Name,
		WorkflowStatus: run.Status,
		Content:        content,
	})
	writeJSON(w, stagedChangesRestoreResponse{RunID: run.ID, Status: "restored", State: state})
}

func (s *Server) handleAgentRunDiscardStagedChanges(w http.ResponseWriter, run session.AgentRunSnapshot) {
	before, _, _, _ := s.runtime.TaskStagingManifest(run.ID)
	manifest, err := s.runtime.DiscardTaskStaging(run.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	content := fmt.Sprintf("已放弃 %d 个暂存变更，真实工作区未改变", len(manifest.Changes))
	if before.WorkspaceMode == "live" {
		content = fmt.Sprintf("已撤销本次任务的 %d 个工作区文件变更", len(manifest.Changes))
	}
	s.runtime.AppendAgentRunEvent(run.ID, session.AgentRunEventSnapshot{
		At:        time.Now().UTC().Format(time.RFC3339Nano),
		Type:      "staged_changes_discarded",
		TaskStage: "deliver",
		Content:   content,
	})
	writeJSON(w, stagedChangesDiscardResponse{RunID: run.ID, Status: "discarded", Manifest: manifest})
}

func (s *Server) handleWorkflowRunDiscardStagedChanges(w http.ResponseWriter, run session.WorkflowRunSnapshot) {
	before, _, _, _ := s.runtime.TaskStagingManifest(run.ID)
	manifest, err := s.runtime.DiscardTaskStaging(run.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	content := fmt.Sprintf("已放弃 %d 个暂存变更，真实工作区未改变", len(manifest.Changes))
	if before.WorkspaceMode == "live" {
		content = fmt.Sprintf("已撤销本次任务的 %d 个工作区文件变更", len(manifest.Changes))
	}
	s.runtime.AppendWorkflowRunEvent(run.ID, session.WorkflowRunEventSnapshot{
		At:             time.Now().UTC().Format(time.RFC3339Nano),
		Type:           "staged_changes_discarded",
		TaskStage:      "deliver",
		WorkflowName:   run.Name,
		WorkflowStatus: run.Status,
		Content:        content,
	})
	writeJSON(w, stagedChangesDiscardResponse{RunID: run.ID, Status: "discarded", Manifest: manifest})
}
