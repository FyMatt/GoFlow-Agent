package sshhostkey

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseComputesOpenSSHFingerprintAndDeduplicates(t *testing.T) {
	blob := []byte("test-public-host-key-blob")
	encoded := base64.StdEncoding.EncodeToString(blob)
	line := "edge.example.test ssh-ed25519 " + encoded + "\n"
	keys, err := Parse("# comment\n" + line + line)
	if err != nil || len(keys) != 1 {
		t.Fatalf("parse host key: keys=%#v err=%v", keys, err)
	}
	digest := sha256.Sum256(blob)
	want := "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
	if keys[0].Fingerprint != want || keys[0].KeyType != "ssh-ed25519" || keys[0].KeyData != encoded {
		t.Fatalf("unexpected host key %#v want=%s", keys[0], want)
	}
}

func TestWindowsKeyscanUnsupportedKEXUsesInstalledCompatibleClient(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Win32 OpenSSH compatibility")
	}
	root := t.TempDir()
	t.Setenv("ProgramFiles", root)
	alternative := filepath.Join(root, "Git", "usr", "bin", "ssh-keyscan.exe")
	if err := os.MkdirAll(filepath.Dir(alternative), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alternative, []byte("fixture; not executed"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"ssh-keyscan", "custom-keyscan"} {
		t.Run(command, func(t *testing.T) {
			calls := 0
			keys, _, err := Scan(t.Context(), command, "192.0.2.10", 2222, func(_ context.Context, name string, args ...string) (CommandOutput, error) {
				calls++
				if calls == 1 {
					return CommandOutput{Stderr: "choose_kex: unsupported KEX method sntrup761x25519-sha512@openssh.com"}, errors.New("exit status 1")
				}
				if name != alternative || strings.Join(args, " ") != "-T 8 -p 2222 192.0.2.10" {
					t.Fatalf("fallback changed target or timeout: %s %v", name, args)
				}
				return CommandOutput{Stdout: "host ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("key"))}, nil
			})
			if command == "ssh-keyscan" {
				if err != nil || calls != 2 || len(keys) != 1 {
					t.Fatalf("compatibility retry failed: calls=%d err=%v", calls, err)
				}
			} else if err == nil || calls != 1 || !strings.Contains(err.Error(), "unsupported KEX method") {
				t.Fatalf("custom client was overridden or diagnostic lost: %v", err)
			}
		})
	}
}

func TestKeyscanDoesNotRetryNetworkErrors(t *testing.T) {
	calls := 0
	_, _, err := Scan(t.Context(), "ssh-keyscan", "192.0.2.10", 22, func(context.Context, string, ...string) (CommandOutput, error) {
		calls++
		return CommandOutput{Stderr: "Connection refused"}, errors.New("exit status 1")
	})
	if calls != 1 || err == nil || !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("network error was retried or hidden: calls=%d err=%v", calls, err)
	}
}

func TestScanUsesBoundedKeyscanArguments(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("host-key"))
	called := false
	keys, output, err := Scan(t.Context(), "custom-keyscan", "192.0.2.10", 2222, func(_ context.Context, name string, args ...string) (CommandOutput, error) {
		called = true
		if name != "custom-keyscan" || strings.Join(args, " ") != "-T 8 -p 2222 192.0.2.10" {
			t.Fatalf("unexpected command %s %v", name, args)
		}
		return CommandOutput{Stdout: "[192.0.2.10]:2222 ssh-ed25519 " + blob + "\n", Stderr: "notice"}, nil
	})
	if err != nil || !called || len(keys) != 1 || output.Stderr != "notice" {
		t.Fatalf("unexpected scan keys=%#v output=%#v err=%v", keys, output, err)
	}
}
