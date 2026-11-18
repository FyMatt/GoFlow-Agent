package interfaces

import "github.com/FyMatt/GoFlow-Agent/pkg/schema"

// SkillManager exposes loaded skills and matching operations.
type SkillManager interface {
	List() []schema.Skill
	Match(query string) (*schema.Skill, bool)
	MatchWithDiagnostics(query string) (*schema.Skill, schema.SkillMatchDiagnostic, bool)
}
