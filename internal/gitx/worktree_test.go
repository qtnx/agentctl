package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestPrepareWorktreeRunsFetchThenWorktreeAdd(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.PrepareWorktree(context.Background(), "/repo", "main", "XL-123", "/worktrees/XL-123")
	if err != nil {
		t.Fatal(err)
	}

	want := []recordedCommand{
		{
			name: "git",
			args: []string{"-C", "/repo", "fetch", "origin", "main"},
		},
		{
			name: "git",
			args: []string{"-C", "/repo", "worktree", "add", "/worktrees/XL-123", "-b", "agent/XL-123", "origin/main"},
		},
	}
	if !reflect.DeepEqual(exec.calls, want) {
		t.Fatalf("commands = %#v, want %#v", exec.calls, want)
	}
}

func TestEnsureRepoCloneClonesWhenCacheIsMissing(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)
	repoPath := filepath.Join(t.TempDir(), "repos", "cache-backend")

	if err := service.EnsureRepoClone(context.Background(), "https://gitlab.example.com/group/backend.git", repoPath); err != nil {
		t.Fatal(err)
	}

	want := []recordedCommand{{
		name: "git",
		args: []string{"clone", "https://gitlab.example.com/group/backend.git", repoPath},
	}}
	if !reflect.DeepEqual(exec.calls, want) {
		t.Fatalf("commands = %#v, want %#v", exec.calls, want)
	}
}

func TestEnsureRepoCloneSkipsExistingGitCache(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)
	repoPath := filepath.Join(t.TempDir(), "repos", "cache-backend")
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0700); err != nil {
		t.Fatal(err)
	}

	if err := service.EnsureRepoClone(context.Background(), "https://gitlab.example.com/group/backend.git", repoPath); err != nil {
		t.Fatal(err)
	}

	if len(exec.calls) != 0 {
		t.Fatalf("commands = %#v, want none", exec.calls)
	}
}

func TestPrepareWorktreeReturnsFetchErrorWithoutAddingWorktree(t *testing.T) {
	fetchErr := errors.New("fetch failed")
	exec := &fakeExecutor{err: fetchErr}
	service := NewService(exec)

	err := service.PrepareWorktree(context.Background(), "/repo", "main", "XL-123", "/worktrees/XL-123")
	if !errors.Is(err, fetchErr) {
		t.Fatalf("error = %v, want %v", err, fetchErr)
	}

	want := []recordedCommand{
		{
			name: "git",
			args: []string{"-C", "/repo", "fetch", "origin", "main"},
		},
	}
	if !reflect.DeepEqual(exec.calls, want) {
		t.Fatalf("commands = %#v, want %#v", exec.calls, want)
	}
}

func TestRemoveWorktreeRunsForceWorktreeRemove(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.RemoveWorktree(context.Background(), "/repo", "/worktrees/XL-123")
	if err != nil {
		t.Fatal(err)
	}

	want := []recordedCommand{
		{
			name: "git",
			args: []string{"-C", "/repo", "worktree", "remove", "/worktrees/XL-123", "--force"},
		},
	}
	if !reflect.DeepEqual(exec.calls, want) {
		t.Fatalf("commands = %#v, want %#v", exec.calls, want)
	}
}

func TestRemoveWorktreeReturnsExecutorError(t *testing.T) {
	removeErr := errors.New("worktree remove failed")
	exec := &fakeExecutor{err: removeErr}
	service := NewService(exec)

	err := service.RemoveWorktree(context.Background(), "/repo", "/worktrees/XL-123")
	if !errors.Is(err, removeErr) {
		t.Fatalf("error = %v, want %v", err, removeErr)
	}
}
