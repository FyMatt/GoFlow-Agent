package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	apipkg "github.com/FyMatt/GoFlow-Agent/internal/api"
	apppkg "github.com/FyMatt/GoFlow-Agent/internal/app"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/memory"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/skill"
	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const (
	defaultConfigFileName = "goflow.yaml"
	binaryConfigFileName  = "goflow.binary.yaml"

	cliWorkflowSchemaBundleKind                = "goflow.workflow_schemas"
	cliWorkflowSchemaBundleVersion             = 2
	cliWorkflowSchemaBundleMinSupportedVersion = 1
)

var terminalInputSupported = func(input io.Reader, output io.Writer) bool {
	stdin, inputOK := input.(*os.File)
	stdout, outputOK := output.(*os.File)
	if !inputOK || !outputOK {
		return false
	}
	inputStat, err := stdin.Stat()
	if err != nil {
		return false
	}
	outputStat, err := stdout.Stat()
	if err != nil {
		return false
	}
	return (inputStat.Mode()&os.ModeCharDevice) != 0 && (outputStat.Mode()&os.ModeCharDevice) != 0
}

type approvalInputReader interface {
	ReadRune() (rune, int, error)
}

var newApprovalInputReader = func(input io.Reader) approvalInputReader {
	file, ok := input.(*os.File)
	if !ok {
		return bufio.NewReader(input)
	}
	if !term.IsTerminal(int(file.Fd())) {
		return nil
	}
	state, err := term.MakeRaw(int(file.Fd()))
	if err != nil {
		return nil
	}
	return &rawApprovalReader{file: file, restore: func() { _ = term.Restore(int(file.Fd()), state) }}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtimeInfo, err := resolveRuntimeHome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve runtime home error: %v\n", err)
		os.Exit(1)
	}
	applyStartupEnvDefaults(runtimeInfo)
	paths, err := resolvePathSettingsWithDefault(runtimeInfo.Root, os.Args[1:], defaultConfigPathForRuntime(runtimeInfo))
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve paths error: %v\n", err)
		os.Exit(1)
	}
	workspaceState := newWorkspaceLifecycle(paths.WorkspaceRoot, paths.WorkspaceExplicit)

	app, err := apppkg.Bootstrap(ctx, apppkg.BootstrapOptions{RuntimeHome: runtimeInfo.Root, WorkspaceRoot: paths.WorkspaceRoot, ConfigPath: paths.ConfigPath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap runtime error: %v\n", err)
		os.Exit(1)
	}
	var appMu sync.Mutex
	currentApp := app
	defer func() {
		appMu.Lock()
		appToClose := currentApp
		currentApp = nil
		appMu.Unlock()
		if err := appToClose.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close runtime error: %v\n", err)
		}
	}()
	cfg := app.Config
	sessionState := app.SessionState
	agentRuntime := app.Runtime
	agentRuntime.SetWorkspaceConfirmed(workspaceState.Confirmed())
	skillManager := app.SkillManager.(*skill.Manager)
	mcpManager := app.MCPClient

	if strings.TrimSpace(paths.HTTPAddr) != "" {
		server := &http.Server{
			Addr: paths.HTTPAddr,
			Handler: apipkg.NewServerWithWorkspaceRebinder(agentRuntime, workspaceState, func(ctx context.Context, path string) (*agent.Runtime, *workspaceLifecycle, error) {
				appMu.Lock()
				activeApp := currentApp
				appMu.Unlock()
				if activeApp == nil || activeApp.Config == nil {
					return nil, nil, fmt.Errorf("runtime app is not available")
				}
				newApp, err := apppkg.Bootstrap(ctx, apppkg.BootstrapOptions{
					RuntimeHome:   activeApp.Config.RuntimeHome,
					WorkspaceRoot: path,
					ConfigPath:    activeApp.ConfigPath,
				})
				if err != nil {
					return nil, nil, err
				}
				newWorkspace := newWorkspaceLifecycle(path, true)
				newApp.Runtime.SetWorkspaceConfirmed(newWorkspace.Confirmed())
				appMu.Lock()
				oldApp := currentApp
				appMu.Unlock()
				if oldApp != nil {
					if err := oldApp.SaveSession(); err != nil {
						_ = newApp.Close()
						return nil, nil, err
					}
					if err := oldApp.Close(); err != nil {
						_ = newApp.Close()
						return nil, nil, err
					}
				}
				appMu.Lock()
				currentApp = newApp
				appMu.Unlock()
				return newApp.Runtime, newWorkspace, nil
			}),
		}
		consoleURL := displayHTTPURL(paths.HTTPAddr, "/console")
		workflowURL := displayHTTPURL(paths.HTTPAddr, "/workflows")
		fmt.Printf("GoFlow HTTP API ready. runtime=%s workspace=%s addr=%s\n", cfg.RuntimeHome, workspaceState.DisplayRoot(), paths.HTTPAddr)
		fmt.Printf("Open Web Studio: %s\n", consoleURL)
		fmt.Printf("Workflow Studio: %s\n", workflowURL)
		fmt.Println("Note: this local server uses plain HTTP, not HTTPS.")
		fmt.Println("Press Ctrl+C to stop the HTTP server.")
		if err := runHTTPServer(ctx, server, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "http server error: %v\n", err)
			os.Exit(1)
		}
		appMu.Lock()
		activeApp := currentApp
		appMu.Unlock()
		if activeApp != nil {
			if err := activeApp.SaveSession(); err != nil {
				fmt.Fprintf(os.Stderr, "save session error: %v\n", err)
			}
		} else if err := sessionState.Save(cfg.Session.PersistPath); err != nil {
			fmt.Fprintf(os.Stderr, "save session error: %v\n", err)
		}
		return
	}

	setTerminalTitle(buildTerminalTitle(cfg.WorkspaceRoot), os.Stdin, os.Stdout)
	fmt.Print(renderStartupBanner(buildStartupDisplayRow(agentRuntime, cfg.RuntimeHome, workspaceState.DisplayRoot())))
	lineReader := newCLIInputLineReader(os.Stdin, os.Stdout, cfg.WorkspaceRoot)
	var lastCancelledTask cancelledTaskState
	for {
		input, ok, err := lineReader.ReadLine(formatPrompt(agentRuntime))
		if err != nil {
			fmt.Fprintf(os.Stderr, "input error: %v\n", err)
			break
		}
		if !ok {
			break
		}
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println(styleStatus("Session closed.", "ready"))
			if err := sessionState.Save(cfg.Session.PersistPath); err != nil {
				fmt.Fprintf(os.Stderr, "save session error: %v\n", err)
			}
			return
		}
		if parseWorkspaceChooseInput(input) {
			selectedPath, cancelled, err := workspace.PickFolder(ctx, workspaceState.Root())
			if err != nil {
				fmt.Println(formatCommandWarning(err.Error()))
				continue
			}
			if cancelled || strings.TrimSpace(selectedPath) == "" {
				fmt.Println(formatCommandWarning("workspace folder selection cancelled"))
				continue
			}
			input = "/workspace use " + selectedPath
		}
		if workspacePath, ok, usageErr := parseWorkspaceUseInput(input); ok {
			if usageErr != "" {
				fmt.Fprintln(os.Stderr, usageErr)
				continue
			}
			if workspaceSamePath(workspacePath, workspaceState.Root()) {
				workspaceState.Confirm()
				agentRuntime.SetWorkspaceConfirmed(true)
				fmt.Println(formatCommandSuccess("workspace", workspaceState.Root()))
				if err := sessionState.Save(cfg.Session.PersistPath); err != nil {
					fmt.Fprintf(os.Stderr, "save session error: %v\n", err)
				}
				continue
			}
			appMu.Lock()
			activeApp := currentApp
			appMu.Unlock()
			newApp, newWorkspace, err := rebindCLIWorkspace(ctx, activeApp, workspacePath)
			if err != nil {
				fmt.Println(formatCLIWorkspaceSwitchError(workspacePath, err))
				continue
			}
			appMu.Lock()
			currentApp = newApp
			appMu.Unlock()
			cfg = newApp.Config
			sessionState = newApp.SessionState
			agentRuntime = newApp.Runtime
			agentRuntime.SetWorkspaceConfirmed(true)
			skillManager = newApp.SkillManager.(*skill.Manager)
			mcpManager = newApp.MCPClient
			workspaceState = newWorkspace
			updateCLIInputWorkspaceRoot(lineReader, cfg.WorkspaceRoot)
			fmt.Println(formatCommandSuccess("workspace rebound", workspaceState.Root()))
			if err := sessionState.Save(cfg.Session.PersistPath); err != nil {
				fmt.Fprintf(os.Stderr, "save session error: %v\n", err)
			}
			continue
		}
		if isCommandInput(input) {
			if handled := handleCommand(ctx, input, skillManager, mcpManager, agentRuntime, workspaceState); handled {
				agentRuntime.SetWorkspaceConfirmed(workspaceState.Confirmed())
				if err := sessionState.Save(cfg.Session.PersistPath); err != nil {
					fmt.Fprintf(os.Stderr, "save session error: %v\n", err)
				}
				continue
			}
		}
		if handled := handlePendingApprovalInput(ctx, input, agentRuntime); handled {
			if err := sessionState.Save(cfg.Session.PersistPath); err != nil {
				fmt.Fprintf(os.Stderr, "save session error: %v\n", err)
			}
			continue
		}
		if agentRuntime.HasPendingHandoff() && strings.EqualFold(agentRuntime.PendingHandoffDecision(input), "confirm") && !ensureWorkspaceConfirmed(workspaceState, "confirming an implementation handoff will run workspace tools") {
			continue
		}
		if handled := handlePendingHandoffInput(ctx, input, os.Stdin, os.Stdout, agentRuntime); handled {
			if err := sessionState.Save(cfg.Session.PersistPath); err != nil {
				fmt.Fprintf(os.Stderr, "save session error: %v\n", err)
			}
			continue
		}
		if !lastCancelledTask.IsZero() {
			if isContinuationOnlyInput(input) {
				fmt.Println(formatRetryingCancelledTask(lastCancelledTask))
				input = lastCancelledTask.Request
			} else {
				lastCancelledTask = cancelledTaskState{}
			}
		}
		if strings.Contains(input, "@") && !ensureWorkspaceConfirmed(workspaceState, "@file reference reads workspace files") {
			continue
		}
		if suggestions, handled, err := formatAtReferenceSuggestions(input, cfg.WorkspaceRoot); handled {
			if err != nil {
				fmt.Fprintf(os.Stderr, "reference suggestion error: %v\n", err)
				continue
			}
			fmt.Print(suggestions)
			continue
		}
		agentRuntime.ClearAudit()
		if requirement := workspaceRequirementForInput(input); requirement.Required && !ensureWorkspaceConfirmed(workspaceState, requirement.Reason) {
			continue
		}
		renderer := newCLIStreamRenderer(agentRuntime.TraceEnabled())
		expandedInput, refs, err := expandAtFileReferencesWithTools(ctx, input, cfg.WorkspaceRoot, mcpManager, renderer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reference error: %v\n", err)
			continue
		}
		expandedInput, artifactRefs, err := expandSessionArtifactReferencesForCLI(expandedInput, agentRuntime, renderer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "artifact reference error: %v\n", err)
			continue
		}
		if len(refs) > 0 {
			names := make([]string, 0, len(refs))
			for _, ref := range refs {
				names = append(names, "@"+ref.RelativePath)
			}
			fmt.Println(formatCommandSuccess("attached", strings.Join(names, ", ")))
		}
		if len(artifactRefs) > 0 {
			fmt.Println(formatCommandSuccess("attached artifacts", strings.Join(artifactRefs, ", ")))
		}
		taskAgent := agentRuntime.ActiveAgent()
		taskMode := agentRuntime.Mode()
		lastCancelledTask = cancelledTaskState{}
		result, err := runCancelableAgentOperation(ctx, os.Stdin, os.Stdout, func(taskCtx context.Context) (schema.AgentResult, error) {
			return agentRuntime.RunStream(taskCtx, expandedInput, renderer.Handle)
		})
		if err != nil {
			renderer.Flush()
			if errors.Is(err, context.Canceled) {
				lastCancelledTask = cancelledTaskState{Request: input, Agent: taskAgent, Mode: taskMode, CancelledAt: time.Now()}
				fmt.Println(formatTaskCancelled())
				continue
			}
			fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
			continue
		}
		renderer.Finish(result.Output)
		if err := handleOrdinaryChatPendingApproval(ctx, os.Stdin, os.Stdout, agentRuntime, renderer); err != nil {
			fmt.Fprintf(os.Stderr, "approval error: %v\n", err)
		}
		if err := sessionState.Save(cfg.Session.PersistPath); err != nil {
			fmt.Fprintf(os.Stderr, "save session error: %v\n", err)
		}
	}

	if err := lineReader.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "input error: %v\n", err)
	}
}

func runHTTPServer(ctx context.Context, server *http.Server, output io.Writer) error {
	if server == nil {
		return fmt.Errorf("http server is nil")
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		if output != nil {
			fmt.Fprintln(output, "GoFlow HTTP API shutting down...")
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown http server: %w", err)
		}
		select {
		case err := <-errCh:
			if err == nil || errors.Is(err, http.ErrServerClosed) {
				if output != nil {
					fmt.Fprintln(output, "GoFlow HTTP API stopped.")
				}
				return nil
			}
			return err
		case <-time.After(5 * time.Second):
			_ = server.Close()
			return fmt.Errorf("http server did not stop after shutdown")
		}
	}
}

func displayHTTPURL(addr, route string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = ":8080"
	}
	route = "/" + strings.TrimLeft(route, "/")
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/") + route
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			host, port = "", strings.TrimPrefix(addr, ":")
		} else if !strings.Contains(addr, ":") {
			host, port = "", addr
		} else {
			return "http://" + addr + route
		}
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	if port == "" {
		return "http://" + host + route
	}
	return "http://" + net.JoinHostPort(host, port) + route
}

type cliInputLineReader interface {
	ReadLine(prompt string) (string, bool, error)
	Err() error
}

func newCLIInputLineReader(input *os.File, output io.Writer, workspaceRoot string) cliInputLineReader {
	if input == nil || !terminalInputSupported(input, output) || !term.IsTerminal(int(input.Fd())) {
		return &scannerInputLineReader{scanner: bufio.NewScanner(input), output: output}
	}
	return &interactiveInputLineReader{input: input, output: output, workspaceRoot: workspaceRoot}
}

func updateCLIInputWorkspaceRoot(reader cliInputLineReader, workspaceRoot string) {
	if interactive, ok := reader.(*interactiveInputLineReader); ok && interactive != nil {
		interactive.workspaceRoot = workspaceRoot
	}
}

type scannerInputLineReader struct {
	scanner *bufio.Scanner
	output  io.Writer
}

func (r *scannerInputLineReader) ReadLine(prompt string) (string, bool, error) {
	fmt.Fprint(r.output, prompt)
	if !r.scanner.Scan() {
		return "", false, nil
	}
	return strings.TrimSpace(r.scanner.Text()), true, nil
}

func (r *scannerInputLineReader) Err() error {
	if r == nil || r.scanner == nil {
		return nil
	}
	return r.scanner.Err()
}

type interactiveInputLineReader struct {
	input         *os.File
	output        io.Writer
	workspaceRoot string
	history       []string
}

func (r *interactiveInputLineReader) ReadLine(prompt string) (string, bool, error) {
	fmt.Fprint(r.output, prompt)
	state, err := term.MakeRaw(int(r.input.Fd()))
	if err != nil {
		return "", false, err
	}
	defer func() { _ = term.Restore(int(r.input.Fd()), state) }()

	reader := bufio.NewReader(r.input)
	buffer := make([]rune, 0, 128)
	cursor := 0
	awaitingEscape := false
	historyIndex := len(r.history)
	var historyDraft []rune
	for {
		ch, _, err := reader.ReadRune()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", false, nil
			}
			return "", false, err
		}
		if awaitingEscape {
			awaitingEscape = false
			if ch == '[' || ch == 'O' {
				action := readInputEscapeAction(reader, ch)
				switch action {
				case inputEscapeUp, inputEscapeDown:
					if next, changed := r.navigateInputHistory(buffer, &historyIndex, &historyDraft, action); changed {
						buffer = next
						cursor = len(buffer)
						redrawInputLineWithCursor(r.output, prompt, buffer, cursor)
					}
				default:
					applyInputEscapeAction(r.output, prompt, &buffer, &cursor, action)
				}
				continue
			}
		}
		switch ch {
		case '\r', '\n':
			fmt.Fprint(r.output, "\r\n")
			line := strings.TrimSpace(string(buffer))
			r.rememberInput(line)
			return line, true, nil
		case 3:
			fmt.Fprint(r.output, "^C\r\n")
			return "", false, io.EOF
		case '\b', 0x7f:
			if cursor > 0 {
				buffer = append(buffer[:cursor-1], buffer[cursor:]...)
				cursor--
				redrawInputLineWithCursor(r.output, prompt, buffer, cursor)
			}
		case '\t':
			next, changed, message := completeInputToken(buffer, cursor, r.workspaceRoot)
			if message != "" {
				fmt.Fprint(r.output, "\r\n", message)
			}
			if changed {
				buffer = next
				cursor = len(buffer)
			}
			if changed || message != "" {
				redrawInputLineWithCursor(r.output, prompt, buffer, cursor)
			}
		case 0x1b:
			// Ignore terminal escape sequences during prompt input. The double-Esc
			// task cancellation watcher is active only while an agent operation runs.
			awaitingEscape = true
			continue
		default:
			if ch < 32 && ch != '\t' {
				continue
			}
			if cursor == len(buffer) {
				buffer = append(buffer, ch)
				cursor++
				fmt.Fprint(r.output, string(ch))
			} else {
				buffer = append(buffer[:cursor], append([]rune{ch}, buffer[cursor:]...)...)
				cursor++
				redrawInputLineWithCursor(r.output, prompt, buffer, cursor)
			}
			if ch == '@' {
				if message := formatInlineAtReferenceSuggestions(buffer[:cursor], r.workspaceRoot); message != "" {
					fmt.Fprint(r.output, "\r\n", message)
					redrawInputLineWithCursor(r.output, prompt, buffer, cursor)
				}
			}
			if ch == '/' && cursor == 1 {
				if message := formatInlineCommandSuggestions(buffer[:cursor]); message != "" {
					fmt.Fprint(r.output, "\r\n", message)
					redrawInputLineWithCursor(r.output, prompt, buffer, cursor)
				}
			}
		}
	}
}

func (r *interactiveInputLineReader) Err() error { return nil }

const maxInteractiveInputHistory = 100

func (r *interactiveInputLineReader) rememberInput(line string) {
	if r == nil {
		return
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(r.history) > 0 && r.history[len(r.history)-1] == line {
		return
	}
	r.history = append(r.history, line)
	if len(r.history) > maxInteractiveInputHistory {
		r.history = append([]string(nil), r.history[len(r.history)-maxInteractiveInputHistory:]...)
	}
}

func (r *interactiveInputLineReader) navigateInputHistory(buffer []rune, historyIndex *int, draft *[]rune, action inputEscapeAction) ([]rune, bool) {
	if r == nil || len(r.history) == 0 || historyIndex == nil || draft == nil {
		return buffer, false
	}
	if *historyIndex < 0 || *historyIndex > len(r.history) {
		*historyIndex = len(r.history)
	}
	switch action {
	case inputEscapeUp:
		if *historyIndex == len(r.history) {
			*draft = append((*draft)[:0], buffer...)
		}
		if *historyIndex == 0 {
			return buffer, false
		}
		*historyIndex--
		return []rune(r.history[*historyIndex]), true
	case inputEscapeDown:
		if *historyIndex >= len(r.history) {
			return buffer, false
		}
		*historyIndex++
		if *historyIndex == len(r.history) {
			return append([]rune(nil), (*draft)...), true
		}
		return []rune(r.history[*historyIndex]), true
	default:
		return buffer, false
	}
}

func redrawInputLine(output io.Writer, prompt string, buffer []rune) {
	fmt.Fprintf(output, "\r\033[2K%s%s", prompt, string(buffer))
}

func redrawInputLineWithCursor(output io.Writer, prompt string, buffer []rune, cursor int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(buffer) {
		cursor = len(buffer)
	}
	redrawInputLine(output, prompt, buffer)
	if delta := len(buffer) - cursor; delta > 0 {
		fmt.Fprintf(output, "\033[%dD", delta)
	}
}

type inputEscapeAction string

const (
	inputEscapeUnknown inputEscapeAction = ""
	inputEscapeLeft    inputEscapeAction = "left"
	inputEscapeRight   inputEscapeAction = "right"
	inputEscapeUp      inputEscapeAction = "up"
	inputEscapeDown    inputEscapeAction = "down"
	inputEscapeHome    inputEscapeAction = "home"
	inputEscapeEnd     inputEscapeAction = "end"
	inputEscapeDelete  inputEscapeAction = "delete"
)

func readInputEscapeAction(reader *bufio.Reader, introducer rune) inputEscapeAction {
	if introducer == 'O' {
		ch, _, err := reader.ReadRune()
		if err != nil {
			return inputEscapeUnknown
		}
		switch ch {
		case 'H':
			return inputEscapeHome
		case 'F':
			return inputEscapeEnd
		default:
			return inputEscapeUnknown
		}
	}
	if introducer != '[' {
		return inputEscapeUnknown
	}
	var seq strings.Builder
	seq.WriteRune('[')
	for i := 0; i < 8; i++ {
		ch, _, err := reader.ReadRune()
		if err != nil {
			return inputEscapeUnknown
		}
		seq.WriteRune(ch)
		if ch >= 0x40 && ch <= 0x7e {
			break
		}
	}
	switch seq.String() {
	case "[D":
		return inputEscapeLeft
	case "[C":
		return inputEscapeRight
	case "[A":
		return inputEscapeUp
	case "[B":
		return inputEscapeDown
	case "[H", "[1~", "[7~":
		return inputEscapeHome
	case "[F", "[4~", "[8~":
		return inputEscapeEnd
	case "[3~":
		return inputEscapeDelete
	default:
		return inputEscapeUnknown
	}
}

func applyInputEscapeAction(output io.Writer, prompt string, buffer *[]rune, cursor *int, action inputEscapeAction) {
	if buffer == nil || cursor == nil {
		return
	}
	switch action {
	case inputEscapeLeft:
		if *cursor > 0 {
			*cursor = *cursor - 1
			fmt.Fprint(output, "\033[D")
		}
	case inputEscapeRight:
		if *cursor < len(*buffer) {
			*cursor = *cursor + 1
			fmt.Fprint(output, "\033[C")
		}
	case inputEscapeHome:
		if *cursor != 0 {
			*cursor = 0
			redrawInputLineWithCursor(output, prompt, *buffer, *cursor)
		}
	case inputEscapeEnd:
		if *cursor != len(*buffer) {
			*cursor = len(*buffer)
			redrawInputLineWithCursor(output, prompt, *buffer, *cursor)
		}
	case inputEscapeDelete:
		if *cursor < len(*buffer) {
			*buffer = append((*buffer)[:*cursor], (*buffer)[*cursor+1:]...)
			redrawInputLineWithCursor(output, prompt, *buffer, *cursor)
		}
	}
}

func formatInlineAtReferenceSuggestions(buffer []rune, workspaceRoot string) string {
	start, prefix, ok := trailingAtReferenceToken(buffer)
	if !ok || start != len(buffer)-1 || prefix != "" {
		return ""
	}
	output, handled, err := formatAtReferenceSuggestions("@", workspaceRoot)
	if err != nil || !handled {
		return ""
	}
	return output
}

func formatInlineCommandSuggestions(buffer []rune) string {
	if len(buffer) != 1 || buffer[0] != '/' {
		return ""
	}
	var b strings.Builder
	b.WriteString(formatCommandWarning("Type /command or press Tab to complete. Available commands:"))
	b.WriteString("\n")
	for _, command := range commandCompletionNames() {
		b.WriteString("  ")
		b.WriteString(command)
		b.WriteString("\n")
	}
	return b.String()
}

func completeInputToken(buffer []rune, cursor int, workspaceRoot string) ([]rune, bool, string) {
	if next, changed, message := completeCommandToken(buffer, cursor); changed || message != "" {
		return next, changed, message
	}
	if cursor != len(buffer) {
		return buffer, false, ""
	}
	return completeAtReferenceToken(buffer, workspaceRoot)
}

func completeCommandToken(buffer []rune, cursor int) ([]rune, bool, string) {
	if cursor != len(buffer) || len(buffer) == 0 || buffer[0] != '/' {
		return buffer, false, ""
	}
	prefix := string(buffer[:cursor])
	if strings.ContainsAny(prefix, " \t\r\n") {
		return buffer, false, ""
	}
	commands := commandCompletionNames()
	matches := make([]string, 0, len(commands))
	for _, command := range commands {
		if strings.HasPrefix(command, prefix) {
			matches = append(matches, command)
		}
	}
	fuzzy := false
	if len(matches) == 0 && prefix != commandPrefix {
		matches = fuzzyFilterStrings(commands, prefix, len(commands))
		fuzzy = len(matches) > 0
		if fuzzy {
			if completion := closeEditDistanceCommandMatch(matches, prefix); completion != "" {
				matches = []string{completion}
			}
		}
	}
	if len(matches) == 0 {
		return buffer, false, formatCommandWarning(fmt.Sprintf("No commands match %s", prefix)) + "\n"
	}
	completion := ""
	if len(matches) == 1 {
		completion = matches[0] + " "
	} else if common := commonStringPrefix(matches); len(common) > len(prefix) {
		completion = common
	}
	if completion != "" {
		return []rune(completion), true, ""
	}
	var b strings.Builder
	if fuzzy {
		b.WriteString(formatCommandWarning(fmt.Sprintf("Fuzzy command matches for %s:", prefix)))
	} else {
		b.WriteString(formatCommandWarning(fmt.Sprintf("Matching commands for %s:", prefix)))
	}
	b.WriteString("\n")
	for _, match := range matches {
		b.WriteString("  ")
		b.WriteString(match)
		b.WriteString("\n")
	}
	return buffer, false, b.String()
}

func closeEditDistanceCommandMatch(matches []string, prefix string) string {
	query := stripCommandPrefix(prefix)
	if strings.TrimSpace(query) == "" {
		return ""
	}
	best := ""
	bestDistance := 3
	ties := 0
	for _, match := range matches {
		distance := editDistance(strings.ToLower(query), strings.ToLower(stripCommandPrefix(match)))
		if distance < bestDistance {
			best = match
			bestDistance = distance
			ties = 1
			continue
		}
		if distance == bestDistance {
			ties++
		}
	}
	if best != "" && ties == 1 {
		return best
	}
	return ""
}

const commandPrefix = "/"

func isCommandInput(input string) bool {
	trimmed := strings.TrimSpace(input)
	return strings.HasPrefix(trimmed, commandPrefix)
}

func stripCommandPrefix(token string) string {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, commandPrefix)
	return token
}

func commandCompletionNames() []string {
	names := []string{
		"/agents",
		"/approve",
		"/artifacts",
		"/config-diagnostics",
		"/cost",
		"/deny",
		"/help",
		"/mode",
		"/memory",
		"/new-agent",
		"/new-kit",
		"/new-policy-rule",
		"/new-provider",
		"/new-skill",
		"/new-team",
		"/new-tool",
		"/new-workflow",
		"/new-workflow-template",
		"/expression-helpers",
		"/policy-rules",
		"/reload",
		"/reload-tools",
		"/session",
		"/skill-templates",
		"/skills",
		"/status",
		"/team-state",
		"/teams",
		"/tools",
		"/trace",
		"/use",
		"/workflow",
		"/workflow-expression-functions",
		"/workflow-node-metadata",
		"/workflow-schemas",
		"/workflow-templates",
	}
	sort.Strings(names)
	return names
}

func completeAtReferenceToken(buffer []rune, workspaceRoot string) ([]rune, bool, string) {
	start, prefix, ok := trailingAtReferenceToken(buffer)
	if !ok {
		return buffer, false, ""
	}
	matches, err := suggestAtReferencePaths(prefix, workspaceRoot, 20)
	if err != nil {
		return buffer, false, formatCommandWarning("reference completion error: "+err.Error()) + "\n"
	}
	if len(matches) == 0 {
		return buffer, false, formatCommandWarning(fmt.Sprintf("No workspace files match @%s", prefix)) + "\n"
	}
	completion := ""
	if len(matches) == 1 {
		completion = matches[0]
	} else if common := commonPathPrefix(matches); len(common) > len(prefix) {
		completion = common
	}
	if completion != "" {
		next := append([]rune(nil), buffer[:start+1]...)
		next = append(next, []rune(completion)...)
		return next, true, ""
	}
	var b strings.Builder
	b.WriteString(formatCommandWarning(fmt.Sprintf("Matching workspace files for @%s:", prefix)))
	b.WriteString("\n")
	for _, match := range matches {
		b.WriteString("  @")
		b.WriteString(match)
		b.WriteString("\n")
	}
	return buffer, false, b.String()
}

func trailingAtReferenceToken(buffer []rune) (int, string, bool) {
	if len(buffer) == 0 {
		return 0, "", false
	}
	start := len(buffer) - 1
	for start >= 0 && !unicode.IsSpace(buffer[start]) {
		start--
	}
	start++
	if start >= len(buffer) || buffer[start] != '@' {
		return 0, "", false
	}
	prefix := strings.TrimRight(string(buffer[start+1:]), ".,;:!?)]}")
	if strings.ContainsAny(prefix, "\"'") {
		return 0, "", false
	}
	return start, prefix, true
}

func commonPathPrefix(values []string) string {
	return commonStringPrefix(values)
}

func commonStringPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	common := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, common) && common != "" {
			common = common[:len(common)-1]
		}
	}
	return common
}

type fuzzyStringMatch struct {
	value string
	score int
}

