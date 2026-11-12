package agent

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
)

func repositoryInstructionPrompt(runtimeRef *Runtime) (string, []string, error) {
	if runtimeRef == nil {
		return "", nil, nil
	}
	items, err := workspace.LoadRepositoryInstructions(runtimeRef.WorkspaceRoot())
	if err != nil {
		return "", nil, err
	}
	paths := make([]string, 0, len(items))
	for _, item := range items {
		if path := strings.TrimSpace(item.Path); path != "" {
			paths = append(paths, path)
		}
	}
	return workspace.RepositoryInstructionPrompt(items), paths, nil
}
