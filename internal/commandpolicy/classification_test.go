package commandpolicy

import (
	"encoding/json"
	"testing"
)

func TestClassifyLocalDevelopmentCommands(t *testing.T) {
	tests := []struct {
		name     string
		program  string
		args     []string
		class    Class
		approval bool
	}{
		{name: "inspect git", program: "git", args: []string{"status", "--short"}, class: ClassInspection},
		{name: "go test", program: "go", args: []string{"test", "./..."}, class: ClassTest},
		{name: "go package shorthand", program: "go", args: []string{"./..."}, class: ClassTest},
		{name: "bounded node test", program: "node", args: []string{"web/app.test.js"}, class: ClassTest},
		{name: "bounded node test runner", program: "node", args: []string{"--test", "web/app.spec.mjs"}, class: ClassTest},
		{name: "python pytest module", program: `.venv\\Scripts\\python.exe`, args: []string{"-m", "pytest", "-q"}, class: ClassTest},
		{name: "python unittest module", program: "python", args: []string{"-m", "unittest", "discover"}, class: ClassTest},
		{name: "python syntax compilation", program: `.venv\Scripts\python.exe`, args: []string{"-m", "py_compile", "app.py"}, class: ClassBuild},
		{name: "arbitrary python code", program: "python", args: []string{"-c", "print('ok')"}, class: ClassUnknown, approval: true},
		{name: "arbitrary node script", program: "node", args: []string{"scripts/deploy.js"}, class: ClassUnknown, approval: true},
		{name: "node traversal", program: "node", args: []string{"../app.test.js"}, class: ClassUnknown, approval: true},
		{name: "absolute node test", program: "node", args: []string{"C:\\tmp\\app.test.js"}, class: ClassUnknown, approval: true},
		{name: "node test with extra arguments", program: "node", args: []string{"web/app.test.js", "--update"}, class: ClassUnknown, approval: true},
		{name: "frontend build", program: "pnpm", args: []string{"build"}, class: ClassBuild},
		{name: "go vet", program: "go", args: []string{"vet", "./..."}, class: ClassLint},
		{name: "gofmt list", program: "gofmt", args: []string{"-l", "./..."}, class: ClassFormat},
		{name: "invalid go fmt list", program: "go", args: []string{"fmt", "-l", "."}, class: ClassUnknown},
		{name: "prettier check", program: "prettier", args: []string{"--check", "."}, class: ClassFormat},
		{name: "formatter", program: "gofmt", args: []string{"-w", "main.go"}, class: ClassFormat, approval: true},
		{name: "dependency install", program: "npm", args: []string{"install"}, class: ClassDependencyInstall, approval: true},
		{name: "destructive git", program: "git", args: []string{"reset", "--hard"}, class: ClassDestructive, approval: true},
		{name: "unknown", program: "custom-tool", args: []string{"run"}, class: ClassUnknown, approval: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Classify(test.program, test.args)
			if got.Class != test.class || got.RequiresExplicitApproval != test.approval {
				t.Fatalf("Classify(%q, %#v) = %#v", test.program, test.args, got)
			}
		})
	}
}

func TestClassifyRejectsInvalidGoFmtListWithActionableReason(t *testing.T) {
	got := Classify("go", []string{"fmt", "-l", "."})
	if got.Class != ClassUnknown || got.RequiresExplicitApproval || got.Reason != "go fmt does not support -l; use gofmt -l . for a read-only formatting check" {
		t.Fatalf("expected deterministic gofmt guidance, got %#v", got)
	}
}

func TestClassifyArgumentsAcceptsCommandStringWithoutShell(t *testing.T) {
	got := ClassifyArguments([]byte(`{"command":"go test ./..."}`))
	if got.Class != ClassTest {
		t.Fatalf("expected go test command string to classify as test, got %#v", got)
	}
	normalized := NormalizeRunCommandArguments([]byte(`{"command":"go test -race ./...","cwd":"."}`))
	var payload map[string]any
	if json.Unmarshal(normalized, &payload) != nil {
		t.Fatalf("normalized arguments must be JSON: %s", normalized)
	}
	if payload["program"] != "go" {
		t.Fatalf("expected program go, got %#v", payload)
	}
	args, _ := payload["args"].([]any)
	if len(args) != 3 || args[0] != "test" || args[1] != "-race" || args[2] != "./..." {
		t.Fatalf("expected structured args, got %#v", payload["args"])
	}
	if ClassifyArguments([]byte(`{"command":"go test && rm -rf ."}`)).Class != ClassUnknown {
		t.Fatal("shell metacharacters must not be treated as a structured command")
	}
	shorthand := NormalizeRunCommandArguments([]byte(`{"program":"go","args":["./..."]}`))
	var rewritten map[string]any
	if json.Unmarshal(shorthand, &rewritten) != nil {
		t.Fatalf("rewritten arguments must be JSON: %s", shorthand)
	}
	if rewritten["program"] != "go" {
		t.Fatalf("expected program go, got %#v", rewritten)
	}
	shortArgs, _ := rewritten["args"].([]any)
	if len(shortArgs) != 2 || shortArgs[0] != "test" || shortArgs[1] != "./..." {
		t.Fatalf("expected go ./... to become go test ./..., got %#v", rewritten["args"])
	}
}

func TestClassifyArgumentsIgnoresCallerClassification(t *testing.T) {
	arguments, err := json.Marshal(map[string]any{
		"program":        "git",
		"args":           []string{"clean", "-fd"},
		"classification": "inspection",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := ClassifyArguments(arguments)
	if got.Class != ClassDestructive || !got.RequiresExplicitApproval {
		t.Fatalf("expected server-side destructive classification, got %#v", got)
	}
}
