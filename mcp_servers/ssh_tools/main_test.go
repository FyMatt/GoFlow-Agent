package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/executiontarget"
)

func TestParseHostKeysComputesOpenSSHFingerprint(t *testing.T) {
	blob := []byte("test-public-host-key-blob")
	encoded := base64.StdEncoding.EncodeToString(blob)
	keys, err := parseHostKeys("edge.example.test ssh-ed25519 " + encoded + "\n")
	if err != nil || len(keys) != 1 {
		t.Fatalf("parse host key: keys=%#v err=%v", keys, err)
	}
	digest := sha256.Sum256(blob)
	want := "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
	if keys[0].Fingerprint != want || keys[0].KeyType != "ssh-ed25519" {
		t.Fatalf("unexpected host key %#v want=%s", keys[0], want)
	}
}

func TestRunToolRejectsCommandOutsideExactAllowlistBeforeNetwork(t *testing.T) {
	root, _, _ := prepareSSHTestTarget(t, []string{"uptime"})
	t.Setenv("GOFLOW_DATA_ROOT", root)
	original := runCommand
	t.Cleanup(func() { runCommand = original })
	called := false
	runCommand = func(context.Context, string, ...string) (commandOutput, error) {
		called = true
		return commandOutput{}, nil
	}
	_, err := runTool(t.Context(), toolInput{TargetID: "edge-a", Command: "uname -a"})
	if err == nil || !strings.Contains(err.Error(), "outside the exact allowlist") || called {
		t.Fatalf("expected pre-network allowlist rejection, called=%t err=%v", called, err)
	}
}

func TestRunToolRejectsPinnedHostKeyMismatch(t *testing.T) {
	root, _, _ := prepareSSHTestTarget(t, []string{"uptime"})
	t.Setenv("GOFLOW_DATA_ROOT", root)
	original := runCommand
	t.Cleanup(func() { runCommand = original })
	otherKey := hostKeyLine([]byte("different-host-key"))
	runCommand = func(_ context.Context, name string, _ ...string) (commandOutput, error) {
		if strings.Contains(filepath.Base(name), "keyscan") {
			return commandOutput{Stdout: otherKey}, nil
		}
		t.Fatal("ssh command must not run after host-key mismatch")
		return commandOutput{}, nil
	}
	_, err := runTool(t.Context(), toolInput{TargetID: "edge-a", Command: "uptime"})
	if err == nil || !strings.Contains(err.Error(), "host key mismatch") {
		t.Fatalf("expected host-key mismatch, got %v", err)
	}
}

func TestRunToolUsesStrictSSHOptionsAndReturnsBoundedEvidence(t *testing.T) {
	root, keyLine, fingerprint := prepareSSHTestTarget(t, []string{"uptime"})
	t.Setenv("GOFLOW_DATA_ROOT", root)
	original := runCommand
	t.Cleanup(func() { runCommand = original })
	sshCalls := 0
	runCommand = func(_ context.Context, name string, args ...string) (commandOutput, error) {
		if strings.Contains(filepath.Base(name), "keyscan") {
			return commandOutput{Stdout: keyLine}, nil
		}
		sshCalls++
		joined := strings.Join(args, " ")
		for _, required := range []string{"BatchMode=yes", "PasswordAuthentication=no", "IdentitiesOnly=yes", "StrictHostKeyChecking=yes", "uptime"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("missing strict SSH option %q in %s", required, joined)
			}
		}
		identity := argumentAfter(args, "-i")
		if data, err := os.ReadFile(identity); err != nil || !strings.Contains(string(data), "PRIVATE KEY") {
			t.Fatalf("temporary identity was not usable: path=%s err=%v", identity, err)
		}
		knownHosts := optionValue(args, "UserKnownHostsFile=")
		if data, err := os.ReadFile(knownHosts); err != nil || !strings.Contains(string(data), fingerprint) && !strings.Contains(string(data), "ssh-ed25519") {
			t.Fatalf("temporary known_hosts was not usable: path=%s data=%q err=%v", knownHosts, data, err)
		}
		return commandOutput{Stdout: "up 10 days\n", ExitCode: 0}, nil
	}
	payload, err := runTool(t.Context(), toolInput{TargetID: "edge-a", Command: " uptime ", TimeoutSeconds: 5})
	if err != nil || sshCalls != 1 || payload["status"] != "ok" || payload["stdout"] != "up 10 days\n" || payload["host_key_sha256"] != fingerprint {
		t.Fatalf("unexpected SSH result calls=%d payload=%#v err=%v", sshCalls, payload, err)
	}
}

