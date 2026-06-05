package cli

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/your-org/agentctl/internal/config"
	"github.com/your-org/agentctl/internal/remote"
	"github.com/your-org/agentctl/internal/state"
)

func TestAttachLoadsStateAndCallsTmuxAttach(t *testing.T) {
	cfg := &config.Config{StateDir: "/tmp/agentctl-state"}
	fakes := &sessionFakes{
		cfg: cfg,
		tasks: map[string]state.Task{
			"XL-123": {
				TaskID:      "XL-123",
				TmuxSession: "agentctl-XL-123",
			},
		},
	}

	cmd := newAttachCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--config", "/tmp/config.yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{"/tmp/config.yaml"}) {
		t.Fatalf("config paths = %#v, want custom config path", fakes.loadConfigPaths)
	}
	if !reflect.DeepEqual(fakes.loadTaskCalls, []loadTaskCall{{stateDir: cfg.StateDir, taskID: "XL-123"}}) {
		t.Fatalf("load task calls = %#v, want configured state dir and task id", fakes.loadTaskCalls)
	}
	if !reflect.DeepEqual(fakes.attachCalls, []string{"XL-123"}) {
		t.Fatalf("attach calls = %#v, want task id", fakes.attachCalls)
	}
	if len(fakes.detachCalls) != 0 {
		t.Fatalf("detach calls = %#v, want none", fakes.detachCalls)
	}
}

func TestShellBehavesLikeAttach(t *testing.T) {
	fakes := &sessionFakes{
		cfg: &config.Config{StateDir: "/tmp/agentctl-state"},
		tasks: map[string]state.Task{
			"XL-123": {TaskID: "XL-123", TmuxSession: "agentctl-XL-123"},
		},
	}

	cmd := newShellCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.attachCalls, []string{"XL-123"}) {
		t.Fatalf("attach calls = %#v, want shell to attach", fakes.attachCalls)
	}
	if len(fakes.detachCalls) != 0 {
		t.Fatalf("detach calls = %#v, want none", fakes.detachCalls)
	}
}

func TestDetachLoadsStateAndCallsTmuxDetach(t *testing.T) {
	cfg := &config.Config{StateDir: "/tmp/agentctl-state"}
	fakes := &sessionFakes{
		cfg: cfg,
		tasks: map[string]state.Task{
			"XL-123": {
				TaskID:      "XL-123",
				TmuxSession: "agentctl-XL-123",
			},
		},
	}

	cmd := newDetachCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--config", "/tmp/config.yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{"/tmp/config.yaml"}) {
		t.Fatalf("config paths = %#v, want custom config path", fakes.loadConfigPaths)
	}
	if !reflect.DeepEqual(fakes.loadTaskCalls, []loadTaskCall{{stateDir: cfg.StateDir, taskID: "XL-123"}}) {
		t.Fatalf("load task calls = %#v, want configured state dir and task id", fakes.loadTaskCalls)
	}
	if !reflect.DeepEqual(fakes.detachCalls, []string{"XL-123"}) {
		t.Fatalf("detach calls = %#v, want task id", fakes.detachCalls)
	}
	if len(fakes.attachCalls) != 0 {
		t.Fatalf("attach calls = %#v, want none", fakes.attachCalls)
	}
}

func TestAttachMissingStateErrorsWithoutTmuxCall(t *testing.T) {
	missingErr := errors.New("task state missing")
	fakes := &sessionFakes{
		cfg:     &config.Config{StateDir: "/tmp/agentctl-state"},
		loadErr: missingErr,
	}

	cmd := newAttachCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123"})

	err := cmd.Execute()
	if !errors.Is(err, missingErr) {
		t.Fatalf("error = %v, want %v", err, missingErr)
	}
	if len(fakes.attachCalls) != 0 {
		t.Fatalf("attach calls = %#v, want none", fakes.attachCalls)
	}
	if len(fakes.detachCalls) != 0 {
		t.Fatalf("detach calls = %#v, want none", fakes.detachCalls)
	}
}

