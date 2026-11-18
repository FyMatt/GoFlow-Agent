package skill

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func BenchmarkNormalizeForMatch(b *testing.B) {
	for _, mode := range []string{"fresh-replacer", "shared-replacer"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				input := "Review source-evidence / domain\\研究"
				var got string
				if mode == "fresh-replacer" {
					replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", "\\", "_")
					got = strings.ToLower(replacer.Replace(strings.TrimSpace(input)))
				} else {
					got = normalizeForMatch(input)
				}
				if got != "review_source_evidence___domain_研究" {
					b.Fatal(got)
				}
			}
		})
	}
}

func BenchmarkMatchWithDiagnostics(b *testing.B) {
	for _, count := range []int{32, 256} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			skills := make([]schema.Skill, count)
			for i := range skills {
				skills[i] = schema.Skill{
					Name: fmt.Sprintf("domain-%03d", i), Description: fmt.Sprintf("Review procedures for domain %d", i),
					Activation: schema.Activation{Keywords: []string{"review", "source evidence", "研究", "domain procedures"}},
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, diagnostic, ok := MatchWithDiagnostics("Review source evidence for domain-031 / 研究", skills)
				if !ok || got.Name != "domain-031" || diagnostic.Score != 11 {
					b.Fatalf("match: skill=%v diagnostic=%v ok=%t", got, diagnostic, ok)
				}
			}
		})
	}
}
