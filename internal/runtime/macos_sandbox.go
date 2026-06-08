package runtime

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type MacOSSandboxOptions struct {
	TaskID           string
	Worktree         string
	StateDir         string
	HomeDir          string
	Command          []string
	Mode             string
	Network          bool
	AllowRead        []string
	AllowWrite       []string
	DenyRead         []string
	Env              map[string]string
	ExitAfterCommand bool
}

func MacOSSandboxInvocationFor(opts MacOSSandboxOptions) (DockerInvocation, error) {
	taskID, err := requiredOption("TaskID", opts.TaskID)
	if err != nil {
		return DockerInvocation{}, err
	}
	if !validTaskIDPattern.MatchString(taskID) {
		return DockerInvocation{}, fmt.Errorf("TaskID %q contains invalid sandbox session characters", taskID)
	}
	worktree, err := requiredOption("Worktree", opts.Worktree)
	if err != nil {
		return DockerInvocation{}, err
	}
	stateDir, err := requiredOption("StateDir", opts.StateDir)
	if err != nil {
		return DockerInvocation{}, err
	}
	homeDir, err := requiredOption("HomeDir", opts.HomeDir)
	if err != nil {
		return DockerInvocation{}, err
	}

	command, err := nativeCommandSuffix(opts.Command)
	if err != nil {
		return DockerInvocation{}, err
	}
	profile, err := macOSSandboxProfile(opts)
	if err != nil {
		return DockerInvocation{}, err
	}

	tmpDir := filepath.Join(homeDir, "tmp")
	args := []string{
		"sandbox-exec",
		"-p", profile,
		"-D", "WORKSPACE=" + worktree,
		"-D", "HOME=" + homeDir,
		"-D", "STATE_DIR=" + stateDir,
		"env",
		"HOME=" + homeDir,
		"TMPDIR=" + tmpDir,
		"WORKSPACE=" + worktree,
	}
	args = append(args, sandboxEnvArgs(opts.Env, worktree, homeDir, stateDir, tmpDir)...)
	args = append(args,
		"bash",
		"-c",
		macOSSandboxScript(opts.ExitAfterCommand),
		"--",
	)
	args = append(args, command...)

	return DockerInvocation{Command: args}, nil
}

func nativeCommandSuffix(command []string) ([]string, error) {
	if len(command) == 0 {
		command = []string{"bash"}
	}
	if strings.TrimSpace(command[0]) == "" {
		return nil, fmt.Errorf("Command is required")
	}
	return command, nil
}

func macOSSandboxProfile(opts MacOSSandboxOptions) (string, error) {
	mode := strings.TrimSpace(opts.Mode)
	defaultedMode := false
	if mode == "" {
		mode = "strict"
		defaultedMode = true
	}
	network := opts.Network
	if defaultedMode {
		network = true
	}

	switch mode {
	case "strict":
		return macOSStrictSandboxProfile(opts, network), nil
	case "write_only":
		return macOSWriteOnlySandboxProfile(network), nil
	default:
		return "", fmt.Errorf("unsupported macOS sandbox mode %q; supported modes: strict, write_only", mode)
	}
}

func macOSWriteOnlySandboxProfile(network bool) string {
	lines := []string{
		"(version 1)",
		"(allow default)",
		"(deny file-write*)",
		"(allow file-write*",
		"  (subpath (param \"WORKSPACE\"))",
		"  (subpath (param \"HOME\"))",
		"  (subpath (param \"STATE_DIR\"))",
		"  (subpath \"/private/tmp\")",
		"  (subpath \"/tmp\")",
		"  (literal \"/dev/null\"))",
	}
	if network {
		lines = append(lines[:2], append([]string{"(allow network*)"}, lines[2:]...)...)
	}
	return strings.Join(lines, "\n")
}

func macOSStrictSandboxProfile(opts MacOSSandboxOptions, network bool) string {
	lines := []string{
		"(version 1)",
		"(allow default)",
	}
	if network {
		lines = append(lines, "(allow network*)")
	}
	lines = append(lines,
		"(deny file-read*)",
		"(deny file-write*)",
	)

	readPaths := append([]string{}, opts.AllowRead...)
	readPaths = append(readPaths,
		`(param "WORKSPACE")`,
		`(param "HOME")`,
		`(param "STATE_DIR")`,
		"/private/tmp",
		"/tmp",
	)
	readLiteralPaths := append([]string{}, readPaths...)
	readLiteralPaths = append(readLiteralPaths, opts.Worktree, opts.HomeDir, opts.StateDir)
	lines = append(lines, sandboxRule("allow", "file-read*", readPaths, strictReadLiterals(readLiteralPaths))...)

	writePaths := append([]string{}, opts.AllowWrite...)
	writePaths = append(writePaths,
		`(param "WORKSPACE")`,
		`(param "HOME")`,
		`(param "STATE_DIR")`,
		"/private/tmp",
		"/tmp",
	)
	lines = append(lines, sandboxRule("allow", "file-write*", writePaths, []string{"/dev/null"})...)
	if len(opts.DenyRead) > 0 {
		lines = append(lines, sandboxRule("deny", "file-read*", opts.DenyRead, nil)...)
	}
	return strings.Join(lines, "\n")
}

