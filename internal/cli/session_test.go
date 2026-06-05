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

type sessionFakes struct {
	cfg             *config.Config
	loadConfigPaths []string
	loadTaskCalls   []loadTaskCall
	tasks           map[string]state.Task
	loadErr         error
	attachCalls     []string
	detachCalls     []string
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
	}
}

type loadTaskCall struct {
	stateDir string
	taskID   string
}
