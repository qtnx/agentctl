package cli

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/your-org/agentctl/internal/config"
	"github.com/your-org/agentctl/internal/execx"
	"github.com/your-org/agentctl/internal/state"
	"github.com/your-org/agentctl/internal/tmux"
)

type sessionDeps struct {
	loadConfig func(path string) (*config.Config, error)
	loadTask   func(stateDir, taskID string) (state.Task, error)
	attach     func(ctx context.Context, taskID string) error
	detach     func(ctx context.Context, taskID string) error
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

	return sessionDeps{
		loadConfig: config.Load,
		loadTask: func(stateDir, taskID string) (state.Task, error) {
			return state.NewStore(stateDir).Load(taskID)
		},
		attach: tmuxService.Attach,
		detach: tmuxService.Detach,
	}
}

func newShellCommandWithDeps(deps sessionDeps) *cobra.Command {
	return newSessionCommandWithDeps("shell TASK_ID", "Open a shell in an agent session", deps, deps.attach)
}

func newAttachCommandWithDeps(deps sessionDeps) *cobra.Command {
	return newSessionCommandWithDeps("attach TASK_ID", "Attach to an agent tmux session", deps, deps.attach)
}

func newDetachCommandWithDeps(deps sessionDeps) *cobra.Command {
	return newSessionCommandWithDeps("detach TASK_ID", "Detach from an agent tmux session", deps, deps.detach)
}

func newSessionCommandWithDeps(use, short string, deps sessionDeps, tmuxAction func(context.Context, string) error) *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := args[0]
			if err := validateRunTaskID(taskID); err != nil {
				return err
			}

			cfg, err := deps.loadConfig(configPath)
			if err != nil {
				return err
			}

			task, err := deps.loadTask(cfg.StateDir, taskID)
			if err != nil {
				return err
			}
			if task.TaskID != "" {
				taskID = task.TaskID
			}

			return tmuxAction(cmd.Context(), taskID)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "Path to config file")
	return cmd
}
