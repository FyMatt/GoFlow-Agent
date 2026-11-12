package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillResourceDeleteRemovesManagedSkillDirectory(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	body := `{"name":"temporary-skill","description":"Temporary skill","instructions":"Do the delegated task and report evidence."}`
	save := httptest.NewRecorder()
	server.ServeHTTP(save, httptest.NewRequest(http.MethodPut, "/api/resources/skills/temporary-skill", strings.NewReader(body)))
	if save.Code != http.StatusOK {
		t.Fatalf("save skill: status=%d body=%s", save.Code, save.Body.String())
	}

	path := filepath.Join(server.runtime.SkillRoot(), "temporary-skill", "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected saved SKILL.md: %v", err)
	}

	remove := httptest.NewRecorder()
	server.ServeHTTP(remove, httptest.NewRequest(http.MethodDelete, "/api/resources/skills/temporary-skill", nil))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("delete skill: status=%d body=%s", remove.Code, remove.Body.String())
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("expected skill directory removal, stat error=%v", err)
	}
}
