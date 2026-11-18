package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// ResourcePin identifies the exact public configuration used by a task.
// Version is a content hash rather than a mutable display version so a task
// remains replayable even when the current resource is edited later.
type ResourcePin struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// NewResourcePin builds a deterministic SHA-256 pin from a JSON-safe public
// resource representation. Callers must remove secrets before passing values.
func NewResourcePin(kind, name string, value any) ResourcePin {
	kind = strings.ToLower(strings.TrimSpace(kind))
	name = strings.TrimSpace(name)
	return ResourcePin{
		Kind:    kind,
		Name:    name,
		Version: ResourceVersion(value),
	}
}

// ResourceVersion returns the stable content hash used in ResourcePin.
func ResourceVersion(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		// JSON-safe resource documents should never fail to marshal. Returning a
		// hash of the error keeps the pin non-empty if a future resource grows an
		// unsupported field.
		data = []byte(err.Error())
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NormalizeResourcePins removes incomplete/duplicate pins and sorts them by
// kind and name for stable API output and persistence.
func NormalizeResourcePins(pins []ResourcePin) []ResourcePin {
	if len(pins) == 0 {
		return nil
	}
	byKey := make(map[string]ResourcePin, len(pins))
	for _, pin := range pins {
		pin.Kind = strings.ToLower(strings.TrimSpace(pin.Kind))
		pin.Name = strings.TrimSpace(pin.Name)
		pin.Version = strings.TrimSpace(pin.Version)
		if pin.Kind == "" || pin.Name == "" || pin.Version == "" {
			continue
		}
		key := pin.Kind + "\x00" + strings.ToLower(pin.Name)
		if existing, ok := byKey[key]; !ok || pin.Version < existing.Version {
			byKey[key] = pin
		}
	}
	if len(byKey) == 0 {
		return nil
	}
	out := make([]ResourcePin, 0, len(byKey))
	for _, pin := range byKey {
		out = append(out, pin)
	}
	sort.Slice(out, func(i, j int) bool {
		leftKind := strings.ToLower(out[i].Kind)
		rightKind := strings.ToLower(out[j].Kind)
		if leftKind != rightKind {
			return leftKind < rightKind
		}
		leftName := strings.ToLower(out[i].Name)
		rightName := strings.ToLower(out[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// CopyResourcePins returns an isolated, normalized copy suitable for a
// snapshot boundary.
func CopyResourcePins(pins []ResourcePin) []ResourcePin {
	return NormalizeResourcePins(append([]ResourcePin(nil), pins...))
}

// MergeResourcePins combines pins from multiple resource selectors.
func MergeResourcePins(groups ...[]ResourcePin) []ResourcePin {
	var merged []ResourcePin
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return NormalizeResourcePins(merged)
}
