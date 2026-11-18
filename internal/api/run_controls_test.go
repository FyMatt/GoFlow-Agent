package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
)

func TestDecodeRunRequestControls(t *testing.T) {
	request := httptest.NewRequest("POST", "/api/runs", strings.NewReader(`{"input":"inspect","reasoning_effort":"high","permission_mode":"workspace"}`))
	decoded, err := decodeRunRequest(request)
	if err != nil {
		t.Fatalf("decodeRunRequest: %v", err)
	}
	controls := runControlsFromRequest(decoded)
	if controls.ReasoningEffort != "high" || controls.PermissionMode != agent.RunPermissionWorkspace {
		t.Fatalf("unexpected controls: %#v", controls)
	}
}
