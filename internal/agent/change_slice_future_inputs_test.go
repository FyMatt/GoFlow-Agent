package agent

import (
	"strings"
	"testing"
)

func TestChangeSliceInputMayBeProducedByDeclaredDependency(t *testing.T) {
	output := "goflow-change-slices\n" + `[
{"id":"generate","title":"Create tests","mode":"write","depends_on":[],"input_files":[],"target_files":["ttlcache/regression_test.go"],"verification_commands":["go test ./..."]},
{"id":"verify","title":"Verify generated tests","mode":"read","depends_on":["generate"],"input_files":["ttlcache/regression_test.go"],"target_files":["ttlcache/regression_test.go"],"verification_commands":["go test ./..."]}]`
	slices := compileWorkflowChangeSlices(output, nil)
	if len(slices) != 2 {
		t.Fatalf("future dependency output rejected: %#v", slices)
	}
	if slices[1].InputHashes["ttlcache/regression_test.go"] != "" {
		t.Fatal("a planned future file must not acquire a fabricated read hash")
	}
	for name, invalid := range map[string]string{
		"unrelated producer": strings.Replace(output, `"depends_on":["generate"]`, `"depends_on":[]`, 1),
		"read-only producer": strings.Replace(output, `"mode":"write"`, `"mode":"read"`, 1),
		"dependency cycle":   strings.Replace(output, `"depends_on":[]`, `"depends_on":["verify"]`, 1),
		"wrong output":       strings.Replace(output, `"target_files":["ttlcache/regression_test.go"]`, `"target_files":["ttlcache/other.go"]`, 1),
	} {
		if got := compileWorkflowChangeSlices(invalid, nil); len(got) != 0 {
			t.Fatalf("%s authorized an unobserved input: %#v", name, got)
		}
	}
}

func TestChangeSliceFutureInputSupportsTransitiveDependencies(t *testing.T) {
	output := "goflow-change-slices\n" + `[
{"id":"generate","title":"Generate","mode":"write","depends_on":[],"input_files":[],"target_files":["new.go"],"verification_commands":["go test ./..."]},
{"id":"review","title":"Review","mode":"read","depends_on":["generate"],"input_files":["new.go"],"target_files":["new.go"],"verification_commands":["go test ./..."]},
{"id":"verify","title":"Verify","mode":"read","depends_on":["review"],"input_files":["new.go"],"target_files":["new.go"],"verification_commands":["go test ./..."]}]`
	if got := compileWorkflowChangeSlices(output, nil); len(got) != 3 {
		t.Fatalf("transitive future input rejected: %#v", got)
	}
}
