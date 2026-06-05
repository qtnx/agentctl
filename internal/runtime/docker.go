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
	RemotePath  string
	Image       string
	Command     []string
	Env         map[string]string
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
	gitDir, err := requiredOption("GitDir", opts.GitDir)
	if err != nil {
		return DockerInvocation{}, err
	}
	gitLabToken, err := requiredOption("GitLabToken", opts.GitLabToken)
	if err != nil {
		return DockerInvocation{}, err
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
		"--name", "agent-" + taskID,
		"--cap-drop=ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit=1024",
		"--memory=8g",
		"--cpus=4",
	}

	env, err := dockerEnv(opts, gitLabToken, image)
	if err != nil {
		return DockerInvocation{}, err
	}
	envKeys := sortedKeys(env)
	for _, key := range envKeys {
		args = append(args, "-e", key)
	}

	args = append(args,
		"-v", worktree+":"+workspacePath+":rw",
		"-v", gitDir+":"+gitDir+":rw",
		"-w", workspacePath,
		image,
	)

	command, err := dockerCommandSuffix(opts.Command)
	if err != nil {
		return DockerInvocation{}, err
	}
	args = append(args, command...)

	return DockerInvocation{
		Command: args,
		Env:     envPairs(env, envKeys),
	}, nil
}

func requiredOption(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func dockerEnv(opts DockerOptions, gitLabToken, image string) (map[string]string, error) {
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

	env["GITLAB_TOKEN"] = gitLabToken

	gitLabHost := strings.TrimSpace(opts.GitLabHost)
	remotePath := strings.TrimSpace(opts.RemotePath)
	if gitLabHost != "" && remotePath != "" {
		env["GITLAB_HOST"] = gitLabHost
		env["GITLAB_REMOTE_PATH"] = remotePath
	}

	if isNodeImage(image) {
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

func dockerCommandSuffix(command []string) ([]string, error) {
	if len(command) == 0 {
		command = []string{"bash"}
	}

	if strings.TrimSpace(command[0]) == "" {
		return nil, fmt.Errorf("Command is required")
	}

	args := []string{"bash", "-lc", defaultDockerScript(), "--"}
	args = append(args, command...)
	return args, nil
}

func defaultDockerScript() string {
	return strings.Join([]string{
		"set -euo pipefail",
		`askpass_path=""`,
		`cleanup() {`,
		`  if [ -n "${askpass_path}" ]; then rm -f "${askpass_path}"; fi`,
		`}`,
		`trap cleanup EXIT`,
		`if [ -n "${GITLAB_HOST:-}" ] && [ -n "${GITLAB_REMOTE_PATH:-}" ]; then`,
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
	}, "\n")
}
