//go:build linux

package mcp

import (
	"reflect"
	"testing"
)

func TestBuildLinuxNetworkNamespaceCommandDefaultsToUnshareNet(t *testing.T) {
	command, args, env, err := buildLinuxNetworkNamespaceCommand(linuxNetworkNamespaceConfig{
		Command: "python",
		Args:    []string{"server.py"},
		Env:     []string{"PATH=/usr/bin"},
	})
	if err != nil {
		t.Fatalf("buildLinuxNetworkNamespaceCommand: %v", err)
	}
	if command != "unshare" {
		t.Fatalf("expected unshare command, got %q", command)
	}
	wantArgs := []string{"--net", "--", "python", "server.py"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("expected args %#v, got %#v", wantArgs, args)
	}
	if !reflect.DeepEqual(env, []string{"PATH=/usr/bin"}) {
		t.Fatalf("expected env to pass through, got %#v", env)
	}
}

func TestBuildLinuxNetworkNamespaceCommandSupportsExplicitUnshare(t *testing.T) {
	command, args, _, err := buildLinuxNetworkNamespaceCommand(linuxNetworkNamespaceConfig{
		Command: "/usr/bin/python3",
		Args:    []string{"server.py"},
		Options: map[string]string{
			"unshare_command": "/usr/bin/unshare",
			"map_root_user":   "true",
		},
	})
	if err != nil {
		t.Fatalf("buildLinuxNetworkNamespaceCommand: %v", err)
	}
	if command != "/usr/bin/unshare" {
		t.Fatalf("expected explicit unshare command, got %q", command)
	}
	wantArgs := []string{"--net", "--map-root-user", "--", "/usr/bin/python3", "server.py"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("expected args %#v, got %#v", wantArgs, args)
	}
}

func TestBuildLinuxNetworkNamespaceCommandRejectsUnsafeUnshareCommand(t *testing.T) {
	_, _, _, err := buildLinuxNetworkNamespaceCommand(linuxNetworkNamespaceConfig{
		Command: "python",
		Options: map[string]string{
			"unshare_command": "relative/unshare",
		},
	})
	if err == nil {
		t.Fatal("expected relative unshare command to fail")
	}
}