func fuzzyFilterStrings(values []string, query string, limit int) []string {
	query = strings.TrimSpace(strings.TrimPrefix(query, commandPrefix))
	if query == "" {
		out := append([]string(nil), values...)
		sort.Strings(out)
		if limit > 0 && len(out) > limit {
			return out[:limit]
		}
		return out
	}
	matches := make([]fuzzyStringMatch, 0, len(values))
	for _, value := range values {
		score, ok := fuzzyStringScore(value, query)
		if ok {
			matches = append(matches, fuzzyStringMatch{value: value, score: score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return matches[i].value < matches[j].value
	})
	if limit <= 0 || limit > len(matches) {
		limit = len(matches)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, matches[i].value)
	}
	return out
}

func fuzzyStringScore(value, query string) (int, bool) {
	candidate := strings.ToLower(stripCommandPrefix(value))
	query = strings.ToLower(stripCommandPrefix(query))
	if query == "" {
		return 0, true
	}
	if strings.HasPrefix(candidate, query) {
		return len(candidate) - len(query), true
	}
	if idx := strings.Index(candidate, query); idx >= 0 {
		return 20 + idx + len(candidate) - len(query), true
	}
	candidateRunes := []rune(candidate)
	queryRunes := []rune(query)
	score := 100
	queryIndex := 0
	lastMatch := -1
	for candidateIndex, ch := range candidateRunes {
		if queryIndex >= len(queryRunes) {
			break
		}
		if ch != queryRunes[queryIndex] {
			continue
		}
		if lastMatch >= 0 {
			score += candidateIndex - lastMatch - 1
		}
		lastMatch = candidateIndex
		queryIndex++
	}
	if queryIndex != len(queryRunes) {
		return 0, false
	}
	score += len(candidateRunes) - len(queryRunes)
	return score, true
}

type pathSettings struct {
	ConfigPath        string
	WorkspaceRoot     string
	HTTPAddr          string
	WorkspaceExplicit bool
}

type runtimeHomeInfo struct {
	Root          string
	BinaryArchive bool
}

func resolveRuntimeHome() (runtimeHomeInfo, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return runtimeHomeInfo{}, fmt.Errorf("get current directory: %w", err)
	}
	exePath, _ := os.Executable()
	return resolveRuntimeHomeFrom(cwd, exePath), nil
}

func resolveRuntimeHomeFrom(cwd, exePath string) runtimeHomeInfo {
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if cwd == "" {
		cwd = "."
	}
	if info, ok := runtimeHomeFromBinDir(cwd); ok {
		return info
	}
	exePath = filepath.Clean(strings.TrimSpace(exePath))
	if exePath != "" && exePath != "." {
		exeDir := filepath.Dir(exePath)
		if info, ok := runtimeHomeFromBinDir(exeDir); ok {
			return info
		}
		if hasRuntimeConfig(exeDir) {
			return runtimeHomeInfo{Root: exeDir}
		}
	}
	if hasRuntimeConfig(cwd) {
		return runtimeHomeInfo{Root: cwd}
	}
	return runtimeHomeInfo{Root: cwd}
}

func runtimeHomeFromBinDir(dir string) (runtimeHomeInfo, bool) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || !strings.EqualFold(filepath.Base(dir), "bin") {
		return runtimeHomeInfo{}, false
	}
	parent := filepath.Dir(dir)
	if !hasRuntimeConfig(parent) {
		return runtimeHomeInfo{}, false
	}
	return runtimeHomeInfo{Root: parent, BinaryArchive: fileExists(filepath.Join(parent, "configs", binaryConfigFileName))}, true
}

func hasRuntimeConfig(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	return fileExists(filepath.Join(root, "configs", defaultConfigFileName)) || fileExists(filepath.Join(root, "configs", binaryConfigFileName))
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func defaultConfigPathForRuntime(info runtimeHomeInfo) string {
	if strings.TrimSpace(info.Root) == "" {
		return ""
	}
	binaryConfig := filepath.Join(info.Root, "configs", binaryConfigFileName)
	if info.BinaryArchive && fileExists(binaryConfig) {
		return binaryConfig
	}
	return filepath.Join(info.Root, "configs", defaultConfigFileName)
}

func applyStartupEnvDefaults(info runtimeHomeInfo) {
	applyBackupProviderEnvDefaults()
	if !info.BinaryArchive || strings.TrimSpace(info.Root) == "" {
		return
	}
	setEnvDefault("GOFLOW_FILE_TOOLS_CMD", filepath.Join(info.Root, "bin", executableName("file_tools")))
	setEnvDefault("GOFLOW_WEB_TOOLS_CMD", filepath.Join(info.Root, "bin", executableName("web_tools")))
	setEnvDefault("GOFLOW_PYTHON_CMD", defaultPythonCommand())
	setEnvDefault("GOFLOW_PYTHON_NOTES_PATH", filepath.Join(info.Root, "mcp_servers", "python_notes.py"))
}

func applyBackupProviderEnvDefaults() {
	setEnvDefault("GOFLOW_BACKUP_BASE_URL", os.Getenv("GOFLOW_BASE_URL"))
	setEnvDefault("GOFLOW_BACKUP_API_KEY", os.Getenv("GOFLOW_API_KEY"))
	setEnvDefault("GOFLOW_BACKUP_MODEL", os.Getenv("GOFLOW_MODEL"))
}

func setEnvDefault(key, value string) {
	if strings.TrimSpace(os.Getenv(key)) != "" || strings.TrimSpace(value) == "" {
		return
	}
	_ = os.Setenv(key, value)
}

func executableName(name string) string {
	if goruntime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func defaultPythonCommand() string {
	if goruntime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

func resolvePaths(runtimeHome string, args []string) (string, string, string, error) {
	settings, err := resolvePathSettings(runtimeHome, args)
	if err != nil {
		return "", "", "", err
	}
	return settings.ConfigPath, settings.WorkspaceRoot, settings.HTTPAddr, nil
}

func resolvePathSettings(runtimeHome string, args []string) (pathSettings, error) {
	return resolvePathSettingsWithDefault(runtimeHome, args, filepath.Join(runtimeHome, "configs", defaultConfigFileName))
}

func resolvePathSettingsWithDefault(runtimeHome string, args []string, defaultConfigPath string) (pathSettings, error) {
	configPath := strings.TrimSpace(defaultConfigPath)
	if configPath == "" {
		configPath = filepath.Join(runtimeHome, "configs", defaultConfigFileName)
	}
	workspaceRoot := ""
	httpAddr := ""
	workspaceExplicit := false
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" {
			if i+1 >= len(args) {
				return pathSettings{}, fmt.Errorf("missing value for --config")
			}
			configPath = args[i+1]
			i++
			continue
		}
		if args[i] == "--workspace" {
			if i+1 >= len(args) {
				return pathSettings{}, fmt.Errorf("missing value for --workspace")
			}
			workspaceRoot = args[i+1]
			workspaceExplicit = true
			i++
			continue
		}
		if args[i] == "--http" {
			if i+1 >= len(args) {
				return pathSettings{}, fmt.Errorf("missing value for --http")
			}
			httpAddr = args[i+1]
			i++
			continue
		}
		remaining = append(remaining, args[i])
	}
	_ = remaining
	if strings.TrimSpace(workspaceRoot) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return pathSettings{}, fmt.Errorf("get current directory: %w", err)
		}
		workspaceRoot = cwd
	}
	if !filepath.IsAbs(workspaceRoot) {
		absoluteWorkspace, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return pathSettings{}, fmt.Errorf("resolve workspace path: %w", err)
		}
		workspaceRoot = absoluteWorkspace
	}
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(runtimeHome, configPath)
	}
	return pathSettings{
		ConfigPath:        filepath.Clean(configPath),
		WorkspaceRoot:     filepath.Clean(workspaceRoot),
		HTTPAddr:          strings.TrimSpace(httpAddr),
		WorkspaceExplicit: workspaceExplicit,
	}, nil
}

func handleCommand(ctx context.Context, input string, skillManager *skill.Manager, mcpClient interfaces.MCPClient, agentRuntime *agent.Runtime, workspaceStates ...*workspaceLifecycle) bool {
	var workspaceState *workspaceLifecycle
	if len(workspaceStates) > 0 {
		workspaceState = workspaceStates[0]
	}
	trimmed := strings.TrimSpace(input)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(fields[0]) {
	case "/help":
		fmt.Print(formatHelpOutput())
		return true
	case "/workspace":
		return handleWorkspaceCommand(fields, workspaceState)
	case "/skills":
		skills := skillManager.List()
		rows := make([]skillDisplayRow, 0, len(skills))
		for _, s := range skills {
			rows = append(rows, skillDisplayRow{
				Name:           s.Name,
				Description:    s.Description,
				Mode:           s.Mode,
				PreferredAgent: s.PreferredAgent,
				OutputKind:     s.OutputKind,
				Keywords:       append([]string(nil), s.Activation.Keywords...),
				NextSkills:     append([]string(nil), s.NextSkills...),
			})
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].Name < rows[j].Name
		})
		fmt.Print(formatSkillsOutput(rows))
		return true
	case "/skill-templates":
		return handleSkillTemplatesCommand(skillManager)
	case "/new-skill":
		return handleNewSkillCommand(fields, skillManager)
	case "/new-tool":
		return handleNewToolCommand(fields, skillManager)
	case "/new-agent":
		return handleNewAgentCommand(fields, skillManager)
	case "/new-kit":
		return handleNewKitCommand(fields, skillManager)
	case "/new-policy-rule":
		return handleNewPolicyRuleCommand(fields, skillManager)
	case "/new-provider":
		return handleNewProviderCommand(fields, skillManager)
	case "/new-team":
		return handleNewTeamCommand(fields, skillManager)
	case "/new-workflow":
		return handleNewWorkflowCommand(fields, skillManager)
	case "/new-workflow-template":
		return handleNewWorkflowTemplateCommand(fields, skillManager)
	case "/tools":
		tools, err := mcpClient.ListTools(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "list tools error: %v\n", err)
			return true
		}
		health := mcpClient.HealthStatus(ctx)
		diagnosticsByTool := buildToolDiagnosticsByQualifiedName(tools)
		serverRisks := buildCLIMCPServerRiskMap(agentRuntime.MCPServerRefs())
		rows := make([]toolDisplayRow, 0, len(tools))
		for _, tool := range tools {
			qualifiedName := formatToolName(tool)
			risk := buildCLIToolRiskProfile(tool, serverRisks[tool.Server])
			rows = append(rows, toolDisplayRow{
				Name:                   qualifiedName,
				Description:            tool.Description,
				Server:                 tool.Server,
				Kind:                   tool.Kind,
				Health:                 health[tool.Server],
				InputSchemaSummary:     summarizeToolSchema(tool.InputSchema),
				Diagnostics:            diagnosticsByTool[qualifiedName],
				RiskLevel:              risk.RiskLevel,
				IsolationLevel:         risk.IsolationLevel,
				Sandboxed:              risk.Sandboxed,
				RequiresSandbox:        risk.RequiresSandbox,
				SandboxFeatures:        risk.SandboxFeatures,
				MissingSandboxFeatures: risk.MissingSandboxFeatures,
				WindowsIsolation:       risk.WindowsIsolation,
				EnvAllowlistSet:        risk.EnvAllowlistSet,
				EnvAllowlist:           risk.EnvAllowlist,
				SensitiveEnv:           risk.SensitiveEnv,
			})
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].Name < rows[j].Name
		})
		fmt.Print(formatToolsOutput(rows))
		return true
	case "/agents":
		rows := make([]agentDisplayRow, 0, len(agentRuntime.AgentNames()))
		for _, name := range agentRuntime.AgentNames() {
			profile, _ := agentRuntime.Profile(name)
			allowedKinds := make([]string, 0, len(profile.AllowedToolKinds))
			for _, kind := range profile.AllowedToolKinds {
				allowedKinds = append(allowedKinds, string(kind))
			}
			allowedTools := append([]string(nil), profile.AllowedTools...)
			sort.Strings(allowedKinds)
			sort.Strings(allowedTools)
			rows = append(rows, agentDisplayRow{
				Name:             name,
				Description:      profile.Description,
				Provider:         profile.Provider,
				Mode:             profile.Mode,
				ToolPolicy:       profile.ToolPolicy.String(),
				AllowedToolKinds: allowedKinds,
				AllowedTools:     allowedTools,
				Active:           name == agentRuntime.ActiveAgent(),
			})
		}
		fmt.Print(formatAgentsOutput(rows))
		return true
	case "/config-diagnostics":
		return handleConfigDiagnosticsCommand(fields, agentRuntime, workspaceState)
	case "/teams":
		return handleTeamsCommand(fields, agentRuntime)
	case "/kits":
		return handleKitsCommand(fields, agentRuntime)
	case "/team-state":
		return handleTeamStateCommand(fields, agentRuntime)
	case "/policy-rules":
		return handlePolicyRulesCommand(fields, agentRuntime)
	case "/workflow-node-metadata":
		return handleWorkflowNodeMetadataCommand(fields, agentRuntime)
	case "/expression-helpers", "/workflow-expression-functions":
		return handleExpressionHelpersCommand(fields, agentRuntime)
	case "/use":
		if len(fields) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /use <agent>")
			return true
		}
		if err := agentRuntime.SetActiveAgent(fields[1]); err != nil {
			fmt.Fprintf(os.Stderr, "switch agent error: %v\n", err)
			return true
		}
		fmt.Println(formatCommandSuccess("agent", agentRuntime.ActiveAgent()))
		return true
	case "/mode":
		if len(fields) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /mode <chat|plan|audit|fix>")
			return true
		}
		if err := agentRuntime.SetMode(fields[1]); err != nil {
			fmt.Fprintf(os.Stderr, "set mode error: %v\n", err)
			return true
		}
		fmt.Println(formatCommandSuccess("mode", agentRuntime.Mode()))
		return true
	case "/workflow":
		if !ensureWorkspaceConfirmed(workspaceState, "workflow execution runs workspace-scoped stages") {
			return true
		}
		return handleWorkflowCommand(ctx, fields[1:], agentRuntime)
	case "/workflow-schemas":
		return handleWorkflowSchemasCommand(fields, agentRuntime)
	case "/workflow-templates":
		return handleWorkflowTemplatesCommand(fields, agentRuntime)
	case "/status":
		fmt.Print(formatStatusLines(agentRuntime.StatusLines(ctx)))
		return true
	case "/cost":
		fmt.Print(formatCostOutput(agentRuntime.SessionSnapshot()))
		return true
	case "/compact":
		summary, err := agentRuntime.CompactContext(ctx, strings.TrimSpace(strings.Join(fields[1:], " ")))
		if err != nil {
			fmt.Fprintf(os.Stderr, "compact error: %v\n", err)
			return true
		}
		fmt.Print(formatContextSummaryOutput(summary))
		return true
	case "/memory":
		return handleMemoryCommand(ctx, fields, agentRuntime)
	case "/artifacts":
		return handleArtifactsCommand(fields, agentRuntime)
	case "/session":
		snapshot := agentRuntime.SessionSnapshot()
		workflowRow := workflowDisplayRow{
			Name:      snapshot.Workflow.Name,
			Status:    snapshot.Workflow.Status,
			NextStage: snapshot.Workflow.NextStage,
			Summary:   snapshot.Workflow.Summary,
		}
		handoffRow := handoffDisplayRow{
			Request:        snapshot.PendingHandoff.Request,
			TargetAgent:    snapshot.PendingHandoff.TargetAgent,
			TargetMode:     snapshot.PendingHandoff.TargetMode,
			ExpectedAction: snapshot.PendingHandoff.ExpectedAction,
			PlanSummary:    snapshot.PendingHandoff.PlanSummary,
		}
		routingRow := routingDisplayRow{
			Request:     snapshot.LastRouting.Request,
			SourceAgent: snapshot.LastRouting.SourceAgent,
			TargetAgent: snapshot.LastRouting.TargetAgent,
			TargetMode:  snapshot.LastRouting.TargetMode,
			Outcome:     snapshot.LastRouting.Outcome,
			Reason:      snapshot.LastRouting.Reason,
		}
		taskStageRow := taskStageDisplayRow{
			Stage:   snapshot.TaskStage.Stage,
			AgentID: snapshot.TaskStage.AgentID,
			Mode:    snapshot.TaskStage.Mode,
			Detail:  snapshot.TaskStage.Detail,
		}
		approvalRows := make([]approvalDisplayRow, 0, len(snapshot.PendingApprovals))
		for _, pending := range snapshot.PendingApprovals {
			approvalRows = append(approvalRows, approvalDisplayRow{
				CallID:           pending.CallID,
				ToolName:         pending.ToolName,
				AgentID:          pending.AgentID,
				Stage:            pending.Stage,
				ArgumentsSummary: pending.ArgumentsSummary,
			})
		}
		fmt.Print(formatSessionOutput(snapshot.ActiveAgent, snapshot.Mode, snapshot.LastSkill, snapshot.RecentPrompts, snapshot.RecentTools, workflowRow, handoffRow, routingRow, approvalRows, taskStageRow))
		return true
	case "/trace":
		if len(fields) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /trace on|off")
			return true
		}
		switch strings.ToLower(fields[1]) {
		case "on":
			agentRuntime.SetTrace(true)
		case "off":
			agentRuntime.SetTrace(false)
		default:
			fmt.Fprintln(os.Stderr, "usage: /trace on|off")
			return true
		}
		fmt.Printf("Trace: %t\n", agentRuntime.TraceEnabled())
		return true
	case "/approve":
		if len(fields) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /approve <tool-call-id>")
			return true
		}
		result, err := agentRuntime.ApproveToolCall(ctx, fields[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "approve error: %v\n", err)
			return true
		}
		fmt.Println(formatCommandSuccess("approved", fmt.Sprintf("%s -> %s", result.CallID, result.ToolName)))
		return true
	case "/deny":
		if len(fields) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /deny <tool-call-id>")
			return true
		}
		result, err := agentRuntime.DenyToolCall(fields[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "deny error: %v\n", err)
			return true
		}
		fmt.Println(formatCommandSuccess("denied", fmt.Sprintf("%s -> %s", result.CallID, result.ToolName)))
		return true
	case "/reload":
		if err := skillManager.Reload(); err != nil {
			fmt.Fprintf(os.Stderr, "reload skills error: %v\n", err)
			return true
		}
		fmt.Println(formatCommandSuccess("skills", "reloaded"))
		return true
	case "/reload-tools":
		if _, err := mcpClient.RefreshTools(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "reload tools error: %v\n", err)
			return true
		}
		fmt.Println(formatCommandSuccess("tools", "refreshed"))
		return true
	default:
		if isCommandInput(fields[0]) {
			fmt.Println(formatUnknownCommand(fields[0]))
			return true
		}
		return false
	}
}

func handleTeamsCommand(fields []string, agentRuntime *agent.Runtime) bool {
	if len(fields) < 2 {
		if agentRuntime != nil {
			fmt.Print(formatTeamTemplatesOutput(agentRuntime.TeamTemplates()))
		} else {
			fmt.Print(formatTeamTemplatesOutput(agent.TeamTemplates()))
		}
		return true
	}
	name := strings.TrimSpace(fields[1])
	var (
		template agent.TeamTemplate
		ok       bool
	)
	if agentRuntime != nil {
		template, ok = agentRuntime.TeamTemplate(name)
	} else {
		template, ok = agent.LoadTeamTemplate(name)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown team template: %s\n", name)
		return true
	}
	fmt.Print(formatTeamTemplateDetailOutput(template))
	return true
}

func handleKitsCommand(fields []string, agentRuntime *agent.Runtime) bool {
	runtimeHome := ""
	if agentRuntime != nil {
		runtimeHome = agentRuntime.RuntimeHome()
	}
	if strings.TrimSpace(runtimeHome) == "" {
		fmt.Fprintln(os.Stderr, "kits unavailable: runtime home is not configured")
		return true
	}
	if len(fields) < 2 {
		rows, warnings := loadCLIKitSummaries(runtimeHome)
		for _, warning := range warnings {
			fmt.Fprintf(os.Stderr, "kit warning: %s\n", warning)
		}
		fmt.Print(formatKitsOutput(rows))
		return true
	}
	if fields[1] == "--import" || fields[1] == "import" {
		return handleKitBundleImportCommand(fields, agentRuntime)
	}
	exportBundle := false
	exportFormat := "json"
	includeSecrets := false
	for i := 2; i < len(fields); i++ {
		switch strings.TrimSpace(fields[i]) {
		case "--export", "export":
			exportBundle = true
		case "--json":
			exportFormat = "json"
		case "--yaml", "--yml":
			exportFormat = "yaml"
		case "--format", "format":
			if i+1 >= len(fields) {
				fmt.Fprintln(os.Stderr, "usage: /kits <name> --export [--format json|yaml] [--include-secrets]")
				return true
			}
			i++
			exportFormat = strings.TrimSpace(fields[i])
		case "--include-secrets":
			includeSecrets = true
		case "":
		default:
			fmt.Fprintln(os.Stderr, "usage: /kits [name] [--export [--format json|yaml] [--include-secrets]] or /kits --import <path> [--replace]")
			return true
		}
	}
	if exportBundle {
		data, _, _, err := apipkg.ExportKitBundle(agentRuntime, fields[1], exportFormat, includeSecrets)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kit export error: %v\n", err)
			return true
		}
		fmt.Print(string(data))
		return true
	}
	doc, ok, err := loadCLIKitDocument(runtimeHome, fields[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "kit error: %v\n", err)
		return true
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown kit: %s\n", fields[1])
		return true
	}
	fmt.Print(formatKitDetailOutput(doc))
	return true
}

func handleKitBundleImportCommand(fields []string, agentRuntime *agent.Runtime) bool {
	if agentRuntime == nil {
		fmt.Fprintln(os.Stderr, "kit import unavailable: runtime not initialized")
		return true
	}
	if len(fields) < 3 {
		fmt.Fprintln(os.Stderr, "usage: /kits --import <path> [--replace]")
		return true
	}
	importPath := strings.TrimSpace(fields[2])
	overwrite := false
	for _, field := range fields[3:] {
		switch strings.TrimSpace(field) {
		case "--replace", "--overwrite":
			overwrite = true
		case "--keep-existing", "":
		default:
			fmt.Fprintln(os.Stderr, "usage: /kits --import <path> [--replace]")
			return true
		}
	}
	data, err := os.ReadFile(importPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kit import error: %v\n", err)
		return true
	}
	contentType := "application/json"
	switch strings.ToLower(filepath.Ext(importPath)) {
	case ".yaml", ".yml":
		contentType = "application/yaml"
	}
	summary, err := apipkg.ImportKitBundleData(agentRuntime, data, contentType, overwrite)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kit import error: %v\n", err)
		return true
	}
	fmt.Print(formatKitBundleImportSummaryOutput(summary, overwrite))
	return true
}

func handlePolicyRulesCommand(fields []string, agentRuntime *agent.Runtime) bool {
	if agentRuntime == nil {
		fmt.Fprintln(os.Stderr, "policy rules unavailable: runtime not initialized")
		return true
	}
	rules := agentRuntime.WorkflowRunner().WorkflowPolicyRules()
	rows := make([]policyRuleDisplayRow, 0, len(rules))
	for _, rule := range rules {
		rows = append(rows, policyRuleDisplayRow{
			Name:        rule.Name,
			Label:       rule.Label,
			Description: rule.Description,
			Source:      rule.Source,
			Operator:    rule.Operator,
			Custom:      rule.Custom,
			Params:      append([]agent.WorkflowNodeFieldOption(nil), rule.Params...),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Source != rows[j].Source {
			return rows[i].Source < rows[j].Source
		}
		return rows[i].Name < rows[j].Name
	})
	if len(fields) < 2 {
		fmt.Print(formatPolicyRulesOutput(rows))
		return true
	}
	target := strings.ToLower(strings.TrimSpace(fields[1]))
	for _, row := range rows {
		if strings.EqualFold(row.Name, target) {
			fmt.Print(formatPolicyRuleDetailOutput(row))
			return true
		}
	}
	fmt.Fprintf(os.Stderr, "unknown policy rule: %s\n", fields[1])
	return true
}

func handleWorkflowNodeMetadataCommand(fields []string, agentRuntime *agent.Runtime) bool {
	if agentRuntime == nil {
		fmt.Fprintln(os.Stderr, "workflow node metadata unavailable: runtime not initialized")
		return true
	}
	nodes := agentRuntime.WorkflowRunner().WorkflowNodeTypes()
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Category != nodes[j].Category {
			return nodes[i].Category < nodes[j].Category
		}
		return nodes[i].Type < nodes[j].Type
	})
	if len(fields) < 2 {
		fmt.Print(formatWorkflowNodeMetadataOutput(nodes))
		return true
	}
	target := strings.ToLower(strings.TrimSpace(fields[1]))
	for _, node := range nodes {
		if strings.EqualFold(node.Type, target) {
			fmt.Print(formatWorkflowNodeMetadataDetailOutput(node))
			return true
		}
	}
	fmt.Fprintf(os.Stderr, "unknown workflow node type: %s\n", fields[1])
	return true
}

func handleExpressionHelpersCommand(fields []string, agentRuntime *agent.Runtime) bool {
	if agentRuntime == nil {
		fmt.Fprintln(os.Stderr, "expression helpers unavailable: runtime not initialized")
		return true
	}
	args := append([]string(nil), fields[1:]...)
	mode := ""
	nodeType := ""
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch arg {
		case "--mode":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: /expression-helpers [name] [--mode <mode>] [--node-type <type>]")
				return true
			}
			i++
			mode = strings.TrimSpace(args[i])
		case "--node-type", "--node":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: /expression-helpers [name] [--mode <mode>] [--node-type <type>]")
				return true
			}
			i++
			nodeType = strings.TrimSpace(args[i])
		case "":
		default:
			filtered = append(filtered, arg)
		}
	}
	helpers := agentRuntime.WorkflowRunner().WorkflowExpressionFunctions()
	helpers = filterExpressionHelpers(helpers, mode, nodeType)
	sort.Slice(helpers, func(i, j int) bool {
		if helpers[i].Category != helpers[j].Category {
			return helpers[i].Category < helpers[j].Category
		}
		return helpers[i].Name < helpers[j].Name
	})
	if len(filtered) == 0 {
		fmt.Print(formatExpressionHelpersOutput(helpers, mode, nodeType))
		return true
	}
	target := strings.ToLower(strings.TrimSpace(filtered[0]))
	for _, helper := range helpers {
		if strings.EqualFold(helper.Name, target) {
			fmt.Print(formatExpressionHelperDetailOutput(helper))
			return true
		}
	}
	fmt.Fprintf(os.Stderr, "unknown expression helper: %s\n", filtered[0])
	return true
}

func filterExpressionHelpers(helpers []agent.WorkflowExpressionFunctionOption, mode, nodeType string) []agent.WorkflowExpressionFunctionOption {
	mode = strings.ToLower(strings.TrimSpace(mode))
	nodeType = strings.ToLower(strings.TrimSpace(nodeType))
	if mode == "" && nodeType == "" {
		return helpers
	}
	filtered := make([]agent.WorkflowExpressionFunctionOption, 0, len(helpers))
	for _, helper := range helpers {
		if mode != "" && !cliStringListContains(helper.Modes, mode) {
			continue
		}
		if nodeType != "" && !cliStringListContains(helper.NodeTypes, nodeType) {
			continue
		}
		filtered = append(filtered, helper)
	}
	return filtered
}

func cliStringListContains(values []string, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return true
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
}

func handleConfigDiagnosticsCommand(fields []string, agentRuntime *agent.Runtime, workspaceState *workspaceLifecycle) bool {
	if agentRuntime == nil {
		fmt.Fprintln(os.Stderr, "config diagnostics unavailable: runtime not initialized")
		return true
	}
	jsonOutput := false
	for _, field := range fields[1:] {
		switch strings.TrimSpace(field) {
		case "--json", "json":
			jsonOutput = true
		case "":
		default:
			fmt.Fprintln(os.Stderr, "usage: /config-diagnostics [--json]")
			return true
		}
	}
	diagnostics := apipkg.ConfigDiagnostics(agentRuntime, workspaceState)
	if jsonOutput {
		data, err := json.MarshalIndent(diagnostics, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "config diagnostics json error: %v\n", err)
			return true
		}
		fmt.Println(string(data))
		return true
	}
	fmt.Print(formatConfigDiagnosticsOutput(diagnostics))
	return true
}

func handleTeamStateCommand(fields []string, agentRuntime *agent.Runtime) bool {
	if agentRuntime == nil {
		fmt.Fprintln(os.Stderr, "team state unavailable: runtime not initialized")
		return true
	}
	runID := ""
	team := ""
	if len(fields) > 1 {
		runID = fields[1]
	}
	if len(fields) > 2 {
		team = fields[2]
	}
	fmt.Print(formatTeamStateOutput(agentRuntime.TeamState(runID, team)))
	return true
}

func runCancelableAgentOperation(ctx context.Context, input io.Reader, output io.Writer, operation func(context.Context) (schema.AgentResult, error)) (schema.AgentResult, error) {
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopEscWatcher := startDoubleEscCancelWatcher(taskCtx, input, output, cancel)
	result, err := operation(taskCtx)
	escCancelled := stopEscWatcher()
	if escCancelled && (err == nil || errors.Is(err, context.Canceled)) {
		return result, context.Canceled
	}
	return result, err
}

func runCancelableWorkflowOperation(ctx context.Context, input io.Reader, output io.Writer, operation func(context.Context) (agent.WorkflowResult, error)) (agent.WorkflowResult, error) {
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopEscWatcher := startDoubleEscCancelWatcher(taskCtx, input, output, cancel)
	result, err := operation(taskCtx)
	escCancelled := stopEscWatcher()
	if escCancelled && (err == nil || errors.Is(err, context.Canceled)) {
		return result, context.Canceled
	}
	return result, err
}

