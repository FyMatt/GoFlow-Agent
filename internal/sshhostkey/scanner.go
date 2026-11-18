package sshhostkey

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const MaxOutputBytes = 1024 * 1024

type Key struct {
	Host        string `json:"host"`
	KeyType     string `json:"key_type"`
	Fingerprint string `json:"fingerprint"`
	KeyData     string `json:"-"`
}

type CommandOutput struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

type CommandRunner func(context.Context, string, ...string) (CommandOutput, error)

type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.truncated = true
	}
	return written, nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }

func Scan(ctx context.Context, command, host string, port int, runner CommandRunner) ([]Key, CommandOutput, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "ssh-keyscan"
	}
	if runner == nil {
		runner = RunBoundedCommand
	}
	output, err := runner(ctx, command, "-T", "8", "-p", strconv.Itoa(port), host)
	// The inbox Win32 keyscan can advertise sntrup without implementing it.
	// Retry only that local client failure with Git's installed OpenSSH; the
	// caller still verifies the collected key against the pinned fingerprint.
	if runtime.GOOS == "windows" && command == "ssh-keyscan" && err != nil && ctx.Err() == nil &&
		strings.TrimSpace(output.Stdout) == "" && strings.Contains(output.Stderr, "unsupported KEX method") {
		alternative := filepath.Join(os.Getenv("ProgramFiles"), "Git", "usr", "bin", "ssh-keyscan.exe")
		if filepath.IsAbs(alternative) {
			if info, statErr := os.Stat(alternative); statErr == nil && info.Mode().IsRegular() {
				output, err = runner(ctx, alternative, "-T", "8", "-p", strconv.Itoa(port), host)
			}
		}
	}
	if err != nil && strings.TrimSpace(output.Stdout) == "" {
		detail := strings.TrimSpace(output.Stderr)
		if len(detail) > 1000 {
			detail = detail[:1000]
		}
		return nil, output, fmt.Errorf("SSH host-key scan failed: %w; %s", err, detail)
	}
	keys, parseErr := Parse(output.Stdout)
	if parseErr != nil {
		return nil, output, parseErr
	}
	if len(keys) == 0 {
		return nil, output, fmt.Errorf("SSH host-key scan returned no usable keys")
	}
	return keys, output, nil
}

func Parse(raw string) ([]Key, error) {
	keys := make([]Key, 0)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		blob, err := base64.StdEncoding.DecodeString(fields[2])
		if err != nil || len(blob) == 0 {
			continue
		}
		digest := sha256.Sum256(blob)
		fingerprint := "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
		key := fingerprint + "\x00" + fields[1]
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, Key{Host: fields[0], KeyType: fields[1], Fingerprint: fingerprint, KeyData: fields[2]})
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("SSH host-key output did not contain a valid public key")
	}
	return keys, nil
}

func RunBoundedCommand(ctx context.Context, name string, args ...string) (CommandOutput, error) {
	var stdout, stderr boundedBuffer
	stdout.limit, stderr.limit = MaxOutputBytes, MaxOutputBytes
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	output := CommandOutput{Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.truncated || stderr.truncated}
	if err == nil {
		return output, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		output.ExitCode = exitErr.ExitCode()
	} else {
		output.ExitCode = -1
	}
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	return output, err
}
