package session

import "strings"

// normalizeRunTargetID keeps legacy snapshots compatible while making the
// current local execution boundary explicit on every newly created task.
func normalizeRunTargetID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return LocalTargetID
	}
	return value
}