type cancelledTaskState struct {
	Request     string
	Agent       string
	Mode        string
	CancelledAt time.Time
}

func (s cancelledTaskState) IsZero() bool {
	return strings.TrimSpace(s.Request) == "" && s.CancelledAt.IsZero()
}

func isContinuationOnlyInput(input string) bool {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return false
	}
	text = strings.Trim(text, " \t\r\n.!！？。，")
	text = strings.Join(strings.Fields(text), " ")
	switch text {
	case "继续", "继续吧", "继续执行", "继续任务", "可以继续", "可以,继续", "可以，继续", "继续上次任务", "重试", "重新执行", "再试一次":
		return true
	case "continue", "continue please", "go ahead", "keep going", "proceed", "retry", "try again", "rerun":
		return true
	default:
		return false
	}
}
func formatRetryingCancelledTask(state cancelledTaskState) string {
	request := truncateCLISummaryValue(state.Request)
	if request == "" {
		request = "previous task"
	}
	agentMode := ""
	if strings.TrimSpace(state.Agent) != "" || strings.TrimSpace(state.Mode) != "" {
		agentMode = fmt.Sprintf(" [%s/%s]", fallbackDisplayText(state.Agent, "agent"), fallbackDisplayText(state.Mode, "mode"))
	}
	return fmt.Sprintf("%s Re-running the cancelled task%s from the beginning: %s", styleStatus("[retry]", "approval"), agentMode, request)
}

func handlePendingApprovalInput(ctx context.Context, input string, agentRuntime *agent.Runtime) bool {
	if agentRuntime == nil {
		return false
	}
	pending := agentRuntime.PendingApprovals()
	if len(pending) == 0 {
		return false
	}
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return false
	}
	if !matchesNaturalLanguageApproval(trimmed) {
		return false
	}
	result, err := agentRuntime.ApproveToolCall(ctx, pending[0].ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "approve error: %v\n", err)
		return true
	}
	fmt.Printf("Approved %s -> %s\n", result.CallID, result.ToolName)
	return true
}

func handleOrdinaryChatPendingApproval(ctx context.Context, input io.Reader, output io.Writer, agentRuntime *agent.Runtime, renderer *cliStreamRenderer) error {
	if agentRuntime == nil {
		return nil
	}
	for {
		pending := agentRuntime.PendingApprovalSummaries()
		if len(pending) == 0 {
			return nil
		}
		current := pending[0]
		callID := current.CallID
		prompt := formatApprovalPrompt(current)
		resolvedCallIDs := make([]string, 0, 1)
		if err := runBlockingApprovalPrompt(input, output, prompt, func() error {
			result, err := agentRuntime.ApproveToolCall(ctx, callID)
			if err != nil {
				return err
			}
			resolvedCallIDs = append(resolvedCallIDs, result.CallID)
			if renderer != nil {
				if err := renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, ArgumentsSummary: current.ArgumentsSummary, Content: result.Content, IsError: result.IsError, Suspended: result.Suspended}); err != nil {
					return err
				}
			}
			fmt.Fprintf(output, "Approved %s -> %s\n", result.CallID, result.ToolName)
			return nil
		}, func() error {
			result, err := agentRuntime.DenyToolCall(callID)
			if err != nil {
				return err
			}
			resolvedCallIDs = append(resolvedCallIDs, result.CallID)
			if renderer != nil {
				if err := renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, ArgumentsSummary: current.ArgumentsSummary, Content: result.Content, IsError: result.IsError, Suspended: result.Suspended}); err != nil {
					return err
				}
			}
			fmt.Fprintf(output, "Denied %s -> %s\n", result.CallID, result.ToolName)
			return nil
		}, nil, func() error {
			result, err := agentRuntime.ApproveToolCallAndRemember(ctx, callID)
			if err != nil {
				return err
			}
			resolvedCallIDs = append(resolvedCallIDs, result.CallID)
			if renderer != nil {
				if err := renderer.Handle(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, ArgumentsSummary: current.ArgumentsSummary, Content: result.Content, IsError: result.IsError, Suspended: result.Suspended}); err != nil {
					return err
				}
			}
			fmt.Fprintf(output, "Approved %s -> %s and remembered for this session\n", result.CallID, result.ToolName)
			return nil
		}); err != nil {
			return err
		}
		for _, resolvedCallID := range resolvedCallIDs {
			var handle func(event schema.StreamEvent) error
			if renderer != nil {
				handle = renderer.Handle
			}
			resumed := false
			result, err := runCancelableAgentOperation(ctx, input, output, func(taskCtx context.Context) (schema.AgentResult, error) {
				var resumeErr error
				var didResume bool
				var resumeResult schema.AgentResult
				resumeResult, didResume, resumeErr = agentRuntime.ResumeApprovedOrdinaryToolCall(taskCtx, resolvedCallID, handle)
				resumed = didResume
				return resumeResult, resumeErr
			})
			if err != nil {
				if errors.Is(err, context.Canceled) {
					if renderer != nil {
						renderer.Flush()
					}
					fmt.Fprintln(output, formatTaskCancelled())
					return nil
				}
				return err
			}
			if resumed && renderer != nil {
				renderer.Finish(result.Output)
				break
			}
			if resumed {
				break
			}
		}
	}
}

func formatApprovalPrompt(pending session.PendingApprovalSnapshot) string {
	prompt := fmt.Sprintf("tool call %s. %s requires confirmation", pending.CallID, pending.ToolName)
	if strings.TrimSpace(pending.ArgumentsSummary) != "" {
		prompt += " " + pending.ArgumentsSummary
	}
	return prompt
}

func handlePendingHandoffInput(ctx context.Context, input string, approvalInput io.Reader, output io.Writer, agentRuntime *agent.Runtime) bool {
	if agentRuntime == nil || !agentRuntime.HasPendingHandoff() {
		return false
	}
	decision := agentRuntime.PendingHandoffDecision(input)
	handoff := agentRuntime.PendingHandoff()
	requestSummary := strings.TrimSpace(handoff.Request)
	if requestSummary == "" {
		requestSummary = "the approved plan"
	}
	switch decision {
	case "confirm":
		renderer := newCLIStreamRenderer(agentRuntime.TraceEnabled())
		result, err := runCancelableAgentOperation(ctx, approvalInput, output, func(taskCtx context.Context) (schema.AgentResult, error) {
			return agentRuntime.ConfirmPendingHandoff(taskCtx, renderer.Handle)
		})
		if err != nil {
			renderer.Flush()
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(output, formatTaskCancelled())
				return true
			}
			fmt.Fprintf(os.Stderr, "handoff error: %v\n", err)
			return true
		}
		renderer.Finish(result.Output)
		if err := handleOrdinaryChatPendingApproval(ctx, approvalInput, output, agentRuntime, renderer); err != nil {
			fmt.Fprintf(os.Stderr, "approval error: %v\n", err)
		}
		return true
	case "reject":
		agentRuntime.RejectPendingHandoff()
		fmt.Fprintf(output, "Cancelled pending implementation handoff for %s.\n", requestSummary)
		return true
	case "revise":
		fmt.Fprintf(output, "Keeping the current plan open for revisions. Please describe what to change before execution.\n")
		return false
	case "question":
		fmt.Fprintf(output, "Keeping the current plan open so the planner can answer your question before execution.\n")
		return false
	default:
		fmt.Fprintf(output, "A plan is waiting for confirmation before execution. Reply with yes / continue / go ahead to proceed, or describe the change you want.\n")
		return true
	}
}

func matchesNaturalLanguageApproval(input string) bool {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return false
	}
	approvals := []string{
		"\u7ee7\u7eed",
		"\u7ee7\u7eed\u5b9e\u73b0",
		"\u53ef\u4ee5",
		"\u53ef\u4ee5\uff0c\u7ee7\u7eed",
		"\u53ef\u4ee5\u7ee7\u7eed",
		"\u76f4\u63a5\u4fee\u6539\u5373\u53ef",
		"\u76f4\u63a5\u505a",
		"\u76f4\u63a5\u6539\u5427",
		"\u6279\u51c6",
		"\u540c\u610f",
		"\u786e\u8ba4",
		"\u786e\u8ba4\u6267\u884c",
		"approve",
		"approved",
		"yes",
		"ok",
		"okay",
		"continue",
	}
	for _, candidate := range approvals {
		if text == candidate {
			return true
		}
	}
	return false
}

func handleWorkflowCommand(ctx context.Context, args []string, agentRuntime *agent.Runtime) bool {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, workflowUsage(agentRuntime))
		return true
	}
	workflowName := strings.ToLower(args[0])
	approve := false
	requestParts := make([]string, 0, len(args)-1)
	for _, arg := range args[1:] {
		if arg == "--approve" {
			approve = true
			continue
		}
		requestParts = append(requestParts, arg)
	}
	request := strings.TrimSpace(strings.Join(requestParts, " "))
	if request == "" {
		fmt.Fprintf(os.Stderr, "usage: /workflow %s [--approve] <request>\n", workflowName)
		return true
	}
	renderer := newCLIStreamRenderer(agentRuntime.TraceEnabled())
	interactiveApproval := terminalInputSupported(os.Stdin, os.Stdout)
	approvalInput := approvalPromptInput(os.Stdin, interactiveApproval)
	workflowResult, err := runCancelableWorkflowOperation(ctx, os.Stdin, os.Stdout, func(taskCtx context.Context) (agent.WorkflowResult, error) {
		return agentRuntime.WorkflowRunner().Run(taskCtx, workflowName, request, approve, renderer.Handle)
	})
	if err != nil {
		renderer.Flush()
		if errors.Is(err, context.Canceled) {
			fmt.Println(formatTaskCancelled())
			return true
		}
		fmt.Fprintf(os.Stderr, "workflow error: %v\n", err)
		return true
	}
	renderer.Finish("")
	for workflowResult.PendingApproval {
		snapshot := agentRuntime.SessionSnapshot().Workflow
		if workflowResult.Status == "awaiting_approval" {
			if err := runApprovalPrompt(approvalInput, os.Stdout, workflowResult.ApprovalPrompt, interactiveApproval, func() error {
				workflowResult, err = runCancelableWorkflowOperation(ctx, os.Stdin, os.Stdout, func(taskCtx context.Context) (agent.WorkflowResult, error) {
					return agentRuntime.WorkflowRunner().Run(taskCtx, workflowName, snapshot.Request, true, renderer.Handle)
				})
				if err == nil {
					renderer.Finish("")
				}
				return err
			}, func() error {
				workflowResult = agent.WorkflowResult{
					Name:            workflowResult.Name,
					Status:          "denied",
					CompletedStages: workflowResult.CompletedStages,
					NextStage:       workflowResult.NextStage,
					ApprovalPrompt:  "workflow stage denied by operator",
				}
				return nil
			}, nil); err != nil {
				if errors.Is(err, context.Canceled) {
					renderer.Flush()
					fmt.Println(formatTaskCancelled())
					return true
				}
				fmt.Fprintf(os.Stderr, "approval error: %v\n", err)
				return true
			}
			continue
		}

		callID := extractApprovalCallID(workflowResult.ApprovalPrompt)
		if strings.TrimSpace(callID) == "" {
			fmt.Fprintln(os.Stderr, "approval error: workflow approval prompt did not include a resumable call id")
			return true
		}
		if err := runApprovalPrompt(approvalInput, os.Stdout, workflowResult.ApprovalPrompt, interactiveApproval, func() error {
			var resumeErr error
			workflowResult, resumeErr = runCancelableWorkflowOperation(ctx, os.Stdin, os.Stdout, func(taskCtx context.Context) (agent.WorkflowResult, error) {
				return agentRuntime.WorkflowRunner().Resume(taskCtx, workflowName, callID, true, renderer.Handle)
			})
			if resumeErr == nil {
				renderer.Finish("")
			}
			return resumeErr
		}, func() error {
			var resumeErr error
			workflowResult, resumeErr = runCancelableWorkflowOperation(ctx, os.Stdin, os.Stdout, func(taskCtx context.Context) (agent.WorkflowResult, error) {
				return agentRuntime.WorkflowRunner().Resume(taskCtx, workflowName, callID, false, renderer.Handle)
			})
			return resumeErr
		}, nil, func() error {
			if rememberErr := agentRuntime.RememberPendingToolApproval(callID); rememberErr != nil {
				return rememberErr
			}
			var resumeErr error
			workflowResult, resumeErr = runCancelableWorkflowOperation(ctx, os.Stdin, os.Stdout, func(taskCtx context.Context) (agent.WorkflowResult, error) {
				return agentRuntime.WorkflowRunner().Resume(taskCtx, workflowName, callID, true, renderer.Handle)
			})
			if resumeErr == nil {
				renderer.Finish("")
			}
			return resumeErr
		}); err != nil {
			if errors.Is(err, context.Canceled) {
				renderer.Flush()
				fmt.Println(formatTaskCancelled())
				return true
			}
			fmt.Fprintf(os.Stderr, "approval error: %v\n", err)
			return true
		}
	}
	fmt.Printf("[workflow] %s status=%s\n", workflowResult.Name, workflowResult.Status)
	if workflowResult.FinalSummary != "" {
		fmt.Print(formatWorkflowSummary(strings.Split(workflowResult.FinalSummary, "\n\n")))
	}
	return true
}

func handleWorkflowTemplatesCommand(fields []string, agentRuntime *agent.Runtime) bool {
	if agentRuntime == nil {
		fmt.Fprintln(os.Stderr, "workflow templates unavailable: runtime not initialized")
		return true
	}
	runner := agentRuntime.WorkflowRunner()
	if len(fields) < 2 {
		rows := make([]workflowTemplateDisplayRow, 0)
		for _, template := range runner.WorkflowTemplates() {
			rows = append(rows, workflowTemplateDisplayRow{
				Name:        template.Name,
				Title:       template.Title,
				Description: template.Description,
				Category:    template.Category,
				Tags:        append([]string(nil), template.Tags...),
				Stages:      template.Stages,
				Source:      template.Source,
				Custom:      template.Custom,
				Path:        template.Path,
			})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Category != rows[j].Category {
				return rows[i].Category < rows[j].Category
			}
			return rows[i].Name < rows[j].Name
		})
		fmt.Print(formatWorkflowTemplatesOutput(rows))
		return true
	}
	template, ok := runner.WorkflowTemplate(fields[1])
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown workflow template: %s\n", fields[1])
		return true
	}
	stageNames := make([]string, 0, len(template.Graph.Stages))
	for _, stage := range template.Graph.Stages {
		if strings.TrimSpace(stage.Name) != "" {
			stageNames = append(stageNames, stage.Name)
		}
	}
	fmt.Print(formatWorkflowTemplateDetailOutput(workflowTemplateDisplayRow{
		Name:        template.Name,
		Title:       template.Title,
		Description: template.Description,
		Category:    template.Category,
		Tags:        append([]string(nil), template.Tags...),
		Stages:      len(template.Graph.Stages),
		Source:      template.Source,
		Custom:      template.Custom,
		Path:        template.Path,
		StageNames:  stageNames,
	}))
	return true
}

func handleWorkflowSchemasCommand(fields []string, agentRuntime *agent.Runtime) bool {
	if agentRuntime == nil {
		fmt.Fprintln(os.Stderr, "workflow schemas unavailable: runtime not initialized")
		return true
	}
	args := append([]string(nil), fields[1:]...)
	rebuild := false
	jsonOutput := false
	exportBundle := false
	clearAll := false
	importPath := ""
	mergeImport := true
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch strings.TrimSpace(arg) {
		case "--rebuild", "rebuild":
			rebuild = true
		case "--json", "json":
			jsonOutput = true
		case "--export", "export":
			exportBundle = true
			jsonOutput = true
		case "--import", "import":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: /workflow-schemas --import <path> [--replace]")
				return true
			}
			i++
			importPath = strings.TrimSpace(args[i])
		case "--replace", "replace":
			mergeImport = false
		case "--clear", "clear":
			clearAll = true
		case "":
		default:
			filtered = append(filtered, arg)
		}
	}
	if importPath != "" {
		imported, err := importWorkflowSchemaBundleFile(agentRuntime, importPath, mergeImport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "workflow schema import error: %v\n", err)
			return true
		}
		fmt.Println(formatCommandSuccess("workflow schemas imported", strconv.Itoa(len(imported))))
		return true
	}
	if clearAll {
		if len(filtered) == 0 {
			fmt.Println(formatCommandSuccess("workflow schemas cleared", strconv.Itoa(agentRuntime.ClearWorkflowSchemas())))
			return true
		}
		name := strings.TrimSpace(filtered[0])
		if !agentRuntime.ClearWorkflowSchema(name) {
			fmt.Fprintf(os.Stderr, "unknown workflow schema: %s\n", name)
			return true
		}
		fmt.Println(formatCommandSuccess("workflow schema cleared", name))
		return true
	}
	if rebuild {
		schemas := agentRuntime.RebuildWorkflowSchemas()
		if exportBundle {
			printWorkflowSchemaJSON(newCLIWorkflowSchemaExportBundle(schemas))
		} else if jsonOutput {
			printWorkflowSchemaJSON(schemas)
		} else {
			fmt.Print(formatWorkflowSchemasOutput(schemas))
		}
		return true
	}
	if len(filtered) == 0 {
		schemas := agentRuntime.WorkflowSchemas()
		if exportBundle {
			printWorkflowSchemaJSON(newCLIWorkflowSchemaExportBundle(schemas))
		} else if jsonOutput {
			printWorkflowSchemaJSON(schemas)
		} else {
			fmt.Print(formatWorkflowSchemasOutput(schemas))
		}
		return true
	}
	name := strings.TrimSpace(filtered[0])
	schema, ok := agentRuntime.WorkflowSchema(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown workflow schema: %s\n", name)
		return true
	}
	if exportBundle {
		printWorkflowSchemaJSON(newCLIWorkflowSchemaExportBundle([]session.WorkflowSchemaSnapshot{schema}))
	} else if jsonOutput {
		printWorkflowSchemaJSON(schema)
	} else {
		fmt.Print(formatWorkflowSchemaDetailOutput(schema))
	}
	return true
}

func printWorkflowSchemaJSON(value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow schema json error: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

type cliWorkflowSchemaBundle struct {
	Kind       string                           `json:"kind,omitempty"`
	Version    int                              `json:"version"`
	MinVersion int                              `json:"min_supported_version,omitempty"`
	Merge      *bool                            `json:"merge,omitempty"`
	Schema     *session.WorkflowSchemaSnapshot  `json:"schema,omitempty"`
	Schemas    []session.WorkflowSchemaSnapshot `json:"schemas,omitempty"`
}

func newCLIWorkflowSchemaExportBundle(schemas []session.WorkflowSchemaSnapshot) cliWorkflowSchemaBundle {
	return cliWorkflowSchemaBundle{
		Kind:       cliWorkflowSchemaBundleKind,
		Version:    cliWorkflowSchemaBundleVersion,
		MinVersion: cliWorkflowSchemaBundleMinSupportedVersion,
		Schemas:    schemas,
	}
}

func importWorkflowSchemaBundleFile(agentRuntime *agent.Runtime, path string, merge bool) ([]session.WorkflowSchemaSnapshot, error) {
	if agentRuntime == nil {
		return nil, fmt.Errorf("runtime not initialized")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("import path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	schemas, effectiveMerge, err := decodeCLIWorkflowSchemaImport(data, merge)
	if err != nil {
		return nil, err
	}
	return agentRuntime.ImportWorkflowSchemas(schemas, effectiveMerge)
}

func decodeCLIWorkflowSchemaImport(data []byte, merge bool) ([]session.WorkflowSchemaSnapshot, bool, error) {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return nil, merge, fmt.Errorf("workflow schema import file is empty")
	}
	var bundle cliWorkflowSchemaBundle
	if err := json.Unmarshal(data, &bundle); err == nil && (bundle.Schema != nil || len(bundle.Schemas) > 0 || strings.TrimSpace(bundle.Kind) != "" || bundle.Version != 0 || bundle.Merge != nil) {
		if err := validateCLIWorkflowSchemaBundle(bundle.Kind, bundle.Version, bundle.MinVersion); err != nil {
			return nil, merge, err
		}
		if bundle.Merge != nil {
			merge = *bundle.Merge
		}
		schemas := append([]session.WorkflowSchemaSnapshot(nil), bundle.Schemas...)
		if bundle.Schema != nil {
			schemas = append(schemas, *bundle.Schema)
		}
		if len(schemas) == 0 {
			return nil, merge, fmt.Errorf("workflow schema import requires schema or schemas")
		}
		return schemas, merge, nil
	}
	var schemas []session.WorkflowSchemaSnapshot
	if err := json.Unmarshal(data, &schemas); err == nil && len(schemas) > 0 {
		return schemas, merge, nil
	}
	var schema session.WorkflowSchemaSnapshot
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, merge, fmt.Errorf("invalid workflow schema import json")
	}
	if strings.TrimSpace(schema.Workflow) == "" {
		return nil, merge, fmt.Errorf("workflow schema import requires workflow")
	}
	return []session.WorkflowSchemaSnapshot{schema}, merge, nil
}

func validateCLIWorkflowSchemaBundle(kind string, version, minVersion int) error {
	if strings.TrimSpace(kind) != "" && strings.TrimSpace(kind) != cliWorkflowSchemaBundleKind {
		return fmt.Errorf("unsupported workflow schema bundle kind %q", kind)
	}
	if version == 0 {
		version = 1
	}
	if version < cliWorkflowSchemaBundleMinSupportedVersion {
		return fmt.Errorf("workflow schema bundle version %d is no longer supported", version)
	}
	if version > cliWorkflowSchemaBundleVersion {
		return fmt.Errorf("workflow schema bundle version %d is newer than supported version %d", version, cliWorkflowSchemaBundleVersion)
	}
	if minVersion > cliWorkflowSchemaBundleVersion {
		return fmt.Errorf("workflow schema bundle requires reader version %d, supported version is %d", minVersion, cliWorkflowSchemaBundleVersion)
	}
	return nil
}

func workflowUsage(agentRuntime *agent.Runtime) string {
	var b strings.Builder
	b.WriteString("usage: /workflow <plan-fix-audit|skill-chain|custom-name> [--approve] <request>")
	names := availableWorkflowNames(agentRuntime)
	if len(names) > 0 {
		b.WriteString("\navailable workflows: ")
		b.WriteString(strings.Join(names, ", "))
	}
	b.WriteString("\ncustom workflows load from workflows/<name>/workflow.yaml; create one with /new-workflow <name> [--template <template>]")
	return b.String()
}

func availableWorkflowNames(agentRuntime *agent.Runtime) []string {
	runtimeHome := ""
	if agentRuntime != nil {
		runtimeHome = agentRuntime.RuntimeHome()
	}
	return availableWorkflowNamesFromRoot(runtimeHome)
}

func availableWorkflowNamesFromRoot(runtimeHome string) []string {
	names := []string{"plan-fix-audit", "skill-chain"}
	runtimeHome = strings.TrimSpace(runtimeHome)
	if runtimeHome == "" {
		return names
	}
	root := filepath.Join(runtimeHome, "workflows")
	entries, err := os.ReadDir(root)
	if err != nil {
		return names
	}
	custom := make([]string, 0, len(entries))
	seen := map[string]struct{}{
		"plan-fix-audit": {},
		"skill-chain":    {},
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name, "workflow.yaml")); err != nil {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		custom = append(custom, name)
	}
	sort.Strings(custom)
	return append(names, custom...)
}

