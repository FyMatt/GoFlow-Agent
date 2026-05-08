package session

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	maxCollaborationMessages = 200
	maxBlackboardEntries     = 100
	maxCollaborationText     = 8192
)

// CollaborationMessageSnapshot is a durable agent-to-agent or operator-to-agent message.
type CollaborationMessageSnapshot struct {
	ID        string            `json:"id"`
	At        string            `json:"at,omitempty"`
	RunID     string            `json:"run_id,omitempty"`
	Stage     string            `json:"stage,omitempty"`
	FromAgent string            `json:"from_agent,omitempty"`
	ToAgent   string            `json:"to_agent,omitempty"`
	Kind      string            `json:"kind,omitempty"`
	Subject   string            `json:"subject,omitempty"`
	Content   string            `json:"content,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// BlackboardEntrySnapshot is a durable shared fact, artifact, decision, or task note.
type BlackboardEntrySnapshot struct {
	ID        string            `json:"id"`
	CreatedAt string            `json:"created_at,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	Scope     string            `json:"scope,omitempty"`
	RunID     string            `json:"run_id,omitempty"`
	Stage     string            `json:"stage,omitempty"`
	AgentID   string            `json:"agent_id,omitempty"`
	Kind      string            `json:"kind,omitempty"`
	Title     string            `json:"title,omitempty"`
	Content   string            `json:"content,omitempty"`
	Status    string            `json:"status,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// CollaborationFilter limits message or blackboard queries.
type CollaborationFilter struct {
	RunID  string
	Stage  string
	Agent  string
	Kind   string
	Scope  string
	Status string
	Limit  int
}

// AddCollaborationMessage appends a bounded timeline message.
func (s *State) AddCollaborationMessage(message CollaborationMessageSnapshot) CollaborationMessageSnapshot {
	if s == nil {
		return CollaborationMessageSnapshot{}
	}
	now := collaborationTimestamp(time.Now().UTC())
	message = normalizeCollaborationMessage(message, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(message.ID) == "" {
		message.ID = uniqueCollaborationMessageID(s.messages, message, now)
	} else {
		for i := range s.messages {
			if s.messages[i].ID != message.ID {
				continue
			}
			if strings.TrimSpace(s.messages[i].At) != "" {
				message.At = s.messages[i].At
			}
			s.messages[i] = message
			return copyCollaborationMessage(message)
		}
	}
	s.messages = append([]CollaborationMessageSnapshot{message}, s.messages...)
	if len(s.messages) > maxCollaborationMessages {
		s.messages = append([]CollaborationMessageSnapshot(nil), s.messages[:maxCollaborationMessages]...)
	}
	return copyCollaborationMessage(message)
}

// CollaborationMessages returns newest-first messages matching the filter.
func (s *State) CollaborationMessages(filter CollaborationFilter) []CollaborationMessageSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CollaborationMessageSnapshot, 0, len(s.messages))
	for _, message := range s.messages {
		if !collaborationMessageMatches(message, filter) {
			continue
		}
		out = append(out, copyCollaborationMessage(message))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out
}

// UpsertBlackboardEntry creates or updates a bounded shared blackboard entry.
func (s *State) UpsertBlackboardEntry(entry BlackboardEntrySnapshot) BlackboardEntrySnapshot {
	if s == nil {
		return BlackboardEntrySnapshot{}
	}
	now := collaborationTimestamp(time.Now().UTC())
	entry = normalizeBlackboardEntry(entry, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(entry.ID) != "" {
		for i := range s.blackboard {
			if s.blackboard[i].ID != entry.ID {
				continue
			}
			if strings.TrimSpace(entry.CreatedAt) == "" {
				entry.CreatedAt = s.blackboard[i].CreatedAt
			}
			if strings.TrimSpace(entry.CreatedAt) == "" {
				entry.CreatedAt = now
			}
			entry.UpdatedAt = now
			s.blackboard[i] = entry
			sortBlackboardEntriesNewestFirst(s.blackboard)
			return copyBlackboardEntry(entry)
		}
	}
	if strings.TrimSpace(entry.ID) == "" {
		entry.ID = uniqueBlackboardEntryID(s.blackboard, entry, now)
	}
	entry.CreatedAt = now
	entry.UpdatedAt = now
	s.blackboard = append([]BlackboardEntrySnapshot{entry}, s.blackboard...)
	if len(s.blackboard) > maxBlackboardEntries {
		s.blackboard = append([]BlackboardEntrySnapshot(nil), s.blackboard[:maxBlackboardEntries]...)
	}
	return copyBlackboardEntry(entry)
}

// BlackboardEntries returns newest-first entries matching the filter.
func (s *State) BlackboardEntries(filter CollaborationFilter) []BlackboardEntrySnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]BlackboardEntrySnapshot, 0, len(s.blackboard))
	for _, entry := range s.blackboard {
		if !blackboardEntryMatches(entry, filter) {
			continue
		}
		out = append(out, copyBlackboardEntry(entry))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out
}

// BlackboardEntry returns one entry by id.
func (s *State) BlackboardEntry(id string) (BlackboardEntrySnapshot, bool) {
	if s == nil || strings.TrimSpace(id) == "" {
		return BlackboardEntrySnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.blackboard {
		if entry.ID == strings.TrimSpace(id) {
			return copyBlackboardEntry(entry), true
		}
	}
	return BlackboardEntrySnapshot{}, false
}

// DeleteBlackboardEntry removes one blackboard entry.
func (s *State) DeleteBlackboardEntry(id string) bool {
	if s == nil || strings.TrimSpace(id) == "" {
		return false
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.blackboard {
		if s.blackboard[i].ID != id {
			continue
		}
		s.blackboard = append(s.blackboard[:i], s.blackboard[i+1:]...)
		return true
	}
	return false
}

func normalizeCollaborationMessage(message CollaborationMessageSnapshot, now string) CollaborationMessageSnapshot {
	message.ID = strings.TrimSpace(message.ID)
	message.At = strings.TrimSpace(message.At)
	if message.At == "" {
		message.At = now
	}
	message.RunID = strings.TrimSpace(message.RunID)
	message.Stage = strings.TrimSpace(message.Stage)
	message.FromAgent = strings.TrimSpace(message.FromAgent)
	message.ToAgent = strings.TrimSpace(message.ToAgent)
	message.Kind = normalizeCollaborationKind(message.Kind, "message")
	message.Subject = trimCollaborationText(message.Subject)
	message.Content = trimCollaborationText(message.Content)
	message.Metadata = cleanStringMap(message.Metadata)
	return message
}

func normalizeBlackboardEntry(entry BlackboardEntrySnapshot, now string) BlackboardEntrySnapshot {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.CreatedAt = strings.TrimSpace(entry.CreatedAt)
	entry.UpdatedAt = strings.TrimSpace(entry.UpdatedAt)
	if entry.CreatedAt == "" {
		entry.CreatedAt = now
	}
	if entry.UpdatedAt == "" {
		entry.UpdatedAt = now
	}
	entry.Scope = normalizeCollaborationKind(entry.Scope, "session")
	entry.RunID = strings.TrimSpace(entry.RunID)
	entry.Stage = strings.TrimSpace(entry.Stage)
	entry.AgentID = strings.TrimSpace(entry.AgentID)
	entry.Kind = normalizeCollaborationKind(entry.Kind, "note")
	entry.Title = trimCollaborationText(entry.Title)
	entry.Content = trimCollaborationText(entry.Content)
	entry.Status = normalizeCollaborationKind(entry.Status, "open")
	entry.Tags = cleanStringSlice(entry.Tags)
	entry.Metadata = cleanStringMap(entry.Metadata)
	return entry
}

func collaborationMessageMatches(message CollaborationMessageSnapshot, filter CollaborationFilter) bool {
	if !matchesFilter(message.RunID, filter.RunID) || !matchesFilter(message.Stage, filter.Stage) || !matchesFilter(message.Kind, filter.Kind) {
		return false
	}
	agent := strings.TrimSpace(filter.Agent)
	if agent == "" {
		return true
	}
	return strings.EqualFold(message.FromAgent, agent) || strings.EqualFold(message.ToAgent, agent)
}

func blackboardEntryMatches(entry BlackboardEntrySnapshot, filter CollaborationFilter) bool {
	return matchesFilter(entry.RunID, filter.RunID) &&
		matchesFilter(entry.Stage, filter.Stage) &&
		matchesFilter(entry.AgentID, filter.Agent) &&
		matchesFilter(entry.Kind, filter.Kind) &&
		matchesFilter(entry.Scope, filter.Scope) &&
		matchesFilter(entry.Status, filter.Status)
}

func matchesFilter(value, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(value), filter)
}

func uniqueCollaborationMessageID(messages []CollaborationMessageSnapshot, message CollaborationMessageSnapshot, now string) string {
	slug := workflowRunSlug(firstNonEmpty(message.Kind, message.Subject, "message"))
	for suffix := 0; ; suffix++ {
		id := fmt.Sprintf("msg-%s-%s", slug, compactIDTimestamp(now))
		if suffix > 0 {
			id = fmt.Sprintf("%s-%d", id, suffix)
		}
		if !collaborationMessageIDExists(messages, id) {
			return id
		}
	}
}

func uniqueBlackboardEntryID(entries []BlackboardEntrySnapshot, entry BlackboardEntrySnapshot, now string) string {
	slug := workflowRunSlug(firstNonEmpty(entry.Title, entry.Kind, "entry"))
	for suffix := 0; ; suffix++ {
		id := fmt.Sprintf("bb-%s-%s", slug, compactIDTimestamp(now))
		if suffix > 0 {
			id = fmt.Sprintf("%s-%d", id, suffix)
		}
		if !blackboardEntryIDExists(entries, id) {
			return id
		}
	}
}

func collaborationMessageIDExists(messages []CollaborationMessageSnapshot, id string) bool {
	for _, message := range messages {
		if message.ID == id {
			return true
		}
	}
	return false
}

func blackboardEntryIDExists(entries []BlackboardEntrySnapshot, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func collaborationTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func compactIDTimestamp(value string) string {
	value = strings.NewReplacer(":", "", "-", "", ".", "", "T", "", "Z", "").Replace(value)
	if value == "" {
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	return value
}

func normalizeCollaborationKind(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if (r == '-' || r == '_' || r == ' ') && !lastDash {
			b.WriteByte('_')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return fallback
	}
	return out
}

func trimCollaborationText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxCollaborationText {
		return value
	}
	return value[:maxCollaborationText]
}

func cleanStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = trimCollaborationText(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sortBlackboardEntriesNewestFirst(entries []BlackboardEntrySnapshot) {
	sort.SliceStable(entries, func(i, j int) bool {
		return strings.Compare(entries[i].UpdatedAt, entries[j].UpdatedAt) > 0
	})
}

func copyCollaborationMessages(messages []CollaborationMessageSnapshot) []CollaborationMessageSnapshot {
	if len(messages) == 0 {
		return nil
	}
	copied := make([]CollaborationMessageSnapshot, len(messages))
	for i, message := range messages {
		copied[i] = copyCollaborationMessage(message)
	}
	return copied
}

func copyCollaborationMessage(message CollaborationMessageSnapshot) CollaborationMessageSnapshot {
	if len(message.Metadata) > 0 {
		message.Metadata = cleanStringMap(message.Metadata)
	}
	return message
}

func copyBlackboardEntries(entries []BlackboardEntrySnapshot) []BlackboardEntrySnapshot {
	if len(entries) == 0 {
		return nil
	}
	copied := make([]BlackboardEntrySnapshot, len(entries))
	for i, entry := range entries {
		copied[i] = copyBlackboardEntry(entry)
	}
	return copied
}

func copyBlackboardEntry(entry BlackboardEntrySnapshot) BlackboardEntrySnapshot {
	if len(entry.Tags) > 0 {
		entry.Tags = append([]string(nil), entry.Tags...)
	}
	if len(entry.Metadata) > 0 {
		entry.Metadata = cleanStringMap(entry.Metadata)
	}
	return entry
}
