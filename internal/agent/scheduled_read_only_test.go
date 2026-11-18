package agent

import (
	"context"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

func TestScheduledReadOnlyContextIntersectsToolKinds(t *testing.T) {
	profile := config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec}}
	restricted := applyContextToolBoundary(WithReadOnlyTools(context.Background()), profile)
	if len(restricted.AllowedToolKinds) != 1 || restricted.AllowedToolKinds[0] != config.ToolKindRead {
		t.Fatalf("unexpected read-only boundary %#v", restricted.AllowedToolKinds)
	}
}