func loadCLIKitSummaries(runtimeHome string) ([]kitDisplayRow, []string) {
	root := filepath.Join(strings.TrimSpace(runtimeHome), "kits")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	rows := make([]kitDisplayRow, 0, len(entries))
	warnings := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && !isCLIYAMLFile(entry.Name()) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if entry.IsDir() {
			name = entry.Name()
		}
		doc, ok, err := loadCLIKitDocument(runtimeHome, name)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if !ok {
			continue
		}
		rows = append(rows, kitDisplayRow{
			Name:        doc.Name,
			Title:       doc.Title,
			Description: doc.Description,
			Category:    doc.Category,
			Tags:        append([]string(nil), doc.Tags...),
			Path:        doc.Path,
			Providers:   len(doc.Providers),
			Agents:      len(doc.Agents),
			Skills:      len(doc.Skills),
			Tools:       len(doc.Tools),
			Workflows:   len(doc.Workflows) + len(doc.WorkflowTemplates),
			Warnings:    append([]string(nil), doc.Warnings...),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Category != rows[j].Category {
			return rows[i].Category < rows[j].Category
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, warnings
}

func loadCLIKitDocument(runtimeHome, name string) (cliKitDocument, bool, error) {
	runtimeHome = strings.TrimSpace(runtimeHome)
	if runtimeHome == "" {
		return cliKitDocument{}, false, fmt.Errorf("runtime home is not configured")
	}
	normalized := normalizeCLIResourceName(name)
	if normalized == "" {
		return cliKitDocument{}, false, fmt.Errorf("kit name is required")
	}
	root := filepath.Join(runtimeHome, "kits")
	path := filepath.Clean(filepath.Join(root, normalized, "kit.yaml"))
	if !referenceWithinBase(root, path) {
		return cliKitDocument{}, false, fmt.Errorf("kit path escapes runtime kit directory")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		legacyPath := filepath.Clean(filepath.Join(root, normalized+".yaml"))
		if !referenceWithinBase(root, legacyPath) {
			return cliKitDocument{}, false, fmt.Errorf("kit path escapes runtime kit directory")
		}
		data, err = os.ReadFile(legacyPath)
		if err != nil {
			if os.IsNotExist(err) {
				return cliKitDocument{}, false, nil
			}
			return cliKitDocument{}, false, fmt.Errorf("read kit %s: %w", normalized, err)
		}
		path = legacyPath
	}
	var doc cliKitDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return cliKitDocument{}, false, fmt.Errorf("parse kit %s: %w", normalized, err)
	}
	doc = normalizeCLIKitDocument(doc, normalized)
	doc.Path = path
	doc.Warnings = append(doc.Warnings, validateCLIKitDocument(doc)...)
	return doc, true, nil
}

func normalizeCLIKitDocument(doc cliKitDocument, fallbackName string) cliKitDocument {
	doc.Name = normalizeCLIResourceName(firstNonEmptyString(doc.Name, fallbackName))
	if strings.TrimSpace(doc.Kind) == "" {
		doc.Kind = "goflow.kit"
	}
	if doc.Version == 0 {
		doc.Version = 1
	}
	doc.Tags = normalizeCLIStringList(doc.Tags)
	doc.Providers = normalizeCLIStringList(doc.Providers)
	doc.Agents = normalizeCLIStringList(doc.Agents)
	doc.Skills = normalizeCLIStringList(doc.Skills)
	doc.Tools = normalizeCLIStringList(doc.Tools)
	doc.Workflows = normalizeCLIStringList(doc.Workflows)
	doc.WorkflowTemplates = normalizeCLIStringList(doc.WorkflowTemplates)
	doc.TeamTemplates = normalizeCLIStringList(doc.TeamTemplates)
	doc.PolicyRules = normalizeCLIStringList(doc.PolicyRules)
	doc.RequiredEnv = normalizeCLIStringList(doc.RequiredEnv)
	return doc
}

func validateCLIKitDocument(doc cliKitDocument) []string {
	warnings := make([]string, 0)
	if doc.Kind != "goflow.kit" {
		warnings = append(warnings, fmt.Sprintf("unsupported kind %q; expected goflow.kit", doc.Kind))
	}
	if doc.Version > 1 {
		warnings = append(warnings, fmt.Sprintf("kit version %d is newer than this CLI supports", doc.Version))
	}
	if doc.Name == "" {
		warnings = append(warnings, "kit name is required")
	}
	if len(doc.Providers)+len(doc.Agents)+len(doc.Skills)+len(doc.Tools)+len(doc.Workflows)+len(doc.WorkflowTemplates)+len(doc.TeamTemplates)+len(doc.PolicyRules) == 0 {
		warnings = append(warnings, "kit has no referenced resources")
	}
	for _, envName := range doc.RequiredEnv {
		if strings.TrimSpace(os.Getenv(envName)) == "" {
			warnings = append(warnings, "required environment variable "+envName+" is not set")
		}
	}
	return warnings
}

func normalizeCLIResourceName(name string) string {
	return strings.Trim(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-")), "-")
}

func normalizeCLIStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isCLIYAMLFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiBlue   = "\x1b[34m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiCyan   = "\x1b[36m"
)

func styleHeader(text string) string {
	return ansiBold + ansiBlue + text + ansiReset
}

func styleLabel(text string) string {
	return ansiBold + ansiCyan + text + ansiReset
}

func styleMuted(text string) string {
	return ansiDim + text + ansiReset
}

func styleStatus(text, kind string) string {
	color := ansiCyan
	switch kind {
	case "done", "ready", "completed":
		color = ansiGreen
	case "pending", "approval", "suspended":
		color = ansiYellow
	case "failed", "error", "denied":
		color = ansiRed
	}
	return color + text + ansiReset
}

func formatPrompt(agentRuntime *agent.Runtime) string {
	if agentRuntime == nil {
		return styleLabel("goflow") + styleMuted(">") + " "
	}
	agentID := fallbackDisplayText(agentRuntime.ActiveAgent(), "agent")
	mode := fallbackDisplayText(agentRuntime.Mode(), "chat")
	return fmt.Sprintf("%s%s%s ", styleLabel("goflow"), styleMuted("["+agentID+"/"+mode+"]"), styleMuted(">"))
}

func formatCommandSuccess(label, value string) string {
	return fmt.Sprintf("%s %s %s", styleStatus("[ok]", "ready"), styleLabel(label), value)
}

func formatCommandWarning(message string) string {
	return fmt.Sprintf("%s %s", styleStatus("[hint]", "approval"), message)
}

func formatUnknownCommand(command string) string {
	suggestion := suggestCommand(command)
	if suggestion != "" {
		return formatCommandWarning(fmt.Sprintf("Unknown command %s. Did you mean %s? Use /help to list commands.", command, suggestion))
	}
	return formatCommandWarning(fmt.Sprintf("Unknown command %s. Use /help to list commands.", command))
}

var knownCLICommands = []string{
	"/help",
	"/skills",
	"/tools",
	"/agents",
	"/kits",
	"/config-diagnostics",
	"/team-state",
	"/teams",
	"/status",
	"/session",
	"/workspace",
	"/use",
	"/mode",
	"/workflow",
	"/trace",
	"/cost",
	"/compact",
	"/memory",
	"/approve",
	"/deny",
	"/reload",
	"/reload-tools",
	"/skill-templates",
	"/new-skill",
	"/new-tool",
	"/new-agent",
	"/new-kit",
	"/new-policy-rule",
	"/new-provider",
	"/new-team",
	"/new-workflow",
	"/new-workflow-template",
	"/expression-helpers",
	"/policy-rules",
	"/workflow-expression-functions",
	"/workflow-node-metadata",
	"/workflow-schemas",
	"/workflow-templates",
}

func suggestCommand(command string) string {
	normalized := strings.ToLower(strings.TrimSpace(command))
	if normalized == "" {
		return ""
	}
	best := ""
	bestDistance := 4
	for _, candidate := range knownCLICommands {
		distance := editDistance(normalized, candidate)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	return best
}

func editDistance(left, right string) int {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	if len(leftRunes) == 0 {
		return len(rightRunes)
	}
	if len(rightRunes) == 0 {
		return len(leftRunes)
	}
	previous := make([]int, len(rightRunes)+1)
	current := make([]int, len(rightRunes)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, leftRune := range leftRunes {
		current[0] = i + 1
		for j, rightRune := range rightRunes {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[j+1] = minInt(previous[j+1]+1, current[j]+1, previous[j]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(rightRunes)]
}

func minInt(values ...int) int {
	if len(values) == 0 {
		return 0
	}
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

func formatWorkflowSummary(stages []string) string {
	if len(stages) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleHeader("Completed stages"))
	b.WriteString("\n")
	for _, stage := range stages {
		if strings.TrimSpace(stage) == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(stage)
		b.WriteString("\n")
	}
	return b.String()
}

type agentDisplayRow struct {
	Name             string
	Description      string
	Provider         string
	Mode             string
	ToolPolicy       string
	AllowedToolKinds []string
	AllowedTools     []string
	Active           bool
}

type startupDisplayRow struct {
	RuntimeHome           string
	WorkspaceRoot         string
	ActiveAgent           string
	Mode                  string
	ToolPolicy            string
	AllowedToolKinds      []string
	AllowedTools          []string
	WorkflowSummary       string
	PendingHandoffSummary string
	LastRoutingSummary    string
	PendingApprovals      int
}

type toolDisplayRow struct {
	Name                   string
	Description            string
	Server                 string
	Kind                   string
	Health                 string
	InputSchemaSummary     string
	Diagnostics            []string
	RiskLevel              string
	IsolationLevel         string
	Sandboxed              bool
	RequiresSandbox        bool
	SandboxFeatures        []string
	MissingSandboxFeatures []string
	WindowsIsolation       *schema.WindowsIsolationProfile
	EnvAllowlistSet        bool
	EnvAllowlist           []string
	SensitiveEnv           []string
}

type skillDisplayRow struct {
	Name           string
	Description    string
	Mode           string
	PreferredAgent string
	OutputKind     string
	Keywords       []string
	NextSkills     []string
}

type policyRuleDisplayRow struct {
	Name        string
	Label       string
	Description string
	Source      string
	Operator    string
	Custom      bool
	Params      []agent.WorkflowNodeFieldOption
}

type workflowTemplateDisplayRow struct {
	Name        string
	Title       string
	Description string
	Category    string
	Tags        []string
	Stages      int
	Source      string
	Custom      bool
	Path        string
	StageNames  []string
}

type workflowNodeMetadataSummary struct {
	Total      int
	Categories map[string]int
	Controls   int
	VisualOnly int
	Custom     int
}

type workflowSchemaDisplayRow struct {
	Workflow string
	Updated  string
	Runs     int
	Stages   int
	Outputs  int
}

type kitDisplayRow struct {
	Name        string
	Title       string
	Description string
	Category    string
	Tags        []string
	Path        string
	Agents      int
	Providers   int
	Skills      int
	Tools       int
	Workflows   int
	Warnings    []string
}

type cliKitDocument struct {
	Kind              string            `yaml:"kind"`
	Version           int               `yaml:"version"`
	MinVersion        int               `yaml:"min_supported_version"`
	Name              string            `yaml:"name"`
	Title             string            `yaml:"title"`
	Description       string            `yaml:"description"`
	Category          string            `yaml:"category"`
	Tags              []string          `yaml:"tags"`
	Providers         []string          `yaml:"providers"`
	Agents            []string          `yaml:"agents"`
	Skills            []string          `yaml:"skills"`
	Tools             []string          `yaml:"tools"`
	Workflows         []string          `yaml:"workflows"`
	WorkflowTemplates []string          `yaml:"workflow_templates"`
	TeamTemplates     []string          `yaml:"team_templates"`
	PolicyRules       []string          `yaml:"policy_rules"`
	RequiredEnv       []string          `yaml:"required_env"`
	Examples          []cliKitExample   `yaml:"examples"`
	Metadata          map[string]string `yaml:"metadata"`
	Path              string            `yaml:"-"`
	Warnings          []string          `yaml:"-"`
}

type cliKitExample struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Request     string `yaml:"request"`
	Workflow    string `yaml:"workflow"`
	Agent       string `yaml:"agent"`
}

type workflowDisplayRow struct {
	Name      string
	Status    string
	NextStage string
	Summary   string
}

type handoffDisplayRow struct {
	Request        string
	TargetAgent    string
	TargetMode     string
	ExpectedAction string
	PlanSummary    string
}

type routingDisplayRow struct {
	Request     string
	SourceAgent string
	TargetAgent string
	TargetMode  string
	Outcome     string
	Reason      string
}

type taskStageDisplayRow struct {
	Stage   string
	AgentID string
	Mode    string
	Detail  string
}

type approvalDisplayRow struct {
	CallID           string
	ToolName         string
	AgentID          string
	Stage            string
	ArgumentsSummary string
}

func formatHelpOutput() string {
	var b strings.Builder
	b.WriteString(styleHeader("Commands"))
	b.WriteString("\n")
	writeCommandGroup(&b, "Explore", []commandHelpRow{
		{Command: "/help", Description: "Show this command reference"},
		{Command: "/skills", Description: "List loaded skills and activation hints"},
		{Command: "/tools", Description: "List MCP tools with server, health, and schema"},
		{Command: "/agents", Description: "List configured agents and tool policies"},
		{Command: "/kits [name] [--export] | /kits --import <path>", Description: "List, inspect, export, or import vertical Agent kits"},
		{Command: "/policy-rules [name]", Description: "List workflow policy rules or show one rule"},
		{Command: "/teams [name]", Description: "List reusable multi-agent team templates or show one template"},
		{Command: "/workflow-templates [name]", Description: "List workflow templates or show one template"},
		{Command: "/workflow-node-metadata [type]", Description: "List workflow node editor metadata or show one node type"},
		{Command: "/expression-helpers [name]", Description: "List workflow expression helpers or show one helper"},
		{Command: "/workflow-schemas [name] [--rebuild|--json|--export|--import <path>|--clear]", Description: "List, inspect, import, export, rebuild, or clear observed workflow output schemas"},
		{Command: "/team-state [run-id] [team]", Description: "Show live collaboration state for a workflow team"},
		{Command: "/cost", Description: "Show prompt/token cost diagnostics and tuning hints"},
		{Command: "/compact [reason]", Description: "Summarize current session context into memory without deleting full history"},
		{Command: "/memory [project|search <query>|rebuild|errors|context]", Description: "Inspect project memory, task summaries, file index, compacted context, and error knowledge"},
		{Command: "/artifacts [query]", Description: "List recent summary-first artifacts and hash refs"},
		{Command: "/config-diagnostics [--json]", Description: "Validate saved config modules and show setup/safety diagnostics"},
		{Command: "/status", Description: "Show runtime, routing, workflow, and MCP state"},
		{Command: "/session", Description: "Show persisted session memory"},
	})
	writeCommandGroup(&b, "Control", []commandHelpRow{
		{Command: "/use <agent>", Description: "Switch active agent"},
		{Command: "/mode <chat|plan|audit|fix>", Description: "Switch session mode"},
		{Command: "/workspace [status|confirm|clear|use <path>|choose]", Description: "Show, confirm, switch, or choose the active workspace"},
		{Command: "/workflow plan-fix-audit [--approve] <request>", Description: "Run planner -> fixer -> auditor"},
		{Command: "/workflow skill-chain <request>", Description: "Run matched skill and declared next_skills"},
		{Command: "/workflow <custom-name> <request>", Description: "Run workflows/<name>/workflow.yaml"},
		{Command: "/trace on|off", Description: "Toggle CLI trace output"},
	})
	writeCommandGroup(&b, "Approval", []commandHelpRow{
		{Command: "/approve <id>", Description: "Approve a pending tool call"},
		{Command: "/deny <id>", Description: "Deny a pending tool call"},
	})
	writeCommandGroup(&b, "Maintenance", []commandHelpRow{
		{Command: "/reload", Description: "Reload skills from disk"},
		{Command: "/reload-tools", Description: "Refresh MCP tool cache"},
		{Command: "/skill-templates", Description: "List built-in skill scaffolds"},
		{Command: "/new-skill <template> <name>", Description: "Create skills/<name>/SKILL.md from a scaffold"},
		{Command: "/new-tool python <name>", Description: "Create a Python MCP server scaffold"},
		{Command: "/new-agent <name>", Description: "Create an agent config snippet"},
		{Command: "/new-policy-rule <preset> <name>", Description: "Create a workflow policy rule"},
		{Command: "/new-provider <name>", Description: "Create a provider config snippet"},
		{Command: "/new-team <preset> <name>", Description: "Create a team template resource"},
		{Command: "/new-workflow <name> [--template <template>]", Description: "Create a workflow blueprint"},
		{Command: "/new-workflow-template <source> <name>", Description: "Fork a workflow template resource"},
		{Command: "/new-kit <preset> <name>", Description: "Create a vertical kit manifest"},
		{Command: "exit", Description: "Quit the CLI"},
	})
	return b.String()
}

func handleMemoryCommand(ctx context.Context, fields []string, agentRuntime *agent.Runtime) bool {
	store := agentRuntime.MemoryStore()
	if store == nil {
		fmt.Println(formatCommandWarning("memory store is not configured"))
		return true
	}
	subcommand := ""
	if len(fields) > 1 {
		subcommand = strings.ToLower(strings.TrimSpace(fields[1]))
	}
	switch subcommand {
	case "", "summary", "status":
		dashboard, err := store.Dashboard(8)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory error: %v\n", err)
			return true
		}
		fmt.Print(formatMemoryDashboardOutput(dashboard))
	case "project":
		project, err := store.Project()
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory project error: %v\n", err)
			return true
		}
		fmt.Print(formatMemoryProjectOutput(project))
	case "search":
		query := strings.TrimSpace(strings.Join(fields[2:], " "))
		if query == "" {
			fmt.Fprintln(os.Stderr, "usage: /memory search <query>")
			return true
		}
		results, err := store.Search(query, 20)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory search error: %v\n", err)
			return true
		}
		fmt.Print(formatMemorySearchOutput(results))
	case "rebuild":
		index, err := store.RebuildFiles(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory rebuild error: %v\n", err)
			return true
		}
		fmt.Println(formatCommandSuccess("memory files indexed", fmt.Sprintf("%d files / %d bytes", index.TotalFiles, index.IndexedBytes)))
	case "errors":
		errorsKB, err := store.Errors()
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory errors error: %v\n", err)
			return true
		}
		fmt.Print(formatMemoryErrorsOutput(errorsKB))
	case "context", "compact":
		contextSummary, err := store.Context()
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory context error: %v\n", err)
			return true
		}
		fmt.Print(formatContextSummaryOutput(contextSummary))
	default:
		fmt.Fprintln(os.Stderr, "usage: /memory [project|search <query>|rebuild|errors|context]")
	}
	return true
}

func handleArtifactsCommand(fields []string, agentRuntime *agent.Runtime) bool {
	query := ""
	if len(fields) > 1 {
		query = strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	artifacts := agentRuntime.SessionArtifacts(session.SessionArtifactFilter{Query: query, Limit: 20})
	fmt.Print(formatArtifactListOutput(artifacts))
	return true
}

func formatMemoryDashboardOutput(dashboard memory.Dashboard) string {
	var b strings.Builder
	b.WriteString(styleHeader("Memory"))
	b.WriteString("\n")
	projectSummary := truncateCLISummaryValue(dashboard.Project.Summary)
	if projectSummary == "" {
		projectSummary = "project memory is empty"
	}
	b.WriteString(styleLabel("project"))
	b.WriteString(" ")
	b.WriteString(projectSummary)
	b.WriteString("\n")
	b.WriteString(styleLabel("tasks"))
	b.WriteString(fmt.Sprintf(" %d retained\n", len(dashboard.Tasks)))
	b.WriteString(styleLabel("errors"))
	b.WriteString(fmt.Sprintf(" %d known\n", len(dashboard.Errors.Errors)))
	b.WriteString(styleLabel("files"))
	b.WriteString(fmt.Sprintf(" %d indexed (%d bytes)\n", dashboard.FileIndex.TotalFiles, dashboard.FileIndex.IndexedBytes))
	if strings.TrimSpace(dashboard.Context.Summary) != "" {
		b.WriteString(styleLabel("context"))
		b.WriteString(fmt.Sprintf(" saved=%d updated=%s\n", dashboard.Context.EstimatedSavedTokens, firstNonEmptyString(dashboard.Context.UpdatedAt, "-")))
	}
	if len(dashboard.Tasks) > 0 {
		b.WriteString("\n")
		b.WriteString(styleHeader("Recent Tasks"))
		b.WriteString("\n")
		for _, task := range dashboard.Tasks {
			b.WriteString("  ")
			b.WriteString(styleLabel(firstNonEmptyString(task.ID, "task")))
			b.WriteString(" ")
			b.WriteString(truncateCLISummaryValue(task.UserGoal))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(styleMuted("Use /compact, /memory search <query>, /memory project, /memory rebuild, /memory context, or /memory errors.\n"))
	return b.String()
}

func formatContextSummaryOutput(summary memory.ContextSummary) string {
	var b strings.Builder
	b.WriteString(styleHeader("Compacted Context"))
	b.WriteString("\n")
	if strings.TrimSpace(summary.Summary) == "" && strings.TrimSpace(summary.ID) == "" {
		b.WriteString(styleMuted("no compacted context recorded\n"))
		return b.String()
	}
	if summary.ID != "" {
		b.WriteString(styleLabel("id"))
		b.WriteString(" ")
		b.WriteString(summary.ID)
		b.WriteString("\n")
	}
	if summary.UpdatedAt != "" {
		b.WriteString(styleLabel("updated"))
		b.WriteString(" ")
		b.WriteString(summary.UpdatedAt)
		b.WriteString("\n")
	}
	b.WriteString(styleLabel("agent"))
	b.WriteString(" ")
	b.WriteString(firstNonEmptyString(summary.ActiveAgent, "-"))
	b.WriteString(" ")
	b.WriteString(styleLabel("mode"))
	b.WriteString(" ")
	b.WriteString(firstNonEmptyString(summary.Mode, "-"))
	if summary.Auto {
		b.WriteString(" ")
		b.WriteString(styleStatus("auto", "ready"))
	}
	b.WriteString("\n")
	if summary.EstimatedSavedTokens > 0 {
		b.WriteString(styleLabel("estimated_saved_tokens"))
		b.WriteString(fmt.Sprintf(" %d\n", summary.EstimatedSavedTokens))
	}
	if strings.TrimSpace(summary.Summary) != "" {
		b.WriteString("\n")
		b.WriteString(summary.Summary)
		b.WriteString("\n")
	}
	writeContextSummaryList(&b, "goals", summary.RecentGoals)
	writeContextSummaryList(&b, "decisions", summary.Decisions)
	writeContextSummaryList(&b, "pending", summary.PendingActions)
	writeContextSummaryList(&b, "files", summary.RelevantFiles)
	writeContextSummaryList(&b, "artifacts", summary.ArtifactRefs)
	return b.String()
}

func writeContextSummaryList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(styleLabel(label))
	b.WriteString("\n")
	limit := len(values)
	if limit > 6 {
		limit = 6
	}
	for _, value := range values[:limit] {
		b.WriteString("  - ")
		b.WriteString(truncateCLISummaryValue(value))
		b.WriteString("\n")
	}
	if len(values) > limit {
		b.WriteString(styleMuted(fmt.Sprintf("  ... %d more\n", len(values)-limit)))
	}
}

func formatMemoryProjectOutput(project memory.ProjectMemory) string {
	var b strings.Builder
	b.WriteString(styleHeader("Project Memory"))
	b.WriteString("\n")
	if project.Path != "" {
		b.WriteString(styleLabel("path"))
		b.WriteString(" ")
		b.WriteString(project.Path)
		b.WriteString("\n")
	}
	if project.UpdatedAt != "" {
		b.WriteString(styleLabel("updated"))
		b.WriteString(" ")
		b.WriteString(project.UpdatedAt)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if strings.TrimSpace(project.Content) == "" {
		b.WriteString(styleMuted("project memory is empty\n"))
		return b.String()
	}
	b.WriteString(project.Content)
	if !strings.HasSuffix(project.Content, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func formatMemorySearchOutput(results memory.SearchResponse) string {
	var b strings.Builder
	b.WriteString(styleHeader("Memory Search"))
	b.WriteString("\n")
	b.WriteString(styleLabel("query"))
	b.WriteString(" ")
	b.WriteString(firstNonEmptyString(results.Query, "-"))
	b.WriteString("\n")
	b.WriteString(styleLabel("results"))
	b.WriteString(fmt.Sprintf(" %d of %d\n", results.Returned, results.Total))
	for _, result := range results.Results {
		b.WriteString("\n")
		b.WriteString(styleLabel(result.Kind))
		b.WriteString(" ")
		b.WriteString(firstNonEmptyString(result.Title, result.Path, "memory item"))
		if result.Score > 0 {
			b.WriteString(fmt.Sprintf(" score=%d", result.Score))
		}
		if result.Path != "" {
			b.WriteString(" ref=")
			b.WriteString(result.Path)
		}
		b.WriteString("\n")
		summary := strings.ReplaceAll(strings.TrimSpace(result.Summary), "\n", " ")
		if summary != "" {
			b.WriteString("  ")
			b.WriteString(truncateCLISummaryValue(summary))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func formatMemoryErrorsOutput(errorsKB memory.ErrorKnowledgeBase) string {
	var b strings.Builder
	b.WriteString(styleHeader("Memory Errors"))
	b.WriteString("\n")
	if len(errorsKB.Errors) == 0 {
		b.WriteString(styleMuted("no error knowledge recorded\n"))
		return b.String()
	}
	for _, item := range errorsKB.Errors {
		b.WriteString(styleLabel(firstNonEmptyString(item.ID, "error")))
		b.WriteString(" ")
		b.WriteString(truncateCLISummaryValue(item.Error))
		b.WriteString("\n")
		if item.RootCause != "" {
			b.WriteString("  cause: ")
			b.WriteString(truncateCLISummaryValue(item.RootCause))
			b.WriteString("\n")
		}
		if item.Fix != "" {
			b.WriteString("  fix: ")
			b.WriteString(truncateCLISummaryValue(item.Fix))
			b.WriteString("\n")
		}
		if item.VerificationCommand != "" {
			b.WriteString("  verify: ")
			b.WriteString(truncateCLISummaryValue(item.VerificationCommand))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func formatArtifactListOutput(artifacts []session.SessionArtifactSnapshot) string {
	var b strings.Builder
	b.WriteString(styleHeader("Artifacts"))
	b.WriteString("\n")
	if len(artifacts) == 0 {
		b.WriteString(styleMuted("no artifacts recorded\n"))
		return b.String()
	}
	for _, artifact := range artifacts {
		ref := firstNonEmptyString(artifact.ArtifactRef, artifact.Ref, artifact.Hash)
		b.WriteString(styleLabel(firstNonEmptyString(artifact.ID, "artifact")))
		b.WriteString(" ")
		b.WriteString(firstNonEmptyString(artifact.Title, artifact.Kind, "Artifact"))
		if artifact.ContentBytes > 0 {
			b.WriteString(fmt.Sprintf(" %d bytes", artifact.ContentBytes))
		}
		if ref != "" {
			b.WriteString(" ref=")
			b.WriteString(ref)
		}
		if artifact.Deduplicated {
			b.WriteString(" deduplicated")
		}
		b.WriteString("\n")
		if strings.TrimSpace(artifact.Summary) != "" {
			b.WriteString("  ")
			b.WriteString(truncateCLISummaryValue(strings.ReplaceAll(artifact.Summary, "\n", " ")))
			b.WriteString("\n")
		}
	}
	return b.String()
}

type commandHelpRow struct {
	Command     string
	Description string
}

func writeCommandGroup(b *strings.Builder, title string, rows []commandHelpRow) {
	if b == nil {
		return
	}
	fmt.Fprintf(b, "%s\n", styleLabel(title))
	for _, row := range rows {
		fmt.Fprintf(b, "  %s %s\n", styleStatus(fmt.Sprintf("%-42s", row.Command), "ready"), row.Description)
	}
}

func formatAgentsOutput(rows []agentDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Agents"))
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(styleMuted("  none configured\n"))
		return b.String()
	}
	fmt.Fprintf(&b, "%s %s\n", styleMuted("summary"), formatAgentSummary(rows))
	for _, mode := range orderedAgentModes(rows) {
		group := filterAgentsByMode(rows, mode)
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s %s %s\n", styleLabel("mode"), styleStatus(mode, stageStatusKind(mode)), styleMuted(fmt.Sprintf("(%d)", len(group))))
		for _, row := range group {
			name := row.Name
			active := ""
			if row.Active {
				name = styleStatus(row.Name, "ready")
				active = " " + styleStatus("active", "ready")
			}
			fmt.Fprintf(&b, "  %s%s  %s=%s  %s=%s\n", name, active, styleLabel("provider"), fallbackDisplayText(row.Provider, "-"), styleLabel("policy"), styleStatus(fallbackDisplayText(row.ToolPolicy, "-"), policyStatusKind(row.ToolPolicy)))
			if len(row.AllowedToolKinds) > 0 || len(row.AllowedTools) > 0 {
				fmt.Fprintf(&b, "    %s %s", styleLabel("kinds"), fallbackDisplayText(strings.Join(row.AllowedToolKinds, ", "), "-"))
				if len(row.AllowedTools) > 0 {
					fmt.Fprintf(&b, "  %s %s", styleLabel("allowlist"), strings.Join(row.AllowedTools, ", "))
				}
				b.WriteString("\n")
			}
			if strings.TrimSpace(row.Description) != "" {
				fmt.Fprintf(&b, "    %s\n", row.Description)
			}
		}
	}
	return b.String()
}

func formatTeamTemplatesOutput(rows []agent.TeamTemplateSummary) string {
	var b strings.Builder
	b.WriteString(styleHeader("Team Templates"))
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(styleMuted("  none configured\n"))
		return b.String()
	}
	categories := make(map[string]int)
	for _, row := range rows {
		categories[fallbackDisplayText(row.Category, "uncategorized")]++
	}
	fmt.Fprintf(&b, "%s total=%d  categories=%s\n", styleMuted("summary"), len(rows), formatCountMap(categories))
	for _, row := range rows {
		source := fallbackDisplayText(row.Source, "built_in")
		fmt.Fprintf(&b, "\n%s %s %s %s\n", styleStatus(row.Name, "ready"), styleMuted(fmt.Sprintf("roles=%d", row.Roles)), styleMuted(fallbackDisplayText(row.Category, "-")), styleMuted(source))
		if strings.TrimSpace(row.Title) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("title"), row.Title)
		}
		if strings.TrimSpace(row.Description) != "" {
			fmt.Fprintf(&b, "  %s\n", row.Description)
		}
		if strings.TrimSpace(row.RecommendedWorkflow) != "" || strings.TrimSpace(row.RecommendedEntryAgent) != "" {
			fmt.Fprintf(&b, "  %s %s  %s %s\n", styleLabel("workflow"), fallbackDisplayText(row.RecommendedWorkflow, "-"), styleLabel("entry"), fallbackDisplayText(row.RecommendedEntryAgent, "-"))
		}
		if strings.TrimSpace(row.Path) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("path"), row.Path)
		}
		if len(row.Tags) > 0 {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("tags"), strings.Join(row.Tags, ", "))
		}
	}
	return b.String()
}

func formatTeamTemplateDetailOutput(template agent.TeamTemplate) string {
	var b strings.Builder
	b.WriteString(styleHeader("Team Template"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s %s\n", styleLabel("name"), template.Name)
	fmt.Fprintf(&b, "%s %s\n", styleLabel("title"), fallbackDisplayText(template.Title, "-"))
	fmt.Fprintf(&b, "%s %s", styleLabel("source"), fallbackDisplayText(template.Source, "built_in"))
	if template.Version > 0 {
		fmt.Fprintf(&b, "  %s v%d", styleLabel("version"), template.Version)
	}
	b.WriteString("\n")
	if strings.TrimSpace(template.Path) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("path"), template.Path)
	}
	if strings.TrimSpace(template.Description) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("description"), template.Description)
	}
	fmt.Fprintf(&b, "%s %s  %s %s  %s %s\n",
		styleLabel("category"), fallbackDisplayText(template.Category, "-"),
		styleLabel("workflow"), fallbackDisplayText(template.RecommendedWorkflow, "-"),
		styleLabel("entry"), fallbackDisplayText(template.RecommendedEntryAgent, "-"))
	if len(template.Tags) > 0 {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("tags"), strings.Join(template.Tags, ", "))
	}
	if len(template.RoleTemplates) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("roles"))
		b.WriteString("\n")
		for _, role := range template.RoleTemplates {
			fmt.Fprintf(&b, "  %s  %s=%s  %s=%s\n", styleStatus(role.Name, "ready"), styleLabel("agent"), fallbackDisplayText(role.Agent, "-"), styleLabel("skill"), fallbackDisplayText(role.Skill, "-"))
			if len(role.Responsibilities) > 0 {
				fmt.Fprintf(&b, "    %s %s\n", styleLabel("does"), strings.Join(role.Responsibilities, "; "))
			}
			if len(role.Consumes) > 0 || len(role.Produces) > 0 {
				fmt.Fprintf(&b, "    %s %s  %s %s\n", styleLabel("consumes"), fallbackDisplayText(strings.Join(role.Consumes, ", "), "-"), styleLabel("produces"), fallbackDisplayText(strings.Join(role.Produces, ", "), "-"))
			}
		}
	}
	if len(template.Handoffs) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("handoffs"))
		b.WriteString("\n")
		for _, handoff := range template.Handoffs {
			fmt.Fprintf(&b, "  %s -> %s  %s=%s\n", handoff.From, handoff.To, styleLabel("kind"), fallbackDisplayText(handoff.Kind, "-"))
			if strings.TrimSpace(handoff.Subject) != "" {
				fmt.Fprintf(&b, "    %s\n", handoff.Subject)
			}
		}
	}
	if len(template.BlackboardTemplates) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("blackboard"))
		b.WriteString("\n")
		for _, item := range template.BlackboardTemplates {
			fmt.Fprintf(&b, "  %s  %s=%s  %s=%s\n", item.Kind, styleLabel("owner"), fallbackDisplayText(item.OwnerRole, "-"), styleLabel("status"), fallbackDisplayText(item.Status, "-"))
		}
	}
	if len(template.OutputContract) > 0 {
		fmt.Fprintf(&b, "\n%s %s\n", styleLabel("outputs"), strings.Join(template.OutputContract, ", "))
	}
	return b.String()
}

func formatPolicyRulesOutput(rows []policyRuleDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Workflow Policy Rules"))
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(styleMuted("  none configured\n"))
		return b.String()
	}
	sources := make(map[string]int)
	operators := make(map[string]int)
	for _, row := range rows {
		sources[fallbackDisplayText(row.Source, "built_in")]++
		operators[fallbackDisplayText(row.Operator, "expression")]++
	}
	fmt.Fprintf(&b, "%s total=%d  sources=%s  operators=%s\n", styleMuted("summary"), len(rows), formatCountMap(sources), formatCountMap(operators))
	for _, row := range rows {
		source := fallbackDisplayText(row.Source, "built_in")
		operator := fallbackDisplayText(row.Operator, "expression")
		fmt.Fprintf(&b, "\n%s %s %s\n", styleStatus(row.Name, "ready"), styleMuted(operator), styleMuted(source))
		if strings.TrimSpace(row.Label) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("label"), row.Label)
		}
		if strings.TrimSpace(row.Description) != "" {
			fmt.Fprintf(&b, "  %s\n", row.Description)
		}
		if len(row.Params) > 0 {
			names := make([]string, 0, len(row.Params))
			for _, param := range row.Params {
				if strings.TrimSpace(param.Name) != "" {
					names = append(names, param.Name)
				}
			}
			if len(names) > 0 {
				fmt.Fprintf(&b, "  %s %s\n", styleLabel("params"), strings.Join(names, ", "))
			}
		}
	}
	b.WriteString("\n")
	b.WriteString(styleMuted("custom policy rules load from policies/workflow_rules/*.yaml; create one with /new-policy-rule <preset> <name>\n"))
	return b.String()
}

