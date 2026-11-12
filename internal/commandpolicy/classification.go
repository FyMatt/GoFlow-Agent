package commandpolicy

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Class describes the operational intent of a structured command call.
type Class string

const (
	ClassInspection        Class = "inspection"
	ClassTest              Class = "test"
	ClassBuild             Class = "build"
	ClassLint              Class = "lint"
	ClassFormat            Class = "format"
	ClassDependencyInstall Class = "dependency_install"
	ClassShell             Class = "shell"
	ClassDestructive       Class = "destructive"
	ClassUnknown           Class = "unknown"
)

// Classification is safe to expose in tool results and approval summaries.
type Classification struct {
	Class                    Class  `json:"class"`
	RiskLevel                string `json:"risk_level"`
	RequiresExplicitApproval bool   `json:"requires_explicit_approval"`
	Reason                   string `json:"reason"`
}

// Classify determines intent from the executable name and structured arguments.
// It never trusts a model-provided category.
func Classify(program string, args []string) Classification {
	program, args = RewriteGoVerificationArgs(program, args)
	name := normalizedProgram(program)
	lowerArgs := normalizedArgs(args)
	first := firstArgument(lowerArgs)

	if name == "go" && first == "fmt" && containsAny(lowerArgs, "-l") {
		return classified(ClassUnknown, "low", false, "go fmt does not support -l; use gofmt -l . for a read-only formatting check")
	}
	if isShellProgram(name) || hasShellExecutionFlag(name, lowerArgs) {
		return classified(ClassShell, "high", true, "shell execution can combine commands and bypass structured argument policy")
	}
	if isDestructiveCommand(name, lowerArgs, first) {
		return classified(ClassDestructive, "high", true, "command can remove data, rewrite history, or alter machine state")
	}
	if isDependencyInstall(name, lowerArgs, first) {
		return classified(ClassDependencyInstall, "medium", true, "dependency changes can modify lockfiles and execute third-party install scripts")
	}
	if isReadOnlyFormatCommand(name, lowerArgs, first) {
		return classified(ClassFormat, "low", false, "formatter is running in check-only mode")
	}
	if isFormatCommand(name, lowerArgs, first) {
		return classified(ClassFormat, "medium", true, "formatter may rewrite workspace files")
	}
	if isTestCommand(name, lowerArgs, first) {
		return classified(ClassTest, "medium", false, "test command executes workspace code with bounded local process controls")
	}
	if isBuildCommand(name, lowerArgs, first) {
		return classified(ClassBuild, "medium", false, "build command executes workspace build logic and may create local outputs")
	}
	if isLintCommand(name, lowerArgs, first) {
		return classified(ClassLint, "low", false, "lint or static-analysis command primarily inspects workspace content")
	}
	if isInspectionCommand(name, lowerArgs, first) {
		return classified(ClassInspection, "low", false, "read-only inspection command")
	}
	return classified(ClassUnknown, "medium", true, "command is not in the built-in local-development policy")
}

// ClassifyArguments decodes a run_command tool call without accepting an
// untrusted classification field from the caller.
func ClassifyArguments(arguments []byte) Classification {
	program, args, ok := decodeRunCommand(arguments)
	if !ok {
		return classified(ClassUnknown, "medium", true, "command arguments could not be classified")
	}
	return Classify(program, args)
}

// NormalizeRunCommandArguments rewrites common lightweight-model shapes into
// the canonical program/args object used by command_tools. It never invokes a
// shell; quoted or metacharacter-bearing command strings are left unchanged.
func NormalizeRunCommandArguments(raw []byte) []byte {
	program, args, payload, ok := decodeRunCommandPayload(raw)
	if !ok || program == "" {
		return raw
	}
	payload["program"] = program
	if args == nil {
		args = []string{}
	}
	payload["args"] = args
	delete(payload, "command")
	delete(payload, "argv")
	normalized, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return normalized
}

func decodeRunCommand(raw []byte) (string, []string, bool) {
	program, args, _, ok := decodeRunCommandPayload(raw)
	return program, args, ok
}

