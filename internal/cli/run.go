package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/your-org/agentctl/internal/config"
	"github.com/your-org/agentctl/internal/execx"
	"github.com/your-org/agentctl/internal/gitx"
	"github.com/your-org/agentctl/internal/runtime"
	"github.com/your-org/agentctl/internal/state"
	"github.com/your-org/agentctl/internal/tmux"
	"github.com/your-org/agentctl/internal/tokenbroker"
)

const defaultConfigPath = "~/.config/agentctl/config.yaml"

var validRunTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type runTokenClient interface {
	CreateProjectToken(ctx context.Context, projectID, name string, expiresAt time.Time) (tokenbroker.CreatedToken, error)
	RevokeProjectToken(ctx context.Context, projectID, tokenID string) error
}

type runDeps struct {
	loadConfig          func(path string) (*config.Config, error)
	getenv              func(key string) string
	now                 func() time.Time
	prepareWorktree     func(ctx context.Context, repoPath, defaultBranch, taskID, worktree string) error
	newTokenClient      func(baseURL, controlPAT string) runTokenClient
	dockerInvocationFor func(opts runtime.DockerOptions) (runtime.DockerInvocation, error)
	startTmux           func(ctx context.Context, taskID, worktree string, command []string) error
	stopTmux            func(ctx context.Context, taskID string) error
	removeEnvFile       func(path string) error
	saveState           func(stateDir string, task state.Task) error
}

func newRunCommand() *cobra.Command {
	return newRunCommandWithDeps(defaultRunDeps())
}

func defaultRunDeps() runDeps {
	exec := execx.LocalExecutor{}
	gitService := gitx.NewService(exec)
	tmuxService := tmux.NewService(exec)

	return runDeps{
		loadConfig:      config.Load,
		getenv:          os.Getenv,
		now:             time.Now,
		prepareWorktree: gitService.PrepareWorktree,
		newTokenClient: func(baseURL, controlPAT string) runTokenClient {
			return tokenbroker.NewGitLabClient(baseURL, controlPAT, nil)
		},
		dockerInvocationFor: runtime.DockerInvocationFor,
		startTmux:           tmuxService.Start,
		stopTmux:            tmuxService.Kill,
		removeEnvFile:       cleanupEnvFile,
		saveState: func(stateDir string, task state.Task) error {
			return state.NewStore(stateDir).Save(task)
		},
	}
}

func newRunCommandWithDeps(deps runDeps) *cobra.Command {
	var repoName string
	var configPath string
	var agent string
	var risk string
	var templateName string
	var remoteName string

	cmd := &cobra.Command{
		Use:   "run TASK_ID",
		Short: "Start an agent workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := runOptions{
				taskID:       args[0],
				repoName:     repoName,
				configPath:   configPath,
				agent:        agent,
				risk:         risk,
				templateName: templateName,
				remoteName:   remoteName,
			}
			return runLocalOrRemote(cmd.Context(), deps, opts)
		},
	}

	cmd.Flags().StringVar(&repoName, "repo", "", "Repository name from config")
	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "Path to config file")
	cmd.Flags().StringVar(&agent, "agent", "codex", "Agent to run")
	cmd.Flags().StringVar(&risk, "risk", "untrusted", "Risk profile")
	cmd.Flags().StringVar(&templateName, "template", "", "Template name")
	cmd.Flags().StringVar(&remoteName, "remote", "", "Remote runner name")

	return cmd
}

type runOptions struct {
	taskID       string
	repoName     string
	configPath   string
	agent        string
	risk         string
	templateName string
	remoteName   string
}

type runRuntimeSelection struct {
	image   string
	command []string
}

type runTemplateSpec struct {
	image     string
	preflight string
}