func TestAttachShellDetachRejectInvalidTaskIDBeforeTmuxCall(t *testing.T) {
	tests := []struct {
		name string
		cmd  func(sessionDeps) *cobra.Command
	}{
		{name: "attach", cmd: newAttachCommandWithDeps},
		{name: "shell", cmd: newShellCommandWithDeps},
		{name: "detach", cmd: newDetachCommandWithDeps},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakes := &sessionFakes{cfg: &config.Config{StateDir: "/tmp/agentctl-state"}}
			cmd := tt.cmd(fakes.deps())
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"../unsafe"})

			err := cmd.Execute()
			if err == nil {
				t.Fatal("error = nil, want invalid task id error")
			}
			if !strings.Contains(err.Error(), "invalid task id") {
				t.Fatalf("error = %v, want invalid task id message", err)
			}
			if len(fakes.loadConfigPaths) != 0 {
				t.Fatalf("config paths = %#v, want no config load before validation", fakes.loadConfigPaths)
			}
			if len(fakes.loadTaskCalls) != 0 {
				t.Fatalf("load task calls = %#v, want none", fakes.loadTaskCalls)
			}
			if len(fakes.attachCalls) != 0 {
				t.Fatalf("attach calls = %#v, want none", fakes.attachCalls)
			}
			if len(fakes.detachCalls) != 0 {
				t.Fatalf("detach calls = %#v, want none", fakes.detachCalls)
			}
		})
	}
}

func TestAttachShellDetachRejectMismatchedSavedTaskIDBeforeTmuxCall(t *testing.T) {
	tests := []struct {
		name string
		cmd  func(sessionDeps) *cobra.Command
	}{
		{name: "attach", cmd: newAttachCommandWithDeps},
		{name: "shell", cmd: newShellCommandWithDeps},
		{name: "detach", cmd: newDetachCommandWithDeps},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakes := &sessionFakes{
				cfg: &config.Config{StateDir: "/tmp/agentctl-state"},
				tasks: map[string]state.Task{
					"XL-123": {
						TaskID:      "OTHER-123",
						TmuxSession: "agentctl-XL-123",
					},
				},
			}
			cmd := tt.cmd(fakes.deps())
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"XL-123"})

			err := cmd.Execute()
			if err == nil {
				t.Fatal("error = nil, want task id mismatch error")
			}
			if want := `state task id "OTHER-123" does not match requested task id "XL-123"`; !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
			if len(fakes.attachCalls) != 0 {
				t.Fatalf("attach calls = %#v, want none", fakes.attachCalls)
			}
			if len(fakes.detachCalls) != 0 {
				t.Fatalf("detach calls = %#v, want none", fakes.detachCalls)
			}
		})
	}
}

func TestAttachShellDetachRejectMismatchedTmuxSessionBeforeTmuxCall(t *testing.T) {
	tests := []struct {
		name string
		cmd  func(sessionDeps) *cobra.Command
	}{
		{name: "attach", cmd: newAttachCommandWithDeps},
		{name: "shell", cmd: newShellCommandWithDeps},
		{name: "detach", cmd: newDetachCommandWithDeps},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakes := &sessionFakes{
				cfg: &config.Config{StateDir: "/tmp/agentctl-state"},
				tasks: map[string]state.Task{
					"XL-123": {
						TaskID:      "XL-123",
						TmuxSession: "agentctl-OTHER-123",
					},
				},
			}
			cmd := tt.cmd(fakes.deps())
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"XL-123"})

			err := cmd.Execute()
			if err == nil {
				t.Fatal("error = nil, want tmux session mismatch error")
			}
			if want := `state tmux session "agentctl-OTHER-123" does not match expected "agentctl-XL-123"`; !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
			if len(fakes.attachCalls) != 0 {
				t.Fatalf("attach calls = %#v, want none", fakes.attachCalls)
			}
			if len(fakes.detachCalls) != 0 {
				t.Fatalf("detach calls = %#v, want none", fakes.detachCalls)
			}
		})
	}
}

