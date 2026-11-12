package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

const maxApprovalScopes = 200

// ApprovalScopeInput describes the trusted context and exact action covered by
// one remembered approval. Run IDs are deliberately excluded so a decision can
// be reused by the same pinned resource without authorizing another resource.
type ApprovalScopeInput struct {
	Workspace     string
	Target        string
	AgentID       string
	WorkflowName  string
	WorkflowStage string
	ToolKind      string
	ToolName      string
	ResourcePins  []ResourcePin
	Arguments     json.RawMessage
}

// ApprovalScope is the durable, explainable form of a remembered tool-call
// decision. Fingerprint is derived from every authorization-relevant field.
type ApprovalScope struct {
	Fingerprint   string          `json:"fingerprint"`
	Workspace     string          `json:"workspace"`
	Target        string          `json:"target"`
	AgentID       string          `json:"agent_id,omitempty"`
	WorkflowName  string          `json:"workflow_name,omitempty"`
	WorkflowStage string          `json:"workflow_stage,omitempty"`
	ToolKind      string          `json:"tool_kind"`
	ToolName      string          `json:"tool_name"`
	ArgumentsHash string          `json:"arguments_hash"`
	Program       string          `json:"program,omitempty"`
	Args          []string        `json:"args,omitempty"`
	Cwd           string          `json:"cwd,omitempty"`
	ResourcePins  []ResourcePin   `json:"resource_pins,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	Arguments     json.RawMessage `json:"-"`
}

// NewApprovalScope normalizes an exact call and builds its stable fingerprint.
func NewApprovalScope(input ApprovalScopeInput, createdAt, expiresAt time.Time) (ApprovalScope, error) {
	canonical, decoded, err := canonicalApprovalArguments(input.Arguments)
	if err != nil {
		return ApprovalScope{}, err
	}
	workspace := normalizeApprovalPath(input.Workspace)
	target := strings.ToLower(strings.TrimSpace(input.Target))
	if target == "" {
		target = "local"
	}
	pins := CopyResourcePins(input.ResourcePins)
	program, args, cwd := approvalCommandDetails(decoded, workspace)
	if program != "" {
		decoded["program"] = program
		decoded["cwd"] = cwd
		canonical, err = json.Marshal(decoded)
		if err != nil {
			return ApprovalScope{}, fmt.Errorf("canonicalize normalized approval arguments: %w", err)
		}
	}
	argumentSum := sha256.Sum256(canonical)
	payload := struct {
		Workspace     string        `json:"workspace"`
		Target        string        `json:"target"`
		AgentID       string        `json:"agent_id,omitempty"`
		WorkflowName  string        `json:"workflow_name,omitempty"`
		WorkflowStage string        `json:"workflow_stage,omitempty"`
		ToolKind      string        `json:"tool_kind"`
		ToolName      string        `json:"tool_name"`
		Arguments     string        `json:"arguments"`
		ResourcePins  []ResourcePin `json:"resource_pins,omitempty"`
	}{
		Workspace:     workspace,
		Target:        target,
		AgentID:       strings.ToLower(strings.TrimSpace(input.AgentID)),
		WorkflowName:  strings.ToLower(strings.TrimSpace(input.WorkflowName)),
		WorkflowStage: strings.ToLower(strings.TrimSpace(input.WorkflowStage)),
		ToolKind:      strings.ToLower(strings.TrimSpace(input.ToolKind)),
		ToolName:      strings.ToLower(strings.TrimSpace(input.ToolName)),
		Arguments:     string(canonical),
		ResourcePins:  pins,
	}
	fingerprintData, err := json.Marshal(payload)
	if err != nil {
		return ApprovalScope{}, fmt.Errorf("marshal approval fingerprint: %w", err)
	}
	fingerprintSum := sha256.Sum256(fingerprintData)
	return ApprovalScope{
		Fingerprint:   "sha256:" + hex.EncodeToString(fingerprintSum[:]),
		Workspace:     workspace,
		Target:        target,
		AgentID:       payload.AgentID,
		WorkflowName:  payload.WorkflowName,
		WorkflowStage: payload.WorkflowStage,
		ToolKind:      payload.ToolKind,
		ToolName:      payload.ToolName,
		ArgumentsHash: "sha256:" + hex.EncodeToString(argumentSum[:]),
		Program:       program,
		Args:          args,
		Cwd:           cwd,
		ResourcePins:  pins,
		CreatedAt:     createdAt.UTC(),
		ExpiresAt:     expiresAt.UTC(),
		Arguments:     append(json.RawMessage(nil), canonical...),
	}, nil
}

func canonicalApprovalArguments(raw json.RawMessage) ([]byte, map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, fmt.Errorf("decode approval arguments: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, nil, fmt.Errorf("canonicalize approval arguments: %w", err)
	}
	object, _ := value.(map[string]any)
	return canonical, object, nil
}

func approvalCommandDetails(arguments map[string]any, workspace string) (string, []string, string) {
	if len(arguments) == 0 {
		return "", nil, ""
	}
	program, _ := arguments["program"].(string)
	program = strings.TrimSpace(program)
	var args []string
	if values, ok := arguments["args"].([]any); ok {
		args = make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if ok {
				args = append(args, text)
			}
		}
	}
	cwd, _ := arguments["cwd"].(string)
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = workspace
	} else if !approvalPathIsAbsolute(cwd) && workspace != "" {
		cwd = path.Join(workspace, strings.ReplaceAll(cwd, "\\", "/"))
	}
	return program, args, normalizeApprovalPath(cwd)
}

func normalizeApprovalPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return ""
	}
	value = path.Clean(value)
	if len(value) >= 2 && value[1] == ':' {
		value = strings.ToUpper(value[:1]) + value[1:]
	}
	return value
}

func approvalPathIsAbsolute(value string) bool {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || (len(value) >= 3 && value[1] == ':' && value[2] == '/')
}

// RememberApprovalScope upserts one exact, unexpired decision.
func (s *State) RememberApprovalScope(scope ApprovalScope) {
	if s == nil || strings.TrimSpace(scope.Fingerprint) == "" || scope.ExpiresAt.IsZero() {
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvalScopes = normalizeApprovalScopes(append(s.approvalScopes, scope), now)
}

// HasApprovalScope reports whether an exact fingerprint remains active.
func (s *State) HasApprovalScope(fingerprint string, now time.Time) bool {
	if s == nil || strings.TrimSpace(fingerprint) == "" {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, scope := range s.approvalScopes {
		if scope.Fingerprint == fingerprint && scope.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

// ApprovalScopes returns active scopes ordered newest first.
func (s *State) ApprovalScopes(now time.Time) []ApprovalScope {
	if s == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyApprovalScopes(normalizeApprovalScopes(s.approvalScopes, now))
}

func normalizeApprovalScopes(scopes []ApprovalScope, now time.Time) []ApprovalScope {
	byFingerprint := make(map[string]ApprovalScope, len(scopes))
	for _, scope := range scopes {
		scope.Fingerprint = strings.TrimSpace(scope.Fingerprint)
		if scope.Fingerprint == "" || scope.ExpiresAt.IsZero() || !scope.ExpiresAt.After(now) {
			continue
		}
		scope.ResourcePins = CopyResourcePins(scope.ResourcePins)
		scope.Args = append([]string(nil), scope.Args...)
		if existing, ok := byFingerprint[scope.Fingerprint]; !ok || scope.ExpiresAt.After(existing.ExpiresAt) {
			byFingerprint[scope.Fingerprint] = scope
		}
	}
	result := make([]ApprovalScope, 0, len(byFingerprint))
	for _, scope := range byFingerprint {
		result = append(result, scope)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if len(result) > maxApprovalScopes {
		result = result[:maxApprovalScopes]
	}
	return result
}

func copyApprovalScopes(scopes []ApprovalScope) []ApprovalScope {
	if len(scopes) == 0 {
		return nil
	}
	result := make([]ApprovalScope, len(scopes))
	for i, scope := range scopes {
		scope.Args = append([]string(nil), scope.Args...)
		scope.ResourcePins = CopyResourcePins(scope.ResourcePins)
		scope.Arguments = append(json.RawMessage(nil), scope.Arguments...)
		result[i] = scope
	}
	return result
}
