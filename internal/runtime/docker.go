package runtime

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultDockerImage = "node:22-bookworm"
	workspacePath      = "/workspace"
)

var validTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type DockerOptions struct {
	TaskID      string
	Worktree    string
	GitDir      string
	GitLabToken string
	GitLabHost  string
	Image       string
	Command     []string
	Env         map[string]string
	Ports       []PortMapping
	Mounts      []Mount
	Detached    bool
}

type Mount struct {
	HostPath      string
	ContainerPath string
	Mode          string
}

type PortMapping struct {
	Local     string
	Container string
}

type DockerInvocation struct {
	Command []string
	Env     []string
}

func DockerInvocationFor(opts DockerOptions) (DockerInvocation, error) {
	taskID, err := requiredOption("TaskID", opts.TaskID)
	if err != nil {
		return DockerInvocation{}, err
	}
	if !validTaskIDPattern.MatchString(taskID) {
		return DockerInvocation{}, fmt.Errorf("TaskID %q contains invalid Docker container name characters", taskID)
	}
	worktree, err := requiredOption("Worktree", opts.Worktree)
	if err != nil {
		return DockerInvocation{}, err
	}
	gitDir := strings.TrimSpace(opts.GitDir)
	gitLabToken := strings.TrimSpace(opts.GitLabToken)
	gitLabHost := strings.TrimSpace(opts.GitLabHost)
	if gitLabToken != "" && gitLabHost == "" {
		return DockerInvocation{}, fmt.Errorf("GitLabHost is required when GitLabToken is set")
	}
	if gitLabHost != "" && gitLabToken == "" {
		return DockerInvocation{}, fmt.Errorf("GitLabToken is required when GitLabHost is set")
	}

	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = defaultDockerImage
	}
	if strings.HasPrefix(image, "-") {
		return DockerInvocation{}, fmt.Errorf("Image %q must not start with -", image)
	}

	args := []string{
		"docker", "run",
		"--rm",
		"-i",
		"-t",
		"--name", "agent-" + taskID,
		"--cap-drop=ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit=1024",
		"--memory=8g",
		"--cpus=4",
	}
	if opts.Detached {
		args = append(args[:4], append([]string{"-d"}, args[4:]...)...)
	}

	env, err := dockerEnv(opts, gitLabToken, gitLabHost, image)
	if err != nil {
		return DockerInvocation{}, err
	}
	envKeys := sortedKeys(env)
	for _, key := range envKeys {
		args = append(args, "-e", key)
	}

	for _, port := range opts.Ports {
		local, container, err := validatePortMapping(port)
		if err != nil {
			return DockerInvocation{}, err
		}
		args = append(args, "-p", "127.0.0.1:"+local+":"+container)
	}

	args = append(args, "-v", worktree+":"+workspacePath+":rw")
	if gitDir != "" {
		args = append(args, "-v", gitDir+":"+gitDir+":rw")
	}
	for _, mount := range opts.Mounts {
		arg, err := dockerMountArg(mount)
		if err != nil {
			return DockerInvocation{}, err
		}
		args = append(args, "-v", arg)
	}
	args = append(args,
		"-w", workspacePath,
		"--entrypoint", "bash",
		image,
	)

	command, err := dockerCommandSuffix(opts.Command, image)
	if err != nil {
		return DockerInvocation{}, err
	}
	args = append(args, command...)

	return DockerInvocation{
		Command: args,
		Env:     envPairs(env, envKeys),
	}, nil
}

func dockerMountArg(mount Mount) (string, error) {
	hostPath := strings.TrimSpace(mount.HostPath)
	if hostPath == "" {
		return "", fmt.Errorf("mount host path is required")
	}
	containerPath := strings.TrimSpace(mount.ContainerPath)
	if containerPath == "" {
		return "", fmt.Errorf("mount container path is required")
	}
	if !strings.HasPrefix(containerPath, "/") {
		return "", fmt.Errorf("mount container path %q must be absolute", containerPath)
	}
	mode := strings.TrimSpace(mount.Mode)
	if mode == "" {
		mode = "rw"
	}
	if mode != "ro" && mode != "rw" {
		return "", fmt.Errorf("mount mode %q is not supported", mode)
	}
	return hostPath + ":" + containerPath + ":" + mode, nil
}

