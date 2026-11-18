//go:build windows

package nativesandbox

import (
	"os"
	"testing"
)

// requireNativeSandboxHost skips AppContainer/ACL integration tests unless the
// host explicitly opts in. GitHub's Windows Server runners do not provide the
// AppContainer profile service that these tests exercise.
func requireNativeSandboxHost(t *testing.T) {
	t.Helper()
	if os.Getenv("GOFLOW_NATIVE_SANDBOX_TESTS") == "1" {
		return
	}
	t.Skip("set GOFLOW_NATIVE_SANDBOX_TESTS=1 on a Windows host with AppContainer support to run this test")
}