func formatPolicyRuleDetailOutput(row policyRuleDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Workflow Policy Rule"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s %s\n", styleLabel("name"), row.Name)
	fmt.Fprintf(&b, "%s %s  %s %s  %s %s\n",
		styleLabel("operator"), fallbackDisplayText(row.Operator, "expression"),
		styleLabel("source"), fallbackDisplayText(row.Source, "built_in"),
		styleLabel("custom"), strconv.FormatBool(row.Custom))
	if strings.TrimSpace(row.Label) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("label"), row.Label)
	}
	if strings.TrimSpace(row.Description) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("description"), row.Description)
	}
	if len(row.Params) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("params"))
		b.WriteString("\n")
		for _, param := range row.Params {
			fmt.Fprintf(&b, "  %s  %s=%s", fallbackDisplayText(param.Name, "-"), styleLabel("type"), fallbackDisplayText(param.Type, "-"))
			if param.Required {
				fmt.Fprintf(&b, "  %s", styleStatus("required", "approval"))
			}
			b.WriteString("\n")
			if strings.TrimSpace(param.Description) != "" {
				fmt.Fprintf(&b, "    %s\n", param.Description)
			}
			if len(param.Options) > 0 {
				fmt.Fprintf(&b, "    %s %s\n", styleLabel("options"), strings.Join(param.Options, ", "))
			}
		}
	}
	return b.String()
}

func formatWorkflowTemplatesOutput(rows []workflowTemplateDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Workflow Templates"))
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(styleMuted("  none configured\n"))
		return b.String()
	}
	categories := make(map[string]int)
	sources := make(map[string]int)
	for _, row := range rows {
		categories[fallbackDisplayText(row.Category, "uncategorized")]++
		sources[fallbackDisplayText(row.Source, "built_in")]++
	}
	fmt.Fprintf(&b, "%s total=%d  categories=%s  sources=%s\n", styleMuted("summary"), len(rows), formatCountMap(categories), formatCountMap(sources))
	for _, row := range rows {
		fmt.Fprintf(&b, "\n%s %s %s %s\n", styleStatus(row.Name, "ready"), styleMuted(fmt.Sprintf("stages=%d", row.Stages)), styleMuted(fallbackDisplayText(row.Category, "-")), styleMuted(fallbackDisplayText(row.Source, "built_in")))
		if strings.TrimSpace(row.Title) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("title"), row.Title)
		}
		if strings.TrimSpace(row.Description) != "" {
			fmt.Fprintf(&b, "  %s\n", row.Description)
		}
		if len(row.Tags) > 0 {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("tags"), strings.Join(row.Tags, ", "))
		}
		if strings.TrimSpace(row.Path) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("path"), row.Path)
		}
	}
	b.WriteString("\n")
	b.WriteString(styleMuted("custom workflow templates load from templates/workflows/*.yaml; fork one with /new-workflow-template <source> <name>\n"))
	return b.String()
}

func formatWorkflowTemplateDetailOutput(row workflowTemplateDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Workflow Template"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s %s\n", styleLabel("name"), row.Name)
	fmt.Fprintf(&b, "%s %s  %s %s  %s %s\n",
		styleLabel("category"), fallbackDisplayText(row.Category, "-"),
		styleLabel("source"), fallbackDisplayText(row.Source, "built_in"),
		styleLabel("stages"), strconv.Itoa(row.Stages))
	if strings.TrimSpace(row.Title) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("title"), row.Title)
	}
	if strings.TrimSpace(row.Description) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("description"), row.Description)
	}
	if len(row.Tags) > 0 {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("tags"), strings.Join(row.Tags, ", "))
	}
	if strings.TrimSpace(row.Path) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("path"), row.Path)
	}
	if len(row.StageNames) > 0 {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("stage_order"), strings.Join(row.StageNames, " -> "))
	}
	return b.String()
}

func formatWorkflowNodeMetadataOutput(nodes []agent.WorkflowNodeTypeOption) string {
	var b strings.Builder
	b.WriteString(styleHeader("Workflow Node Metadata"))
	b.WriteString("\n")
	if len(nodes) == 0 {
		b.WriteString(styleMuted("  no workflow node metadata available\n"))
		return b.String()
	}
	summary := summarizeWorkflowNodeMetadata(nodes)
	fmt.Fprintf(&b, "%s total=%d  categories=%s  control=%d  visual=%d",
		styleMuted("summary"), summary.Total, formatCountMap(summary.Categories), summary.Controls, summary.VisualOnly)
	if summary.Custom > 0 {
		fmt.Fprintf(&b, "  custom=%d", summary.Custom)
	}
	b.WriteString("\n")
	for _, node := range nodes {
		flags := make([]string, 0, 3)
		if node.Control {
			flags = append(flags, "control")
		}
		if node.VisualOnly {
			flags = append(flags, "visual")
		}
		if node.Custom {
			flags = append(flags, "custom")
		}
		name := styleStatus(node.Type, "ready")
		if node.VisualOnly {
			name = styleMuted(node.Type)
		}
		fmt.Fprintf(&b, "\n%s %s %s\n", name, styleMuted(fallbackDisplayText(node.Category, "-")), styleMuted(strings.Join(flags, ",")))
		if strings.TrimSpace(node.Label) != "" && !strings.EqualFold(node.Label, node.Type) {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("label"), node.Label)
		}
		if strings.TrimSpace(node.Description) != "" {
			fmt.Fprintf(&b, "  %s\n", node.Description)
		}
		if len(node.Fields) > 0 || len(node.Outputs) > 0 {
			fmt.Fprintf(&b, "  %s fields=%d outputs=%d examples=%d\n", styleLabel("schema"), len(node.Fields), len(node.Outputs), len(node.Examples))
		}
		if strings.TrimSpace(node.Path) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("path"), node.Path)
		}
	}
	b.WriteString("\n")
	b.WriteString(styleMuted("custom metadata loads from metadata/workflow_nodes/*.yaml; details: /workflow-node-metadata <type>\n"))
	return b.String()
}

func summarizeWorkflowNodeMetadata(nodes []agent.WorkflowNodeTypeOption) workflowNodeMetadataSummary {
	summary := workflowNodeMetadataSummary{Total: len(nodes), Categories: make(map[string]int)}
	for _, node := range nodes {
		summary.Categories[fallbackDisplayText(node.Category, "uncategorized")]++
		if node.Control {
			summary.Controls++
		}
		if node.VisualOnly {
			summary.VisualOnly++
		}
		if node.Custom {
			summary.Custom++
		}
	}
	return summary
}

func formatWorkflowNodeMetadataDetailOutput(node agent.WorkflowNodeTypeOption) string {
	var b strings.Builder
	b.WriteString(styleHeader("Workflow Node Metadata"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s %s\n", styleLabel("type"), node.Type)
	fmt.Fprintf(&b, "%s %s  %s %s", styleLabel("label"), fallbackDisplayText(node.Label, "-"), styleLabel("category"), fallbackDisplayText(node.Category, "-"))
	if node.Control {
		fmt.Fprintf(&b, "  %s", styleStatus("control", "approval"))
	}
	if node.VisualOnly {
		fmt.Fprintf(&b, "  %s", styleMuted("visual"))
	}
	if node.Custom {
		fmt.Fprintf(&b, "  %s", styleStatus("custom", "ready"))
	}
	b.WriteString("\n")
	if strings.TrimSpace(node.Description) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("description"), node.Description)
	}
	if strings.TrimSpace(node.Path) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("path"), node.Path)
	}
	writeWorkflowNodeFields(&b, "fields", node.Fields)
	writeWorkflowNodeOutputs(&b, node.Outputs)
	if len(node.Tags) > 0 {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("tags"), strings.Join(node.Tags, ", "))
	}
	for _, hint := range node.Hints {
		if strings.TrimSpace(hint) != "" {
			fmt.Fprintf(&b, "%s %s\n", styleLabel("hint"), hint)
		}
	}
	for _, warning := range node.Warnings {
		if strings.TrimSpace(warning) != "" {
			fmt.Fprintf(&b, "%s %s\n", styleStatus("warning", "approval"), warning)
		}
	}
	if len(node.Examples) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("examples"))
		b.WriteString("\n")
		for _, example := range node.Examples {
			fmt.Fprintf(&b, "  %s\n", fallbackDisplayText(example.Title, "Example"))
			if strings.TrimSpace(example.Description) != "" {
				fmt.Fprintf(&b, "    %s\n", example.Description)
			}
		}
	}
	return b.String()
}

func writeWorkflowNodeFields(b *strings.Builder, title string, fields []agent.WorkflowNodeFieldOption) {
	if b == nil || len(fields) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(styleLabel(title))
	b.WriteString("\n")
	for _, field := range fields {
		fmt.Fprintf(b, "  %s  %s=%s", fallbackDisplayText(field.Name, "-"), styleLabel("type"), fallbackDisplayText(field.Type, "-"))
		if field.Required {
			fmt.Fprintf(b, "  %s", styleStatus("required", "approval"))
		}
		b.WriteString("\n")
		if strings.TrimSpace(field.Description) != "" {
			fmt.Fprintf(b, "    %s\n", field.Description)
		}
		if len(field.Options) > 0 {
			fmt.Fprintf(b, "    %s %s\n", styleLabel("options"), strings.Join(field.Options, ", "))
		}
	}
}

func writeWorkflowNodeOutputs(b *strings.Builder, outputs []agent.WorkflowNodeVariableOption) {
	if b == nil || len(outputs) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(styleLabel("outputs"))
	b.WriteString("\n")
	for _, output := range outputs {
		fmt.Fprintf(b, "  %s", fallbackDisplayText(output.Name, "-"))
		if strings.TrimSpace(output.Description) != "" {
			fmt.Fprintf(b, "  %s", output.Description)
		}
		b.WriteString("\n")
	}
}

func formatExpressionHelpersOutput(helpers []agent.WorkflowExpressionFunctionOption, mode, nodeType string) string {
	var b strings.Builder
	b.WriteString(styleHeader("Expression Helpers"))
	b.WriteString("\n")
	if len(helpers) == 0 {
		b.WriteString(styleMuted("  no expression helpers match the filters\n"))
		return b.String()
	}
	categories := make(map[string]int)
	custom := 0
	for _, helper := range helpers {
		categories[fallbackDisplayText(helper.Category, "uncategorized")]++
		if helper.Custom {
			custom++
		}
	}
	fmt.Fprintf(&b, "%s total=%d  categories=%s", styleMuted("summary"), len(helpers), formatCountMap(categories))
	if custom > 0 {
		fmt.Fprintf(&b, "  custom=%d", custom)
	}
	if strings.TrimSpace(mode) != "" || strings.TrimSpace(nodeType) != "" {
		fmt.Fprintf(&b, "  %s mode=%s node_type=%s", styleLabel("filter"), fallbackDisplayText(mode, "-"), fallbackDisplayText(nodeType, "-"))
	}
	b.WriteString("\n")
	for _, helper := range helpers {
		source := fallbackDisplayText(helper.Source, "built_in")
		fmt.Fprintf(&b, "\n%s %s %s\n", styleStatus(helper.Name, "ready"), styleMuted(fallbackDisplayText(helper.Category, "-")), styleMuted(source))
		if strings.TrimSpace(helper.Signature) != "" {
			fmt.Fprintf(&b, "  %s %s  %s %s\n", styleLabel("signature"), helper.Signature, styleLabel("returns"), fallbackDisplayText(helper.ReturnType, "-"))
		}
		if strings.TrimSpace(helper.Description) != "" {
			fmt.Fprintf(&b, "  %s\n", helper.Description)
		}
		if len(helper.Modes) > 0 || len(helper.NodeTypes) > 0 {
			fmt.Fprintf(&b, "  %s %s  %s %s\n", styleLabel("modes"), strings.Join(helper.Modes, ", "), styleLabel("nodes"), strings.Join(limitStrings(helper.NodeTypes, 6), ", "))
		}
		if strings.TrimSpace(helper.Path) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("path"), helper.Path)
		}
	}
	b.WriteString("\n")
	b.WriteString(styleMuted("custom helper metadata loads from metadata/expression_helpers/*.yaml; details: /expression-helpers <name>\n"))
	return b.String()
}

func formatExpressionHelperDetailOutput(helper agent.WorkflowExpressionFunctionOption) string {
	var b strings.Builder
	b.WriteString(styleHeader("Expression Helper"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s %s\n", styleLabel("name"), helper.Name)
	fmt.Fprintf(&b, "%s %s  %s %s  %s %s\n",
		styleLabel("category"), fallbackDisplayText(helper.Category, "-"),
		styleLabel("source"), fallbackDisplayText(helper.Source, "built_in"),
		styleLabel("returns"), fallbackDisplayText(helper.ReturnType, "-"))
	if strings.TrimSpace(helper.Label) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("label"), helper.Label)
	}
	if strings.TrimSpace(helper.Signature) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("signature"), helper.Signature)
	}
	if strings.TrimSpace(helper.InsertText) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("insert"), helper.InsertText)
	}
	if strings.TrimSpace(helper.Description) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("description"), helper.Description)
	}
	fmt.Fprintf(&b, "%s min=%d max=%d\n", styleLabel("args"), helper.MinArgs, helper.MaxArgs)
	if len(helper.Modes) > 0 {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("modes"), strings.Join(helper.Modes, ", "))
	}
	if len(helper.NodeTypes) > 0 {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("node_types"), strings.Join(helper.NodeTypes, ", "))
	}
	if strings.TrimSpace(helper.Path) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("path"), helper.Path)
	}
	writeExpressionHelperArgs(&b, helper.Args)
	if len(helper.Examples) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("examples"))
		b.WriteString("\n")
		for _, example := range helper.Examples {
			if strings.TrimSpace(example) != "" {
				fmt.Fprintf(&b, "  %s\n", example)
			}
		}
	}
	for _, hint := range helper.Hints {
		if strings.TrimSpace(hint) != "" {
			fmt.Fprintf(&b, "%s %s\n", styleLabel("hint"), hint)
		}
	}
	for _, warning := range helper.Warnings {
		if strings.TrimSpace(warning) != "" {
			fmt.Fprintf(&b, "%s %s\n", styleStatus("warning", "approval"), warning)
		}
	}
	return b.String()
}

func writeExpressionHelperArgs(b *strings.Builder, args []agent.WorkflowExpressionFunctionArgument) {
	if b == nil || len(args) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(styleLabel("arguments"))
	b.WriteString("\n")
	for _, arg := range args {
		fmt.Fprintf(b, "  %s  %s=%s", fallbackDisplayText(arg.Name, "-"), styleLabel("type"), fallbackDisplayText(arg.Type, "-"))
		if arg.Required {
			fmt.Fprintf(b, "  %s", styleStatus("required", "approval"))
		}
		b.WriteString("\n")
		if strings.TrimSpace(arg.Description) != "" {
			fmt.Fprintf(b, "    %s\n", arg.Description)
		}
		if len(arg.Accepts) > 0 {
			fmt.Fprintf(b, "    %s %s\n", styleLabel("accepts"), strings.Join(arg.Accepts, ", "))
		}
	}
}

func formatConfigDiagnosticsOutput(diagnostics apipkg.ConfigDiagnosticsResponse) string {
	var b strings.Builder
	b.WriteString(styleHeader("Config Diagnostics"))
	b.WriteString("\n")
	statusKind := "ready"
	if diagnostics.Status == "error" {
		statusKind = "failed"
	} else if diagnostics.Status == "warning" || diagnostics.Diagnostics.Warnings > 0 {
		statusKind = "approval"
	}
	fmt.Fprintf(&b, "%s %s  %s providers=%d agents=%d mcp=%d skills=%d workflows=%d policies=%d kits=%d\n",
		styleLabel("status"), styleStatus(fallbackDisplayText(diagnostics.Status, "ok"), statusKind),
		styleMuted("summary"),
		diagnostics.Summary.Providers,
		diagnostics.Summary.Agents,
		diagnostics.Summary.MCPServers,
		diagnostics.Summary.Skills,
		diagnostics.Summary.Workflows,
		diagnostics.Summary.PolicyRules,
		diagnostics.Summary.Kits)
	fmt.Fprintf(&b, "%s errors=%d warnings=%d info=%d total=%d\n",
		styleLabel("diagnostics"),
		diagnostics.Diagnostics.Errors,
		diagnostics.Diagnostics.Warnings,
		diagnostics.Diagnostics.Info,
		diagnostics.Diagnostics.Total)
	if diagnostics.RestartRequired {
		fmt.Fprintf(&b, "%s %s\n", styleStatus("restart_required", "approval"), "saved config differs from the active runtime")
	}
	if strings.TrimSpace(diagnostics.ConfigPath) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("config"), diagnostics.ConfigPath)
	}
	if strings.TrimSpace(diagnostics.RuntimeHome) != "" || strings.TrimSpace(diagnostics.WorkspaceRoot) != "" {
		fmt.Fprintf(&b, "%s %s  %s %s\n", styleLabel("runtime"), fallbackDisplayText(diagnostics.RuntimeHome, "-"), styleLabel("workspace"), fallbackDisplayText(diagnostics.WorkspaceRoot, "-"))
	}
	if len(diagnostics.Modules) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("modules"))
		b.WriteString("\n")
		for i, module := range diagnostics.Modules {
			if i >= 12 {
				fmt.Fprintf(&b, "  %s\n", styleMuted(fmt.Sprintf("... %d more modules", len(diagnostics.Modules)-i)))
				break
			}
			fmt.Fprintf(&b, "  %s  %s=%d", fallbackDisplayText(module.Kind, "-"), styleLabel("files"), len(module.Files))
			if len(module.Files) > 0 && strings.TrimSpace(module.Root) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("root"), module.Root)
			}
			b.WriteString("\n")
		}
	}
	displayItems, hiddenOptional := visibleConfigDiagnosticItems(diagnostics.Items)
	if len(displayItems) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("items"))
		b.WriteString("\n")
		for i, item := range displayItems {
			if i >= 15 {
				fmt.Fprintf(&b, "  %s\n", styleMuted(fmt.Sprintf("... %d more visible items", len(displayItems)-i)))
				break
			}
			writeConfigDiagnosticItem(&b, item)
		}
	}
	if hiddenOptional > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleMuted(fmt.Sprintf("%d optional extension-directory info items hidden; use --json for the full envelope", hiddenOptional)))
	}
	b.WriteString("\n")
	b.WriteString(styleMuted("json: /config-diagnostics --json\n"))
	return b.String()
}

func visibleConfigDiagnosticItems(items []apipkg.ConfigDiagnosticItem) ([]apipkg.ConfigDiagnosticItem, int) {
	visible := make([]apipkg.ConfigDiagnosticItem, 0, len(items))
	hiddenOptional := 0
	for _, item := range items {
		if item.Optional && item.Actionable != nil && !*item.Actionable {
			hiddenOptional++
			continue
		}
		visible = append(visible, item)
	}
	sort.SliceStable(visible, func(i, j int) bool {
		return configDiagnosticSeverityRank(visible[i].Severity) < configDiagnosticSeverityRank(visible[j].Severity)
	})
	return visible, hiddenOptional
}

func configDiagnosticSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

func writeConfigDiagnosticItem(b *strings.Builder, item apipkg.ConfigDiagnosticItem) {
	if b == nil {
		return
	}
	severity := fallbackDisplayText(item.Severity, "info")
	statusKind := "ready"
	switch severity {
	case "error":
		statusKind = "failed"
	case "warning":
		statusKind = "approval"
	}
	fmt.Fprintf(b, "  %s %s", styleStatus(severity, statusKind), fallbackDisplayText(item.Code, "diagnostic"))
	if strings.TrimSpace(item.TargetKind) != "" || strings.TrimSpace(item.TargetName) != "" {
		fmt.Fprintf(b, "  %s=%s/%s", styleLabel("target"), fallbackDisplayText(item.TargetKind, "-"), fallbackDisplayText(item.TargetName, "-"))
	}
	if strings.TrimSpace(item.Field) != "" {
		fmt.Fprintf(b, "  %s=%s", styleLabel("field"), item.Field)
	}
	if item.Optional {
		fmt.Fprintf(b, "  %s", styleMuted("optional"))
	}
	if item.Actionable != nil && !*item.Actionable {
		fmt.Fprintf(b, "  %s", styleMuted("not-actionable"))
	}
	b.WriteString("\n")
	if strings.TrimSpace(item.Message) != "" {
		fmt.Fprintf(b, "    %s\n", item.Message)
	}
	if strings.TrimSpace(item.Recommendation) != "" {
		fmt.Fprintf(b, "    %s %s\n", styleLabel("fix"), item.Recommendation)
	}
	if strings.TrimSpace(item.Path) != "" {
		fmt.Fprintf(b, "    %s %s\n", styleLabel("path"), item.Path)
	}
}

func formatWorkflowSchemasOutput(schemas []session.WorkflowSchemaSnapshot) string {
	var b strings.Builder
	b.WriteString(styleHeader("Workflow Schemas"))
	b.WriteString("\n")
	if len(schemas) == 0 {
		b.WriteString(styleMuted("  no observed workflow schemas yet\n"))
		b.WriteString(styleMuted("  run a workflow with structured outputs, then use /workflow-schemas --rebuild if needed\n"))
		return b.String()
	}
	rows := make([]workflowSchemaDisplayRow, 0, len(schemas))
	totalStages := 0
	totalOutputs := 0
	for _, schema := range schemas {
		row := workflowSchemaDisplayRow{
			Workflow: schema.Workflow,
			Updated:  schema.UpdatedAt,
			Runs:     len(schema.RunIDs),
			Stages:   len(schema.Stages),
			Outputs:  countWorkflowSchemaOutputs(schema),
		}
		totalStages += row.Stages
		totalOutputs += row.Outputs
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Workflow) < strings.ToLower(rows[j].Workflow)
	})
	fmt.Fprintf(&b, "%s total=%d  stages=%d  outputs=%d\n", styleMuted("summary"), len(rows), totalStages, totalOutputs)
	for _, row := range rows {
		fmt.Fprintf(&b, "\n%s %s %s %s\n", styleStatus(row.Workflow, "ready"), styleMuted(fmt.Sprintf("stages=%d", row.Stages)), styleMuted(fmt.Sprintf("outputs=%d", row.Outputs)), styleMuted(fmt.Sprintf("runs=%d", row.Runs)))
		if strings.TrimSpace(row.Updated) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("updated"), row.Updated)
		}
	}
	b.WriteString("\n")
	b.WriteString(styleMuted("details: /workflow-schemas <workflow>; refresh from retained runs: /workflow-schemas --rebuild\n"))
	return b.String()
}

func formatWorkflowSchemaDetailOutput(schema session.WorkflowSchemaSnapshot) string {
	var b strings.Builder
	b.WriteString(styleHeader("Workflow Schema"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s %s\n", styleLabel("workflow"), schema.Workflow)
	fmt.Fprintf(&b, "%s stages=%d  outputs=%d  runs=%d\n", styleMuted("summary"), len(schema.Stages), countWorkflowSchemaOutputs(schema), len(schema.RunIDs))
	if strings.TrimSpace(schema.UpdatedAt) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("updated"), schema.UpdatedAt)
	}
	if len(schema.RunIDs) > 0 {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("runs"), strings.Join(schema.RunIDs, ", "))
	}
	stageNames := make([]string, 0, len(schema.Stages))
	for name := range schema.Stages {
		stageNames = append(stageNames, name)
	}
	sort.Strings(stageNames)
	for _, name := range stageNames {
		stage := schema.Stages[name]
		fmt.Fprintf(&b, "\n%s %s", styleStatus(fallbackDisplayText(stage.Stage, name), "ready"), styleMuted(fallbackDisplayText(stage.NodeType, "stage")))
		if strings.TrimSpace(stage.AgentID) != "" {
			fmt.Fprintf(&b, "  %s=%s", styleLabel("agent"), stage.AgentID)
		}
		if strings.TrimSpace(stage.Skill) != "" {
			fmt.Fprintf(&b, "  %s=%s", styleLabel("skill"), stage.Skill)
		}
		if strings.TrimSpace(stage.Tool) != "" {
			fmt.Fprintf(&b, "  %s=%s", styleLabel("tool"), stage.Tool)
		}
		b.WriteString("\n")
		outputNames := make([]string, 0, len(stage.Outputs))
		for output := range stage.Outputs {
			outputNames = append(outputNames, output)
		}
		sort.Strings(outputNames)
		if len(outputNames) == 0 {
			b.WriteString(styleMuted("  no typed outputs recorded\n"))
			continue
		}
		for _, output := range outputNames {
			writeWorkflowSchemaValue(&b, "  ", output, stage.Outputs[output])
		}
	}
	return b.String()
}

func countWorkflowSchemaOutputs(schema session.WorkflowSchemaSnapshot) int {
	total := 0
	for _, stage := range schema.Stages {
		total += len(stage.Outputs)
	}
	return total
}

func writeWorkflowSchemaValue(b *strings.Builder, indent, name string, value session.WorkflowValueSchemaSnapshot) {
	if b == nil {
		return
	}
	fmt.Fprintf(b, "%s%s  %s=%s", indent, name, styleLabel("type"), fallbackDisplayText(value.Type, "unknown"))
	if value.Observed > 0 {
		fmt.Fprintf(b, "  %s=%d", styleLabel("observed"), value.Observed)
	}
	if strings.TrimSpace(value.LastRunID) != "" {
		fmt.Fprintf(b, "  %s=%s", styleLabel("last_run"), value.LastRunID)
	}
	b.WriteString("\n")
	fieldNames := make([]string, 0, len(value.Fields))
	for field := range value.Fields {
		fieldNames = append(fieldNames, field)
	}
	sort.Strings(fieldNames)
	for i, field := range fieldNames {
		if i >= 8 {
			fmt.Fprintf(b, "%s  %s\n", indent, styleMuted("..."))
			break
		}
		writeWorkflowSchemaValue(b, indent+"  ", field, value.Fields[field])
	}
	if value.Items != nil {
		writeWorkflowSchemaValue(b, indent+"  ", "items", *value.Items)
	}
}

func formatKitsOutput(rows []kitDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Vertical Kits"))
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(styleMuted("  none configured\n"))
		b.WriteString(styleMuted("  create one with /new-kit <preset> <name>\n"))
		return b.String()
	}
	categories := make(map[string]int)
	warnings := 0
	for _, row := range rows {
		categories[fallbackDisplayText(row.Category, "uncategorized")]++
		warnings += len(row.Warnings)
	}
	fmt.Fprintf(&b, "%s total=%d  categories=%s", styleMuted("summary"), len(rows), formatCountMap(categories))
	if warnings > 0 {
		fmt.Fprintf(&b, "  %s", styleStatus(fmt.Sprintf("warnings=%d", warnings), "approval"))
	}
	b.WriteString("\n")
	for _, row := range rows {
		statusKind := "ready"
		if len(row.Warnings) > 0 {
			statusKind = "approval"
		}
		totalRefs := row.Providers + row.Agents + row.Skills + row.Tools + row.Workflows
		fmt.Fprintf(&b, "\n%s %s %s\n", styleStatus(row.Name, statusKind), styleMuted(fallbackDisplayText(row.Category, "-")), styleMuted(fmt.Sprintf("refs=%d", totalRefs)))
		if strings.TrimSpace(row.Title) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("title"), row.Title)
		}
		if strings.TrimSpace(row.Description) != "" {
			fmt.Fprintf(&b, "  %s\n", row.Description)
		}
		if len(row.Tags) > 0 {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("tags"), strings.Join(row.Tags, ", "))
		}
		fmt.Fprintf(&b, "  %s providers=%d agents=%d skills=%d tools=%d workflows=%d\n", styleLabel("contains"), row.Providers, row.Agents, row.Skills, row.Tools, row.Workflows)
		if strings.TrimSpace(row.Path) != "" {
			fmt.Fprintf(&b, "  %s %s\n", styleLabel("path"), row.Path)
		}
		for _, warning := range row.Warnings {
			fmt.Fprintf(&b, "  %s %s\n", styleStatus("warning", "approval"), warning)
		}
	}
	b.WriteString("\n")
	b.WriteString(styleMuted("custom kits load from kits/<name>/kit.yaml; create one with /new-kit <preset> <name>\n"))
	return b.String()
}

func formatKitDetailOutput(doc cliKitDocument) string {
	var b strings.Builder
	b.WriteString(styleHeader("Vertical Kit"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s %s\n", styleLabel("name"), doc.Name)
	fmt.Fprintf(&b, "%s %s  %s %s  %s %d\n",
		styleLabel("category"), fallbackDisplayText(doc.Category, "-"),
		styleLabel("kind"), fallbackDisplayText(doc.Kind, "goflow.kit"),
		styleLabel("version"), doc.Version)
	if strings.TrimSpace(doc.Title) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("title"), doc.Title)
	}
	if strings.TrimSpace(doc.Description) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("description"), doc.Description)
	}
	if strings.TrimSpace(doc.Path) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("path"), doc.Path)
	}
	if len(doc.Tags) > 0 {
		fmt.Fprintf(&b, "%s %s\n", styleLabel("tags"), strings.Join(doc.Tags, ", "))
	}
	writeKitReferenceSection(&b, "providers", doc.Providers)
	writeKitReferenceSection(&b, "agents", doc.Agents)
	writeKitReferenceSection(&b, "skills", doc.Skills)
	writeKitReferenceSection(&b, "tools", doc.Tools)
	writeKitReferenceSection(&b, "workflows", doc.Workflows)
	writeKitReferenceSection(&b, "workflow_templates", doc.WorkflowTemplates)
	writeKitReferenceSection(&b, "team_templates", doc.TeamTemplates)
	writeKitReferenceSection(&b, "policy_rules", doc.PolicyRules)
	writeKitReferenceSection(&b, "required_env", doc.RequiredEnv)
	if len(doc.Examples) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("examples"))
		b.WriteString("\n")
		for _, example := range doc.Examples {
			fmt.Fprintf(&b, "  %s", fallbackDisplayText(example.Title, "Example"))
			if strings.TrimSpace(example.Agent) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("agent"), example.Agent)
			}
			if strings.TrimSpace(example.Workflow) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("workflow"), example.Workflow)
			}
			b.WriteString("\n")
			if strings.TrimSpace(example.Description) != "" {
				fmt.Fprintf(&b, "    %s\n", example.Description)
			}
			if strings.TrimSpace(example.Request) != "" {
				fmt.Fprintf(&b, "    %s %s\n", styleLabel("request"), example.Request)
			}
		}
	}
	if len(doc.Metadata) > 0 {
		keys := make([]string, 0, len(doc.Metadata))
		for key := range doc.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteString("\n")
		b.WriteString(styleLabel("metadata"))
		b.WriteString("\n")
		for _, key := range keys {
			fmt.Fprintf(&b, "  %s=%s\n", key, doc.Metadata[key])
		}
	}
	if len(doc.Warnings) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("warnings"))
		b.WriteString("\n")
		for _, warning := range doc.Warnings {
			fmt.Fprintf(&b, "  %s %s\n", styleStatus("warning", "approval"), warning)
		}
	}
	return b.String()
}

