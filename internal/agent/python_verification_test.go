package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func verificationPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python", "python3"} {
		if path, err := exec.LookPath(name); err == nil {
			if err := exec.Command(path, "-c", "import venv, unittest").Run(); err == nil {
				return path
			}
		}
	}
	t.Skip("Python with venv is required for real interpreter provenance tests")
	return ""
}

func verificationVenvPython(root, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(root, name, "Scripts", "python.exe")
	}
	return filepath.Join(root, name, "bin", "python")
}

func createVerificationVenv(t *testing.T, root, name string) string {
	t.Helper()
	// Copies keep the executable within the same trusted boundary as command_tools.
	cmd := exec.Command(verificationPython(t), "-m", "venv", "--without-pip", "--copies", filepath.Join(root, name))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create real venv: %v\n%s", err, output)
	}
	return verificationVenvPython(root, name)
}

func writeVerificationPythonTest(t *testing.T, root, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "test_environment.py"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveredPythonVerificationUsesRealProjectInterpreter(t *testing.T) {
	for _, name := range []string{".venv", "venv"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project with spaces")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			python := createVerificationVenv(t, root, name)
			if name == ".venv" {
				createVerificationVenv(t, root, "venv")
			}
			// This dependency exists only in the selected project's site-packages.
			output, err := exec.Command(python, "-c", "import sysconfig; print(sysconfig.get_path('purelib'))").Output()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(strings.TrimSpace(string(output)), "goflow_provenance_probe.py"), []byte("MARKER = 'project-only'\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			executableJSON, _ := json.Marshal(python)
			prefixJSON, _ := json.Marshal(filepath.Join(root, name))
			writeVerificationPythonTest(t, root, fmt.Sprintf(`import os, sys
import unittest
import goflow_provenance_probe
class Environment(unittest.TestCase):
    def test_provenance(self):
        self.assertEqual(os.path.normcase(sys.executable), os.path.normcase(%s))
        self.assertEqual(os.path.normcase(sys.prefix), os.path.normcase(%s))
        self.assertEqual(goflow_provenance_probe.MARKER, 'project-only')
        print('EXECUTABLE=' + sys.executable)
        print('PREFIX=' + sys.prefix)
`, executableJSON, prefixJSON))
			got := runDiscoveredWorkspaceVerification(root)
			if len(got) != 1 || got[0].Status != "passed" || !strings.Contains(got[0].Detail, python) || !strings.Contains(got[0].Detail, "EXECUTABLE=") || !strings.Contains(got[0].Detail, "PREFIX=") {
				t.Fatalf("real project interpreter and dependency must be used: %#v", got)
			}
		})
	}
}

func TestDiscoveredPythonVerificationBrokenEnvironmentDoesNotFallback(t *testing.T) {
	for _, broken := range []string{"missing interpreter", "invalid executable", "environment is file"} {
		t.Run(broken, func(t *testing.T) {
			root := t.TempDir()
			writeVerificationPythonTest(t, root, "import unittest\nclass Environment(unittest.TestCase):\n    def test_ok(self): pass\n")
			if broken == "environment is file" {
				if err := os.WriteFile(filepath.Join(root, ".venv"), []byte("broken"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				python := verificationVenvPython(root, ".venv")
				if err := os.MkdirAll(filepath.Dir(python), 0o755); err != nil {
					t.Fatal(err)
				}
				if broken == "invalid executable" {
					if err := os.WriteFile(python, []byte("not an executable"), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			createVerificationVenv(t, root, "venv")
			got := runDiscoveredWorkspaceVerification(root)
			if len(got) != 1 || got[0].Status != "failed" || !strings.Contains(got[0].Detail, ".venv") {
				t.Fatalf("broken .venv must fail instead of using venv or PATH: %#v", got)
			}
		})
	}
}

func TestDiscoveredPythonVerificationRejectsEscapingEnvironment(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	createVerificationVenv(t, outside, "venv")
	target, link := filepath.Join(outside, "venv"), filepath.Join(root, ".venv")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		// Directory junctions exercise the same escape without symlink privileges.
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("create test junction: %v\n%s", err, output)
		}
	}
	writeVerificationPythonTest(t, root, "import unittest\nclass Environment(unittest.TestCase): pass\n")
	got := runDiscoveredWorkspaceVerification(root)
	if len(got) != 1 || got[0].Status != "failed" || !strings.Contains(got[0].Detail, "outside workspace") {
		t.Fatalf("untrusted interpreter must be rejected: %#v", got)
	}
}

func TestDiscoveredPythonVerificationUsesPATHWithoutProjectEnvironment(t *testing.T) {
	python := verificationPython(t)
	// Discovery currently emits python; make its trusted PATH choice explicit.
	t.Setenv("PATH", filepath.Dir(python)+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	executableJSON, _ := json.Marshal(python)
	writeVerificationPythonTest(t, root, fmt.Sprintf(`import os, sys
import unittest
class Environment(unittest.TestCase):
    def test_provenance(self):
        self.assertEqual(os.path.normcase(sys.executable), os.path.normcase(%s))
        print('PATH_EXECUTABLE=' + sys.executable)
`, executableJSON))
	got := runDiscoveredWorkspaceVerification(root)
	if len(got) != 1 || got[0].Status != "passed" || !strings.Contains(got[0].Detail, python) || !strings.Contains(got[0].Detail, "PATH_EXECUTABLE=") {
		t.Fatalf("PATH is allowed only without a project environment: %#v", got)
	}
}

func TestDiscoveredPythonVerificationMissingDependencyDoesNotFallback(t *testing.T) {
	root := t.TempDir()
	python := createVerificationVenv(t, root, ".venv")
	writeVerificationPythonTest(t, root, "import unittest\nimport goflow_nonexistent_dependency\n")
	got := runDiscoveredWorkspaceVerification(root)
	if len(got) != 1 || got[0].Status != "failed" || !strings.HasPrefix(got[0].Detail, python+" ") || !strings.Contains(got[0].Detail, "goflow_nonexistent_dependency") {
		t.Fatalf("missing dependency must retain project interpreter failure: %#v", got)
	}
}

func TestDiscoverExistingPythonUnittestInsteadOfInventingPytest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "test_ui.py"), []byte("import unittest\nclass UI(unittest.TestCase): pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commands := discoverWorkspaceVerificationCommands(root)
	if !containsWorkspaceCommand(commands, "python -m unittest discover -s tests -v") || strings.Contains(strings.Join(commands, "\n"), "pytest") {
		t.Fatalf("existing unittest suite was not selected: %#v", commands)
	}
	if !strings.Contains(workflowGraphWorkspaceVerificationPrompt(root), "virtual environment") {
		t.Fatal("project environment guidance is missing")
	}
	if err := os.WriteFile(filepath.Join(root, "pytest.ini"), []byte("[pytest]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !containsWorkspaceCommand(discoverWorkspaceVerificationCommands(root), "python -m pytest -q") {
		t.Fatal("explicit pytest configuration must be honored")
	}
}