func runLocalOrRemote(ctx context.Context, deps runDeps, opts runOptions) error {
	if err := validateRunTaskID(opts.taskID); err != nil {
		return err
	}

	if opts.remoteName != "" {
		return fmt.Errorf("remote run %q is not implemented in Task 8", opts.remoteName)
	}

	if opts.risk != "untrusted" {
		return fmt.Errorf("risk %q is not supported; only untrusted is supported", opts.risk)
	}

	if opts.repoName == "" {
		return fmt.Errorf("--repo is required for local run")
	}

	cfg, err := deps.loadConfig(opts.configPath)
	if err != nil {
		return err
	}

	repo, ok := cfg.Repos[opts.repoName]
	if !ok {
		return fmt.Errorf("repo %q not found in config", opts.repoName)
	}

	defaultBranch := strings.TrimSpace(repo.DefaultBranch)
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	runRuntime, err := runRuntimeFor(cfg, opts)
	if err != nil {
		return err
	}

	controlPAT := deps.getenv("GITLAB_CONTROL_PAT")
	if controlPAT == "" {
		return fmt.Errorf("GITLAB_CONTROL_PAT is required")
	}

	worktree := filepath.Join(cfg.BaseDir, opts.taskID)
	gitDir := filepath.Join(repo.Path, ".git")
	if err := deps.prepareWorktree(ctx, repo.Path, defaultBranch, opts.taskID, worktree); err != nil {
		return err
	}

	now := deps.now()
	tokenName := fmt.Sprintf("agent-%s-%d", opts.taskID, now.Unix())
	tokenClient := deps.newTokenClient("https://"+cfg.GitLab.Host, controlPAT)
	createdToken, err := tokenClient.CreateProjectToken(ctx, repo.ProjectID, tokenName, now.Add(24*time.Hour))
	if err != nil {
		return err
	}

	tokenCreated := true
	revokeOnFailure := func(failure error) error {
		if failure != nil && tokenCreated {
			if err := tokenClient.RevokeProjectToken(ctx, repo.ProjectID, createdToken.ID); err != nil {
				return joinCleanup(failure, "token revoke cleanup failed", err)
			}
		}
		return failure
	}

	invocation, err := deps.dockerInvocationFor(runtime.DockerOptions{
		TaskID:      opts.taskID,
		Worktree:    worktree,
		GitDir:      gitDir,
		GitLabToken: createdToken.Token,
		GitLabHost:  cfg.GitLab.Host,
		Image:       runRuntime.image,
		Command:     runRuntime.command,
	})
	if err != nil {
		return revokeOnFailure(err)
	}

	envFile, err := writeDockerEnvFile(cfg.StateDir, opts.taskID, invocation.Env)
	if err != nil {
		return revokeOnFailure(err)
	}

	tmuxCommand := wrapDockerCommandWithEnvFile(envFile, invocation.Command)
	if err := deps.startTmux(ctx, opts.taskID, worktree, tmuxCommand); err != nil {
		err = joinCleanup(err, "env file remove cleanup failed", deps.removeEnvFile(envFile))
		return revokeOnFailure(err)
	}

	task := state.Task{
		TaskID:          opts.taskID,
		Repo:            opts.repoName,
		Branch:          "agent/" + opts.taskID,
		Worktree:        worktree,
		TmuxSession:     tmux.SessionName(opts.taskID),
		ContainerName:   "agent-" + opts.taskID,
		TokenID:         createdToken.ID,
		TokenName:       tokenName,
		GitLabProjectID: repo.ProjectID,
		GitLabHost:      cfg.GitLab.Host,
		Status:          "running",
		CreatedAt:       now,
	}
	if err := deps.saveState(cfg.StateDir, task); err != nil {
		return cleanupAfterStateSaveFailure(ctx, deps, envFile, opts.taskID, tokenClient, repo.ProjectID, createdToken.ID, err)
	}

	tokenCreated = false
	return nil
}

func runRuntimeFor(cfg *config.Config, opts runOptions) (runRuntimeSelection, error) {
	templateName := strings.TrimSpace(opts.templateName)
	if templateName == "" {
		templateName = strings.TrimSpace(cfg.Templates.Default)
	}
	if templateName == "" {
		templateName = "generic"
	}

	template, ok := supportedRunTemplates()[templateName]
	if !ok {
		return runRuntimeSelection{}, fmt.Errorf("unsupported template %q; supported templates: generic, node, golang, python", templateName)
	}

	agent := strings.TrimSpace(opts.agent)
	if agent == "" {
		agent = "codex"
	}
	switch agent {
	case "codex", "shell":
	default:
		return runRuntimeSelection{}, fmt.Errorf("unsupported agent %q; supported agents: codex, shell", agent)
	}

	return runRuntimeSelection{
		image:   template.image,
		command: buildRunCommand(template, agent),
	}, nil
}

