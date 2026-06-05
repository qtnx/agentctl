package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/your-org/agentctl/internal/config"
	"github.com/your-org/agentctl/internal/execx"
	"github.com/your-org/agentctl/internal/remote"
	"github.com/your-org/agentctl/internal/state"
	"github.com/your-org/agentctl/internal/tmux"
)

type sessionDeps struct {
	loadConfig    func(path string) (*config.Config, error)
	loadTask      func(stateDir, taskID string) (state.Task, error)
	attach        func(ctx context.Context, taskID string) error
	detach        func(ctx context.Context, taskID string) error
	forwardRemote func(ctx context.Context, target remote.Target, interactive bool, args ...string) error
}

func newShellCommand() *cobra.Command {
	return newShellCommandWithDeps(defaultSessionDeps())
}

func newAttachCommand() *cobra.Command {
	return newAttachCommandWithDeps(defaultSessionDeps())
}

func newDetachCommand() *cobra.Command {
	return newDetachCommandWithDeps(defaultSessionDeps())
}

func defaultSessionDeps() sessionDeps {
	localExec := execx.LocalExecutor{}
	tmuxService := tmux.NewService(localExec)
	remoteService := remote.NewService(localExec)

	return sessionDeps{
		loadConfig: config.Load,
		loadTask: func(stateDir, taskID string) (state.Task, error) {
			return state.NewStore(stateDir).Load(taskID)
		},
		attach:        tmuxService.Attach,
		detach:        tmuxService.Detach,
		forwardRemote: remoteService.Run,
	}
}

func newShellCommandWithDeps(deps sessionDeps) *cobra.Command {
	return newSessionCommandWithDeps("shell TASK_ID", "Open a shell in an agent session", "shell", true, deps, deps.attach)
}

func newAttachCommandWithDeps(deps sessionDeps) *cobra.Command {
	return newSessionCommandWithDeps("attach TASK_ID", "Attach to an agent tmux session", "attach", true, deps, deps.attach)
}

func newDetachCommandWithDeps(deps sessionDeps) *cobra.Command {
	return newSessionCommandWithDeps("detach TASK_ID", "Detach from an agent tmux session", "detach", false, deps, deps.detach)
}

func newSessionCommandWithDeps(use, short, remoteCommand string, remoteInteractive bool, deps sessionDeps, tmuxAction func(context.Context, string) error) *cobra.Command {
	var configPath string
	var remoteName string

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			requestedTaskID := args[0]
			if err := validateRunTaskID(requestedTaskID); err != nil {
				return err
			}

			if remoteName != "" {
				cfg, err := deps.loadConfig(configPath)
				if err != nil {
					return err
				}
				target, err := remoteTargetFromConfig(cfg, remoteName)
				if err != nil {
					return err
				}
				return deps.forwardRemote(cmd.Context(), target, remoteInteractive, remoteCommand, requestedTaskID, "--config", configPath)
			}

			cfg, err := deps.loadConfig(configPath)
			if err != nil {
				return err
			}

			task, err := deps.loadTask(cfg.StateDir, requestedTaskID)
			if err != nil {
				return err
			}
			if task.TaskID != "" && task.TaskID != requestedTaskID {
				return fmt.Errorf("state task id %q does not match requested task id %q", task.TaskID, requestedTaskID)
			}
			expectedSession := tmux.SessionName(requestedTaskID)
			if task.TmuxSession != expectedSession {
				return fmt.Errorf("state tmux session %q does not match expected %q", task.TmuxSession, expectedSession)
			}

			return tmuxAction(cmd.Context(), requestedTaskID)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "Path to config file")
	cmd.Flags().StringVar(&remoteName, "remote", "", "Remote runner name")
	return cmd
}
