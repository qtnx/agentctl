package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	cleanuppkg "github.com/your-org/agentctl/internal/cleanup"
	"github.com/your-org/agentctl/internal/config"
	"github.com/your-org/agentctl/internal/execx"
	"github.com/your-org/agentctl/internal/gitx"
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
	exec := execx.LocalExecutor{}
	gitService := gitx.NewService(exec)
	tmuxService := tmux.NewService(exec)
	remoteService := remote.NewService(exec)
	service := cleanuppkg.NewService(cleanuppkg.Deps{
		RevokeToken: func(ctx context.Context, baseURL, controlPAT, projectID, tokenID string) error {
			return tokenbroker.NewGitLabClient(baseURL, controlPAT, nil).RevokeProjectToken(ctx, projectID, tokenID)
		},
		RemoveContainer: func(ctx context.Context, containerName string) error {
			return exec.Run(ctx, "docker", "rm", "-f", containerName)
		},
		KillSession:    tmuxService.Kill,
		RemoveWorktree: gitService.RemoveWorktree,
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
		return deps.forwardRemote(ctx, target, false, "cleanup", taskID, "--config", configPath)
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