func TestRemoteAttachForwardsInteractiveSSHWithoutLoadingLocalState(t *testing.T) {
	cfg := &config.Config{
		StateDir: "/tmp/agentctl-state",
		Remotes: map[string]config.Remote{
			"buildbox-1": {
				Host:         "buildbox-1.example.com",
				User:         "deploy",
				AgentctlPath: "/usr/local/bin/agentctl",
			},
		},
	}
	fakes := &sessionFakes{cfg: cfg}

	cmd := newAttachCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--remote", "buildbox-1", "--config", "/tmp/config.yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{"/tmp/config.yaml"}) {
		t.Fatalf("config paths = %#v, want remote config load", fakes.loadConfigPaths)
	}
	wantCall := sessionRemoteCall{
		target: remote.Target{
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
		interactive: true,
		args:        []string{"attach", "XL-123"},
	}
	if !reflect.DeepEqual(fakes.remoteCalls, []sessionRemoteCall{wantCall}) {
		t.Fatalf("remote calls = %#v, want %#v", fakes.remoteCalls, []sessionRemoteCall{wantCall})
	}
	assertNoForwardedRemoteFlag(t, fakes.remoteCalls[0].args)
	if len(fakes.loadTaskCalls) != 0 {
		t.Fatalf("load task calls = %#v, want none", fakes.loadTaskCalls)
	}
	if len(fakes.attachCalls) != 0 {
		t.Fatalf("attach calls = %#v, want none", fakes.attachCalls)
	}
	if len(fakes.detachCalls) != 0 {
		t.Fatalf("detach calls = %#v, want none", fakes.detachCalls)
	}
}

