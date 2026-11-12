package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/commandpolicy"
)

const managedProcessLogLimit = 64 * 1024

type managedProcessInput struct {
	Program            string   `json:"program"`
	Args               []string `json:"args"`
	CWD                string   `json:"cwd"`
	EnvRefs            []string `json:"env_refs"`
	StartupWaitSeconds int      `json:"startup_wait_seconds"`
}

type managedProcess struct {
	id             string
	program        string
	args           []string
	cwd            string
	classification commandpolicy.Classification
	envRefs        []string
	cmd            *exec.Cmd
	guard          *processTreeGuard
	stdout         *managedProcessLog
	stderr         *managedProcessLog
	startedAt      time.Time
	done           chan struct{}
	mu             sync.RWMutex
	exitCode       int
	waitError      string
	stopRequested  bool
}

type managedProcessLog struct {
	mu        sync.Mutex
	data      []byte
	total     int64
	truncated bool
}

func (l *managedProcessLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total += int64(len(data))
	l.data = append(l.data, data...)
	if len(l.data) > managedProcessLogLimit {
		l.data = append([]byte(nil), l.data[len(l.data)-managedProcessLogLimit:]...)
		l.truncated = true
	}
	return len(data), nil
}

func (l *managedProcessLog) snapshot() (string, int64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(bytes.ToValidUTF8(l.data, []byte("�"))), l.total, l.truncated
}

var managedProcesses = struct {
	sync.Mutex
	items map[string]*managedProcess
}{items: make(map[string]*managedProcess)}

func startManagedProcess(input managedProcessInput) (map[string]any, error) {
	if len(input.Args) > 128 {
		return nil, fmt.Errorf("too many arguments: maximum 128")
	}
	for index, arg := range input.Args {
		if len(arg) > 8192 || strings.ContainsRune(arg, '\x00') {
			return nil, fmt.Errorf("argument %d is invalid", index+1)
		}
	}
	cwd, err := resolveWorkspaceDirectory(input.CWD, workspaceRoot)
	if err != nil {
		return nil, err
	}
	program, executable, _, err := resolveCommandProgram(input.Program, cwd, workspaceRoot)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSuffix(filepath.Base(program), ".exe"), "git") {
		return nil, fmt.Errorf("git is not a supported managed service process")
	}
	env, envRefs, err := commandEnvironment(input.EnvRefs)
	if err != nil {
		return nil, err
	}
	id, err := newManagedProcessID()
	if err != nil {
		return nil, err
	}
	stdout := &managedProcessLog{}
	stderr := &managedProcessLog{}
	cmd := exec.Command(executable, input.Args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	prepareCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", program, err)
	}
	guard, err := attachProcessTree(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, fmt.Errorf("attach managed process tree: %w", err)
	}
	process := &managedProcess{
		id:             id,
		program:        program,
		args:           append([]string(nil), input.Args...),
		cwd:            cwd,
		classification: commandpolicy.Classify(program, input.Args),
		envRefs:        envRefs,
		cmd:            cmd,
		guard:          guard,
		stdout:         stdout,
		stderr:         stderr,
		startedAt:      time.Now().UTC(),
		done:           make(chan struct{}),
		exitCode:       -1,
	}
	managedProcesses.Lock()
	pruneManagedProcessesLocked(16)
	managedProcesses.items[id] = process
	managedProcesses.Unlock()
	go process.wait()
	wait := input.StartupWaitSeconds
	if wait <= 0 {
		wait = 1
	}
	if wait > 15 {
		wait = 15
	}
	select {
	case <-process.done:
	case <-time.After(time.Duration(wait) * time.Second):
	}
	return process.payload(), nil
}

func (p *managedProcess) wait() {
	err := p.cmd.Wait()
	closeProcessTreeGuard(p.guard)
	p.mu.Lock()
	if p.cmd.ProcessState != nil {
		p.exitCode = p.cmd.ProcessState.ExitCode()
	}
	if err != nil {
		p.waitError = err.Error()
	}
	p.mu.Unlock()
	close(p.done)
}

func managedProcessStatus(id string) (map[string]any, error) {
	process, err := findManagedProcess(id)
	if err != nil {
		return nil, err
	}
	return process.payload(), nil
}

func stopManagedProcess(id string) (map[string]any, error) {
	process, err := findManagedProcess(id)
	if err != nil {
		return nil, err
	}
	process.mu.Lock()
	process.stopRequested = true
	process.mu.Unlock()
	select {
	case <-process.done:
		return process.payload(), nil
	default:
	}
	if err := terminateProcessTree(process.cmd, process.guard); err != nil {
		return nil, err
	}
	select {
	case <-process.done:
	case <-time.After(5 * time.Second):
		_ = process.cmd.Process.Kill()
		<-process.done
	}
	return process.payload(), nil
}

func stopAllManagedProcesses() {
	managedProcesses.Lock()
	items := make([]*managedProcess, 0, len(managedProcesses.items))
	for _, process := range managedProcesses.items {
		items = append(items, process)
	}
	managedProcesses.Unlock()
	for _, process := range items {
		_, _ = stopManagedProcess(process.id)
	}
}

func findManagedProcess(id string) (*managedProcess, error) {
	if len(id) != 24 {
		return nil, fmt.Errorf("process_id is invalid")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return nil, fmt.Errorf("process_id is invalid")
	}
	managedProcesses.Lock()
	process := managedProcesses.items[id]
	managedProcesses.Unlock()
	if process == nil {
		return nil, fmt.Errorf("managed process not found")
	}
	return process, nil
}

func (p *managedProcess) payload() map[string]any {
	p.mu.RLock()
	exitCode := p.exitCode
	waitError := p.waitError
	stopRequested := p.stopRequested
	p.mu.RUnlock()
	running := true
	select {
	case <-p.done:
		running = false
	default:
	}
	stdout, stdoutBytes, stdoutTruncated := p.stdout.snapshot()
	stderr, stderrBytes, stderrTruncated := p.stderr.snapshot()
	return map[string]any{
		"process_id":       p.id,
		"pid":              p.cmd.Process.Pid,
		"program":          p.program,
		"args":             p.args,
		"env_refs":         p.envRefs,
		"cwd":              p.cwd,
		"relative_cwd":     relativeWorkspacePath(p.cwd),
		"classification":   p.classification.Class,
		"risk_level":       p.classification.RiskLevel,
		"running":          running,
		"exit_code":        exitCode,
		"started_at":       p.startedAt.Format(time.RFC3339Nano),
		"stop_requested":   stopRequested,
		"process_error":    waitError,
		"stdout":           stdout,
		"stderr":           stderr,
		"stdout_bytes":     stdoutBytes,
		"stderr_bytes":     stderrBytes,
		"stdout_truncated": stdoutTruncated,
		"stderr_truncated": stderrTruncated,
	}
}

func pruneManagedProcessesLocked(limit int) {
	if len(managedProcesses.items) < limit {
		return
	}
	type completed struct {
		id      string
		started time.Time
	}
	items := make([]completed, 0)
	for id, process := range managedProcesses.items {
		select {
		case <-process.done:
			items = append(items, completed{id: id, started: process.startedAt})
		default:
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].started.Before(items[j].started) })
	for len(managedProcesses.items) >= limit && len(items) > 0 {
		delete(managedProcesses.items, items[0].id)
		items = items[1:]
	}
}

func newManagedProcessID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