func validatePortMapping(port PortMapping) (string, string, error) {
	local := strings.TrimSpace(port.Local)
	container := strings.TrimSpace(port.Container)
	if local == "" || container == "" {
		return "", "", fmt.Errorf("port mapping requires local and container ports")
	}
	if !isTCPPort(local) {
		return "", "", fmt.Errorf("invalid local port %q", local)
	}
	if !isTCPPort(container) {
		return "", "", fmt.Errorf("invalid container port %q", container)
	}
	return local, container, nil
}

func isTCPPort(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func requiredOption(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func dockerEnv(opts DockerOptions, gitLabToken, gitLabHost, image string) (map[string]string, error) {
	env := map[string]string{}
	for key, value := range opts.Env {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("env key is required")
		}
		if strings.Contains(key, "=") {
			return nil, fmt.Errorf("env key %q must not contain =", key)
		}
		if !isAllowedExtraEnv(key) {
			return nil, fmt.Errorf("env key %q is not allowed", key)
		}
		env[key] = value
	}

	if gitLabToken != "" {
		env["GITLAB_TOKEN"] = gitLabToken
		env["GITLAB_HOST"] = gitLabHost
	}

	if isNodeImage(image) {
		env["COREPACK_ENABLE_DOWNLOAD_PROMPT"] = "0"
		env["NPM_CONFIG_IGNORE_SCRIPTS"] = "true"
	}

	return env, nil
}

func isAllowedExtraEnv(key string) bool {
	switch key {
	case "CI", "NO_COLOR", "TERM", "TZ":
		return true
	default:
		return false
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func envPairs(env map[string]string, keys []string) []string {
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+env[key])
	}
	return pairs
}

func isNodeImage(image string) bool {
	return image == defaultDockerImage || strings.HasPrefix(image, "node:")
}

func dockerCommandSuffix(command []string, image string) ([]string, error) {
	if len(command) == 0 {
		command = []string{"bash"}
	}
	command = preserveNodeToolPathForShell(command, image)

	if strings.TrimSpace(command[0]) == "" {
		return nil, fmt.Errorf("Command is required")
	}

	args := []string{"-lc", defaultDockerScript(image), "--"}
	args = append(args, command...)
	return args, nil
}

func preserveNodeToolPathForShell(command []string, image string) []string {
	if !isNodeImage(image) || len(command) < 3 {
		return command
	}
	switch baseCommandName(command[0]) {
	case "sh", "bash", "zsh":
	default:
		return command
	}
	for i := 1; i < len(command)-1; i++ {
		arg := command[i]
		if strings.HasPrefix(arg, "-") && strings.Contains(arg, "c") {
			rewritten := append([]string(nil), command...)
			rewritten[i+1] = `export PATH="${AGENTCTL_NODE_BIN:-/tmp/agentctl-node-bin}:${PATH}"; ` + rewritten[i+1]
			return rewritten
		}
	}
	return command
}

func baseCommandName(command string) string {
	command = strings.TrimSpace(command)
	if i := strings.LastIndex(command, "/"); i >= 0 {
		return command[i+1:]
	}
	return command
}

func defaultDockerScript(image string) string {
	lines := []string{
		"set -euo pipefail",
		`askpass_path=""`,
		`cleanup() {`,
		`  if [ -n "${askpass_path}" ]; then rm -f "${askpass_path}"; fi`,
		`}`,
		`trap cleanup EXIT`,
	}
	if isNodeImage(image) {
		lines = append(lines,
			`if command -v corepack >/dev/null 2>&1; then`,
			`  export COREPACK_ENABLE_DOWNLOAD_PROMPT="${COREPACK_ENABLE_DOWNLOAD_PROMPT:-0}"`,
			`  export COREPACK_HOME="${COREPACK_HOME:-/tmp/agentctl-corepack}"`,
			`  export AGENTCTL_NODE_BIN="${AGENTCTL_NODE_BIN:-/tmp/agentctl-node-bin}"`,
			`  mkdir -p "${COREPACK_HOME}" "${AGENTCTL_NODE_BIN}"`,
			`  corepack enable --install-directory "${AGENTCTL_NODE_BIN}" >/dev/null 2>&1`,
			`  export PATH="${AGENTCTL_NODE_BIN}:${PATH}"`,
			`fi`,
		)
	}
	lines = append(lines,
		`if [ -n "${GITLAB_HOST:-}" ]; then`,
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
		`exec "$@"`,
	)
	return strings.Join(lines, "\n")
}
