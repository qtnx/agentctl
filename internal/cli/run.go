package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func runLocalOrRemote(ctx context.Context, deps runDeps, opts runOptions) error {
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

	worktree := filepath.Join(cfg.BaseDir, opts.taskID)
	gitDir := filepath.Join(repo.Path, ".git")
	if err := deps.prepareWorktree(ctx, repo.Path, defaultBranch, opts.taskID, worktree); err != nil {
		return err
	}

	controlPAT := deps.getenv("GITLAB_CONTROL_PAT")
	if controlPAT == "" {
		return fmt.Errorf("GITLAB_CONTROL_PAT is required")
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
			_ = tokenClient.RevokeProjectToken(ctx, repo.ProjectID, createdToken.ID)
		}
		return failure
	}

	invocation, err := deps.dockerInvocationFor(runtime.DockerOptions{
		TaskID:      opts.taskID,
		Worktree:    worktree,
		GitDir:      gitDir,
		GitLabToken: createdToken.Token,
		GitLabHost:  cfg.GitLab.Host,
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
		_ = os.Remove(envFile)
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
		return revokeOnFailure(err)
	}

	tokenCreated = false
	return nil
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
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Chmod(envFile, 0600); err != nil {
		return "", err
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
