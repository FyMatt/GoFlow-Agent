package scaffold

import (
	"fmt"
	"strings"
)

// KitRouting declares task selection and required capabilities, never grants permissions.
// A nil contract keeps the existing Kit behavior. Enabled defaults to true.
type KitRouting struct {
	Version       int      `json:"version" yaml:"version"`
	Priority      int      `json:"priority,omitempty" yaml:"priority,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	MatchTerms    []string `json:"match_terms" yaml:"match_terms"`
	EntryAgent    string   `json:"entry_agent,omitempty" yaml:"entry_agent,omitempty"`
	EntryWorkflow string   `json:"entry_workflow,omitempty" yaml:"entry_workflow,omitempty"`
	RequiredTools []string `json:"required_tools,omitempty" yaml:"required_tools,omitempty"`
}

func (r *KitRouting) IsEnabled() bool {
	return r != nil && (r.Enabled == nil || *r.Enabled)
}

// NormalizeKitRouting returns an independent copy, preserving the contract version.
func NormalizeKitRouting(r *KitRouting) *KitRouting {
	if r == nil {
		return nil
	}
	out := *r
	if r.Enabled != nil {
		enabled := *r.Enabled
		out.Enabled = &enabled
	}
	out.EntryAgent = normalizeName(r.EntryAgent)
	out.EntryWorkflow = normalizeName(r.EntryWorkflow)
	out.MatchTerms = normalizeStringList(r.MatchTerms, true)
	out.RequiredTools = normalizeStringList(r.RequiredTools, true)
	return &out
}

func ValidateKitRouting(r *KitRouting, agents, workflows []string) error {
	if r == nil {
		return nil
	}
	if r.Version != 1 {
		return fmt.Errorf("routing.version %d is unsupported; expected 1", r.Version)
	}
	if r.Priority < -100 || r.Priority > 100 {
		return fmt.Errorf("routing.priority %d is outside the supported range -100..100", r.Priority)
	}
	if len(r.MatchTerms) == 0 {
		return fmt.Errorf("routing.match_terms must contain at least one non-empty task term")
	}
	for _, term := range r.MatchTerms {
		if strings.TrimSpace(term) == "" {
			return fmt.Errorf("routing.match_terms contains an empty task term")
		}
	}
	if (r.EntryAgent == "") == (r.EntryWorkflow == "") {
		return fmt.Errorf("routing must define exactly one of entry_agent or entry_workflow")
	}
	entry, refs := r.EntryAgent, agents
	if r.EntryWorkflow != "" {
		entry, refs = r.EntryWorkflow, workflows
	}
	if !kitRoutingIdentifier(entry) {
		return fmt.Errorf("routing entry %q is not a resource name", entry)
	}
	found := false
	for _, ref := range refs {
		if normalizeName(ref) == entry {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("routing entry %q must be included in kit agents or workflows", entry)
	}
	for _, name := range r.RequiredTools {
		parts := strings.Split(name, "/")
		if len(parts) != 2 || !kitRoutingIdentifier(parts[0]) || !kitRoutingIdentifier(parts[1]) {
			return fmt.Errorf("routing.required_tools %q must be a qualified server/tool name", name)
		}
	}
	return nil
}

func kitRoutingIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
