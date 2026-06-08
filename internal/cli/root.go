package cli

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/qtnx/agentctl/internal/version"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	return newRootCommandWithDeps(defaultRunDeps())
}

func newRootCommandWithDeps(runDeps runDeps) *cobra.Command {
	var repoName string
	var configPath string
	var agent string
	var risk string
	var templateName string
	var remoteName string
	var detach bool
	var noTmux bool

	cmd := &cobra.Command{
		Use:          "agentctl",
		Short:        "Spawn isolated agent workspaces",
		SilenceUsage: true,
		Version:      version.String(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("agent") {
				return cmd.Help()
			}
			if len(args) > 1 {
				return fmt.Errorf("expected at most one task id, got %d", len(args))
			}
			taskID := ""
			if len(args) == 1 {
				taskID = args[0]
			} else {
				now := time.Now
				if runDeps.now != nil {
					now = runDeps.now
				}
				taskID = generatedRunTaskID(agent, now())
			}
			opts := runOptions{
				taskID:       taskID,
				repoName:     repoName,
				configPath:   configPath,
				agent:        agent,
				risk:         risk,
				templateName: templateName,
				remoteName:   remoteName,
				detach:       detach,
				noTmux:       noTmux,
				out:          cmd.OutOrStdout(),
			}
			return runLocalOrRemote(cmd.Context(), runDeps, opts)
		},
	}

	cmd.Flags().StringVar(&repoName, "repo", "", "Repository name from config or GitLab repo URL")
	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "Path to config file")
	cmd.Flags().StringVar(&agent, "agent", "codex", "Agent to run")
	cmd.Flags().StringVar(&risk, "risk", "untrusted", "Risk profile")
	cmd.Flags().StringVar(&templateName, "template", "", "Template name")
	cmd.Flags().StringVar(&remoteName, "remote", "", "Remote runner name")
	cmd.Flags().BoolVar(&detach, "detach", false, "Start the session without attaching")
	cmd.Flags().BoolVar(&noTmux, "no-tmux", false, "Start Docker directly without tmux")
	cmd.SetVersionTemplate("{{.Version}}\n")

	for _, sub := range []*cobra.Command{
		newRunCommandWithDeps(runDeps),
		newDevCommand(),
		newShellCommand(),
		newAttachCommand(),
		newDetachCommand(),
		newCleanupCommand(),
		newListCommand(),
		newVersionCommand(),
		newTokenBrokerCommand(),
	} {
		cmd.AddCommand(sub)
	}

	return cmd
}

func generatedRunTaskID(agent string, now time.Time) string {
	name := strings.ToLower(strings.TrimSpace(agent))
	if name == "" {
		name = "agent"
	}
	var cleaned strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '-' {
			cleaned.WriteRune(r)
			continue
		}
		cleaned.WriteByte('-')
	}
	name = cleaned.String()
	name = strings.Trim(name, ".-_")
	if name == "" {
		name = "agent"
	}
	return fmt.Sprintf("%s-%d", name, now.Unix())
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print agentctl version",
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), version.String())
		},
	}
}

func newTokenBrokerCommand() *cobra.Command {
	return &cobra.Command{Use: "token-broker", Short: "Manage short-lived agent tokens"}
}
