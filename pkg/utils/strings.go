package utils

import "strings"

// NormalizeWhitespace compacts repeated whitespace for simple prompt hygiene.
func NormalizeWhitespace(input string) string {
	return strings.Join(strings.Fields(input), " ")
}
