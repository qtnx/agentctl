package runtime

import (
	"strings"
	"testing"
)

func TestMacOSSandboxInvocationBuildsSandboxExecCommand(t *testing.T) {
	invocation, err := MacOSSandboxInvocationFor(MacOSSandboxOptions{
		TaskID:   "XL-123",
		Worktree: "/tmp/worktree",
		StateDir: "/tmp/state",
		HomeDir:  "/tmp/state/homes/XL-123",
		Command:  []string{"bash"},
	})
	if err != nil {
		t.Fatal(err)
	}

	args := invocation.Command
	if len(args) < 2 || args[0] != "sandbox-exec" || args[1] != "-p" {
		t.Fatalf("command starts with %#v, want sandbox-exec -p", args[:min(len(args), 2)])
	}
	for _, want := range [][]string{
		{"-D", "WORKSPACE=/tmp/worktree"},
		{"-D", "HOME=/tmp/state/homes/XL-123"},
		{"-D", "STATE_DIR=/tmp/state"},
		{"env", "HOME=/tmp/state/homes/XL-123"},
		{"WORKSPACE=/tmp/worktree"},
		{"bash", "-c"},
	} {
		if !containsSequence(args, want) {
			t.Fatalf("command = %#v, want sequence %#v", args, want)
		}
	}
	if containsSequence(args, []string{"bash", "-lc"}) {
		t.Fatalf("command = %#v, must not run macOS sandbox shell as login shell", args)
	}

	commandText := strings.Join(args, "\n")
	for _, want := range []string{
		"(allow default)",
		"(deny file-read*)",
		"(deny file-write*)",
		"(allow network*)",
		`(allow file-read*`,
		`(allow file-write*`,
		`(subpath (param "WORKSPACE"))`,
		`(subpath (param "HOME"))`,
		`(subpath (param "STATE_DIR"))`,
		`(literal "/dev/null")`,
		"GIT_ASKPASS",
		`"$@"`,
		"staying inside macOS sandbox shell",
		"exec zsh -l",
		"exec bash -l",
	} {
		if !strings.Contains(commandText, want) {
			t.Fatalf("command = %#v, want %q", args, want)
		}
	}
}

