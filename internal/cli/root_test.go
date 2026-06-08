package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRootCommandHasExpectedSubcommands(t *testing.T) {
	cmd := NewRootCommand()
	if !cmd.SilenceUsage {
		t.Fatal("root command should silence usage for runtime errors")
	}

	names := map[string]bool{}
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}

	for _, want := range []string{"run", "dev", "shell", "attach", "detach", "cleanup", "list", "version", "token-broker"} {
		if !names[want] {
			t.Fatalf("missing subcommand %q", want)
		}
	}
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	cmd := newRootCommandWithDeps(newRunFakes(testRunConfig(t.TempDir()), time.Now()).deps())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if got := out.String(); !strings.Contains(got, "agentctl dev") {
		t.Fatalf("version output = %q, want dev version", got)
	}
}

func TestRootVersionFlagPrintsVersion(t *testing.T) {
	cmd := newRootCommandWithDeps(newRunFakes(testRunConfig(t.TempDir()), time.Now()).deps())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if got := out.String(); !strings.HasPrefix(got, "agentctl dev ") {
		t.Fatalf("version output = %q, want direct version string", got)
	}
}

func TestRootAgentFlagRunsGeneratedTaskID(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	hostHome := createAgentAuthFixtures(t, tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.userHome = hostHome

	cmd := newRootCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--agent", "claude", "--repo", "backend", "--detach", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	opts := singleDockerOptions(t, fakes)
	if opts.TaskID != "claude-1780662896" {
		t.Fatalf("task id = %q, want generated claude task id", opts.TaskID)
	}
	assertRunCommandContains(t, opts.Command, "command -v claude", "exec claude")
	if got, want := opts.Mounts, wantAgentAuthMounts(hostHome); !reflect.DeepEqual(got, want) {
		t.Fatalf("docker mounts = %#v, want claude/codex auth mounts %#v", got, want)
	}
	if len(fakes.savedTasks) != 1 || fakes.savedTasks[0].TaskID != "claude-1780662896" {
		t.Fatalf("saved tasks = %#v, want generated task id", fakes.savedTasks)
	}
}

func TestRootAgentNoTmuxStartsDetachedDockerWithClaudeCreds(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	hostHome := createAgentAuthFixtures(t, tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.userHome = hostHome
	fakes.tmuxAvailable = true

	cmd := newRootCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--agent", "claude", "--repo", "backend", "--no-tmux", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.tmuxStarts) != 0 {
		t.Fatalf("tmux starts = %#v, want none with --no-tmux", fakes.tmuxStarts)
	}
	opts := singleDockerOptions(t, fakes)
	if !opts.Detached {
		t.Fatalf("docker opts = %#v, want detached direct Docker", opts)
	}
	if got, want := opts.Mounts, wantAgentAuthMounts(hostHome); !reflect.DeepEqual(got, want) {
		t.Fatalf("docker mounts = %#v, want claude/codex auth mounts %#v", got, want)
	}
	if len(fakes.detachedDockerStarts) != 1 {
		t.Fatalf("docker starts = %#v, want direct Docker start", fakes.detachedDockerStarts)
	}
	if len(fakes.containerAttachCalls) != 1 || fakes.containerAttachCalls[0] != "agent-claude-1780662896" {
		t.Fatalf("docker attach calls = %#v, want auto attach to direct Docker", fakes.containerAttachCalls)
	}
}