func TestRunToolPropagatesCancellationToSSHCommand(t *testing.T) {
	root, keyLine, _ := prepareSSHTestTarget(t, []string{"uptime"})
	t.Setenv("GOFLOW_DATA_ROOT", root)
	original := runCommand
	t.Cleanup(func() { runCommand = original })
	started := make(chan struct{})
	runCommand = func(ctx context.Context, name string, _ ...string) (commandOutput, error) {
		if strings.Contains(filepath.Base(name), "keyscan") {
			return commandOutput{Stdout: keyLine}, nil
		}
		close(started)
		<-ctx.Done()
		return commandOutput{ExitCode: -1}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan map[string]any, 1)
	go func() {
		payload, _ := runTool(ctx, toolInput{TargetID: "edge-a", Command: "uptime", TimeoutSeconds: 30})
		done <- payload
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("SSH command did not start")
	}
	select {
	case payload := <-done:
		if payload["status"] != "failed" || payload["is_error"] != true || !strings.Contains(payload["error"].(string), "canceled") {
			t.Fatalf("unexpected cancellation payload %#v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSH command did not stop after cancellation")
	}
}

func TestScanToolRequiresExplicitAuthorizationForUnstoredHost(t *testing.T) {
	_, err := scanTool(t.Context(), toolInput{Host: "192.0.2.10", Port: 22})
	if err == nil || !strings.Contains(err.Error(), "authorized_scope") {
		t.Fatalf("expected authorization requirement, got %v", err)
	}
}

func prepareSSHTestTarget(t *testing.T, commands []string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	line := hostKeyLine([]byte("test-host-key"))
	keys, err := parseHostKeys(line)
	if err != nil {
		t.Fatalf("parse test host key: %v", err)
	}
	fingerprint := keys[0].Fingerprint
	reference := "secret://targets/edge-a/private_key"
	secretPath, err := config.SecretReferencePath(root, reference)
	if err != nil {
		t.Fatalf("secret path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatalf("create secret dir: %v", err)
	}
	if err := os.WriteFile(secretPath, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatalf("write test private key: %v", err)
	}
	_, err = executiontarget.NewStore(root).Put(executiontarget.Target{
		ID: "edge-a", Kind: executiontarget.KindSSH, DisplayName: "Edge A", Host: "192.0.2.10", Port: 2222,
		User: "operator", CredentialRef: reference, HostKeySHA256: fingerprint, AllowedCommands: commands, Enabled: true,
	})
	if err != nil {
		t.Fatalf("store test target: %v", err)
	}
	return root, line, fingerprint
}

func hostKeyLine(blob []byte) string {
	return "[192.0.2.10]:2222 ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + "\n"
}

func argumentAfter(args []string, name string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == name {
			return args[index+1]
		}
	}
	return ""
}

func optionValue(args []string, prefix string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func TestKnownHostNameIncludesNonDefaultPort(t *testing.T) {
	if got := knownHostName("2001:db8::1", 2222); got != "[2001:db8::1]:2222" {
		t.Fatalf("unexpected known-host name %q", got)
	}
	if got := knownHostName("example.test", 22); got != "example.test" {
		t.Fatalf("unexpected default-port host %q", got)
	}
}
