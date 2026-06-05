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

func newTokenBrokerCommand() *cobra.Command {
	return &cobra.Command{Use: "token-broker", Short: "Manage short-lived agent tokens"}
}
