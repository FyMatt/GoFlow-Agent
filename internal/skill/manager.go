package skill

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Manager handles skill discovery and matching.
type Manager struct {
	root        string
	builtinRoot string
	roots       []string
	hidden      func(string) (bool, error)
	mu          sync.RWMutex
	skills      []schema.Skill
}

// NewManager builds a skill manager and loads skills from disk.
func NewManager(root string) (*Manager, error) {
	m := &Manager{root: root, builtinRoot: root, roots: []string{root}}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// NewLayeredManager loads immutable built-ins first and user overrides second.
func NewLayeredManager(builtinRoot, userRoot string, hidden func(string) (bool, error)) (*Manager, error) {
	roots := []string{builtinRoot}
	if strings.TrimSpace(userRoot) != "" && !strings.EqualFold(filepath.Clean(builtinRoot), filepath.Clean(userRoot)) {
		roots = append(roots, userRoot)
	}
	m := &Manager{root: userRoot, builtinRoot: builtinRoot, roots: roots, hidden: hidden}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// Reload rescans the skill directory.
func (m *Manager) Reload() error {
	byName := make(map[string]schema.Skill)
	for _, root := range m.roots {
		paths, err := filepath.Glob(filepath.Join(root, "*", "SKILL.md"))
		if err != nil {
			return fmt.Errorf("glob skill files: %w", err)
		}
		sort.Strings(paths)
		for _, path := range paths {
			item, err := ParseFile(path)
			if err != nil {
				return err
			}
			byName[strings.ToLower(strings.TrimSpace(item.Name))] = *item
		}
	}
	loaded := make([]schema.Skill, 0, len(byName))
	for name, item := range byName {
		if m.hidden != nil {
			hidden, err := m.hidden(name)
			if err != nil {
				return fmt.Errorf("check skill tombstone %s: %w", name, err)
			}
			if hidden {
				continue
			}
		}
		loaded = append(loaded, item)
	}
	if len(loaded) == 0 {
		return fmt.Errorf("no SKILL.md files found under configured skill roots")
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Name < loaded[j].Name })

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

// BuiltinRoot returns the immutable built-in Skill directory.
func (m *Manager) BuiltinRoot() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.builtinRoot
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
