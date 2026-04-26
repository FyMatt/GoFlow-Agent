package skill

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Match performs a keyword-based skill selection.
func Match(query string, skills []schema.Skill) (*schema.Skill, bool) {
	matched, _, ok := MatchWithDiagnostics(query, skills)
	return matched, ok
}

// MatchWithDiagnostics performs keyword-based skill selection and explains the match.
func MatchWithDiagnostics(query string, skills []schema.Skill) (*schema.Skill, schema.SkillMatchDiagnostic, bool) {
	normalizedQuery := normalizeForMatch(query)
	bestScore := 0
	var best *schema.Skill
	var bestDiagnostic schema.SkillMatchDiagnostic

	for i := range skills {
		skill := skills[i]
		score := 0
		keywordHits := make([]string, 0)
		for _, keyword := range skill.Activation.Keywords {
			if strings.Contains(normalizedQuery, normalizeForMatch(keyword)) {
				score += 3
				keywordHits = append(keywordHits, keyword)
			}
		}
		nameHit := false
		if strings.Contains(normalizedQuery, normalizeForMatch(skill.Name)) {
			score += 2
			nameHit = true
		}
		descriptionHit := false
		if strings.Contains(normalizedQuery, normalizeForMatch(skill.Description)) {
			score += 1
			descriptionHit = true
		}
		if best == nil || score > bestScore || (score == bestScore && score > 0 && skill.Name < best.Name) {
			copySkill := skill
			bestScore = score
			best = &copySkill
			bestDiagnostic = schema.SkillMatchDiagnostic{
				SkillName:      skill.Name,
				Score:          score,
				KeywordHits:    keywordHits,
				NameHit:        nameHit,
				DescriptionHit: descriptionHit,
			}
		}
	}

	if best == nil || bestScore <= 0 {
		return nil, schema.SkillMatchDiagnostic{}, false
	}
	bestDiagnostic.Reason = buildMatchReason(bestDiagnostic)
	return best, bestDiagnostic, true
}

func normalizeForMatch(input string) string {
	replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", "\\", "_")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(input)))
}

func buildMatchReason(diagnostic schema.SkillMatchDiagnostic) string {
	parts := make([]string, 0, 4)
	if len(diagnostic.KeywordHits) > 0 {
		parts = append(parts, "keywords:"+strings.Join(diagnostic.KeywordHits, ","))
	}
	if diagnostic.NameHit {
		parts = append(parts, "name")
	}
	if diagnostic.DescriptionHit {
		parts = append(parts, "description")
	}
	if len(parts) == 0 {
		return "score only"
	}
	return strings.Join(parts, "; ")
}
