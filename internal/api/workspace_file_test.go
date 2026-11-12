package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
)

func TestWorkspaceFileDownloadAndInlinePreview(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deliverables", "office"), 0o755); err != nil {
		t.Fatal(err)
	}
	pdfPath := filepath.Join(root, "deliverables", "office", "报告.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.4\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewServerWithWorkspace(newAPITestRuntime(t), workspace.New(root, true))

	preview := httptest.NewRecorder()
	server.ServeHTTP(preview, httptest.NewRequest(http.MethodGet, "/api/workspace-file?path=deliverables%2Foffice%2F%E6%8A%A5%E5%91%8A.pdf", nil))
	if preview.Code != http.StatusOK || preview.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("expected inline PDF, got status=%d headers=%v body=%s", preview.Code, preview.Header(), preview.Body.String())
	}
	if !strings.HasPrefix(preview.Header().Get("Content-Disposition"), "inline") || preview.Header().Get("X-GoFlow-Artifact-Path") != "deliverables/office/报告.pdf" {
		t.Fatalf("unexpected preview headers: %v", preview.Header())
	}

	download := httptest.NewRecorder()
	server.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/api/workspace-file?path=deliverables%2Foffice%2F%E6%8A%A5%E5%91%8A.pdf&download=1", nil))
	if download.Code != http.StatusOK || !strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("expected PDF attachment, got status=%d headers=%v", download.Code, download.Header())
	}
}

func TestWorkspaceFileRejectsEscapeSensitiveAndSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(outside, filepath.Join(root, "outside-link"))
	server := NewServerWithWorkspace(newAPITestRuntime(t), workspace.New(root, true))

	for _, path := range []string{"../outside.txt", ".env", "outside-link/outside.txt"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/workspace-file?path="+path, nil)
		server.ServeHTTP(response, request)
		if response.Code == http.StatusOK {
			t.Fatalf("expected path %q to be rejected", path)
		}
	}
}