func decodeRunCommandPayload(raw []byte) (string, []string, map[string]any, bool) {
	if len(raw) == 0 {
		return "", nil, nil, false
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return "", nil, nil, false
	}
	if program := stringField(payload, "program"); program != "" {
		args := stringList(payload["args"])
		if parts, ok := splitSafeCommandLine(program); ok && len(parts) > 1 {
			combined := append(append([]string(nil), parts[1:]...), args...)
			// Only repair recognized development commands. Quoted paths,
			// shell syntax, destructive commands and unknown programs stay literal.
			switch Classify(parts[0], combined).Class {
			case ClassTest, ClassBuild, ClassLint, ClassFormat, ClassInspection:
				program, args = parts[0], combined
			}
		}
		return rewrittenCommand(program, args, payload)
	}
	if argv := stringList(payload["argv"]); len(argv) > 0 {
		return rewrittenCommand(argv[0], append([]string(nil), argv[1:]...), payload)
	}
	switch command := payload["command"].(type) {
	case []any:
		parts := stringList(command)
		if len(parts) == 0 {
			return "", nil, payload, false
		}
		return rewrittenCommand(parts[0], append([]string(nil), parts[1:]...), payload)
	case string:
		parts, ok := splitSafeCommandLine(command)
		if !ok || len(parts) == 0 {
			return "", nil, payload, false
		}
		return rewrittenCommand(parts[0], append([]string(nil), parts[1:]...), payload)
	default:
		return "", nil, payload, false
	}
}

func rewrittenCommand(program string, args []string, payload map[string]any) (string, []string, map[string]any, bool) {
	if normalizedProgram(program) == "gofmt" && len(args) > 1 && strings.HasPrefix(args[1], "-") && (strings.EqualFold(args[0], "gofmt") || strings.EqualFold(args[0], "gofmt.exe")) {
		args = append([]string(nil), args[1:]...)
	}
	program, args = RewriteGoVerificationArgs(program, args)
	return program, args, payload, true
}

func stringField(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func stringList(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		out = append(out, text)
	}
	return out
}

func splitSafeCommandLine(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "|&;<>()$`*?{}[]!#\\\"'") {
		return nil, false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, false
	}
	return fields, true
}

func classified(class Class, risk string, approval bool, reason string) Classification {
	return Classification{Class: class, RiskLevel: risk, RequiresExplicitApproval: approval, Reason: reason}
}

// RewriteGoVerificationArgs turns common lightweight-model shapes such as
// `go ./...` into the canonical `go test ./...` used by local verification.
func RewriteGoVerificationArgs(program string, args []string) (string, []string) {
	if normalizedProgram(program) != "go" {
		return program, args
	}
	if len(args) == 0 {
		return program, []string{"test", "./..."}
	}
	first := firstArgument(normalizedArgs(args))
	if first == "" || first == "." || first == "..." || strings.HasPrefix(first, "./") {
		return program, append([]string{"test"}, args...)
	}
	return program, args
}

func normalizedProgram(program string) string {
	name := strings.ToLower(strings.TrimSpace(filepath.Base(program)))
	return strings.TrimSuffix(name, ".exe")
}

func normalizedArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, strings.ToLower(strings.TrimSpace(arg)))
	}
	return out
}

