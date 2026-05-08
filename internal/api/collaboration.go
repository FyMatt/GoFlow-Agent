package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func (s *Server) handleCollaborationMessages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.CollaborationMessages(collaborationFilterFromRequest(r)))
	case http.MethodPost:
		var doc session.CollaborationMessageSnapshot
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(doc.Content) == "" && strings.TrimSpace(doc.Subject) == "" {
			http.Error(w, "message content or subject is required", http.StatusBadRequest)
			return
		}
		writeJSON(w, s.runtime.AddCollaborationMessage(doc))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleBlackboardCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.BlackboardEntries(collaborationFilterFromRequest(r)))
	case http.MethodPost:
		var doc session.BlackboardEntrySnapshot
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(doc.Title) == "" && strings.TrimSpace(doc.Content) == "" {
			http.Error(w, "blackboard title or content is required", http.StatusBadRequest)
			return
		}
		writeJSON(w, s.runtime.UpsertBlackboardEntry(doc))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleBlackboardItem(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/collaboration/blackboard/"), "/")
	parts := strings.Split(tail, "/")
	if tail == "" || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		action := strings.TrimSpace(parts[1])
		if action == "" {
			http.NotFound(w, r)
			return
		}
		s.handleBlackboardItemAction(w, r, id, action)
		return
	}
	switch r.Method {
	case http.MethodGet:
		entry, ok := s.runtime.BlackboardEntry(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, entry)
	case http.MethodPut:
		var doc session.BlackboardEntrySnapshot
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(doc.ID) != "" && strings.TrimSpace(doc.ID) != id {
			http.Error(w, "blackboard id does not match URL", http.StatusBadRequest)
			return
		}
		doc.ID = id
		if strings.TrimSpace(doc.Title) == "" && strings.TrimSpace(doc.Content) == "" {
			http.Error(w, "blackboard title or content is required", http.StatusBadRequest)
			return
		}
		if existing, ok := s.runtime.BlackboardEntry(id); ok {
			doc = mergeBlackboardUpdate(existing, doc)
		}
		writeJSON(w, s.runtime.UpsertBlackboardEntry(doc))
	case http.MethodDelete:
		if !s.runtime.DeleteBlackboardEntry(id) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type blackboardLifecycleRequest struct {
	Content       string            `json:"content,omitempty"`
	Note          string            `json:"note,omitempty"`
	AgentID       string            `json:"agent_id,omitempty"`
	OwnerAgent    string            `json:"owner_agent,omitempty"`
	OwnerRole     string            `json:"owner_role,omitempty"`
	AssignedAgent string            `json:"assigned_agent,omitempty"`
	AssignedTo    string            `json:"assigned_to,omitempty"`
	EscalateTo    string            `json:"escalate_to,omitempty"`
	Severity      string            `json:"severity,omitempty"`
	Priority      string            `json:"priority,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

func (s *Server) handleBlackboardItemAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	entry, ok := s.runtime.BlackboardEntry(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var doc blackboardLifecycleRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil && err != io.EOF {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
	}
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "resolve":
		entry.Status = "resolved"
	case "reopen":
		entry.Status = "open"
	case "assign":
		assignedAgent := firstBlackboardLifecycleValue(doc.AssignedAgent, doc.AssignedTo, doc.OwnerAgent, doc.AgentID, doc.Metadata["assigned_agent"], doc.Metadata["owner_agent"], doc.Metadata["agent_id"])
		ownerRole := firstBlackboardLifecycleValue(doc.OwnerRole, doc.Metadata["owner_role"])
		if assignedAgent == "" && ownerRole == "" {
			http.Error(w, "assigned agent or owner role is required", http.StatusBadRequest)
			return
		}
		entry.Status = "open"
		if assignedAgent != "" {
			entry.AgentID = assignedAgent
		}
	case "escalate":
		escalatedTo := firstBlackboardLifecycleValue(doc.EscalateTo, doc.AssignedTo, doc.OwnerAgent, doc.AgentID, doc.Metadata["escalated_to"], doc.Metadata["assigned_agent"], doc.Metadata["owner_agent"], doc.Metadata["agent_id"])
		entry.Status = "escalated"
		if escalatedTo != "" {
			entry.AgentID = escalatedTo
		}
	default:
		http.NotFound(w, r)
		return
	}
	if strings.TrimSpace(doc.Content) != "" {
		entry.Content = strings.TrimSpace(doc.Content)
	}
	entry.Metadata = mergeBlackboardLifecycleMetadata(entry.Metadata, blackboardLifecycleMetadata(doc, action), action, doc.Note)
	writeJSON(w, s.runtime.UpsertBlackboardEntry(entry))
}

func (s *Server) handleTeamState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	writeJSON(w, s.runtime.TeamState(query.Get("run_id"), query.Get("team")))
}

func mergeBlackboardUpdate(existing, update session.BlackboardEntrySnapshot) session.BlackboardEntrySnapshot {
	if strings.TrimSpace(update.CreatedAt) == "" {
		update.CreatedAt = existing.CreatedAt
	}
	if strings.TrimSpace(update.Scope) == "" {
		update.Scope = existing.Scope
	}
	if strings.TrimSpace(update.RunID) == "" {
		update.RunID = existing.RunID
	}
	if strings.TrimSpace(update.Stage) == "" {
		update.Stage = existing.Stage
	}
	if strings.TrimSpace(update.AgentID) == "" {
		update.AgentID = existing.AgentID
	}
	if strings.TrimSpace(update.Kind) == "" {
		update.Kind = existing.Kind
	}
	if strings.TrimSpace(update.Status) == "" {
		update.Status = existing.Status
	}
	if len(update.Tags) == 0 {
		update.Tags = existing.Tags
	}
	if len(update.Metadata) == 0 {
		update.Metadata = existing.Metadata
	}
	return update
}

func mergeBlackboardLifecycleMetadata(existing, update map[string]string, action, note string) map[string]string {
	merged := make(map[string]string, len(existing)+len(update)+2)
	for key, value := range existing {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			merged[key] = value
		}
	}
	for key, value := range update {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			merged[key] = value
		}
	}
	action = strings.TrimSpace(action)
	if action != "" {
		merged["lifecycle_action"] = action
	}
	if note = strings.TrimSpace(note); note != "" {
		merged[action+"_note"] = note
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func blackboardLifecycleMetadata(doc blackboardLifecycleRequest, action string) map[string]string {
	metadata := make(map[string]string, len(doc.Metadata)+8)
	for key, value := range doc.Metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			metadata[key] = value
		}
	}
	if value := firstBlackboardLifecycleValue(doc.AgentID); value != "" {
		metadata["agent_id"] = value
	}
	if value := firstBlackboardLifecycleValue(doc.OwnerAgent); value != "" {
		metadata["owner_agent"] = value
	}
	if value := firstBlackboardLifecycleValue(doc.OwnerRole); value != "" {
		metadata["owner_role"] = value
	}
	if value := firstBlackboardLifecycleValue(doc.AssignedAgent, doc.AssignedTo); value != "" {
		metadata["assigned_agent"] = value
	}
	if value := firstBlackboardLifecycleValue(doc.EscalateTo); value != "" {
		metadata["escalated_to"] = value
	}
	if value := firstBlackboardLifecycleValue(doc.Severity); value != "" {
		metadata["severity"] = value
	}
	if value := firstBlackboardLifecycleValue(doc.Priority); value != "" {
		metadata["priority"] = value
	}
	if action == "escalate" {
		metadata["escalated"] = "true"
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func firstBlackboardLifecycleValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func collaborationFilterFromRequest(r *http.Request) session.CollaborationFilter {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(query.Get("limit")))
	if limit < 0 {
		limit = 0
	}
	return session.CollaborationFilter{
		RunID:  query.Get("run_id"),
		Stage:  query.Get("stage"),
		Agent:  query.Get("agent"),
		Kind:   query.Get("kind"),
		Scope:  query.Get("scope"),
		Status: query.Get("status"),
		Limit:  limit,
	}
}
