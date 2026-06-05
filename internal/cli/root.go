package cli

import "github.com/spf13/cobra"

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agentctl",
		Short: "Spawn isolated agent workspaces",
	}

	for _, sub := range []*cobra.Command{
		newRunCommand(),
		newShellCommand(),
		newAttachCommand(),
		newDetachCommand(),
		newCleanupCommand(),
		newListCommand(),
		newTokenBrokerCommand(),
	} {
		cmd.AddCommand(sub)
	}

	return cmd
}

func newRunCommand() *cobra.Command {
	return &cobra.Command{Use: "run TASK_ID", Short: "Start an agent workspace"}
}

func newShellCommand() *cobra.Command {
	return &cobra.Command{Use: "shell TASK_ID", Short: "Open a shell in an agent session"}
}

func newAttachCommand() *cobra.Command {
	return &cobra.Command{Use: "attach TASK_ID", Short: "Attach to an agent tmux session"}
}

func newDetachCommand() *cobra.Command {
	return &cobra.Command{Use: "detach TASK_ID", Short: "Detach from an agent tmux session"}
}

func newCleanupCommand() *cobra.Command {
	return &cobra.Command{Use: "cleanup TASK_ID", Short: "Clean up an agent workspace"}
}

func newListCommand() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List agent workspaces"}
}

func newTokenBrokerCommand() *cobra.Command {
	return &cobra.Command{Use: "token-broker", Short: "Manage short-lived agent tokens"}
}
