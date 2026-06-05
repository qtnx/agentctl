package tmux

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type recordedCommand struct {
	name string
	args []string
}

type fakeExecutor struct {
	calls []recordedCommand
	err   error
}

func (f *fakeExecutor) Run(_ context.Context, name string, args ...string) error {
	f.calls = append(f.calls, recordedCommand{
		name: name,
		args: append([]string(nil), args...),
	})

	if f.err != nil {
		return f.err
	}

	return nil
}

func TestSessionNamePrefixesTaskID(t *testing.T) {
	got := SessionName("XL-123")
	want := "agentctl-XL-123"
	if got != want {
		t.Fatalf("session name = %q, want %q", got, want)
	}
}

func TestStartRunsDetachedTmuxSessionInWorktree(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Start(context.Background(), "XL-123", "/tmp/worktree", []string{"bash", "-lc", "echo ok"})
	if err != nil {
		t.Fatal(err)
	}

	want := []recordedCommand{
		{
			name: "tmux",
			args: []string{"new-session", "-d", "-s", "agentctl-XL-123", "-c", "/tmp/worktree", "bash", "-lc", "echo ok"},
		},
	}
	if !reflect.DeepEqual(exec.calls, want) {
		t.Fatalf("commands = %#v, want %#v", exec.calls, want)
	}
}

func TestStartRejectsEmptyCommandWithoutRunningExecutor(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Start(context.Background(), "XL-123", "/tmp/worktree", []string{})
	if err == nil {
		t.Error("expected error for empty command")
	}
	if len(exec.calls) != 0 {
		t.Fatalf("commands = %#v, want no executor calls", exec.calls)
	}
}

func TestAttachRunsTmuxAttachSession(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Attach(context.Background(), "XL-123")
	if err != nil {
		t.Fatal(err)
	}

	want := []recordedCommand{
		{
			name: "tmux",
			args: []string{"attach-session", "-t", "agentctl-XL-123"},
		},
	}
	if !reflect.DeepEqual(exec.calls, want) {
		t.Fatalf("commands = %#v, want %#v", exec.calls, want)
	}
}

func TestDetachRunsTmuxDetachClient(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Detach(context.Background(), "XL-123")
	if err != nil {
		t.Fatal(err)
	}

	want := []recordedCommand{
		{
			name: "tmux",
			args: []string{"detach-client", "-s", "agentctl-XL-123"},
		},
	}
	if !reflect.DeepEqual(exec.calls, want) {
		t.Fatalf("commands = %#v, want %#v", exec.calls, want)
	}
}

func TestServiceMethodsReturnExecutorErrors(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Service) error
	}{
		{
			name: "start",
			run: func(service *Service) error {
				return service.Start(context.Background(), "XL-123", "/tmp/worktree", []string{"bash", "-lc", "echo ok"})
			},
		},
		{
			name: "attach",
			run: func(service *Service) error {
				return service.Attach(context.Background(), "XL-123")
			},
		},
		{
			name: "detach",
			run: func(service *Service) error {
				return service.Detach(context.Background(), "XL-123")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmuxErr := errors.New("tmux failed")
			exec := &fakeExecutor{err: tmuxErr}
			service := NewService(exec)

			err := tt.run(service)
			if !errors.Is(err, tmuxErr) {
				t.Fatalf("error = %v, want %v", err, tmuxErr)
			}
		})
	}
}
