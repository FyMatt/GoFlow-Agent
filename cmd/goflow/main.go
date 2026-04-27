package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	apipkg "github.com/FyMatt/GoFlow-Agent/internal/api"
	apppkg "github.com/FyMatt/GoFlow-Agent/internal/app"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/skill"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"golang.org/x/term"
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
	cfg := app.Config
	sessionState := app.SessionState
	agentRuntime := app.Runtime
	agentRuntime.SetWorkspaceConfirmed(workspaceState.Confirmed())
	skillManager := app.SkillManager.(*skill.Manager)
	mcpManager := app.MCPClient

	if strings.TrimSpace(paths.HTTPAddr) != "" {
		server := &http.Server{
			Addr:    paths.HTTPAddr,
			Handler: apipkg.NewServerWithWorkspace(agentRuntime, workspaceState),
		}
		fmt.Printf("GoFlow HTTP API ready. runtime=%s workspace=%s addr=%s\n", cfg.RuntimeHome, workspaceState.DisplayRoot(), paths.HTTPAddr)
		fmt.Println("Press Ctrl+C to stop the HTTP server.")
		if err := runHTTPServer(ctx, server, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "http server error: %v\n", err)
			os.Exit(1)
		}
		if err := sessionState.Save(cfg.Session.PersistPath); err != nil {
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
		if len(refs) > 0 {
			names := make([]string, 0, len(refs))
			for _, ref := range refs {
				names = append(names, "@"+ref.RelativePath)
			}
			fmt.Println(formatCommandSuccess("attached", strings.Join(names, ", ")))
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
		"/deny",
		"/help",
		"/mode",
		"/new-agent",
		"/new-skill",
		"/new-tool",
		"/new-workflow",
		"/reload",
		"/reload-tools",
		"/session",
		"/skill-templates",
		"/skills",
		"/status",
		"/tools",
		"/trace",
		"/use",
		"/workflow",
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
	return runtimeHomeInfo{Root: parent, BinaryArchive: fileExists(filepath.Join(parent, "configs", "agent.binary.yaml"))}, true
}

func hasRuntimeConfig(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	return fileExists(filepath.Join(root, "configs", "agent.yaml")) || fileExists(filepath.Join(root, "configs", "agent.binary.yaml"))
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func defaultConfigPathForRuntime(info runtimeHomeInfo) string {
	if strings.TrimSpace(info.Root) == "" {
		return ""
	}
	binaryConfig := filepath.Join(info.Root, "configs", "agent.binary.yaml")
	if info.BinaryArchive && fileExists(binaryConfig) {
		return binaryConfig
	}
	return filepath.Join(info.Root, "configs", "agent.yaml")
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
	return resolvePathSettingsWithDefault(runtimeHome, args, filepath.Join(runtimeHome, "configs", "agent.yaml"))
}

func resolvePathSettingsWithDefault(runtimeHome string, args []string, defaultConfigPath string) (pathSettings, error) {
	configPath := strings.TrimSpace(defaultConfigPath)
	if configPath == "" {
		configPath = filepath.Join(runtimeHome, "configs", "agent.yaml")
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
		return handleSkillTemplatesCommand()
	case "/new-skill":
		return handleNewSkillCommand(fields, skillManager)
	case "/new-tool":
		return handleNewToolCommand(fields, skillManager)
	case "/new-agent":
		return handleNewAgentCommand(fields, skillManager)
	case "/new-workflow":
		return handleNewWorkflowCommand(fields, skillManager)
	case "/tools":
		tools, err := mcpClient.ListTools(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "list tools error: %v\n", err)
			return true
		}
		health := mcpClient.HealthStatus(ctx)
		diagnosticsByTool := buildToolDiagnosticsByQualifiedName(tools)
		rows := make([]toolDisplayRow, 0, len(tools))
		for _, tool := range tools {
			qualifiedName := formatToolName(tool)
			rows = append(rows, toolDisplayRow{
				Name:               qualifiedName,
				Description:        tool.Description,
				Server:             tool.Server,
				Kind:               tool.Kind,
				Health:             health[tool.Server],
				InputSchemaSummary: summarizeToolSchema(tool.InputSchema),
				Diagnostics:        diagnosticsByTool[qualifiedName],
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
	case "/status":
		fmt.Print(formatStatusLines(agentRuntime.StatusLines(ctx)))
		return true
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
	text = strings.Trim(text, " \t\r\n.!！?？。")
	text = strings.ReplaceAll(text, "，", ",")
	text = strings.ReplaceAll(text, "、", ",")
	text = strings.Join(strings.Fields(text), " ")
	switch text {
	case "继续", "继续吧", "继续执行", "继续任务", "可以继续", "可以,继续", "继续上次任务", "重试", "重新执行", "再试一次":
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

func workflowUsage(agentRuntime *agent.Runtime) string {
	var b strings.Builder
	b.WriteString("usage: /workflow <plan-fix-audit|skill-chain|custom-name> [--approve] <request>")
	names := availableWorkflowNames(agentRuntime)
	if len(names) > 0 {
		b.WriteString("\navailable workflows: ")
		b.WriteString(strings.Join(names, ", "))
	}
	b.WriteString("\ncustom workflows load from workflows/<name>/workflow.yaml; create one with /new-workflow <name>")
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
	"/status",
	"/session",
	"/workspace",
	"/use",
	"/mode",
	"/workflow",
	"/trace",
	"/approve",
	"/deny",
	"/reload",
	"/reload-tools",
	"/skill-templates",
	"/new-skill",
	"/new-tool",
	"/new-agent",
	"/new-workflow",
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
	Name               string
	Description        string
	Server             string
	Kind               string
	Health             string
	InputSchemaSummary string
	Diagnostics        []string
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
		{Command: "/status", Description: "Show runtime, routing, workflow, and MCP state"},
		{Command: "/session", Description: "Show persisted session memory"},
	})
	writeCommandGroup(&b, "Control", []commandHelpRow{
		{Command: "/use <agent>", Description: "Switch active agent"},
		{Command: "/mode <chat|plan|audit|fix>", Description: "Switch session mode"},
		{Command: "/workspace [status|confirm|clear|use <path>]", Description: "Show or confirm the active workspace"},
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
		{Command: "/new-workflow <name>", Description: "Create a workflow blueprint"},
		{Command: "exit", Description: "Quit the CLI"},
	})
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
	warnings := 0
	for _, row := range rows {
		servers[fallbackDisplayText(row.Server, "unknown")]++
		kinds[fallbackDisplayText(row.Kind, "unknown")]++
		warnings += nonEmptyCount(row.Diagnostics)
	}
	parts := []string{fmt.Sprintf("total=%d", len(rows)), fmt.Sprintf("servers=%d", len(servers)), "kinds=" + formatCountMap(kinds)}
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
	warnings := 0
	for _, row := range rows {
		health[fallbackDisplayText(row.Health, "unknown")]++
		warnings += nonEmptyCount(row.Diagnostics)
	}
	summary := "health=" + formatCountMap(health)
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
		styleHeader(`██████�? ██████�?███████╗██�?     ██████�?██�?   ██╗`),
		styleHeader(`██╔════╝ ██╔═══██╗██╔════╝██║     ██╔═══██╗██║    ██║`),
		styleHeader(`██�? ███╗██�?  ██║█████�? ██�?    ██�?  ██║██║ █╗ ██║`),
		styleHeader(`██�?  ██║██║   ██║██╔══�? ██�?    ██�?  ██║██║███╗██║`),
		styleHeader(`╚██████╔╝╚██████╔╝██�?    ███████╗╚██████╔╝╚███╔███╔╝`),
		styleHeader(` ╚═════�? ╚═════�?╚═�?    ╚══════╝ ╚═════�? ╚══╝╚══╝`),
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
			cursor = styleLabel("�?")
			label = styleStatus(option.label, option.kind)
		}
		lines = append(lines, cursor+label)
	}
	lines = append(lines, styleLabel("Enter to confirm �?�?�?to move"))
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
			cursor = styleLabel("�?")
			label = styleStatus(option.label, option.kind)
		}
		lines = append(lines, cursor+label)
	}
	lines = append(lines, styleLabel("Enter to confirm �?�?�?to move"))
	return lines
}

func renderApprovalMenuError(output io.Writer, options []approvalOption, selected int) {
	clearApprovalMenuTransient(output)
	fmt.Fprintln(output, styleStatus("Use �?�?to choose and Enter to confirm.", "error"))
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
