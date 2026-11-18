package commandpolicy

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalizeEmbeddedProgramArguments(t *testing.T) {
	for _, test := range []struct {
		raw     string
		program string
		args    []string
		class   Class
	}{
		{`{"program":"go test","args":["./..."],"cwd":"."}`, "go", []string{"test", "./..."}, ClassTest},
		{`{"program":"go vet ./..."}`, "go", []string{"vet", "./..."}, ClassLint},
		{`{"program":"python -m unittest","args":["discover"]}`, "python", []string{"-m", "unittest", "discover"}, ClassTest},
		{`{"program":"pnpm build"}`, "pnpm", []string{"build"}, ClassBuild},
		{`{"program":"gofmt","args":["gofmt","-w","a.go"]}`, "gofmt", []string{"-w", "a.go"}, ClassFormat},
		{`{"program":"gofmt.exe","args":["gofmt.exe","-w","."]}`, "gofmt.exe", []string{"-w", "."}, ClassFormat},
		{`{"program":"gofmt","args":["gofmt","a.go"]}`, "gofmt", []string{"gofmt", "a.go"}, ClassFormat},
		{`{"program":"gofmt","args":["./gofmt","-w","a.go"]}`, "gofmt", []string{"./gofmt", "-w", "a.go"}, ClassFormat},
	} {
		t.Run(test.program+"/"+string(test.class), func(t *testing.T) {
			var payload struct {
				Program string   `json:"program"`
				Args    []string `json:"args"`
			}
			if err := json.Unmarshal(NormalizeRunCommandArguments([]byte(test.raw)), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Program != test.program || !reflect.DeepEqual(payload.Args, test.args) {
				t.Fatalf("incorrect normalized command: %#v", payload)
			}
			if got := ClassifyArguments([]byte(test.raw)); got.Class != test.class {
				t.Fatalf("classification disagrees with normalized execution: %#v", got)
			}
		})
	}
}

func TestNormalizeEmbeddedProgramPreservesAmbiguousOrUnsafeValues(t *testing.T) {
	for _, program := range []string{
		`C:\Program Files\Go\bin\go.exe`, `/workspace/my tools/check`,
		`"go test"`, `go test && whoami`, `go test; whoami`,
		`sh -c whoami`, `git reset --hard`, `custom tool`,
	} {
		t.Run(program, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"program": program, "args": []string{"unchanged"}})
			var payload map[string]any
			if err := json.Unmarshal(NormalizeRunCommandArguments(raw), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["program"] != program || !reflect.DeepEqual(payload["args"], []any{"unchanged"}) {
				t.Fatalf("ambiguous program must not be split: %#v", payload)
			}
		})
	}
}
