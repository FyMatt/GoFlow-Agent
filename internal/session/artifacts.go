package session

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxSessionArtifacts            = 80
	maxSessionArtifactContentBytes = 1024 * 1024
	maxSessionArtifactSummaryBytes = 4096
)

// SessionArtifactSnapshot stores a large runtime observation outside the prompt.
type SessionArtifactSnapshot struct {
	ID            string            `json:"id"`
	Ref           string            `json:"ref"`
	CreatedAt     string            `json:"created_at,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Title         string            `json:"title,omitempty"`
	Summary       string            `json:"summary,omitempty"`
	Content       string            `json:"content,omitempty"`
	ContentBytes  int               `json:"content_bytes,omitempty"`
	StoredBytes   int               `json:"stored_bytes,omitempty"`
	Truncated     bool              `json:"truncated,omitempty"`
	ToolName      string            `json:"tool_name,omitempty"`
	ToolCallID    string            `json:"tool_call_id,omitempty"`
	AgentID       string            `json:"agent_id,omitempty"`
	Mode          string            `json:"mode,omitempty"`
	WorkflowRunID string            `json:"workflow_run_id,omitempty"`
	Stage         string            `json:"stage,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type SessionArtifactFilter struct {
	Kind    string
	Tool    string
	Agent   string
	Query   string
	Limit   int
	Content bool
}

func (s *State) AddArtifact(artifact SessionArtifactSnapshot) SessionArtifactSnapshot {
	if s == nil {
		return SessionArtifactSnapshot{}
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact = normalizeSessionArtifactLocked(s.artifacts, artifact, now)
	s.artifacts = append([]SessionArtifactSnapshot{artifact}, s.artifacts...)
	if len(s.artifacts) > maxSessionArtifacts {
		s.artifacts = append([]SessionArtifactSnapshot(nil), s.artifacts[:maxSessionArtifacts]...)
	}
	return copySessionArtifact(artifact)
}

func (s *State) Artifact(idOrRef string) (SessionArtifactSnapshot, bool) {
	if s == nil || strings.TrimSpace(idOrRef) == "" {
		return SessionArtifactSnapshot{}, false
	}
	id := sessionArtifactIDFromRef(idOrRef)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, artifact := range s.artifacts {
		if artifact.ID == id || artifact.Ref == idOrRef {
			return copySessionArtifact(artifact), true
		}
	}
	return SessionArtifactSnapshot{}, false
}

func (s *State) Artifacts(filter SessionArtifactFilter) []SessionArtifactSnapshot {
	if s == nil {
		return nil
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionArtifactSnapshot, 0, len(s.artifacts))
	for _, artifact := range s.artifacts {
		if !sessionArtifactMatchesFilter(artifact, filter) {
			continue
		}
		copied := copySessionArtifact(artifact)
		if !filter.Content {
			copied.Content = ""
		}
		out = append(out, copied)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out
}

func normalizeSessionArtifactLocked(existing []SessionArtifactSnapshot, artifact SessionArtifactSnapshot, now time.Time) SessionArtifactSnapshot {
	artifact.ID = sanitizeSessionArtifactID(artifact.ID)
	if artifact.ID == "" {
		artifact.ID = uniqueSessionArtifactID(existing, artifact, now)
	}
	artifact.Ref = "goflow://session-artifacts/" + artifact.ID
	if strings.TrimSpace(artifact.CreatedAt) == "" {
		artifact.CreatedAt = now.Format(time.RFC3339Nano)
	}
	artifact.Kind = fallbackSessionArtifactValue(artifact.Kind, "tool_result")
	artifact.Title = fallbackSessionArtifactValue(artifact.Title, fallbackSessionArtifactValue(artifact.ToolName, "Artifact"))
	artifact.ContentBytes = len([]byte(artifact.Content))
	if len([]byte(artifact.Content)) > maxSessionArtifactContentBytes {
		artifact.Content = trimSessionArtifactBytes(artifact.Content, maxSessionArtifactContentBytes)
		artifact.Truncated = true
	}
	artifact.StoredBytes = len([]byte(artifact.Content))
	if strings.TrimSpace(artifact.Summary) == "" {
		artifact.Summary = artifact.Content
	}
	if len([]byte(artifact.Summary)) > maxSessionArtifactSummaryBytes {
		artifact.Summary = trimSessionArtifactBytes(artifact.Summary, maxSessionArtifactSummaryBytes)
	}
	artifact.Metadata = copyStringMapForArtifact(artifact.Metadata)
	return artifact
}

func uniqueSessionArtifactID(existing []SessionArtifactSnapshot, artifact SessionArtifactSnapshot, now time.Time) string {
	base := sanitizeSessionArtifactID(strings.Join([]string{artifact.Kind, artifact.ToolName, artifact.ToolCallID}, "-"))
	if base == "" {
		base = "artifact"
	}
	base = fmt.Sprintf("%s-%d", base, now.UnixNano())
	used := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		used[item.ID] = struct{}{}
	}
	for i := 0; ; i++ {
		id := base
		if i > 0 {
			id = fmt.Sprintf("%s-%d", base, i+1)
		}
		if _, ok := used[id]; !ok {
			return id
		}
	}
}

func sessionArtifactIDFromRef(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "goflow://session-artifacts/")
	return sanitizeSessionArtifactID(value)
}

func sanitizeSessionArtifactID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if (r == '-' || r == '_' || r == '.') && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func sessionArtifactMatchesFilter(artifact SessionArtifactSnapshot, filter SessionArtifactFilter) bool {
	if filter.Kind != "" && !strings.EqualFold(strings.TrimSpace(artifact.Kind), strings.TrimSpace(filter.Kind)) {
		return false
	}
	if filter.Tool != "" && !strings.EqualFold(strings.TrimSpace(artifact.ToolName), strings.TrimSpace(filter.Tool)) {
		return false
	}
	if filter.Agent != "" && !strings.EqualFold(strings.TrimSpace(artifact.AgentID), strings.TrimSpace(filter.Agent)) {
		return false
	}
	if filter.Query != "" {
		query := strings.ToLower(strings.TrimSpace(filter.Query))
		if !strings.Contains(strings.ToLower(strings.Join([]string{artifact.ID, artifact.Ref, artifact.Kind, artifact.Title, artifact.Summary, artifact.ToolName, artifact.ToolCallID, artifact.AgentID, artifact.Mode}, "\n")), query) {
			return false
		}
	}
	return true
}

func copySessionArtifacts(artifacts []SessionArtifactSnapshot) []SessionArtifactSnapshot {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]SessionArtifactSnapshot, len(artifacts))
	for i, artifact := range artifacts {
		out[i] = copySessionArtifact(artifact)
	}
	return out
}

func copySessionArtifact(artifact SessionArtifactSnapshot) SessionArtifactSnapshot {
	artifact.Metadata = copyStringMapForArtifact(artifact.Metadata)
	return artifact
}

func copyStringMapForArtifact(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func fallbackSessionArtifactValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func trimSessionArtifactBytes(value string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(value)) <= maxBytes {
		return value
	}
	trimmed := value[:maxBytes]
	for !utf8.ValidString(trimmed) && len(trimmed) > 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}