func supportedRunTemplates() map[string]runTemplateSpec {
	return map[string]runTemplateSpec{
		"generic": {
			image: "node:22-bookworm",
		},
		"node": {
			image:     "node:22-bookworm",
			preflight: "node --version",
		},
		"golang": {
			image:     "golang:1.22-bookworm",
			preflight: "go version",
		},
		"python": {
			image:     "python:3.12-bookworm",
			preflight: "python --version",
		},
	}
}

func buildRunCommand(template runTemplateSpec, agent string) []string {
	lines := []string{"set -euo pipefail"}
	if template.preflight != "" {
		lines = append(lines, template.preflight)
	}

	switch agent {
	case "shell":
		lines = append(lines, "exec bash")
	case "codex":
		lines = append(lines,
			"if command -v codex >/dev/null 2>&1; then",
			"  exec codex",
			"fi",
			`printf '%s\n' 'codex executable not found; falling back to bash' >&2`,
			"exec bash",
		)
	}

	return []string{"bash", "-lc", strings.Join(lines, "\n")}
}

func validateRunTaskID(taskID string) error {
	if !validRunTaskIDPattern.MatchString(taskID) {
		return fmt.Errorf("invalid task id: %q", taskID)
	}

	return nil
}

func cleanupAfterStateSaveFailure(ctx context.Context, deps runDeps, envFile, taskID string, tokenClient runTokenClient, projectID, tokenID string, saveErr error) error {
	errs := []error{saveErr}
	if err := deps.removeEnvFile(envFile); err != nil {
		errs = append(errs, fmt.Errorf("env file remove cleanup failed: %w", err))
	}
	if err := deps.stopTmux(ctx, taskID); err != nil {
		errs = append(errs, fmt.Errorf("tmux cleanup failed: %w", err))
	}
	if err := tokenClient.RevokeProjectToken(ctx, projectID, tokenID); err != nil {
		errs = append(errs, fmt.Errorf("token revoke cleanup failed: %w", err))
	}

	return errors.Join(errs...)
}

func joinCleanup(original error, label string, cleanupErr error) error {
	if cleanupErr == nil {
		return original
	}

	return errors.Join(original, fmt.Errorf("%s: %w", label, cleanupErr))
}

func cleanupEnvFile(envFile string) error {
	if envFile == "" {
		return nil
	}

	err := os.Remove(envFile)
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

func writeDockerEnvFile(stateDir, taskID string, env []string) (string, error) {
	envDir := filepath.Join(stateDir, "env")
	if err := os.MkdirAll(envDir, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(envDir, 0700); err != nil {
		return "", err
	}

	var lines []string
	for _, pair := range env {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return "", fmt.Errorf("invalid Docker env pair %q", pair)
		}
		lines = append(lines, "export "+key+"="+shellSingleQuote(value))
	}

	envFile := filepath.Join(envDir, taskID+".env")
	file, err := os.OpenFile(envFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(strings.Join(lines, "\n") + "\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return "", joinCleanup(errors.Join(writeErr, closeErr), "env file cleanup failed", cleanupEnvFile(envFile))
	}
	if err := os.Chmod(envFile, 0600); err != nil {
		return "", joinCleanup(err, "env file cleanup failed", cleanupEnvFile(envFile))
	}

	return envFile, nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func wrapDockerCommandWithEnvFile(envFile string, dockerCommand []string) []string {
	script := strings.Join([]string{
		"set -euo pipefail",
		`env_file="$1"`,
		"shift",
		`cleanup() { rm -f "$env_file"; }`,
		"trap cleanup EXIT",
		"set -a",
		`. "$env_file"`,
		"set +a",
		`rm -f "$env_file"`,
		"trap - EXIT",
		`exec "$@"`,
	}, "\n")

	command := []string{"bash", "-lc", script, "--", envFile}
	command = append(command, dockerCommand...)
	return command
}
