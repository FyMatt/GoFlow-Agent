package skill

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Manager handles skill discovery and matching.
type Manager struct {
	root   string
	mu     sync.RWMutex
	skills []schema.Skill
}

// NewManager builds a skill manager and loads skills from disk.
func NewManager(root string) (*Manager, error) {
	m := &Manager{root: root}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// Reload rescans the skill directory.
func (m *Manager) Reload() error {
	paths, err := filepath.Glob(filepath.Join(m.root, "*", "SKILL.md"))
	if err != nil {
		return fmt.Errorf("glob skill files: %w", err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("no SKILL.md files found under %s", m.root)
	}
	sort.Strings(paths)

	loaded := make([]schema.Skill, 0, len(paths))
	for _, path := range paths {
		skill, err := ParseFile(path)
		if err != nil {
			return err
		}
		loaded = append(loaded, *skill)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.skills = loaded
	return nil
}

// List returns all loaded skills.
func (m *Manager) List() []schema.Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	copied := make([]schema.Skill, len(m.skills))
	copy(copied, m.skills)
	return copied
}

// Root returns the skill directory watched by the manager.
func (m *Manager) Root() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root
}

// Match finds the best matching skill for a query.
func (m *Manager) Match(query string) (*schema.Skill, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Match(query, m.skills)
}

// MatchWithDiagnostics finds the best matching skill and explains the match.
func (m *Manager) MatchWithDiagnostics(query string) (*schema.Skill, schema.SkillMatchDiagnostic, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return MatchWithDiagnostics(query, m.skills)
}
