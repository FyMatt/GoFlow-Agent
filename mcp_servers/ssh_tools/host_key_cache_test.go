package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/executiontarget"
)

func TestSSHCommandsReusePinnedKeyButAlwaysVerifyWithSSH(t *testing.T) {
	root, keyLine, _ := prepareSSHTestTarget(t, []string{"uptime"})
	t.Setenv("GOFLOW_DATA_ROOT", root)
	original := runCommand
	t.Cleanup(func() { runCommand = original })
	scans, logins := 0, 0
	runCommand = func(_ context.Context, name string, args ...string) (commandOutput, error) {
		if strings.Contains(filepath.Base(name), "keyscan") {
			scans++
			return commandOutput{Stdout: keyLine}, nil
		}
		logins++
		if !strings.Contains(strings.Join(args, " "), "StrictHostKeyChecking=yes") {
			t.Fatal("key cache bypassed per-connection host identity verification")
		}
		return commandOutput{Stdout: "up"}, nil
	}
	ctx := withHostKeyCache(t.Context())
	if _, err := scanTool(ctx, toolInput{TargetID: "edge-a"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		result, err := runTool(ctx, toolInput{TargetID: "edge-a", Command: "uptime"})
		if err != nil {
			t.Fatal(err)
		}
		if result["host_key_cached"] != true {
			t.Fatal("result hides the use of a cached public key")
		}
	}
	if scans != 1 || logins != 3 {
		t.Fatalf("repeated unauthenticated key scans: scans=%d logins=%d", scans, logins)
	}
	// Changing the pin must cause fresh collection and rejection, not reuse.
	store := executiontarget.NewStore(root)
	target, _, _ := store.Get("edge-a")
	target.HostKeySHA256 = "SHA256:" + strings.Repeat("A", 43)
	if _, err := store.Put(target); err != nil {
		t.Fatal(err)
	}
	if _, err := runTool(ctx, toolInput{TargetID: "edge-a", Command: "uptime"}); err == nil || !strings.Contains(err.Error(), "host key mismatch") {
		t.Fatalf("changed fingerprint used cached trust: %v", err)
	}
	if logins != 3 || scans != 2 {
		t.Fatal("mismatched host was authenticated")
	}
}

func TestPinnedKeyCacheIsBoundedExpiresAndSeparatesEndpoints(t *testing.T) {
	ctx := withHostKeyCache(t.Context())
	target := executiontarget.Target{Host: "192.0.2.10", Port: 22, HostKeySHA256: "pin"}
	cachedPinnedHostKey(ctx, target, "public key")
	other := target
	other.Port = 2222
	if cachedPinnedHostKey(ctx, other, "") != "" {
		t.Fatal("cache crossed a target port")
	}
	cache := ctx.Value(hostKeyCacheContextKey{}).(*hostKeyCache)
	for key, entry := range cache.entries {
		entry.expires = time.Now().Add(-time.Second)
		cache.entries[key] = entry
	}
	if cachedPinnedHostKey(ctx, target, "") != "" {
		t.Fatal("expired key was reused")
	}
	for port := 1; port <= 100; port++ {
		target.Port = port
		cachedPinnedHostKey(ctx, target, "public key")
	}
	if len(cache.entries) > 64 {
		t.Fatal("unbounded host key cache")
	}
}
