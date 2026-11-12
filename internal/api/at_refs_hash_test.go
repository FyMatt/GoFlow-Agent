package api

import "testing"

func TestAtReferenceArgumentsHashIsStableAndRedacted(t *testing.T) {
	first := atReferenceArgumentsHash("path", "src/main.go")
	second := atReferenceArgumentsHash("path", "src/main.go")
	if first == "" || first != second {
		t.Fatalf("expected stable argument hash, got %q and %q", first, second)
	}
	if len(first) != len("sha256:")+64 {
		t.Fatalf("unexpected hash format: %q", first)
	}
	if first == "src/main.go" || first == "sha256:src/main.go" {
		t.Fatalf("argument hash leaked raw path: %q", first)
	}
	if first == atReferenceArgumentsHash("path", "src/other.go") {
		t.Fatal("different paths must not share an argument hash")
	}
}
