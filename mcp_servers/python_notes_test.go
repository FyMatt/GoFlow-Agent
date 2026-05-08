package mcp_servers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPythonNotesMCPValidationScript(t *testing.T) {
	python, args := findPythonForTest()
	if python == "" {
		t.Skip("python3/python not found on PATH")
	}
	repoRoot := repoRootForPythonNotesTest(t)
	script := filepath.Join(repoRoot, "scripts", "validate_python_mcp.py")
	cmdArgs := append(append([]string(nil), args...), script)
	cmd := exec.Command(python, cmdArgs...)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validate_python_mcp.py failed: %v\n%s", err, output)
	}
}

func findPythonForTest() (string, []string) {
	for _, candidate := range []struct {
		name string
		args []string
	}{
		{name: "python3"},
		{name: "python"},
		{name: "py", args: []string{"-3"}},
	} {
		path, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		versionArgs := append(append([]string(nil), candidate.args...), "--version")
		if err := exec.Command(path, versionArgs...).Run(); err != nil {
			continue
		}
		return path, candidate.args
	}
	return "", nil
}

func repoRootForPythonNotesTest(t *testing.T) string {
	t.Helper()
	wd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "scripts", "validate_python_mcp.py")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("repository root with scripts/validate_python_mcp.py not found")
		}
		wd = parent
	}
}