func firstArgument(args []string) string {
	for _, arg := range args {
		if arg != "" && !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func isShellProgram(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "fish", "cmd", "powershell", "pwsh", "wsl":
		return true
	default:
		return false
	}
}

func hasShellExecutionFlag(name string, args []string) bool {
	if !isShellProgram(name) {
		return false
	}
	for _, arg := range args {
		if arg == "-c" || arg == "/c" || arg == "-command" || arg == "-encodedcommand" {
			return true
		}
	}
	return false
}

func isDestructiveCommand(name string, args []string, first string) bool {
	switch name {
	case "rm", "rmdir", "del", "erase", "remove-item", "format", "shutdown", "reboot":
		return true
	case "git":
		switch first {
		case "clean", "reset", "push", "rebase", "filter-branch", "filter-repo":
			return true
		case "checkout", "restore":
			return containsAny(args, "--force", "-f", ".", ":/")
		}
	}
	return false
}

func isDependencyInstall(name string, args []string, first string) bool {
	switch name {
	case "npm", "pnpm", "yarn", "bun":
		return first == "install" || first == "add" || first == "update" || first == "upgrade" || first == "ci"
	case "pip", "pip3", "uv", "poetry":
		return first == "install" || first == "add" || first == "update" || first == "sync"
	case "go":
		return first == "get" || first == "install" || first == "work" || first == "mod" && containsAny(args, "tidy", "download", "vendor")
	case "cargo":
		return first == "add" || first == "install" || first == "update" || first == "fetch"
	case "dotnet":
		return first == "add" || first == "restore"
	}
	return false
}

func isFormatCommand(name string, args []string, first string) bool {
	switch name {
	case "gofmt", "rustfmt", "prettier", "black", "clang-format", "stylua":
		return true
	case "go":
		return first == "fmt"
	case "cargo":
		return first == "fmt"
	case "dotnet":
		return first == "format"
	case "npm", "pnpm", "yarn", "bun":
		return isRunScript(args, "format", "fmt")
	}
	return false
}

func isReadOnlyFormatCommand(name string, args []string, first string) bool {
	switch name {
	case "gofmt":
		return containsAny(args, "-d", "-l") && !containsAny(args, "-w")
	case "rustfmt":
		return containsAny(args, "--check")
	case "prettier":
		return containsAny(args, "--check", "--list-different")
	case "black", "stylua":
		return containsAny(args, "--check")
	case "clang-format":
		return containsAny(args, "--dry-run") && !containsAny(args, "-i")
	case "cargo":
		return first == "fmt" && containsAny(args, "--check")
	}
	return false
}

func isTestCommand(name string, args []string, first string) bool {
	switch name {
	case "pytest", "gotestsum", "jest", "vitest", "phpunit":
		return true
	case "python", "python3", "py":
		return pythonTestModule(args)
	case "node":
		return isBoundedNodeTest(args)
	case "go", "cargo", "dotnet", "gradle", "gradlew", "mvn", "mvnw":
		return first == "test"
	case "npm", "pnpm", "yarn", "bun":
		return first == "test" || isRunScript(args, "test")
	}
	return false
}

func pythonTestModule(args []string) bool {
	for index := 0; index+1 < len(args); index++ {
		if strings.EqualFold(strings.TrimSpace(args[index]), "-m") {
			module := strings.ToLower(strings.TrimSpace(args[index+1]))
			return module == "pytest" || module == "unittest"
		}
	}
	return false
}

func isBoundedNodeTest(args []string) bool {
	if len(args) == 2 && args[0] == "--test" {
		args = args[1:]
	}
	if len(args) != 1 {
		return false
	}
	candidate := strings.ReplaceAll(strings.TrimSpace(args[0]), "\\", "/")
	if candidate == "" || strings.HasPrefix(candidate, "/") || strings.Contains(candidate, ":") {
		return false
	}
	for _, part := range strings.Split(candidate, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	base := candidate[strings.LastIndex(candidate, "/")+1:]
	for _, suffix := range []string{".test.js", ".spec.js", ".test.mjs", ".spec.mjs", ".test.cjs", ".spec.cjs"} {
		if strings.HasSuffix(base, suffix) && len(base) > len(suffix) {
			return true
		}
	}
	return false
}

func isBuildCommand(name string, args []string, first string) bool {
	switch name {
	case "python", "python3", "py":
		return len(args) >= 3 && args[0] == "-m" && args[1] == "py_compile"
	case "make", "cmake", "ninja", "msbuild":
		return true
	case "go", "cargo", "dotnet", "gradle", "gradlew", "mvn", "mvnw":
		return first == "build" || first == "compile" || first == "package"
	case "npm", "pnpm", "yarn", "bun":
		return isRunScript(args, "build", "compile")
	}
	return false
}

func isLintCommand(name string, args []string, first string) bool {
	switch name {
	case "golangci-lint", "eslint", "ruff", "mypy", "pylint", "clippy", "shellcheck", "staticcheck":
		return true
	case "go":
		return first == "vet"
	case "cargo":
		return first == "clippy" || first == "check"
	case "npm", "pnpm", "yarn", "bun":
		return isRunScript(args, "lint", "check")
	}
	return false
}

func isInspectionCommand(name string, args []string, first string) bool {
	switch name {
	case "rg", "grep", "find", "fd", "ls", "dir", "tree", "type", "cat", "head", "tail", "pwd", "where", "which", "wc":
		return true
	case "git":
		switch first {
		case "status", "diff", "log", "show", "branch", "remote", "tag", "grep", "ls-files", "rev-parse":
			return true
		}
	case "go":
		return first == "env" || first == "version" || first == "list"
	case "node", "python", "python3", "java":
		return containsAny(args, "--version", "-version", "version", "info", "ps", "images")
	}
	return false
}

func isRunScript(args []string, names ...string) bool {
	if len(args) == 0 {
		return false
	}
	index := 0
	if args[0] == "run" || args[0] == "run-script" {
		index = 1
	}
	if index >= len(args) {
		return false
	}
	for _, name := range names {
		if args[index] == name {
			return true
		}
	}
	return false
}

func containsAny(values []string, targets ...string) bool {
	for _, value := range values {
		for _, target := range targets {
			if value == target {
				return true
			}
		}
	}
	return false
}
