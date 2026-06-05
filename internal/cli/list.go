package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/your-org/agentctl/internal/config"
	"github.com/your-org/agentctl/internal/state"
)

type listDeps struct {
	loadConfig func(path string) (*config.Config, error)
	listTasks  func(stateDir string) ([]state.Task, error)
}

func newListCommand() *cobra.Command {
	return newListCommandWithDeps(defaultListDeps())
}

func defaultListDeps() listDeps {
	return listDeps{
		loadConfig: config.Load,
		listTasks: func(stateDir string) ([]state.Task, error) {
			return state.NewStore(stateDir).List()
		},
	}
}

func newListCommandWithDeps(deps listDeps) *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agent workspaces",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := deps.loadConfig(configPath)
			if err != nil {
				return err
			}

			tasks, err := deps.listTasks(cfg.StateDir)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "TASK_ID\tREPO\tSTATUS\tWORKTREE\tTMUX_SESSION"); err != nil {
				return err
			}
			for _, task := range tasks {
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", task.TaskID, task.Repo, task.Status, task.Worktree, task.TmuxSession); err != nil {
					return err
				}
			}

			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "Path to config file")
	return cmd
}