func formatKitBundleImportSummaryOutput(summary apipkg.KitBundleImportSummary, overwrite bool) string {
	var b strings.Builder
	b.WriteString(styleHeader("Kit Bundle Import"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s %s  %s %s\n",
		styleLabel("kit"),
		fallbackDisplayText(summary.KitName, "-"),
		styleLabel("mode"),
		map[bool]string{true: "replace", false: "keep-existing"}[overwrite])
	fmt.Fprintf(&b, "%s saved=%d skipped=%d errors=%d warnings=%d\n",
		styleMuted("summary"),
		len(summary.Saved),
		len(summary.Skipped),
		len(summary.Errors),
		len(summary.Warnings)+len(summary.ValidationIssues))
	if summary.RestartRequired {
		fmt.Fprintf(&b, "%s %s\n", styleStatus("restart_required", "approval"), "provider, agent, or tool modules were imported")
	}
	writeKitBundleStatusRows(&b, "saved", summary.Saved, "ready")
	writeKitBundleStatusRows(&b, "skipped", summary.Skipped, "approval")
	writeKitBundleStatusRows(&b, "errors", summary.Errors, "failed")
	if len(summary.Warnings) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("warnings"))
		b.WriteString("\n")
		for _, warning := range summary.Warnings {
			fmt.Fprintf(&b, "  %s %s\n", styleStatus("warning", "approval"), warning)
		}
	}
	if len(summary.ValidationIssues) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("validation"))
		b.WriteString("\n")
		for _, issue := range summary.ValidationIssues {
			statusKind := "ready"
			if issue.Severity == "error" {
				statusKind = "failed"
			} else if issue.Severity == "warning" {
				statusKind = "approval"
			}
			fmt.Fprintf(&b, "  %s %s", styleStatus(fallbackDisplayText(issue.Severity, "info"), statusKind), fallbackDisplayText(issue.Code, "issue"))
			if strings.TrimSpace(issue.Ref) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("ref"), issue.Ref)
			}
			if strings.TrimSpace(issue.Field) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("field"), issue.Field)
			}
			fmt.Fprintf(&b, "\n    %s\n", issue.Message)
		}
	}
	return b.String()
}

func writeKitBundleStatusRows(b *strings.Builder, label string, rows []apipkg.KitBundleResourceStatus, statusKind string) {
	if b == nil || len(rows) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(styleLabel(label))
	b.WriteString("\n")
	for _, row := range rows {
		fmt.Fprintf(b, "  %s %s/%s  %s", styleStatus(row.Status, statusKind), fallbackDisplayText(row.Kind, "-"), fallbackDisplayText(row.Name, "-"), fallbackDisplayText(row.Message, "ok"))
		b.WriteString("\n")
	}
}

func writeKitReferenceSection(b *strings.Builder, label string, values []string) {
	if b == nil || len(values) == 0 {
		return
	}
	items := append([]string(nil), values...)
	sort.Strings(items)
	fmt.Fprintf(b, "%s %s\n", styleLabel(label), strings.Join(items, ", "))
}

func formatTeamStateOutput(state agent.TeamState) string {
	var b strings.Builder
	b.WriteString(styleHeader("Team State"))
	b.WriteString("\n")
	if strings.TrimSpace(state.RunID) == "" && strings.TrimSpace(state.Team) == "" && len(state.Messages) == 0 && len(state.Blackboard) == 0 {
		b.WriteString(styleMuted("  no team state recorded yet\n"))
		return b.String()
	}
	fmt.Fprintf(&b, "%s %s  %s %s  %s %s\n", styleLabel("run"), fallbackDisplayText(state.RunID, "-"), styleLabel("workflow"), fallbackDisplayText(state.Workflow, "-"), styleLabel("status"), fallbackDisplayText(state.Status, "-"))
	fmt.Fprintf(&b, "%s %s  %s %s  %s %s\n", styleLabel("team"), fallbackDisplayText(state.Team, "-"), styleLabel("owner"), fallbackDisplayText(state.ActiveOwner, "-"), styleLabel("next"), fallbackDisplayText(state.NextStage, "-"))
	if state.Template != nil {
		fmt.Fprintf(&b, "%s %s  %s %s\n", styleLabel("template"), fallbackDisplayText(state.Template.Title, state.Template.Name), styleLabel("recommended"), fallbackDisplayText(state.Template.RecommendedWorkflow, "-"))
	}
	fmt.Fprintf(&b, "%s messages=%d  handoffs=%d  blackboard=%d  unresolved=%d\n", styleMuted("summary"), len(state.Messages), len(state.Handoffs), len(state.Blackboard), len(state.UnresolvedItems))
	if len(state.Handoffs) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("handoffs"))
		b.WriteString("\n")
		for i, message := range state.Handoffs {
			if i >= 6 {
				fmt.Fprintf(&b, "  %s\n", styleMuted("..."))
				break
			}
			fmt.Fprintf(&b, "  %s -> %s  %s\n", fallbackDisplayText(message.FromAgent, "-"), fallbackDisplayText(message.ToAgent, "-"), fallbackDisplayText(message.Subject, message.Kind))
		}
	}
	if len(state.UnresolvedItems) > 0 {
		b.WriteString("\n")
		b.WriteString(styleLabel("unresolved"))
		b.WriteString("\n")
		for i, item := range state.UnresolvedItems {
			if i >= 8 {
				fmt.Fprintf(&b, "  %s\n", styleMuted("..."))
				break
			}
			fmt.Fprintf(&b, "  %s  %s=%s  %s\n", fallbackDisplayText(item.Kind, "item"), styleLabel("status"), fallbackDisplayText(item.Status, "-"), fallbackDisplayText(item.Title, item.ID))
		}
	}
	if len(state.PendingApprovals) > 0 || state.PendingInput {
		b.WriteString("\n")
		b.WriteString(styleLabel("pending"))
		b.WriteString("\n")
		if state.PendingInput {
			fmt.Fprintf(&b, "  %s\n", styleStatus("input required", "approval"))
		}
		for _, approval := range state.PendingApprovals {
			fmt.Fprintf(&b, "  %s %s  %s=%s\n", styleStatus("approval", "approval"), fallbackDisplayText(approval.CallID, "-"), styleLabel("tool"), fallbackDisplayText(approval.ToolName, "-"))
		}
	}
	return b.String()
}

func formatAgentSummary(rows []agentDisplayRow) string {
	policies := make(map[string]int)
	active := "-"
	for _, row := range rows {
		policies[fallbackDisplayText(row.ToolPolicy, "unknown")]++
		if row.Active {
			active = row.Name
		}
	}
	return fmt.Sprintf("total=%d  active=%s  policies=%s", len(rows), active, formatCountMap(policies))
}

func orderedAgentModes(rows []agentDisplayRow) []string {
	seen := make(map[string]struct{})
	modes := make([]string, 0)
	for _, row := range rows {
		mode := fallbackDisplayText(row.Mode, "unknown")
		if _, ok := seen[mode]; ok {
			continue
		}
		seen[mode] = struct{}{}
		modes = append(modes, mode)
	}
	sort.SliceStable(modes, func(i, j int) bool {
		return agentModeRank(modes[i]) < agentModeRank(modes[j]) || (agentModeRank(modes[i]) == agentModeRank(modes[j]) && modes[i] < modes[j])
	})
	return modes
}

func agentModeRank(mode string) int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "chat":
		return 0
	case "plan":
		return 1
	case "fix":
		return 2
	case "audit":
		return 3
	default:
		return 10
	}
}

func filterAgentsByMode(rows []agentDisplayRow, mode string) []agentDisplayRow {
	group := make([]agentDisplayRow, 0)
	for _, row := range rows {
		if fallbackDisplayText(row.Mode, "unknown") == mode {
			group = append(group, row)
		}
	}
	return group
}

func policyStatusKind(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "allow":
		return "ready"
	case "confirm":
		return "approval"
	case "deny":
		return "denied"
	default:
		return ""
	}
}

func formatSkillsOutput(rows []skillDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Skills"))
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(styleMuted("  none loaded\n"))
		return b.String()
	}
	fmt.Fprintf(&b, "%s %s\n", styleMuted("summary"), formatSkillSummary(rows))
	for _, mode := range orderedSkillModes(rows) {
		group := filterSkillsByMode(rows, mode)
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s %s %s\n", styleLabel("mode"), styleStatus(mode, stageStatusKind(mode)), styleMuted(fmt.Sprintf("(%d)", len(group))))
		for _, row := range group {
			fmt.Fprintf(&b, "  %s", styleStatus(row.Name, "ready"))
			if strings.TrimSpace(row.PreferredAgent) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("agent"), row.PreferredAgent)
			}
			if strings.TrimSpace(row.OutputKind) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("output"), row.OutputKind)
			}
			b.WriteString("\n")
			if strings.TrimSpace(row.Description) != "" {
				fmt.Fprintf(&b, "    %s\n", row.Description)
			}
			if len(row.Keywords) > 0 {
				keywords := append([]string(nil), row.Keywords...)
				sort.Strings(keywords)
				fmt.Fprintf(&b, "    %s %s\n", styleLabel("activates"), strings.Join(limitStrings(keywords, 6), ", "))
			}
			if len(row.NextSkills) > 0 {
				nextSkills := append([]string(nil), row.NextSkills...)
				sort.Strings(nextSkills)
				fmt.Fprintf(&b, "    %s %s\n", styleLabel("next"), strings.Join(nextSkills, ", "))
			}
		}
	}
	return b.String()
}

func formatSkillSummary(rows []skillDisplayRow) string {
	modes := make(map[string]int)
	agents := make(map[string]int)
	for _, row := range rows {
		modes[fallbackDisplayText(row.Mode, "unknown")]++
		if strings.TrimSpace(row.PreferredAgent) != "" {
			agents[row.PreferredAgent]++
		}
	}
	parts := []string{fmt.Sprintf("total=%d", len(rows)), "modes=" + formatCountMap(modes)}
	if len(agents) > 0 {
		parts = append(parts, "agents="+formatCountMap(agents))
	}
	return strings.Join(parts, "  ")
}

func orderedSkillModes(rows []skillDisplayRow) []string {
	seen := make(map[string]struct{})
	modes := make([]string, 0)
	for _, row := range rows {
		mode := fallbackDisplayText(row.Mode, "unknown")
		if _, ok := seen[mode]; ok {
			continue
		}
		seen[mode] = struct{}{}
		modes = append(modes, mode)
	}
	sort.SliceStable(modes, func(i, j int) bool {
		return agentModeRank(modes[i]) < agentModeRank(modes[j]) || (agentModeRank(modes[i]) == agentModeRank(modes[j]) && modes[i] < modes[j])
	})
	return modes
}

func filterSkillsByMode(rows []skillDisplayRow, mode string) []skillDisplayRow {
	group := make([]skillDisplayRow, 0)
	for _, row := range rows {
		if fallbackDisplayText(row.Mode, "unknown") == mode {
			group = append(group, row)
		}
	}
	return group
}

func formatToolsOutput(rows []toolDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Tools"))
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(styleMuted("  none discovered\n"))
		return b.String()
	}
	fmt.Fprintf(&b, "%s %s\n", styleMuted("summary"), formatToolSummary(rows))
	for _, server := range orderedToolServers(rows) {
		group := filterToolsByServer(rows, server)
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s %s %s %s\n", styleLabel("server"), styleStatus(server, serverHealthStatusKind(group)), styleMuted(fmt.Sprintf("(%d tools)", len(group))), styleMuted(formatToolServerSummary(group)))
		for _, row := range group {
			warningCount := nonEmptyCount(row.Diagnostics)
			fmt.Fprintf(&b, "  %s", styleStatus(row.Name, toolHealthStatusKind(row.Health)))
			if strings.TrimSpace(row.Kind) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("kind"), row.Kind)
			}
			if strings.TrimSpace(row.Health) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("health"), styleStatus(row.Health, toolHealthStatusKind(row.Health)))
			}
			if strings.TrimSpace(row.RiskLevel) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("risk"), styleStatus(row.RiskLevel, toolRiskStatusKind(row.RiskLevel)))
			}
			if strings.TrimSpace(row.IsolationLevel) != "" {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("isolation"), row.IsolationLevel)
			}
			if row.Sandboxed {
				fmt.Fprintf(&b, "  %s=true", styleLabel("sandboxed"))
			} else if row.RequiresSandbox {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("sandbox"), styleStatus("recommended", "approval"))
			}
			if row.EnvAllowlistSet {
				fmt.Fprintf(&b, "  %s=%d", styleLabel("env"), len(row.EnvAllowlist))
			}
			if len(row.SensitiveEnv) > 0 {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("sensitive_env"), styleStatus(strings.Join(row.SensitiveEnv, ","), "approval"))
			}
			if warningCount > 0 {
				fmt.Fprintf(&b, "  %s=%s", styleLabel("warnings"), styleStatus(strconv.Itoa(warningCount), "approval"))
			}
			b.WriteString("\n")
			if strings.TrimSpace(row.Description) != "" {
				fmt.Fprintf(&b, "    %s\n", row.Description)
			}
			if strings.TrimSpace(row.InputSchemaSummary) != "" {
				fmt.Fprintf(&b, "    %s %s\n", styleLabel("schema"), row.InputSchemaSummary)
			}
			if row.EnvAllowlistSet {
				fmt.Fprintf(&b, "    %s %s\n", styleLabel("env_allowlist"), strings.Join(row.EnvAllowlist, ", "))
			}
			if len(row.SandboxFeatures) > 0 {
				fmt.Fprintf(&b, "    %s %s\n", styleLabel("sandbox_features"), strings.Join(row.SandboxFeatures, ", "))
			}
			if len(row.MissingSandboxFeatures) > 0 {
				fmt.Fprintf(&b, "    %s %s\n", styleLabel("missing_sandbox"), strings.Join(row.MissingSandboxFeatures, ", "))
			}
			if row.WindowsIsolation != nil && row.WindowsIsolation.JobObject && row.WindowsIsolation.LifecycleOnly && !row.WindowsIsolation.RestrictedToken && !row.WindowsIsolation.AppContainer {
				fmt.Fprintf(&b, "    %s windows_job uses Job Object lifecycle cleanup only; restricted token and AppContainer are not enabled\n", styleStatus("warning", "approval"))
			}
			if len(row.SensitiveEnv) > 0 {
				fmt.Fprintf(&b, "    %s env_allowlist includes sensitive variable names: %s\n", styleStatus("warning", "approval"), strings.Join(row.SensitiveEnv, ", "))
			}
			for _, diagnostic := range row.Diagnostics {
				if strings.TrimSpace(diagnostic) == "" {
					continue
				}
				fmt.Fprintf(&b, "    %s %s\n", styleStatus("warning", "approval"), diagnostic)
			}
		}
	}
	return b.String()
}

func formatToolSummary(rows []toolDisplayRow) string {
	servers := make(map[string]int)
	kinds := make(map[string]int)
	risks := make(map[string]int)
	warnings := 0
	for _, row := range rows {
		servers[fallbackDisplayText(row.Server, "unknown")]++
		kinds[fallbackDisplayText(row.Kind, "unknown")]++
		if strings.TrimSpace(row.RiskLevel) != "" {
			risks[row.RiskLevel]++
		}
		warnings += nonEmptyCount(row.Diagnostics)
	}
	parts := []string{fmt.Sprintf("total=%d", len(rows)), fmt.Sprintf("servers=%d", len(servers)), "kinds=" + formatCountMap(kinds)}
	if len(risks) > 0 {
		parts = append(parts, "risk="+formatCountMap(risks))
	}
	if warnings > 0 {
		parts = append(parts, styleStatus(fmt.Sprintf("warnings=%d", warnings), "approval"))
	}
	return strings.Join(parts, "  ")
}

func orderedToolServers(rows []toolDisplayRow) []string {
	seen := make(map[string]struct{})
	servers := make([]string, 0)
	for _, row := range rows {
		server := fallbackDisplayText(row.Server, "unknown")
		if _, ok := seen[server]; ok {
			continue
		}
		seen[server] = struct{}{}
		servers = append(servers, server)
	}
	sort.Strings(servers)
	return servers
}

func filterToolsByServer(rows []toolDisplayRow, server string) []toolDisplayRow {
	group := make([]toolDisplayRow, 0)
	for _, row := range rows {
		if fallbackDisplayText(row.Server, "unknown") == server {
			group = append(group, row)
		}
	}
	sort.SliceStable(group, func(i, j int) bool {
		return group[i].Name < group[j].Name
	})
	return group
}

func formatToolServerSummary(rows []toolDisplayRow) string {
	health := make(map[string]int)
	risks := make(map[string]int)
	warnings := 0
	for _, row := range rows {
		health[fallbackDisplayText(row.Health, "unknown")]++
		if strings.TrimSpace(row.RiskLevel) != "" {
			risks[row.RiskLevel]++
		}
		warnings += nonEmptyCount(row.Diagnostics)
	}
	summary := "health=" + formatCountMap(health)
	if len(risks) > 0 {
		summary += "  risk=" + formatCountMap(risks)
	}
	if warnings > 0 {
		summary += "  warnings=" + strconv.Itoa(warnings)
	}
	return summary
}

func serverHealthStatusKind(rows []toolDisplayRow) string {
	for _, row := range rows {
		if toolHealthStatusKind(row.Health) == "failed" {
			return "failed"
		}
	}
	for _, row := range rows {
		if toolHealthStatusKind(row.Health) == "approval" || nonEmptyCount(row.Diagnostics) > 0 {
			return "approval"
		}
	}
	return "ready"
}

func toolRiskStatusKind(risk string) string {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "high":
		return "failed"
	case "medium":
		return "approval"
	default:
		return "ready"
	}
}

func nonEmptyCount(values []string) int {
	count := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			count++
		}
	}
	return count
}

func formatCountMap(counts map[string]int) string {
	if len(counts) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ",")
}

func toolHealthStatusKind(health string) string {
	lower := strings.ToLower(health)
	switch {
	case strings.Contains(lower, "ready"):
		return "ready"
	case strings.Contains(lower, "cooldown"), strings.Contains(lower, "pending"):
		return "approval"
	case strings.Contains(lower, "error"), strings.Contains(lower, "failed"), strings.Contains(lower, "stopped"):
		return "failed"
	default:
		return ""
	}
}

func fallbackDisplayText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func limitStrings(values []string, max int) []string {
	if max <= 0 || len(values) <= max {
		return values
	}
	limited := append([]string(nil), values[:max]...)
	limited = append(limited, fmt.Sprintf("+%d more", len(values)-max))
	return limited
}

func formatSessionOutput(activeAgent, mode, lastSkill string, prompts, tools []string, workflow workflowDisplayRow, handoff handoffDisplayRow, routing routingDisplayRow, approvals []approvalDisplayRow, taskStages ...taskStageDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Session"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "- %s: %s\n", styleLabel("active_agent"), activeAgent)
	fmt.Fprintf(&b, "- %s: %s\n", styleLabel("mode"), mode)
	fmt.Fprintf(&b, "- %s: %s\n", styleLabel("last_skill"), lastSkill)
	if len(taskStages) > 0 && strings.TrimSpace(taskStages[0].Stage) != "" {
		stage := taskStages[0]
		b.WriteString(styleHeader("Task stage"))
		b.WriteString("\n")
		fmt.Fprintf(&b, "- %s=%s, %s=%s, %s=%s\n", styleLabel("stage"), styleStatus(stage.Stage, stageStatusKind(stage.Stage)), styleLabel("agent"), fallbackDisplayText(stage.AgentID, "-"), styleLabel("mode"), fallbackDisplayText(stage.Mode, "-"))
		if strings.TrimSpace(stage.Detail) != "" {
			fmt.Fprintf(&b, "  %s\n", stage.Detail)
		}
	}
	if strings.TrimSpace(workflow.Name) != "" || strings.TrimSpace(workflow.Status) != "" {
		b.WriteString(styleHeader("Workflow"))
		b.WriteString("\n")
		fmt.Fprintf(&b, "- %s (%s=%s", workflow.Name, styleLabel("status"), workflow.Status)
		if strings.TrimSpace(workflow.NextStage) != "" {
			fmt.Fprintf(&b, ", %s=%s", styleLabel("next"), workflow.NextStage)
		}
		b.WriteString(")\n")
		if strings.TrimSpace(workflow.Summary) != "" {
			fmt.Fprintf(&b, "  %s\n", workflow.Summary)
		}
	}
	if strings.TrimSpace(handoff.TargetAgent) != "" || strings.TrimSpace(handoff.ExpectedAction) != "" {
		b.WriteString(styleHeader("Pending handoff"))
		b.WriteString("\n")
		fmt.Fprintf(&b, "- %s=%s, %s=%s, %s=%s\n", styleLabel("target"), handoff.TargetAgent, styleLabel("mode"), handoff.TargetMode, styleLabel("action"), handoff.ExpectedAction)
		if strings.TrimSpace(handoff.Request) != "" {
			fmt.Fprintf(&b, "  request: %s\n", handoff.Request)
		}
		if strings.TrimSpace(handoff.PlanSummary) != "" {
			fmt.Fprintf(&b, "  plan: %s\n", handoff.PlanSummary)
		}
	}
	if strings.TrimSpace(routing.Outcome) != "" || strings.TrimSpace(routing.TargetAgent) != "" || strings.TrimSpace(routing.SourceAgent) != "" {
		b.WriteString(styleHeader("Last routing"))
		b.WriteString("\n")
		fmt.Fprintf(&b, "- %s=%s, %s=%s, %s=%s, %s=%s\n", styleLabel("outcome"), routing.Outcome, styleLabel("from"), routing.SourceAgent, styleLabel("target"), routing.TargetAgent, styleLabel("mode"), routing.TargetMode)
		if strings.TrimSpace(routing.Request) != "" {
			fmt.Fprintf(&b, "  request: %s\n", routing.Request)
		}
		if strings.TrimSpace(routing.Reason) != "" {
			fmt.Fprintf(&b, "  reason: %s\n", routing.Reason)
		}
	}
	if len(approvals) > 0 {
		b.WriteString(styleHeader("Pending approvals"))
		b.WriteString("\n")
		for _, approval := range approvals {
			fmt.Fprintf(&b, "- %s %s (%s=%s, %s=%s)\n", approval.CallID, approval.ToolName, styleLabel("agent"), approval.AgentID, styleLabel("stage"), approval.Stage)
			if strings.TrimSpace(approval.ArgumentsSummary) != "" {
				fmt.Fprintf(&b, "  %s\n", approval.ArgumentsSummary)
			}
		}
	}
	b.WriteString(styleHeader("Recent prompts"))
	b.WriteString("\n")
	for _, prompt := range prompts {
		b.WriteString("- ")
		b.WriteString(prompt)
		b.WriteString("\n")
	}
	b.WriteString(styleHeader("Recent tools"))
	b.WriteString("\n")
	for _, tool := range tools {
		styledTool := tool
		lower := strings.ToLower(tool)
		switch {
		case strings.Contains(lower, "suspended"):
			styledTool = styleStatus(tool, "suspended")
		case strings.Contains(lower, "failed"), strings.Contains(lower, "error"), strings.Contains(lower, "denied"):
			styledTool = styleStatus(tool, "failed")
		case strings.Contains(lower, "done"), strings.Contains(lower, "ready"):
			styledTool = styleStatus(tool, "done")
		}
		b.WriteString("- ")
		b.WriteString(styledTool)
		b.WriteString("\n")
	}
	return b.String()
}

type cliCostTrend struct {
	Key                          string
	PromptBudgetSamples          int
	TokenUsageSamples            int
	AverageEstimatedPromptTokens int
	MaxEstimatedPromptTokens     int
	AverageNonCacheableTokens    int
	HistoryEstimatedSavedTokens  int
	HistoryDeduplicatedItems     int
	HistoryCompactedOlderItems   int
	MemoryBlockSamples           int
	MemoryEstimatedSavedTokens   int
	ArtifactRefSamples           int
	ArtifactOmittedTokens        int
	SkillOmittedTokens           int
	TotalPromptTokens            int
	TotalOutputTokens            int
	TotalCachedTokens            int
	TotalTokens                  int
}

func formatCostOutput(snapshot session.Snapshot) string {
	budgets := append([]schema.PromptBudget(nil), snapshot.PromptBudgets...)
	latest := snapshot.PromptBudget
	if latest == nil && len(budgets) > 0 {
		copied := budgets[len(budgets)-1]
		latest = &copied
	}
	if latest != nil && len(budgets) == 0 {
		budgets = append(budgets, *latest)
	}
	usages := append([]schema.TokenUsageSample(nil), snapshot.TokenUsages...)

	var b strings.Builder
	b.WriteString(styleHeader("Cost Diagnostics"))
	b.WriteString("\n")
	if len(budgets) == 0 && len(usages) == 0 {
		b.WriteString(styleMuted("  no prompt budget or token usage samples recorded yet\n"))
		b.WriteString(styleMuted("  run a task with a provider that reports usage, then try /cost again\n"))
		return b.String()
	}

	avgPrompt, maxPrompt, avgCacheable, avgNonCacheable, uniquePrefixes := summarizePromptBudgets(budgets)
	historySavedTokens, historyDeduped, historyCompacted := summarizePromptHistorySavings(budgets)
	promptTotal, outputTotal, cachedTotal, totalTokens := summarizeTokenUsages(usages)
	fmt.Fprintf(&b, "%s prompt_samples=%d  token_samples=%d  avg_prompt=%d  max_prompt=%d\n", styleMuted("summary"), len(budgets), len(usages), avgPrompt, maxPrompt)
	fmt.Fprintf(&b, "%s cacheable_avg=%d  non_cacheable_avg=%d  prompt_prefixes=%d\n", styleMuted("cache"), avgCacheable, avgNonCacheable, uniquePrefixes)
	if historySavedTokens > 0 || historyDeduped > 0 || historyCompacted > 0 {
		fmt.Fprintf(&b, "%s saved_est=%d  deduped=%d  compacted_old=%d\n", styleMuted("history"), historySavedTokens, historyDeduped, historyCompacted)
	}
	if len(usages) > 0 {
		fmt.Fprintf(&b, "%s input=%d  output=%d  cached=%d  total=%d\n", styleMuted("tokens"), promptTotal, outputTotal, cachedTotal, totalTokens)
	}

	if latest != nil {
		b.WriteString(styleLabel("latest"))
		b.WriteString("\n")
		fmt.Fprintf(&b, "  %s=%s  %s=%s  %s=%s", styleLabel("agent"), fallbackDisplayText(latest.AgentID, "-"), styleLabel("mode"), fallbackDisplayText(latest.Mode, "-"), styleLabel("stage"), fallbackDisplayText(latest.TaskStage, "-"))
		if strings.TrimSpace(latest.WorkflowName) != "" {
			fmt.Fprintf(&b, "  %s=%s", styleLabel("workflow"), latest.WorkflowName)
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "  %s estimated=%d  system=%d  messages=%d  tools=%d  cacheable=%d\n", styleMuted("breakdown"), latest.EstimatedPromptTokens, latest.SystemTokens, latest.MessageTokens, latest.ToolSchemaTokens, latest.CacheablePrefixTokens)
		if latest.MemoryBlockCount > 0 || latest.ArtifactRefCount > 0 || latest.SkillOmittedTokens > 0 || len(latest.OmittedContext) > 0 {
			fmt.Fprintf(&b, "  %s memory_blocks=%d  memory_saved=%d  artifacts=%d  artifact_omitted=%d  skill_mode=%s  skill_saved=%d  omitted=%d\n",
				styleMuted("context"),
				latest.MemoryBlockCount,
				latest.MemoryEstimatedSavedTokens,
				latest.ArtifactRefCount,
				latest.ArtifactOmittedTokens,
				fallbackDisplayText(latest.SkillInstructionMode, "-"),
				latest.SkillOmittedTokens,
				len(latest.OmittedContext))
		}
		if latest.HistoryEstimatedSavedTokens > 0 || latest.HistoryPromptDeduplicatedItems+latest.HistoryToolDeduplicatedItems > 0 || latest.HistoryPromptCompactedOlderItems+latest.HistoryToolCompactedOlderItems > 0 {
			fmt.Fprintf(&b, "  %s saved_est=%d  prompts=%d/%d  tools=%d/%d  deduped=%d  compacted_old=%d\n",
				styleMuted("history"),
				latest.HistoryEstimatedSavedTokens,
				latest.HistoryPromptRetainedItems,
				latest.HistoryPromptItems,
				latest.HistoryToolRetainedItems,
				latest.HistoryToolItems,
				latest.HistoryPromptDeduplicatedItems+latest.HistoryToolDeduplicatedItems,
				latest.HistoryPromptCompactedOlderItems+latest.HistoryToolCompactedOlderItems)
		}
		fmt.Fprintf(&b, "  %s exposed_tools=%d/%d  filtered=%d  tool_selection=%s  prefix=%s\n", styleMuted("visibility"), latest.ExposedToolCount, latest.TotalToolCount, latest.FilteredToolCount, fallbackDisplayText(latest.ToolSchemaSelection, "-"), fallbackDisplayText(latest.PromptPrefixHash, "-"))
	}

	trends := buildCLICostTrends(budgets, usages)
	if len(trends) > 0 {
		b.WriteString(styleLabel("top agents"))
		b.WriteString("\n")
		for _, trend := range limitCLICostTrends(trends, 5) {
			fmt.Fprintf(&b, "  %s  %s=%d  %s=%d  %s=%d  %s=%d\n",
				styleStatus(trend.Key, "ready"),
				styleLabel("avg_prompt"), trend.AverageEstimatedPromptTokens,
				styleLabel("max_prompt"), trend.MaxEstimatedPromptTokens,
				styleLabel("total_tokens"), trend.TotalTokens,
				styleLabel("samples"), trend.PromptBudgetSamples+trend.TokenUsageSamples)
		}
	}

	recommendations := buildCLICostRecommendations(budgets, trends)
	if len(recommendations) > 0 {
		b.WriteString(styleLabel("recommendations"))
		b.WriteString("\n")
		for _, recommendation := range recommendations {
			fmt.Fprintf(&b, "  %s %s\n", styleStatus(recommendation.code, recommendation.kind), recommendation.message)
		}
	}
	return b.String()
}