func TestRemoteShellForwardsInteractiveSSH(t *testing.T) {
	cfg := &config.Config{
		StateDir: "/tmp/agentctl-state",
		Remotes: map[string]config.Remote{
			"buildbox-1": {
				Host:         "buildbox-1.example.com",
				User:         "deploy",
				AgentctlPath: "/usr/local/bin/agentctl",
			},
		},
	}
	fakes := &sessionFakes{cfg: cfg}

	cmd := newShellCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--remote", "buildbox-1", "--config", "/tmp/config.yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	wantCall := sessionRemoteCall{
		target: remote.Target{
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
		interactive: true,
		args:        []string{"shell", "XL-123"},
	}
	if !reflect.DeepEqual(fakes.remoteCalls, []sessionRemoteCall{wantCall}) {
		t.Fatalf("remote calls = %#v, want %#v", fakes.remoteCalls, []sessionRemoteCall{wantCall})
	}
	assertNoForwardedRemoteFlag(t, fakes.remoteCalls[0].args)
	if len(fakes.loadTaskCalls) != 0 {
		t.Fatalf("load task calls = %#v, want none", fakes.loadTaskCalls)
	}
}

func TestRemoteDetachForwardsNonInteractiveSSH(t *testing.T) {
	cfg := &config.Config{
		StateDir: "/tmp/agentctl-state",
		Remotes: map[string]config.Remote{
			"buildbox-1": {
				Host:         "buildbox-1.example.com",
				User:         "deploy",
				AgentctlPath: "/usr/local/bin/agentctl",
			},
		},
	}
	fakes := &sessionFakes{cfg: cfg}

	cmd := newDetachCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--remote", "buildbox-1", "--config", "/tmp/config.yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	wantCall := sessionRemoteCall{
		target: remote.Target{
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
		interactive: false,
		args:        []string{"detach", "XL-123"},
	}
	if !reflect.DeepEqual(fakes.remoteCalls, []sessionRemoteCall{wantCall}) {
		t.Fatalf("remote calls = %#v, want %#v", fakes.remoteCalls, []sessionRemoteCall{wantCall})
	}
	assertNoForwardedRemoteFlag(t, fakes.remoteCalls[0].args)
	if len(fakes.loadTaskCalls) != 0 {
		t.Fatalf("load task calls = %#v, want none", fakes.loadTaskCalls)
	}
}

func TestRemoteAttachMissingRemoteErrorsWithoutLoadingLocalState(t *testing.T) {
	cfg := &config.Config{StateDir: "/tmp/agentctl-state"}
	fakes := &sessionFakes{cfg: cfg}

	cmd := newAttachCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--remote", "missing", "--config", "/tmp/config.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want missing remote")
	}
	if !strings.Contains(err.Error(), `remote "missing" not found in config`) {
		t.Fatalf("error = %v, want missing remote message", err)
	}
	if len(fakes.loadTaskCalls) != 0 {
		t.Fatalf("load task calls = %#v, want none", fakes.loadTaskCalls)
	}
	if len(fakes.remoteCalls) != 0 {
		t.Fatalf("remote calls = %#v, want none", fakes.remoteCalls)
	}
}

func TestRemoteAttachForwardsConfiguredRemoteConfigPath(t *testing.T) {
	cfg := &config.Config{
		StateDir: "/tmp/agentctl-state",
		Remotes: map[string]config.Remote{
			"buildbox-1": {
				Host:         "buildbox-1.example.com",
				User:         "deploy",
				AgentctlPath: "/usr/local/bin/agentctl",
				ConfigPath:   "/etc/agentctl/config.yaml",
			},
		},
	}
	fakes := &sessionFakes{cfg: cfg}

	cmd := newAttachCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--remote", "buildbox-1", "--config", "/tmp/local-config.yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	wantCall := sessionRemoteCall{
		target: remote.Target{
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
		interactive: true,
		args:        []string{"attach", "XL-123", "--config", "/etc/agentctl/config.yaml"},
	}
	if !reflect.DeepEqual(fakes.remoteCalls, []sessionRemoteCall{wantCall}) {
		t.Fatalf("remote calls = %#v, want %#v", fakes.remoteCalls, []sessionRemoteCall{wantCall})
	}
	assertNoForwardedRemoteFlag(t, fakes.remoteCalls[0].args)
	if len(fakes.loadTaskCalls) != 0 {
		t.Fatalf("load task calls = %#v, want none", fakes.loadTaskCalls)
	}
}

type sessionFakes struct {
	cfg             *config.Config
	loadConfigPaths []string
	loadTaskCalls   []loadTaskCall
	tasks           map[string]state.Task
	loadErr         error
	attachCalls     []string
	detachCalls     []string
	remoteCalls     []sessionRemoteCall
	remoteErr       error
}

func (f *sessionFakes) deps() sessionDeps {
	return sessionDeps{
		loadConfig: func(path string) (*config.Config, error) {
			f.loadConfigPaths = append(f.loadConfigPaths, path)
			return f.cfg, nil
		},
		loadTask: func(stateDir, taskID string) (state.Task, error) {
			f.loadTaskCalls = append(f.loadTaskCalls, loadTaskCall{stateDir: stateDir, taskID: taskID})
			if f.loadErr != nil {
				return state.Task{}, f.loadErr
			}
			task, ok := f.tasks[taskID]
			if !ok {
				return state.Task{}, errors.New("task state missing")
			}
			return task, nil
		},
		attach: func(_ context.Context, taskID string) error {
			f.attachCalls = append(f.attachCalls, taskID)
			return nil
		},
		detach: func(_ context.Context, taskID string) error {
			f.detachCalls = append(f.detachCalls, taskID)
			return nil
		},
		forwardRemote: func(_ context.Context, target remote.Target, interactive bool, args ...string) error {
			f.remoteCalls = append(f.remoteCalls, sessionRemoteCall{
				target:      target,
				interactive: interactive,
				args:        append([]string(nil), args...),
			})
			return f.remoteErr
		},
	}
}

type loadTaskCall struct {
	stateDir string
	taskID   string
}

type sessionRemoteCall struct {
	target      remote.Target
	interactive bool
	args        []string
}