func sandboxRule(action, operation string, subpaths, literals []string) []string {
	lines := []string{"(" + action + " " + operation}
	for _, path := range uniqueStrings(subpaths) {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if strings.HasPrefix(path, `(param "`) {
			lines = append(lines, "  (subpath "+path+")")
			continue
		}
		lines = append(lines, "  (subpath "+sandboxQuote(path)+")")
	}
	for _, path := range uniqueStrings(literals) {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		lines = append(lines, "  (literal "+sandboxQuote(path)+")")
	}
	lines[len(lines)-1] += ")"
	return lines
}

func strictReadLiterals(paths []string) []string {
	literals := []string{"/", "/dev/null"}
	for _, path := range paths {
		if strings.HasPrefix(path, `(param "`) {
			continue
		}
		literals = append(literals, path)
		literals = append(literals, ancestorDirs(path)...)
	}
	return uniqueStrings(literals)
}

func ancestorDirs(path string) []string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." || path == "/" {
		return nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	ancestors := make([]string, 0, len(parts)-1)
	current := ""
	for i := 0; i < len(parts)-1; i++ {
		current += "/" + parts[i]
		ancestors = append(ancestors, current)
	}
	return ancestors
}

func sandboxQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func sandboxEnvArgs(env map[string]string, worktree, homeDir, stateDir, tmpDir string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	args := make([]string, 0, len(keys))
	for _, key := range keys {
		args = append(args, key+"="+expandSandboxEnvValue(env[key], worktree, homeDir, stateDir, tmpDir))
	}
	return args
}

func expandSandboxEnvValue(value, worktree, homeDir, stateDir, tmpDir string) string {
	replacements := map[string]string{
		"${WORKSPACE}": worktree,
		"${TASK_HOME}": homeDir,
		"${HOME}":      homeDir,
		"${STATE_DIR}": stateDir,
		"${TMPDIR}":    tmpDir,
	}
	for token, replacement := range replacements {
		value = strings.ReplaceAll(value, token, replacement)
	}
	return value
}

func macOSSandboxScript(exitAfterCommand bool) string {
	lines := []string{
		"set -euo pipefail",
		`mkdir -p "$HOME" "$TMPDIR"`,
		`cd "$WORKSPACE"`,
		`askpass_path=""`,
		`cleanup() {`,
		`  if [ -n "${askpass_path}" ]; then rm -f "${askpass_path}"; fi`,
		`}`,
		`trap cleanup EXIT`,
		`if [ -n "${AGENTCTL_DEVELOPER_DIR:-}" ]; then`,
		`  export DEVELOPER_DIR="$AGENTCTL_DEVELOPER_DIR"`,
		`fi`,
		`if [ -n "${AGENTCTL_PATH_PREFIX:-}" ]; then`,
		`  export PATH="$AGENTCTL_PATH_PREFIX:${PATH:-/bin:/usr/bin}"`,
		`fi`,
		`if [ -n "${GITLAB_HOST:-}" ] && [ -n "${GITLAB_TOKEN:-}" ]; then`,
		`  export GIT_CONFIG_COUNT=2`,
		`  export GIT_CONFIG_KEY_0="url.https://${GITLAB_HOST}/.insteadOf"`,
		`  export GIT_CONFIG_VALUE_0="git@${GITLAB_HOST}:"`,
		`  export GIT_CONFIG_KEY_1="url.https://${GITLAB_HOST}/.insteadOf"`,
		`  export GIT_CONFIG_VALUE_1="ssh://git@${GITLAB_HOST}/"`,
		`  askpass_path="$(mktemp)"`,
		`  cat > "${askpass_path}" <<'EOF'`,
		`#!/usr/bin/env sh`,
		`case "$1" in`,
		`  *Username*) printf '%s\n' oauth2 ;;`,
		`  *Password*) printf '%s\n' "$GITLAB_TOKEN" ;;`,
		`  *) printf '\n' ;;`,
		`esac`,
		`EOF`,
		`  chmod 700 "${askpass_path}"`,
		`  export GIT_ASKPASS="${askpass_path}"`,
		`  export GIT_TERMINAL_PROMPT=0`,
		"fi",
		"set +e",
		`"$@"`,
		"rc=$?",
		"set -e",
	}
	if exitAfterCommand {
		lines = append(lines, `exit "$rc"`)
	} else {
		lines = append(lines,
			`printf '\n[agentctl] command exited with status %s\n' "$rc"`,
			`printf '[agentctl] staying inside macOS sandbox shell. Run tools here; exit shell or run agentctl cleanup when done.\n'`,
		)
		lines = append(lines, macOSInteractiveShellLines()...)
	}
	return strings.Join(lines, "\n")
}

func macOSInteractiveShellLines() []string {
	return []string{
		"if command -v zsh >/dev/null 2>&1; then",
		"  exec zsh -l",
		"fi",
		"if command -v bash >/dev/null 2>&1; then",
		"  exec bash -l",
		"fi",
		"exec sh",
	}
}