type cliCostRecommendation struct {
	kind    string
	code    string
	message string
}

func summarizePromptBudgets(budgets []schema.PromptBudget) (avgPrompt, maxPrompt, avgCacheable, avgNonCacheable, uniquePrefixes int) {
	if len(budgets) == 0 {
		return 0, 0, 0, 0, 0
	}
	prefixes := make(map[string]struct{})
	totalPrompt := 0
	totalCacheable := 0
	totalNonCacheable := 0
	for _, budget := range budgets {
		totalPrompt += budget.EstimatedPromptTokens
		totalCacheable += budget.CacheablePrefixTokens
		totalNonCacheable += promptBudgetNonCacheableTokens(budget)
		if budget.EstimatedPromptTokens > maxPrompt {
			maxPrompt = budget.EstimatedPromptTokens
		}
		if strings.TrimSpace(budget.PromptPrefixHash) != "" {
			prefixes[budget.PromptPrefixHash] = struct{}{}
		}
	}
	return totalPrompt / len(budgets), maxPrompt, totalCacheable / len(budgets), totalNonCacheable / len(budgets), len(prefixes)
}

func summarizePromptHistorySavings(budgets []schema.PromptBudget) (savedTokens, deduplicatedItems, compactedOlderItems int) {
	for _, budget := range budgets {
		savedTokens += budget.HistoryEstimatedSavedTokens
		deduplicatedItems += budget.HistoryPromptDeduplicatedItems + budget.HistoryToolDeduplicatedItems
		compactedOlderItems += budget.HistoryPromptCompactedOlderItems + budget.HistoryToolCompactedOlderItems
	}
	return savedTokens, deduplicatedItems, compactedOlderItems
}

func summarizeTokenUsages(usages []schema.TokenUsageSample) (prompt, output, cached, total int) {
	for _, usage := range usages {
		prompt += usage.PromptTokens
		output += usage.OutputTokens
		cached += usage.CachedTokens
		if usage.TotalTokens > 0 {
			total += usage.TotalTokens
		} else {
			total += usage.PromptTokens + usage.OutputTokens
		}
	}
	return prompt, output, cached, total
}

func buildCLICostTrends(budgets []schema.PromptBudget, usages []schema.TokenUsageSample) []cliCostTrend {
	items := make(map[string]*cliCostTrend)
	for _, budget := range budgets {
		key := strings.TrimSpace(budget.AgentID)
		if key == "" {
			key = "(unknown)"
		}
		item := ensureCLICostTrend(items, key)
		item.PromptBudgetSamples++
		item.AverageEstimatedPromptTokens += budget.EstimatedPromptTokens
		item.AverageNonCacheableTokens += promptBudgetNonCacheableTokens(budget)
		item.HistoryEstimatedSavedTokens += budget.HistoryEstimatedSavedTokens
		item.HistoryDeduplicatedItems += budget.HistoryPromptDeduplicatedItems + budget.HistoryToolDeduplicatedItems
		item.HistoryCompactedOlderItems += budget.HistoryPromptCompactedOlderItems + budget.HistoryToolCompactedOlderItems
		item.MemoryBlockSamples += budget.MemoryBlockCount
		item.MemoryEstimatedSavedTokens += budget.MemoryEstimatedSavedTokens
		item.ArtifactRefSamples += budget.ArtifactRefCount
		item.ArtifactOmittedTokens += budget.ArtifactOmittedTokens
		item.SkillOmittedTokens += budget.SkillOmittedTokens
		if budget.EstimatedPromptTokens > item.MaxEstimatedPromptTokens {
			item.MaxEstimatedPromptTokens = budget.EstimatedPromptTokens
		}
	}
	for _, usage := range usages {
		key := strings.TrimSpace(usage.AgentID)
		if key == "" {
			key = "(unknown)"
		}
		item := ensureCLICostTrend(items, key)
		item.TokenUsageSamples++
		item.TotalPromptTokens += usage.PromptTokens
		item.TotalOutputTokens += usage.OutputTokens
		item.TotalCachedTokens += usage.CachedTokens
		if usage.TotalTokens > 0 {
			item.TotalTokens += usage.TotalTokens
		} else {
			item.TotalTokens += usage.PromptTokens + usage.OutputTokens
		}
	}
	out := make([]cliCostTrend, 0, len(items))
	for _, item := range items {
		if item.PromptBudgetSamples > 0 {
			item.AverageEstimatedPromptTokens /= item.PromptBudgetSamples
			item.AverageNonCacheableTokens /= item.PromptBudgetSamples
		}
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalTokens == out[j].TotalTokens {
			if out[i].AverageEstimatedPromptTokens == out[j].AverageEstimatedPromptTokens {
				return out[i].Key < out[j].Key
			}
			return out[i].AverageEstimatedPromptTokens > out[j].AverageEstimatedPromptTokens
		}
		return out[i].TotalTokens > out[j].TotalTokens
	})
	return out
}

func ensureCLICostTrend(items map[string]*cliCostTrend, key string) *cliCostTrend {
	if item, ok := items[key]; ok {
		return item
	}
	item := &cliCostTrend{Key: key}
	items[key] = item
	return item
}

func limitCLICostTrends(items []cliCostTrend, max int) []cliCostTrend {
	if max <= 0 || len(items) <= max {
		return items
	}
	return items[:max]
}

func buildCLICostRecommendations(budgets []schema.PromptBudget, trends []cliCostTrend) []cliCostRecommendation {
	if len(budgets) == 0 {
		return nil
	}
	latest := budgets[len(budgets)-1]
	out := make([]cliCostRecommendation, 0, 4)
	if latest.MessageTokens >= 2000 {
		out = append(out, cliCostRecommendation{
			kind:    "approval",
			code:    "message_context_high",
			message: "Recent session/tool context is large; prefer artifact refs, shorter stage inputs, or a fresh focused workflow.",
		})
	}
	if latest.ToolSchemaTokens >= 1000 && latest.FilteredToolCount > 0 {
		out = append(out, cliCostRecommendation{
			kind:    "approval",
			code:    "tool_schema_high",
			message: "Visible tool schemas are costly; narrow allowed_tools or skill tool declarations.",
		})
	}
	nonCacheable := promptBudgetNonCacheableTokens(latest)
	if latest.EstimatedPromptTokens > 0 && nonCacheable >= 1000 && nonCacheable*2 >= latest.EstimatedPromptTokens {
		out = append(out, cliCostRecommendation{
			kind:    "approval",
			code:    "non_cacheable_context_high",
			message: "Most prompt tokens are outside the stable cacheable prefix; keep repeated instructions/tool visibility stable.",
		})
	}
	if latest.MemoryBlockCount == 0 && latest.EstimatedPromptTokens >= 1500 {
		out = append(out, cliCostRecommendation{
			kind:    "approval",
			code:    "memory_not_injected",
			message: "No memory blocks were injected; rebuild or update project/file memory for summary-first context.",
		})
	}
	if latest.SkillOmittedTokens >= 500 {
		out = append(out, cliCostRecommendation{
			kind:    "done",
			code:    "skill_summary_saving",
			message: "Large skill instructions were summarized before prompt injection.",
		})
	}
	_, _, _, _, uniquePrefixes := summarizePromptBudgets(budgets)
	if len(budgets) >= 4 && uniquePrefixes > len(budgets)/2 {
		out = append(out, cliCostRecommendation{
			kind:    "approval",
			code:    "prompt_prefix_churn",
			message: "Prompt prefixes are changing frequently; stable prompts and tool schemas improve provider prompt-cache behavior.",
		})
	}
	if len(trends) > 0 && trends[0].AverageEstimatedPromptTokens >= 6000 {
		out = append(out, cliCostRecommendation{
			kind:    "approval",
			code:    "agent_prompt_high",
			message: fmt.Sprintf("%s has a high average prompt size; consider a narrower profile or smaller workflow stages.", trends[0].Key),
		})
	}
	return out
}

func promptBudgetNonCacheableTokens(budget schema.PromptBudget) int {
	value := budget.EstimatedPromptTokens - budget.CacheablePrefixTokens
	if value < 0 {
		return 0
	}
	return value
}

func runBlockingApprovalPrompt(input io.Reader, output io.Writer, prompt string, approve func() error, deny func() error, approveAll func() error, remember ...func() error) error {
	interactive := terminalInputSupported(input, output)
	return runApprovalPrompt(approvalPromptInput(input, interactive), output, prompt, interactive, approve, deny, approveAll, remember...)
}

func approvalPromptInput(input io.Reader, interactive bool) io.Reader {
	if interactive {
		return input
	}
	return bufio.NewReader(input)
}

func runApprovalPrompt(input io.Reader, output io.Writer, prompt string, interactive bool, approve func() error, deny func() error, approveAll func() error, remember ...func() error) error {
	if interactive {
		if reader := newApprovalInputReader(input); reader != nil {
			return runInteractiveApprovalMenu(reader, output, prompt, approve, deny, approveAll, remember...)
		}
	}
	return runNumericApprovalPrompt(input, output, prompt, approve, deny, approveAll, remember...)
}

func runNumericApprovalPrompt(input io.Reader, output io.Writer, prompt string, approve func() error, deny func() error, approveAll func() error, remember ...func() error) error {
	reader := bufio.NewReader(input)
	options := buildApprovalOptions(approve, deny, approveAll, firstApprovalCallback(remember))
	for {
		fmt.Fprintln(output, styleStatus("[approval]", "approval"), prompt)
		for i, option := range options {
			fmt.Fprintln(output, styleStatus(fmt.Sprintf("%d.", i+1), option.kind), option.label)
		}
		fmt.Fprint(output, styleLabel(">"), " ")
		choice, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		selected, ok := parseApprovalChoice(strings.TrimSpace(choice), len(options))
		if ok {
			return options[selected].action()
		}
		fmt.Fprintln(output, styleStatus(formatApprovalChoiceError(len(options)), "error"))
	}
}

func runInteractiveApprovalMenu(reader approvalInputReader, output io.Writer, prompt string, approve func() error, deny func() error, approveAll func() error, remember ...func() error) error {
	if closer, ok := reader.(interface{ close() }); ok {
		defer closer.close()
	}
	options := buildApprovalOptions(approve, deny, approveAll, firstApprovalCallback(remember))
	selected := 0
	renderApprovalMenu(output, prompt, options, selected)
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			return err
		}
		switch r {
		case '\r':
			fmt.Fprintln(output)
			return options[selected].action()
		case '\n':
			continue
		case 0x1b:
			next, _, readErr := reader.ReadRune()
			if readErr != nil {
				return readErr
			}
			if next != '[' {
				renderApprovalMenuError(output, options, selected)
				continue
			}
			direction, _, readErr := reader.ReadRune()
			if readErr != nil {
				return readErr
			}
			switch direction {
			case 'A':
				selected = moveApprovalSelection(selected, len(options), -1)
				renderApprovalMenuSelection(output, options, selected)
			case 'B':
				selected = moveApprovalSelection(selected, len(options), 1)
				renderApprovalMenuSelection(output, options, selected)
			default:
				renderApprovalMenuError(output, options, selected)
			}
		default:
			renderApprovalMenuError(output, options, selected)
		}
	}
}

func buildApprovalOptions(approve func() error, deny func() error, approveAll func() error, remember func() error) []approvalOption {
	options := []approvalOption{
		{label: "Approve", action: approve, kind: "ready"},
		{label: "Deny", action: deny, kind: "denied"},
	}
	if approveAll != nil {
		options = append(options, approvalOption{label: "Approve all pending tool calls", action: approveAll, kind: "ready"})
	}
	if remember != nil {
		options = append(options, approvalOption{label: "Approve and remember this tool for this session", action: remember, kind: "ready"})
	}
	return options
}

func parseApprovalChoice(choice string, count int) (int, bool) {
	selected, err := strconv.Atoi(choice)
	if err != nil || selected < 1 || selected > count {
		return 0, false
	}
	return selected - 1, true
}

func formatApprovalChoiceError(count int) string {
	if count <= 1 {
		return "Please enter 1."
	}
	choices := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		choices = append(choices, strconv.Itoa(i))
	}
	if count == 2 {
		return "Please enter " + strings.Join(choices, " or ") + "."
	}
	return "Please enter " + strings.Join(choices[:count-1], ", ") + ", or " + choices[count-1] + "."
}

func firstApprovalCallback(callbacks []func() error) func() error {
	for _, callback := range callbacks {
		if callback != nil {
			return callback
		}
	}
	return nil
}

func buildStartupDisplayRow(agentRuntime *agent.Runtime, runtimeHome, workspaceRoot string) startupDisplayRow {
	row := startupDisplayRow{
		RuntimeHome:   runtimeHome,
		WorkspaceRoot: workspaceRoot,
	}
	if agentRuntime == nil {
		return row
	}

	row.ActiveAgent = agentRuntime.ActiveAgent()
	row.Mode = agentRuntime.Mode()
	if profile, ok := agentRuntime.Profile(row.ActiveAgent); ok {
		row.ToolPolicy = profile.ToolPolicy.String()
		for _, kind := range profile.AllowedToolKinds {
			row.AllowedToolKinds = append(row.AllowedToolKinds, string(kind))
		}
		row.AllowedTools = append(row.AllowedTools, profile.AllowedTools...)
	}

	snapshot := agentRuntime.SessionSnapshot()
	if strings.TrimSpace(snapshot.ActiveAgent) != "" {
		row.ActiveAgent = snapshot.ActiveAgent
	}
	if strings.TrimSpace(snapshot.Mode) != "" {
		row.Mode = snapshot.Mode
	}
	row.PendingApprovals = len(snapshot.PendingApprovals)
	sort.Strings(row.AllowedToolKinds)
	sort.Strings(row.AllowedTools)

	workflow := snapshot.Workflow
	if strings.TrimSpace(workflow.Name) != "" {
		parts := []string{workflow.Name}
		if strings.TrimSpace(workflow.Status) != "" {
			parts = append(parts, "("+workflow.Status+")")
		}
		row.WorkflowSummary = strings.Join(parts, " ")
		if strings.TrimSpace(workflow.Summary) != "" {
			row.WorkflowSummary += ": " + workflow.Summary
		}
	}
	if strings.TrimSpace(snapshot.PendingHandoff.TargetAgent) != "" || strings.TrimSpace(snapshot.PendingHandoff.ExpectedAction) != "" {
		targetMode := strings.TrimSpace(snapshot.PendingHandoff.TargetMode)
		if targetMode == "" {
			targetMode = "-"
		}
		row.PendingHandoffSummary = fmt.Sprintf("target=%s mode=%s action=%s", snapshot.PendingHandoff.TargetAgent, targetMode, snapshot.PendingHandoff.ExpectedAction)
	}
	if strings.TrimSpace(snapshot.LastRouting.Outcome) != "" || strings.TrimSpace(snapshot.LastRouting.TargetAgent) != "" {
		targetMode := strings.TrimSpace(snapshot.LastRouting.TargetMode)
		if targetMode == "" {
			targetMode = "-"
		}
		reason := strings.TrimSpace(snapshot.LastRouting.Reason)
		if reason == "" {
			reason = "-"
		}
		row.LastRoutingSummary = fmt.Sprintf("outcome=%s target=%s mode=%s reason=%s", snapshot.LastRouting.Outcome, snapshot.LastRouting.TargetAgent, targetMode, reason)
	}

	return row
}

func renderStartupBanner(row startupDisplayRow) string {
	lines := []string{
		styleHeader(`██████╗  ██████╗ ███████╗██╗      ██████╗ ██╗    ██╗`),
		styleHeader(`██╔════╝ ██╔═══██╗██╔════╝██║     ██╔═══██╗██║    ██║`),
		styleHeader(`██║  ███╗██║   ██║█████╗  ██║     ██║   ██║██║ █╗ ██║`),
		styleHeader(`██║   ██║██║   ██║██╔══╝  ██║     ██║   ██║██║███╗██║`),
		styleHeader(`╚██████╔╝╚██████╔╝██║     ███████╗╚██████╔╝╚███╔███╔╝`),
		styleHeader(` ╚═════╝  ╚═════╝ ╚═╝     ╚══════╝ ╚═════╝  ╚══╝╚══╝`),
		fmt.Sprintf("%s %s", styleStatus("GoFlow Agent", "ready"), styleMuted("local workflow runtime")),
		formatStartupKV("runtime", row.RuntimeHome),
		formatStartupKV("workspace", row.WorkspaceRoot),
	}
	if strings.TrimSpace(row.ActiveAgent) != "" {
		lines = append(lines, formatStartupKV("agent", row.ActiveAgent))
	}
	if strings.TrimSpace(row.Mode) != "" {
		lines = append(lines, formatStartupKV("mode", row.Mode))
	}
	if strings.TrimSpace(row.ToolPolicy) != "" {
		lines = append(lines, formatStartupKV("policy", styleStatus(row.ToolPolicy, policyStatusKind(row.ToolPolicy))))
	}
	if len(row.AllowedToolKinds) > 0 {
		lines = append(lines, formatStartupKV("tool_kinds", strings.Join(row.AllowedToolKinds, ", ")))
	}
	if len(row.AllowedTools) > 0 {
		lines = append(lines, formatStartupKV("tool_allowlist", strings.Join(row.AllowedTools, ", ")))
	}
	if strings.TrimSpace(row.WorkflowSummary) != "" {
		lines = append(lines, formatStartupKV("workflow", row.WorkflowSummary))
	}
	if strings.TrimSpace(row.PendingHandoffSummary) != "" {
		lines = append(lines, formatStartupKV("handoff", row.PendingHandoffSummary))
	}
	if strings.TrimSpace(row.LastRoutingSummary) != "" {
		lines = append(lines, formatStartupKV("routing", row.LastRoutingSummary))
	}
	if row.PendingApprovals > 0 {
		lines = append(lines, formatStartupKV("approvals", styleStatus(fmt.Sprint(row.PendingApprovals), "approval")))
	}
	lines = append(lines, styleLabel("Type a request, or use /help. exit quits."), "")
	return strings.Join(lines, "\n")
}

func formatStartupKV(key, value string) string {
	return fmt.Sprintf("  %s %s", styleLabel(fmt.Sprintf("%-14s", key)), value)
}

func buildTerminalTitle(workspaceRoot string) string {
	normalized := strings.TrimSpace(workspaceRoot)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	base := path.Base(path.Clean(normalized))
	if base == "." || base == "/" || strings.TrimSpace(base) == "" {
		return "GoFlow Agent"
	}
	return "GoFlow Agent - " + base
}

func setTerminalTitle(title string, input io.Reader, output io.Writer) {
	if !terminalInputSupported(input, output) {
		return
	}
	if strings.TrimSpace(title) == "" {
		return
	}
	fmt.Fprintf(output, "\033]0;%s\007", strings.ReplaceAll(title, "\a", ""))
}

type approvalOption struct {
	label  string
	action func() error
	kind   string
}

type rawApprovalReader struct {
	file    *os.File
	restore func()
	closed  bool
}

func (r *rawApprovalReader) ReadRune() (rune, int, error) {
	if r == nil || r.file == nil {
		return 0, 0, io.EOF
	}
	buf := make([]byte, 1)
	for {
		n, err := r.file.Read(buf)
		if err != nil {
			r.close()
			return 0, 0, err
		}
		if n == 0 {
			continue
		}
		return rune(buf[0]), 1, nil
	}
}

func (r *rawApprovalReader) close() {
	if r == nil || r.closed {
		return
	}
	r.closed = true
	if r.restore != nil {
		r.restore()
	}
}

func renderApprovalMenu(output io.Writer, prompt string, options []approvalOption, selected int) {
	for _, line := range approvalMenuLines(prompt, options, selected) {
		fmt.Fprintln(output, line)
	}
	fmt.Fprint(output, styleLabel(">"), " ")
}

func approvalMenuLines(prompt string, options []approvalOption, selected int) []string {
	lines := make([]string, 0, len(options)+2)
	lines = append(lines, fmt.Sprint(styleStatus("[approval]", "approval"), " ", prompt))
	for i, option := range options {
		cursor := "  "
		label := option.label
		if i == selected {
			cursor = styleLabel("→")
			label = styleStatus(option.label, option.kind)
		}
		lines = append(lines, cursor+label)
	}
	lines = append(lines, styleLabel("Enter to confirm • ↑/↓ to move"))
	return lines
}

func renderApprovalMenuSelection(output io.Writer, options []approvalOption, selected int) {
	clearApprovalMenuTransient(output)
	for _, line := range approvalMenuSelectionLines(options, selected) {
		fmt.Fprintln(output, line)
	}
	fmt.Fprint(output, styleLabel(">"), " ")
}

func approvalMenuSelectionLines(options []approvalOption, selected int) []string {
	lines := make([]string, 0, len(options)+1)
	for i, option := range options {
		cursor := "  "
		label := option.label
		if i == selected {
			cursor = styleLabel("→")
			label = styleStatus(option.label, option.kind)
		}
		lines = append(lines, cursor+label)
	}
	lines = append(lines, styleLabel("Enter to confirm • ↑/↓ to move"))
	return lines
}

func renderApprovalMenuError(output io.Writer, options []approvalOption, selected int) {
	clearApprovalMenuTransient(output)
	fmt.Fprintln(output, styleStatus("Use ↑/↓ to choose and Enter to confirm.", "error"))
	for _, line := range approvalMenuSelectionLines(options, selected) {
		fmt.Fprintln(output, line)
	}
	fmt.Fprint(output, styleLabel(">"), " ")
}

func clearApprovalMenuTransient(output io.Writer) {
	fmt.Fprint(output, "\r\x1b[4A")
	for i := 0; i < 5; i++ {
		fmt.Fprint(output, "\x1b[2K")
		if i < 4 {
			fmt.Fprint(output, "\x1b[1B")
		}
	}
	fmt.Fprint(output, "\r\x1b[4A")
}

func moveApprovalSelection(selected, length, delta int) int {
	next := selected + delta
	if next < 0 {
		return 0
	}
	if next >= length {
		return length - 1
	}
	return next
}

func isIgnorableApprovalRune(r rune) bool {
	if r == '\t' || r == ' ' {
		return true
	}
	return !utf8.ValidRune(r)
}

func extractApprovalCallID(prompt string) string {
	fields := strings.Fields(prompt)
	for i := 0; i+3 < len(fields); i++ {
		if fields[i] == "tool" && fields[i+1] == "call" {
			return strings.TrimRight(fields[i+2], ".")
		}
	}
	return ""
}

func formatStatusLines(lines []string) string {
	var runtimeLines []string
	var workflowLines []string
	var mcpLines []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "approval "):
			workflowLines = append(workflowLines, "PENDING "+line)
		case strings.HasPrefix(line, "mcp "):
			mcpLines = append(mcpLines, line)
		default:
			runtimeLines = append(runtimeLines, line)
		}
	}

	var b strings.Builder
	if len(runtimeLines) > 0 {
		b.WriteString(styleHeader("Runtime"))
		b.WriteString("\n")
		for _, line := range runtimeLines {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(workflowLines) > 0 {
		b.WriteString(styleHeader("Workflow / approvals"))
		b.WriteString("\n")
		for _, line := range workflowLines {
			b.WriteString("- ")
			b.WriteString(styleStatus(line, "pending"))
			b.WriteString("\n")
		}
	}
	if len(mcpLines) > 0 {
		b.WriteString(styleHeader("MCP"))
		b.WriteString("\n")
		for _, line := range mcpLines {
			b.WriteString("- ")
			b.WriteString(styleStatus(line, "ready"))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func summarizeToolSchema(inputSchema json.RawMessage) string {
	summary, _ := summarizeToolSchemaWithDiagnostics(inputSchema)
	return summary
}

func summarizeToolSchemaWithDiagnostics(inputSchema json.RawMessage) (string, []string) {
	if len(inputSchema) == 0 {
		return "", []string{"missing input_schema"}
	}
	var schemaDoc struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties any                        `json:"additionalProperties"`
	}
	if err := json.Unmarshal(inputSchema, &schemaDoc); err != nil {
		return "invalid-json", []string{"invalid input_schema JSON: " + err.Error()}
	}
	diagnostics := make([]string, 0, 3)
	if schemaDoc.Type == "" {
		diagnostics = append(diagnostics, "input_schema is missing type")
		return "json", diagnostics
	}
	if schemaDoc.Type != "object" {
		diagnostics = append(diagnostics, "input_schema type should be object")
	}
	if schemaDoc.Type == "object" && schemaDoc.Properties == nil {
		diagnostics = append(diagnostics, "object schema is missing properties")
	}
	if schemaDoc.Type == "object" && schemaDoc.AdditionalProperties == nil {
		diagnostics = append(diagnostics, "object schema should set additionalProperties=false")
	}
	for _, required := range schemaDoc.Required {
		if _, ok := schemaDoc.Properties[required]; !ok {
			diagnostics = append(diagnostics, fmt.Sprintf("required field %q is not declared in properties", required))
		}
	}
	if len(schemaDoc.Properties) == 0 {
		return schemaDoc.Type, diagnostics
	}
	keys := make([]string, 0, len(schemaDoc.Properties))
	for key := range schemaDoc.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s(%s)", schemaDoc.Type, strings.Join(keys, ", ")), diagnostics
}

func buildToolDiagnosticsByQualifiedName(tools []schema.Tool) map[string][]string {
	diagnostics := make(map[string][]string)
	shortNames := make(map[string][]string)
	for _, tool := range tools {
		qualified := formatToolName(tool)
		if strings.TrimSpace(qualified) == "" {
			continue
		}
		shortNames[tool.Name] = append(shortNames[tool.Name], qualified)
		if _, schemaDiagnostics := summarizeToolSchemaWithDiagnostics(tool.InputSchema); len(schemaDiagnostics) > 0 {
			diagnostics[qualified] = append(diagnostics[qualified], schemaDiagnostics...)
		}
	}
	for shortName, qualifiedNames := range shortNames {
		qualifiedNames = uniqueSortedStrings(qualifiedNames)
		if len(qualifiedNames) < 2 {
			continue
		}
		message := fmt.Sprintf("short name %q is ambiguous; use one of: %s", shortName, strings.Join(qualifiedNames, ", "))
		for _, qualified := range qualifiedNames {
			diagnostics[qualified] = append(diagnostics[qualified], message)
		}
	}
	return diagnostics
}

type cliMCPServerRisk struct {
	IsolationLevel         string
	RiskLevel              string
	Sandboxed              bool
	RequiresSandbox        bool
	SandboxFeatures        []string
	MissingSandboxFeatures []string
	WindowsIsolation       *schema.WindowsIsolationProfile
	EnvAllowlistSet        bool
	EnvAllowlist           []string
	SensitiveEnv           []string
}

type cliToolRisk struct {
	RiskLevel              string
	IsolationLevel         string
	Sandboxed              bool
	RequiresSandbox        bool
	SandboxFeatures        []string
	MissingSandboxFeatures []string
	WindowsIsolation       *schema.WindowsIsolationProfile
	EnvAllowlistSet        bool
	EnvAllowlist           []string
	SensitiveEnv           []string
}

func buildCLIMCPServerRiskMap(refs []config.MCPServerRef) map[string]cliMCPServerRisk {
	out := make(map[string]cliMCPServerRisk, len(refs))
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			continue
		}
		isolation := strings.ToLower(strings.TrimSpace(ref.Isolation))
		if isolation == "" {
			isolation = "none"
		}
		risk := cliMCPServerRisk{
			RiskLevel:              "high",
			IsolationLevel:         "none",
			RequiresSandbox:        true,
			MissingSandboxFeatures: []string{"process_lifecycle_isolation", "filesystem_policy", "network_policy", "privilege_reduction"},
			EnvAllowlistSet:        len(ref.EnvAllowlist) > 0,
			EnvAllowlist:           cliSanitizedEnvAllowlist(ref.EnvAllowlist),
			SensitiveEnv:           cliSensitiveEnvAllowlistEntries(ref.EnvAllowlist),
		}
		switch isolation {
		case "linux_cgroup":
			risk.RiskLevel = "medium"
			risk.IsolationLevel = "resource_control"
			risk.SandboxFeatures = []string{"cgroup_resource_limits"}
			risk.MissingSandboxFeatures = []string{"filesystem_policy", "network_policy", "privilege_reduction"}
		case "linux_netns":
			risk.RiskLevel = "medium"
			risk.IsolationLevel = "network_namespace"
			risk.Sandboxed = true
			risk.SandboxFeatures = []string{"network_namespace"}
			risk.MissingSandboxFeatures = []string{"filesystem_policy", "privilege_reduction"}
		case "process_group":
			risk.RiskLevel = "high"
			risk.IsolationLevel = "lifecycle"
			risk.SandboxFeatures = []string{"process_group_lifecycle"}
			risk.MissingSandboxFeatures = []string{"filesystem_policy", "network_policy", "privilege_reduction"}
		case "windows_job":
			risk.RiskLevel = "high"
			risk.IsolationLevel = "lifecycle"
			risk.SandboxFeatures = []string{"windows_job_object_lifecycle"}
			risk.MissingSandboxFeatures = []string{"windows_restricted_token", "windows_appcontainer", "filesystem_policy", "network_policy", "privilege_reduction"}
			risk.WindowsIsolation = &schema.WindowsIsolationProfile{
				JobObject:       true,
				RestrictedToken: false,
				AppContainer:    false,
				LifecycleOnly:   true,
			}
		case "container":
			risk.RiskLevel = "medium"
			risk.IsolationLevel = "container_configured"
			risk.Sandboxed = true
			risk.RequiresSandbox = false
			risk.SandboxFeatures = []string{"container_runtime", "filesystem_mount_policy", "privilege_reduction"}
			risk.MissingSandboxFeatures = nil
			if cliContainerNetworkEnforced(ref.IsolationOptions, ref.NetworkDisabled) {
				risk.SandboxFeatures = append(risk.SandboxFeatures, "network_policy")
			} else {
				risk.MissingSandboxFeatures = append(risk.MissingSandboxFeatures, "network_policy")
			}
			if cliContainerResourceLimited(ref.IsolationOptions) {
				risk.SandboxFeatures = append(risk.SandboxFeatures, "resource_limits")
			} else {
				risk.MissingSandboxFeatures = append(risk.MissingSandboxFeatures, "resource_limits")
			}
			if cliContainerNetworkEnforced(ref.IsolationOptions, ref.NetworkDisabled) && cliContainerResourceLimited(ref.IsolationOptions) {
				risk.RiskLevel = "low"
			}
		}
		out[name] = risk
	}
	return out
}

func cliContainerNetworkEnforced(options map[string]string, networkDisabled bool) bool {
	network := strings.ToLower(strings.TrimSpace(options["network"]))
	if network == "" || network == "default" {
		return networkDisabled
	}
	return network == "disabled" || network == "none"
}

func cliContainerResourceLimited(options map[string]string) bool {
	return strings.TrimSpace(options["memory"]) != "" || strings.TrimSpace(options["cpus"]) != "" || strings.TrimSpace(options["pids_limit"]) != ""
}

func buildCLIToolRiskProfile(tool schema.Tool, server cliMCPServerRisk) cliToolRisk {
	kind := strings.ToLower(strings.TrimSpace(tool.Kind))
	if kind == "" {
		kind = "unknown"
	}
	risk := cliToolRisk{
		RiskLevel:              cliToolKindRiskLevel(kind),
		IsolationLevel:         fallbackDisplayText(server.IsolationLevel, "unknown"),
		Sandboxed:              server.Sandboxed,
		RequiresSandbox:        cliToolKindNeedsSandbox(kind) && !server.Sandboxed,
		SandboxFeatures:        append([]string(nil), server.SandboxFeatures...),
		MissingSandboxFeatures: append([]string(nil), server.MissingSandboxFeatures...),
		EnvAllowlistSet:        server.EnvAllowlistSet,
		EnvAllowlist:           append([]string(nil), server.EnvAllowlist...),
		SensitiveEnv:           append([]string(nil), server.SensitiveEnv...),
	}
	if server.WindowsIsolation != nil {
		windowsIsolation := *server.WindowsIsolation
		risk.WindowsIsolation = &windowsIsolation
	}
	if cliToolLooksDestructive(tool) {
		risk.RiskLevel = "high"
		risk.RequiresSandbox = true
	}
	return risk
}

func cliSanitizedEnvAllowlist(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" {
			continue
		}
		key := strings.ToUpper(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToUpper(out[i]) < strings.ToUpper(out[j]) })
	return out
}

