package runtime

import (
	"fmt"
	"sort"
	"strings"
)

const (
	defaultDockerImage = "node:22-bookworm"
	workspacePath      = "/workspace"
)

type DockerOptions struct {
	TaskID      string
	Worktree    string
	GitLabToken string
	GitLabHost  string
	RemotePath  string
	Image       string
	Command     []string
	Env         map[string]string
}

func DockerCommand(opts DockerOptions) ([]string, error) {
	taskID, err := requiredOption("TaskID", opts.TaskID)
	if err != nil {
		return nil, err
	}
	worktree, err := requiredOption("Worktree", opts.Worktree)
	if err != nil {
		return nil, err
	}
	gitLabToken, err := requiredOption("GitLabToken", opts.GitLabToken)
	if err != nil {
		return nil, err
	}

	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = defaultDockerImage
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
		return nil, err
	}
	for _, key := range sortedKeys(env) {
		args = append(args, "-e", key+"="+env[key])
	}

	args = append(args,
		"-v", worktree+":"+workspacePath+":rw",
		"-w", workspacePath,
		image,
	)

	command, err := dockerCommandSuffix(opts.Command)
	if err != nil {
		return nil, err
	}
	args = append(args, command...)

	return args, nil
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

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isNodeImage(image string) bool {
	return image == defaultDockerImage || strings.HasPrefix(image, "node:")
}

func dockerCommandSuffix(command []string) ([]string, error) {
	if len(command) == 0 {
		return []string{"bash", "-lc", defaultDockerScript()}, nil
	}

	if strings.TrimSpace(command[0]) == "" {
		return nil, fmt.Errorf("Command is required")
	}

	return append([]string(nil), command...), nil
}

func defaultDockerScript() string {
	return strings.Join([]string{
		"set -euo pipefail",
		`if [ -n "${GITLAB_HOST:-}" ] && [ -n "${GITLAB_REMOTE_PATH:-}" ]; then`,
		`  remote_path="${GITLAB_REMOTE_PATH%.git}"`,
		`  git remote set-url origin "https://oauth2:${GITLAB_TOKEN}@${GITLAB_HOST}/${remote_path}.git"`,
		"fi",
		"exec bash",
	}, "\n")
}
