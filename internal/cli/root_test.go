package cli

import "testing"

func TestRootCommandHasExpectedSubcommands(t *testing.T) {
	cmd := NewRootCommand()
	names := map[string]bool{}
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}

	for _, want := range []string{"run", "shell", "attach", "detach", "cleanup", "list", "token-broker"} {
		if !names[want] {
			t.Fatalf("missing subcommand %q", want)
		}
	}
}