func cliSensitiveEnvAllowlistEntries(values []string) []string {
	var sensitive []string
	for _, name := range cliSanitizedEnvAllowlist(values) {
		if cliIsSensitiveEnvName(name) {
			sensitive = append(sensitive, name)
		}
	}
	return sensitive
}

func cliIsSensitiveEnvName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return false
	}
	for _, marker := range []string{
		"API_KEY",
		"APP_KEY",
		"AUTH_TOKEN",
		"ACCESS_TOKEN",
		"REFRESH_TOKEN",
		"BEARER_TOKEN",
		"SECRET",
		"PASSWORD",
		"PASSWD",
		"PRIVATE_KEY",
		"CREDENTIAL",
		"CREDENTIALS",
		"SESSION_TOKEN",
		"CLIENT_SECRET",
	} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return strings.HasSuffix(upper, "_TOKEN") ||
		strings.HasSuffix(upper, "_KEY") ||
		strings.HasSuffix(upper, "_SECRET") ||
		strings.HasSuffix(upper, "_PASSWORD")
}

func cliToolKindRiskLevel(kind string) string {
	switch kind {
	case "read":
		return "low"
	case "network":
		return "medium"
	case "write", "exec", "unknown":
		return "high"
	default:
		return "high"
	}
}

func cliToolKindNeedsSandbox(kind string) bool {
	switch kind {
	case "write", "exec", "network", "unknown":
		return true
	default:
		return false
	}
}

func cliToolLooksDestructive(tool schema.Tool) bool {
	kind := strings.ToLower(strings.TrimSpace(tool.Kind))
	if kind == "write" || kind == "exec" || kind == "unknown" || kind == "" {
		return true
	}
	name := strings.ToLower(formatToolName(tool))
	for _, marker := range []string{"delete", "remove", "write", "patch", "move", "rename", "exec", "run", "shell", "upload", "deploy"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func formatToolName(tool schema.Tool) string {
	if strings.TrimSpace(tool.Server) == "" {
		return tool.Name
	}
	return fmt.Sprintf("%s/%s", tool.Server, tool.Name)
}

type cliStreamRenderer struct {
	printedText         bool
	traceEnabled        bool
	announcedToolCalls  map[string]toolAnnouncement
	streamedContent     strings.Builder
	writeBatch          []writeBatchItem
	writeBatchPrinted   bool
	promptTokens        int
	outputTokens        int
	cachedTokens        int
	tokenUsagePrinted   bool
	printedPromptTokens int
	printedOutputTokens int
	printedCachedTokens int
	lastTaskStageKey    string
}

func newCLIStreamRenderer(traceEnabled bool) *cliStreamRenderer {
	return &cliStreamRenderer{traceEnabled: traceEnabled, announcedToolCalls: make(map[string]toolAnnouncement)}
}

type toolAnnouncement struct {
	kind    string
	summary string
}

type writeBatchItem struct {
	Path         string
	Status       string
	AddedLines   int
	DeletedLines int
	BytesWritten int
	LineRange    string
}

func (r *cliStreamRenderer) Handle(event schema.StreamEvent) error {
	switch event.Type {
	case schema.StreamEventText:
		fmt.Print(event.Content)
		r.printedText = true
		r.streamedContent.WriteString(event.Content)
	case schema.StreamEventFinalMessage:
		if strings.TrimSpace(event.Content) == "" {
			return nil
		}
		r.ensureLineBreak()
		fmt.Print(event.Content)
		r.printedText = true
		r.streamedContent.WriteString(event.Content)
	case schema.StreamEventWorkflowResult:
		r.ensureLineBreak()
		fmt.Println(formatWorkflowResultEventLine(event))
	case schema.StreamEventToolCall:
		key := strings.TrimSpace(event.ToolCallID)
		kind := strings.TrimSpace(event.Content)
		summary := strings.TrimSpace(event.ArgumentsSummary)
		if strings.EqualFold(kind, "unknown") {
			kind = ""
		}
		if kind == "" && summary == "" {
			return nil
		}
		r.ensureLineBreak()
		if key == "" {
			key = anonymousToolAnnouncementKey(event.ToolName)
		}
		if key != "" {
			previous, seen := r.announcedToolCalls[key]
			if seen && previous.kind == kind && previous.summary == summary {
				return nil
			}
			if seen && (previous.kind != kind || previous.summary != summary) {
				r.announcedToolCalls[key] = toolAnnouncement{kind: kind, summary: summary}
				fmt.Println(formatToolLine("calling", event.ToolName, kind, summary))
				return nil
			}
			r.announcedToolCalls[key] = toolAnnouncement{kind: kind, summary: summary}
		}
		fmt.Println(formatToolLine("calling", event.ToolName, kind, summary))
	case schema.StreamEventToolResult:
		r.ensureLineBreak()
		status := "done"
		if event.Suspended {
			status = "suspended"
		} else if event.IsError {
			status = "failed"
		}
		summary := strings.TrimSpace(event.ArgumentsSummary)
		if summary == "" && strings.TrimSpace(event.ToolCallID) != "" {
			if previous, ok := r.announcedToolCalls[event.ToolCallID]; ok {
				summary = previous.summary
			}
		}
		summary = joinToolSummaries(summary, summarizeToolResultContent(event.Content))
		fmt.Println(formatToolLine(status, event.ToolName, "", summary))
		if notice := formatWriteChangeNotice(event, status); notice != "" {
			fmt.Println(notice)
		}
		if diff := formatWriteDiffPreview(event, status); diff != "" {
			fmt.Print(diff)
		}
		r.recordWriteBatchItem(event, status)
	case schema.StreamEventApproval:
		r.ensureLineBreak()
		summary := strings.TrimSpace(event.ArgumentsSummary)
		if summary == "" && strings.TrimSpace(event.ToolCallID) != "" {
			if previous, ok := r.announcedToolCalls[event.ToolCallID]; ok {
				summary = previous.summary
			}
		}
		if summary == "" {
			fmt.Printf("%s %s requires confirmation\n", styleStatus("[approval]", "approval"), event.ToolName)
			return nil
		}
		fmt.Printf("%s %s requires confirmation %s\n", styleStatus("[approval]", "approval"), event.ToolName, styleLabel(summary))
	case schema.StreamEventTokenUsage:
		r.recordTokenUsage(event)
	case schema.StreamEventPromptBudget:
		r.printPromptBudget(event)
	case schema.StreamEventTaskStage:
		r.printTaskStage(event)
	case schema.StreamEventStatus:
		if event.PromptTokens != 0 || event.OutputTokens != 0 || event.CachedTokens != 0 {
			r.recordTokenUsage(event)
			return nil
		}
		if !r.traceEnabled && !event.NeedsAction {
			return nil
		}
		r.ensureLineBreak()
		fmt.Printf("%s %s\n", styleLabel("[status]"), event.Content)
	case schema.StreamEventError:
		r.ensureLineBreak()
		fmt.Printf("%s %s\n", styleStatus("[error]", "error"), event.Content)
	}
	return nil
}

func formatWorkflowResultEventLine(event schema.StreamEvent) string {
	parts := make([]string, 0, 4)
	if strings.TrimSpace(event.WorkflowName) != "" {
		parts = append(parts, event.WorkflowName)
	}
	if strings.TrimSpace(event.WorkflowStatus) != "" {
		parts = append(parts, "status="+event.WorkflowStatus)
	}
	if strings.TrimSpace(event.RunID) != "" {
		parts = append(parts, "run="+event.RunID)
	}
	if strings.TrimSpace(event.NextStage) != "" {
		parts = append(parts, "next="+event.NextStage)
	}
	if len(parts) == 0 && event.WorkflowResult != nil {
		if strings.TrimSpace(event.WorkflowResult.Name) != "" {
			parts = append(parts, event.WorkflowResult.Name)
		}
		if strings.TrimSpace(event.WorkflowResult.Status) != "" {
			parts = append(parts, "status="+event.WorkflowResult.Status)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "workflow result")
	}
	return fmt.Sprintf("%s %s", styleLabel("[workflow]"), strings.Join(parts, " | "))
}

func (r *cliStreamRenderer) printPromptBudget(event schema.StreamEvent) {
	if r == nil || !r.traceEnabled || event.PromptBudget == nil {
		return
	}
	r.ensureLineBreak()
	fmt.Println(formatPromptBudgetLine(*event.PromptBudget))
}

func (r *cliStreamRenderer) printTaskStage(event schema.StreamEvent) {
	if r == nil || strings.TrimSpace(event.TaskStage) == "" {
		return
	}
	key := strings.Join([]string{event.TaskStage, event.AgentID, event.Mode, strings.TrimSpace(event.Content)}, "\x00")
	if key == r.lastTaskStageKey {
		return
	}
	r.lastTaskStageKey = key
	r.ensureLineBreak()
	fmt.Println(formatTaskStageLine(event))
}

func (r *cliStreamRenderer) recordTokenUsage(event schema.StreamEvent) {
	if r == nil || (event.PromptTokens == 0 && event.OutputTokens == 0 && event.CachedTokens == 0) {
		return
	}
	r.promptTokens += event.PromptTokens
	r.outputTokens += event.OutputTokens
	r.cachedTokens += event.CachedTokens
	r.tokenUsagePrinted = false
}

func joinToolSummaries(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" || strings.Contains(left, right) {
		return left
	}
	return left + " " + right
}

func anonymousToolAnnouncementKey(toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ""
	}
	return "anonymous:" + toolName
}

func summarizeToolResultContent(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return ""
	}
	parts := make([]string, 0, 8)
	if value, ok := payload["path"].(string); ok && strings.TrimSpace(value) != "" {
		parts = append(parts, "result_path="+truncateCLISummaryValue(value))
	}
	if value, ok := payload["url"].(string); ok && strings.TrimSpace(value) != "" {
		parts = append(parts, "url="+truncateCLISummaryValue(value))
	}
	if value, ok := payload["query"].(string); ok && strings.TrimSpace(value) != "" {
		parts = append(parts, "query="+truncateCLISummaryValue(value))
	}
	if value := payloadString(payload, "status"); value != "" {
		parts = append(parts, "status="+value)
	}
	if value := formatPayloadLineDelta(payload); value != "" {
		parts = append(parts, "lines="+value)
	}
	if value := formatPayloadRange(payload); value != "" {
		parts = append(parts, "range="+value)
	}
	if value, ok := numberLike(payload["bytes_written"]); ok {
		parts = append(parts, "bytes="+value)
	}
	if value, ok := numberLike(payload["bytes_read"]); ok {
		parts = append(parts, "bytes_read="+value)
	}
	if value, ok := numberLike(payload["size"]); ok {
		parts = append(parts, "size="+value)
	}
	if entries, ok := payload["entries"].([]any); ok {
		parts = append(parts, fmt.Sprintf("entries=%d", len(entries)))
	}
	if results, ok := payload["results"].([]any); ok {
		parts = append(parts, fmt.Sprintf("results=%d", len(results)))
	}
	if assets, ok := payload["assets"].([]any); ok {
		parts = append(parts, fmt.Sprintf("assets=%d", len(assets)))
	}
	if payloadString(payload, "status") == "" {
		value, _ := payload["line_summary"].(string)
		if strings.TrimSpace(value) != "" {
			parts = append(parts, "change="+truncateCLISummaryValue(value))
		}
	}
	return strings.Join(parts, " ")
}

func formatWriteChangeNotice(event schema.StreamEvent, toolStatus string) string {
	if toolStatus != "done" || event.ToolName != "write_file" || strings.TrimSpace(event.Content) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		return ""
	}
	path, _ := payload["path"].(string)
	lineSummary, _ := payload["line_summary"].(string)
	bytesWritten, _ := numberLike(payload["bytes_written"])
	status := payloadString(payload, "status")
	lineDelta := formatPayloadLineDelta(payload)
	lineRange := formatPayloadRange(payload)
	if strings.TrimSpace(path) == "" && strings.TrimSpace(lineSummary) == "" && strings.TrimSpace(bytesWritten) == "" {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", styleLabel("[write]"), styleStatus(writeChangeStatusCode(status), writeChangeStatusKind(status)))
	if strings.TrimSpace(path) != "" {
		fmt.Fprintf(&b, " %s", styleLabel(truncateCLISummaryValue(path)))
	}
	if strings.TrimSpace(status) != "" {
		fmt.Fprintf(&b, " %s %s", styleMuted("|"), "status="+status)
	}
	if strings.TrimSpace(lineDelta) != "" {
		fmt.Fprintf(&b, " %s %s", styleMuted("|"), lineDelta)
	}
	if strings.TrimSpace(lineRange) != "" {
		fmt.Fprintf(&b, " %s %s", styleMuted("|"), lineRange)
	} else if strings.TrimSpace(lineSummary) != "" {
		fmt.Fprintf(&b, " %s %s", styleMuted("|"), lineSummary)
	}
	if strings.TrimSpace(bytesWritten) != "" {
		fmt.Fprintf(&b, " %s %s bytes", styleMuted("|"), bytesWritten)
	}
	return b.String()
}

func (r *cliStreamRenderer) recordWriteBatchItem(event schema.StreamEvent, toolStatus string) {
	if r == nil || toolStatus != "done" || event.ToolName != "write_file" || strings.TrimSpace(event.Content) == "" {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		return
	}
	path, _ := payload["path"].(string)
	if strings.TrimSpace(path) == "" {
		return
	}
	item := writeBatchItem{
		Path:         path,
		Status:       payloadString(payload, "status"),
		AddedLines:   payloadInt(payload["added_lines"]),
		DeletedLines: payloadInt(payload["deleted_lines"]),
		BytesWritten: payloadInt(payload["bytes_written"]),
		LineRange:    formatPayloadRange(payload),
	}
	r.writeBatch = append(r.writeBatch, item)
	r.writeBatchPrinted = false
}

func (r *cliStreamRenderer) printWriteBatchSummary() {
	if r == nil || r.writeBatchPrinted || len(r.writeBatch) < 2 {
		return
	}
	fmt.Print(formatWriteBatchSummary(r.writeBatch))
	r.writeBatchPrinted = true
}

func formatWriteBatchSummary(items []writeBatchItem) string {
	if len(items) < 2 {
		return ""
	}
	totalAdded := 0
	totalDeleted := 0
	totalBytes := 0
	statusCounts := make(map[string]int)
	for _, item := range items {
		totalAdded += item.AddedLines
		totalDeleted += item.DeletedLines
		totalBytes += item.BytesWritten
		statusCounts[normalizedWriteStatus(item.Status)]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %d files %s +%d -%d %s %d bytes", styleLabel("[write-summary]"), len(items), styleMuted("|"), totalAdded, totalDeleted, styleMuted("|"), totalBytes)
	if statusText := formatWriteBatchStatusCounts(statusCounts); statusText != "" {
		fmt.Fprintf(&b, " %s %s", styleMuted("|"), statusText)
	}
	b.WriteString("\n")

	const maxItems = 6
	limit := len(items)
	if limit > maxItems {
		limit = maxItems
	}
	for i := 0; i < limit; i++ {
		item := items[i]
		fmt.Fprintf(&b, "%s %s %s", styleMuted("  -"), styleStatus(writeChangeStatusCode(item.Status), writeChangeStatusKind(item.Status)), styleLabel(truncateCLISummaryValue(item.Path)))
		fmt.Fprintf(&b, " %s +%d -%d", styleMuted("|"), item.AddedLines, item.DeletedLines)
		if strings.TrimSpace(item.LineRange) != "" {
			fmt.Fprintf(&b, " %s %s", styleMuted("|"), item.LineRange)
		}
		if item.BytesWritten > 0 {
			fmt.Fprintf(&b, " %s %d bytes", styleMuted("|"), item.BytesWritten)
		}
		b.WriteString("\n")
	}
	if remaining := len(items) - limit; remaining > 0 {
		fmt.Fprintf(&b, "%s %d more files\n", styleMuted("  ..."), remaining)
	}
	return b.String()
}

func (r *cliStreamRenderer) printTokenUsageSummary() {
	if r == nil || r.promptTokens+r.outputTokens+r.cachedTokens == 0 {
		return
	}
	if r.tokenUsagePrinted &&
		r.printedPromptTokens == r.promptTokens &&
		r.printedOutputTokens == r.outputTokens &&
		r.printedCachedTokens == r.cachedTokens {
		return
	}
	fmt.Println(formatTokenUsageSummary(r.promptTokens, r.outputTokens, r.cachedTokens))
	r.tokenUsagePrinted = true
	r.printedPromptTokens = r.promptTokens
	r.printedOutputTokens = r.outputTokens
	r.printedCachedTokens = r.cachedTokens
}

func formatTokenUsageSummary(promptTokens, outputTokens, cachedTokens int) string {
	totalTokens := promptTokens + outputTokens
	parts := []string{
		"input=" + formatCount(promptTokens),
		"output=" + formatCount(outputTokens),
		"total=" + formatCount(totalTokens),
	}
	if cachedTokens > 0 {
		parts = append(parts, "cached="+formatCount(cachedTokens))
	}
	return fmt.Sprintf("%s %s", styleLabel("[tokens]"), strings.Join(parts, " "+styleMuted("|")+" "))
}

func formatPromptBudgetLine(budget schema.PromptBudget) string {
	parts := []string{
		"est_input=" + formatCount(budget.EstimatedPromptTokens),
		"system=" + formatCount(budget.SystemTokens),
		"messages=" + formatCount(budget.MessageTokens),
		"tools=" + formatCount(budget.ToolSchemaTokens),
	}
	if budget.SkillTokens > 0 {
		parts = append(parts, "skill="+formatCount(budget.SkillTokens))
	}
	if budget.MemoryBlockCount > 0 {
		parts = append(parts, fmt.Sprintf("memory_blocks=%d", budget.MemoryBlockCount))
	}
	if budget.MemoryEstimatedSavedTokens > 0 {
		parts = append(parts, "memory_saved="+formatCount(budget.MemoryEstimatedSavedTokens))
	}
	if budget.ArtifactRefCount > 0 {
		parts = append(parts, fmt.Sprintf("artifacts=%d", budget.ArtifactRefCount))
	}
	if budget.ArtifactOmittedTokens > 0 {
		parts = append(parts, "artifact_omitted="+formatCount(budget.ArtifactOmittedTokens))
	}
	if budget.TotalToolCount > 0 {
		parts = append(parts, fmt.Sprintf("tool_schemas=%d/%d", budget.ExposedToolCount, budget.TotalToolCount))
	}
	if budget.FilteredToolCount > 0 {
		parts = append(parts, "filtered="+formatCount(budget.FilteredToolCount))
	}
	if strings.TrimSpace(budget.ToolSchemaSelection) != "" {
		parts = append(parts, "tool_selection="+budget.ToolSchemaSelection)
	}
	if budget.CacheablePrefixTokens > 0 {
		parts = append(parts, "cacheable_prefix="+formatCount(budget.CacheablePrefixTokens))
	}
	if strings.TrimSpace(budget.PromptPrefixHash) != "" {
		parts = append(parts, "prefix="+budget.PromptPrefixHash)
	}
	return fmt.Sprintf("%s %s", styleLabel("[budget]"), strings.Join(parts, " "+styleMuted("|")+" "))
}

func formatTaskStageLine(event schema.StreamEvent) string {
	stage := strings.ToLower(strings.TrimSpace(event.TaskStage))
	parts := []string{styleStatus(stage, stageStatusKind(stage))}
	if strings.TrimSpace(event.AgentID) != "" {
		parts = append(parts, "agent="+event.AgentID)
	}
	if strings.TrimSpace(event.Mode) != "" {
		parts = append(parts, "mode="+event.Mode)
	}
	if strings.TrimSpace(event.Content) != "" {
		parts = append(parts, strings.TrimSpace(event.Content))
	}
	return fmt.Sprintf("%s %s", styleLabel("[stage]"), strings.Join(parts, " "+styleMuted("|")+" "))
}

func stageStatusKind(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "modify":
		return "approval"
	case "verify":
		return "pending"
	case "summarize":
		return "done"
	default:
		return "ready"
	}
}

func formatCount(value int) string {
	if value < 1000 && value > -1000 {
		return strconv.Itoa(value)
	}
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	raw := strconv.Itoa(value)
	first := len(raw) % 3
	if first == 0 {
		first = 3
	}
	parts := []string{raw[:first]}
	for i := first; i < len(raw); i += 3 {
		parts = append(parts, raw[i:i+3])
	}
	return sign + strings.Join(parts, ",")
}

func normalizedWriteStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return "modified"
	}
	return status
}

func formatWriteBatchStatusCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, " ")
}

func formatWriteDiffPreview(event schema.StreamEvent, status string) string {
	if status != "done" || event.ToolName != "write_file" || strings.TrimSpace(event.Content) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		return ""
	}
	preview, _ := payload["diff_preview"].(string)
	preview = strings.TrimRight(preview, "\n")
	if strings.TrimSpace(preview) == "" {
		return ""
	}
	path, _ := payload["path"].(string)
	var b strings.Builder
	headerParts := make([]string, 0, 3)
	changeStatus := payloadString(payload, "status")
	if changeStatus != "" {
		headerParts = append(headerParts, writeChangeStatusCode(changeStatus))
	}
	if strings.TrimSpace(path) != "" {
		headerParts = append(headerParts, truncateCLISummaryValue(path))
	}
	if delta := formatPayloadLineDelta(payload); delta != "" {
		headerParts = append(headerParts, delta)
	}
	if strings.TrimSpace(path) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleStatus("[diff]", "approval"), styleLabel(strings.Join(headerParts, " ")))
		writeDiffFileHeaders(&b, path, changeStatus)
	} else {
		fmt.Fprintf(&b, "%s\n", styleStatus("[diff]", "approval"))
	}
	for _, line := range strings.Split(preview, "\n") {
		switch {
		case strings.HasPrefix(line, "+"):
			b.WriteString(styleStatus(line, "ready"))
		case strings.HasPrefix(line, "-"):
			b.WriteString(styleStatus(line, "failed"))
		case strings.HasPrefix(line, "@@"):
			b.WriteString(styleLabel(line))
		case strings.TrimSpace(line) == "...":
			b.WriteString(styleMuted(line))
		default:
			b.WriteString(styleMuted(line))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func writeDiffFileHeaders(b *strings.Builder, path, status string) {
	path = truncateCLISummaryValue(path)
	oldPath := "a/" + path
	newPath := "b/" + path
	if strings.EqualFold(strings.TrimSpace(status), "created") {
		oldPath = "/dev/null"
	}
	fmt.Fprintf(b, "%s\n", styleMuted(fmt.Sprintf("diff --goflow %s %s", oldPath, newPath)))
	fmt.Fprintf(b, "%s\n", styleStatus("--- "+oldPath, "failed"))
	fmt.Fprintf(b, "%s\n", styleStatus("+++ "+newPath, "ready"))
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func formatPayloadLineDelta(payload map[string]any) string {
	added, addedOK := numberLike(payload["added_lines"])
	deleted, deletedOK := numberLike(payload["deleted_lines"])
	if !addedOK && !deletedOK {
		return ""
	}
	if !addedOK {
		added = "0"
	}
	if !deletedOK {
		deleted = "0"
	}
	return fmt.Sprintf("+%s -%s", added, deleted)
}

func formatPayloadRange(payload map[string]any) string {
	oldRange := payloadString(payload, "old_range")
	newRange := payloadString(payload, "new_range")
	switch {
	case oldRange != "" && oldRange != "-" && newRange != "" && newRange != "-":
		return fmt.Sprintf("old %s -> new %s", oldRange, newRange)
	case oldRange != "" && oldRange != "-":
		return "old " + oldRange
	case newRange != "" && newRange != "-":
		return "new " + newRange
	default:
		return ""
	}
}

func writeChangeStatusCode(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created":
		return "A"
	case "cleared":
		return "M"
	case "unchanged":
		return "="
	default:
		return "M"
	}
}

func writeChangeStatusKind(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "unchanged":
		return "pending"
	case "cleared":
		return "approval"
	default:
		return "ready"
	}
}

func numberLike(value any) (string, bool) {
	switch typed := value.(type) {
	case float64:
		return fmt.Sprintf("%.0f", typed), true
	case json.Number:
		return typed.String(), true
	default:
		return "", false
	}
}

func payloadInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := strconv.Atoi(typed.String())
		if err == nil {
			return parsed
		}
		floatValue, err := strconv.ParseFloat(typed.String(), 64)
		if err == nil {
			return int(floatValue)
		}
	case int:
		return typed
	}
	return 0
}

func truncateCLISummaryValue(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", " "))
	if len(value) <= 90 {
		return value
	}
	return value[:87] + "..."
}

func formatToolLine(action, toolName, kind, summary string) string {
	var b strings.Builder
	if action == "calling" {
		fmt.Fprintf(&b, "%s %s %s", styleLabel("[tool]"), action, toolName)
	} else {
		fmt.Fprintf(&b, "%s %s %s", styleLabel("[tool]"), toolName, styleStatus(action, action))
	}
	if strings.TrimSpace(kind) != "" {
		fmt.Fprintf(&b, " (%s)", kind)
	}
	if strings.TrimSpace(summary) != "" {
		fmt.Fprintf(&b, " %s", styleLabel(summary))
	}
	return b.String()
}

func (r *cliStreamRenderer) Flush() {
	if r.printedText {
		fmt.Println()
		r.printedText = false
	}
}

func (r *cliStreamRenderer) Finish(final string) {
	if r.printedText {
		fmt.Println()
		r.printedText = false
	}
	shouldPrintFinal := strings.TrimSpace(final) != "" && !r.finalAlreadyStreamed(final)
	if shouldPrintFinal {
		fmt.Println(final)
	}
	r.printWriteBatchSummary()
	r.printTokenUsageSummary()
}

func (r *cliStreamRenderer) finalAlreadyStreamed(final string) bool {
	streamed := strings.TrimSpace(r.streamedContent.String())
	final = strings.TrimSpace(final)
	if streamed == "" || final == "" {
		return false
	}
	return streamed == final || strings.Contains(streamed, final)
}

func (r *cliStreamRenderer) ensureLineBreak() {
	if r.printedText {
		fmt.Println()
		r.printedText = false
	}
}
