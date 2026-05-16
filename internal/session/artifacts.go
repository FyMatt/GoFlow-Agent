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
	ArtifactRef   string            `json:"artifact_ref,omitempty"`
	Hash          string            `json:"hash,omitempty"`
	Mime          string            `json:"mime,omitempty"`
	CreatedAt     string            `json:"created_at,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Title         string            `json:"title,omitempty"`
	Summary       string            `json:"summary,omitempty"`
	Content       string            `json:"content,omitempty"`
	ContentBytes  int               `json:"content_bytes,omitempty"`
	StoredBytes   int               `json:"stored_bytes,omitempty"`
	Deduplicated  bool              `json:"deduplicated,omitempty"`
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
	rawContent := artifact.Content
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact = normalizeSessionArtifactLocked(s.artifacts, artifact, now)
	if s.artifactStore != nil && rawContent != "" {
		artifact.Content = rawContent
		artifact.ContentBytes = len([]byte(rawContent))
		artifact.Truncated = false
	}
	artifact = s.externalizeArtifactLocked(artifact)
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
			return s.hydrateArtifactLocked(copySessionArtifact(artifact)), true
		}
		if strings.TrimSpace(artifact.ArtifactRef) != "" && artifact.ArtifactRef == strings.TrimSpace(idOrRef) {
			return s.hydrateArtifactLocked(copySessionArtifact(artifact)), true
		}
		if strings.TrimSpace(artifact.Hash) != "" && (artifact.Hash == strings.TrimPrefix(strings.TrimSpace(idOrRef), "sha256:")) {
			return s.hydrateArtifactLocked(copySessionArtifact(artifact)), true
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
	artifact.Mime = fallbackSessionArtifactValue(artifact.Mime, "text/plain")
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

func (s *State) externalizeArtifactLocked(artifact SessionArtifactSnapshot) SessionArtifactSnapshot {
	if s == nil || s.artifactStore == nil || strings.TrimSpace(artifact.Content) == "" {
		return artifact
	}
	object, deduplicated, err := s.artifactStore.Put(ArtifactObject{
		CreatedAt: artifact.CreatedAt,
		Mime:      artifact.Mime,
		Summary:   artifact.Summary,
		Content:   artifact.Content,
		Kind:      artifact.Kind,
		Title:     artifact.Title,
		Metadata:  artifact.Metadata,
	})
	if err != nil {
		return artifact
	}
	artifact.ArtifactRef = object.Ref
	artifact.Hash = object.Hash
	artifact.ContentBytes = object.Size
	artifact.StoredBytes = int(object.StoredBytes)
	artifact.Deduplicated = deduplicated
	artifact.Content = ""
	artifact.Truncated = false
	if strings.TrimSpace(artifact.Summary) == "" {
		artifact.Summary = object.Summary
	}
	return artifact
}

func (s *State) hydrateArtifactLocked(artifact SessionArtifactSnapshot) SessionArtifactSnapshot {
	if s == nil || s.artifactStore == nil || strings.TrimSpace(artifact.Content) != "" {
		return artifact
	}
	ref := firstArtifactObjectLookup(artifact.ArtifactRef, artifact.Hash)
	if ref == "" {
		return artifact
	}
	object, ok, err := s.artifactStore.Get(ref)
	if err != nil || !ok {
		return artifact
	}
	artifact.Content = object.Content
	artifact.ArtifactRef = fallbackSessionArtifactValue(artifact.ArtifactRef, object.Ref)
	artifact.Hash = fallbackSessionArtifactValue(artifact.Hash, object.Hash)
	artifact.Mime = fallbackSessionArtifactValue(artifact.Mime, object.Mime)
	if artifact.ContentBytes == 0 {
		artifact.ContentBytes = object.Size
	}
	if artifact.StoredBytes == 0 {
		artifact.StoredBytes = int(object.StoredBytes)
	}
	if strings.TrimSpace(artifact.Summary) == "" {
		artifact.Summary = object.Summary
	}
	return artifact
}

func firstArtifactObjectLookup(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
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
		if !strings.Contains(strings.ToLower(strings.Join([]string{artifact.ID, artifact.Ref, artifact.ArtifactRef, artifact.Hash, artifact.Kind, artifact.Title, artifact.Summary, artifact.ToolName, artifact.ToolCallID, artifact.AgentID, artifact.Mode}, "\n")), query) {
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