func TestMacOSSandboxInvocationUsesStrictAllowlistsAndTaskEnv(t *testing.T) {
	invocation, err := MacOSSandboxInvocationFor(MacOSSandboxOptions{
		TaskID:   "XL-123",
		Worktree: "/repo/worktree",
		StateDir: "/tmp/state",
		HomeDir:  "/tmp/state/homes/XL-123",
		Command:  []string{"go", "test", "./..."},
		Mode:     "strict",
		Network:  false,
		AllowRead: []string{
			"/bin",
			"/usr",
			"/opt/homebrew",
		},
		AllowWrite: []string{
			"/repo/worktree",
			"/tmp/state/homes/XL-123",
		},
		DenyRead: []string{
			"/Users/qqq/.ssh",
		},
		Env: map[string]string{
			"GOCACHE":          "${TASK_HOME}/.cache/go-build",
			"npm_config_cache": "${TASK_HOME}/.npm",
			"CARGO_HOME":       "${TASK_HOME}/.cargo",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	commandText := strings.Join(invocation.Command, "\n")
	for _, want := range []string{
		"(allow default)",
		"(deny file-read*)",
		"(deny file-write*)",
		`(allow file-read*`,
		`(subpath "/bin")`,
		`(subpath "/usr")`,
		`(subpath "/opt/homebrew")`,
		`(subpath (param "WORKSPACE"))`,
		`(subpath (param "HOME"))`,
		`(literal "/")`,
		`(deny file-read*`,
		`(subpath "/Users/qqq/.ssh")`,
		"GOCACHE=/tmp/state/homes/XL-123/.cache/go-build",
		"npm_config_cache=/tmp/state/homes/XL-123/.npm",
		"CARGO_HOME=/tmp/state/homes/XL-123/.cargo",
	} {
		if !strings.Contains(commandText, want) {
			t.Fatalf("command = %#v, want %q", invocation.Command, want)
		}
	}
	if strings.Contains(commandText, "(allow network*)") {
		t.Fatalf("command = %#v, want network disabled", invocation.Command)
	}
}

func TestMacOSSandboxInvocationAllowsAncestorDirectoriesForStrictPaths(t *testing.T) {
	invocation, err := MacOSSandboxInvocationFor(MacOSSandboxOptions{
		TaskID:   "XL-123",
		Worktree: "/Users/qqq/code/project",
		StateDir: "/Users/qqq/.local/state/agentctl",
		HomeDir:  "/Users/qqq/.local/state/agentctl/homes/XL-123",
		Command:  []string{"/bin/echo", "ok"},
		Mode:     "strict",
		Network:  true,
		AllowRead: []string{
			"/bin",
			"/usr",
			"/var/db/xcode_select_link",
		},
		AllowWrite: []string{
			"/Users/qqq/code/project",
			"/Users/qqq/.local/state/agentctl/homes/XL-123",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	commandText := strings.Join(invocation.Command, "\n")
	for _, want := range []string{
		`(literal "/")`,
		`(literal "/Users")`,
		`(literal "/Users/qqq")`,
		`(literal "/Users/qqq/code")`,
		`(literal "/Users/qqq/.local")`,
		`(literal "/Users/qqq/.local/state")`,
		`(literal "/var/db/xcode_select_link")`,
	} {
		if !strings.Contains(commandText, want) {
			t.Fatalf("command = %#v, want ancestor literal %q", invocation.Command, want)
		}
	}
}

func TestMacOSSandboxInvocationSupportsWriteOnlyCompatibilityMode(t *testing.T) {
	invocation, err := MacOSSandboxInvocationFor(MacOSSandboxOptions{
		TaskID:   "XL-123",
		Worktree: "/tmp/worktree",
		StateDir: "/tmp/state",
		HomeDir:  "/tmp/state/homes/XL-123",
		Command:  []string{"bash"},
		Mode:     "write_only",
		Network:  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	commandText := strings.Join(invocation.Command, "\n")
	if strings.Contains(commandText, "(deny file-read*)") {
		t.Fatalf("command = %#v, want write-only mode to allow reads", invocation.Command)
	}
	for _, want := range []string{"(allow default)", "(deny file-write*)", "(allow network*)"} {
		if !strings.Contains(commandText, want) {
			t.Fatalf("command = %#v, want %q", invocation.Command, want)
		}
	}
}

func TestMacOSSandboxInvocationCanExitAfterCommand(t *testing.T) {
	invocation, err := MacOSSandboxInvocationFor(MacOSSandboxOptions{
		TaskID:           "XL-123",
		Worktree:         "/tmp/worktree",
		StateDir:         "/tmp/state",
		HomeDir:          "/tmp/state/homes/XL-123",
		Command:          []string{"pnpm", "dev"},
		ExitAfterCommand: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	commandText := strings.Join(invocation.Command, "\n")
	if strings.Contains(commandText, "staying inside macOS sandbox shell") || strings.Contains(commandText, `"${SHELL:-/bin/sh}"`) {
		t.Fatalf("command = %#v, want no follow-up sandbox shell", invocation.Command)
	}
	if !strings.Contains(commandText, `exit "$rc"`) {
		t.Fatalf("command = %#v, want exit with command status", invocation.Command)
	}
}

func TestMacOSSandboxInvocationRejectsMissingRequiredOptions(t *testing.T) {
	tests := []struct {
		name   string
		update func(*MacOSSandboxOptions)
		want   string
	}{
		{
			name: "task id",
			update: func(opts *MacOSSandboxOptions) {
				opts.TaskID = ""
			},
			want: "TaskID",
		},
		{
			name: "worktree",
			update: func(opts *MacOSSandboxOptions) {
				opts.Worktree = ""
			},
			want: "Worktree",
		},
		{
			name: "home dir",
			update: func(opts *MacOSSandboxOptions) {
				opts.HomeDir = ""
			},
			want: "HomeDir",
		},
		{
			name: "state dir",
			update: func(opts *MacOSSandboxOptions) {
				opts.StateDir = ""
			},
			want: "StateDir",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := MacOSSandboxOptions{
				TaskID:   "XL-123",
				Worktree: "/tmp/worktree",
				StateDir: "/tmp/state",
				HomeDir:  "/tmp/state/homes/XL-123",
				Command:  []string{"bash"},
			}
			tt.update(&opts)

			_, err := MacOSSandboxInvocationFor(opts)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
