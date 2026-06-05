package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	cleanuppkg "github.com/your-org/agentctl/internal/cleanup"
	"github.com/your-org/agentctl/internal/config"
	"github.com/your-org/agentctl/internal/execx"
	"github.com/your-org/agentctl/internal/remote"
	"github.com/your-org/agentctl/internal/state"
	"github.com/your-org/agentctl/internal/tmux"
	"github.com/your-org/agentctl/internal/tokenbroker"
)

type cleanupDeps struct {
	loadConfig    func(path string) (*config.Config, error)
	getenv        func(key string) string
	loadTask      func(stateDir, taskID string) (state.Task, error)
	runCleanup    func(ctx context.Context, req cleanuppkg.Request) error
	forwardRemote func(ctx context.Context, target remote.Target, interactive bool, args ...string) error
}

func newCleanupCommand() *cobra.Command {
	return newCleanupCommandWithDeps(defaultCleanupDeps())
}

func defaultCleanupDeps() cleanupDeps {
	localExec := execx.LocalExecutor{}
	commandRunner := localCleanupCommandRunner
	remoteService := remote.NewService(localExec)
	service := cleanuppkg.NewService(cleanuppkg.Deps{
		RevokeToken: func(ctx context.Context, baseURL, controlPAT, projectID, tokenID string) error {
			return tokenbroker.NewGitLabClient(baseURL, controlPAT, nil).RevokeProjectToken(ctx, projectID, tokenID)
		},
		RemoveContainer: func(ctx context.Context, containerName string) error {
			return removeDockerContainer(ctx, commandRunner, containerName)
		},
		KillSession: func(ctx context.Context, taskID string) error {
			return killTmuxSession(ctx, commandRunner, taskID)
		},
		RemoveWorktree: func(ctx context.Context, repoPath, worktree string) error {
			return removeGitWorktree(ctx, commandRunner, repoPath, worktree)
		},
		DeleteState: func(stateDir, taskID string) error {
			return state.NewStore(stateDir).Delete(taskID)
		},
	})

	return cleanupDeps{
		loadConfig: config.Load,
		getenv:     os.Getenv,
		loadTask: func(stateDir, taskID string) (state.Task, error) {
			return state.NewStore(stateDir).Load(taskID)
		},
		runCleanup:    service.Cleanup,
		forwardRemote: remoteService.Run,
	}
}

type cleanupCommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

func localCleanupCommandRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func removeDockerContainer(ctx context.Context, runner cleanupCommandRunner, containerName string) error {
	args := []string{"rm", "-f", containerName}
	output, err := runner(ctx, "docker", args...)
	if err == nil {
		return nil
	}
	if outputContains(output, "no such container") {
		return cleanuppkg.ErrAlreadyRemoved
	}
	return cleanupCommandError("docker", args, output, err)
}

func killTmuxSession(ctx context.Context, runner cleanupCommandRunner, taskID string) error {
	args := []string{"kill-session", "-t", tmux.SessionName(taskID)}
	output, err := runner(ctx, "tmux", args...)
	if err == nil {
		return nil
	}
	if outputContains(output, "can't find session") || outputContains(output, "no server running") {
		return cleanuppkg.ErrAlreadyRemoved
	}
	return cleanupCommandError("tmux", args, output, err)
}

func removeGitWorktree(ctx context.Context, runner cleanupCommandRunner, repoPath, worktree string) error {
	args := []string{"-C", repoPath, "worktree", "remove", worktree, "--force"}
	output, err := runner(ctx, "git", args...)
	if err == nil {
		return nil
	}
	if outputContains(output, "not a working tree") || outputContains(output, "not a worktree") {
		return cleanuppkg.ErrAlreadyRemoved
	}
	return cleanupCommandError("git", args, output, err)
}

func outputContains(output []byte, needle string) bool {
	return strings.Contains(strings.ToLower(string(output)), needle)
}

func cleanupCommandError(name string, args []string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}
	return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, message)
}

func newCleanupCommandWithDeps(deps cleanupDeps) *cobra.Command {
	var configPath string
	var remoteName string

	cmd := &cobra.Command{
		Use:   "cleanup TASK_ID",
		Short: "Clean up an agent workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cleanupTask(cmd.Context(), deps, args[0], configPath, remoteName)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "Path to config file")
	cmd.Flags().StringVar(&remoteName, "remote", "", "Remote runner name")
	return cmd
}

func cleanupTask(ctx context.Context, deps cleanupDeps, taskID, configPath, remoteName string) error {
	if err := validateRunTaskID(taskID); err != nil {
		return err
	}

	cfg, err := deps.loadConfig(configPath)
	if err != nil {
		return err
	}

	if remoteName != "" {
		target, err := remoteTargetFromConfig(cfg, remoteName)
		if err != nil {
			return err
		}
		args := appendRemoteConfigArgs([]string{"cleanup", taskID}, remoteConfigPathFromConfig(cfg, remoteName))
		return deps.forwardRemote(ctx, target, false, args...)
	}

	task, err := deps.loadTask(cfg.StateDir, taskID)
	if err != nil {
		return err
	}

	var repoPath string
	var repoLookupErr error
	if repo, ok := cfg.Repos[task.Repo]; ok {
		repoPath = repo.Path
		if task.GitLabProjectID == "" {
			task.GitLabProjectID = repo.ProjectID
		}
	} else {
		repoLookupErr = fmt.Errorf("repo %q not found in config", task.Repo)
	}

	host := strings.TrimSpace(task.GitLabHost)
	if host == "" {
		host = strings.TrimSpace(cfg.GitLab.Host)
	}

	return deps.runCleanup(ctx, cleanuppkg.Request{
		TaskID:              taskID,
		Task:                task,
		RepoPath:            repoPath,
		StateDir:            cfg.StateDir,
		GitLabBaseURL:       "https://" + host,
		ControlPAT:          deps.getenv("GITLAB_CONTROL_PAT"),
		ExpectedTmuxSession: tmux.SessionName(taskID),
		ExpectedWorktree:    filepath.Join(cfg.BaseDir, taskID),
		RepoLookupError:     repoLookupErr,
	})
}
