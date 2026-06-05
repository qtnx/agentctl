package cleanup

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/your-org/agentctl/internal/state"
)

func TestCleanupRunsStepsInOrderAndDeletesStateWhenAllSucceed(t *testing.T) {
	fakes := &cleanupFakes{}
	service := NewService(fakes.deps())
	task := cleanupTestTask()

	err := service.Cleanup(context.Background(), Request{
		TaskID:              "XL-123",
		Task:                task,
		RepoPath:            "/repo/backend",
		StateDir:            "/state",
		GitLabBaseURL:       "https://gitlab.example.com",
		ControlPAT:          "control-pat",
		ExpectedTmuxSession: "agentctl-XL-123",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []cleanupCall{
		{step: "revoke", baseURL: "https://gitlab.example.com", controlPAT: "control-pat", projectID: "123", tokenID: "98765"},
		{step: "container", containerName: "agent-XL-123"},
		{step: "tmux", taskID: "XL-123"},
		{step: "worktree", repoPath: "/repo/backend", worktree: "/worktrees/XL-123"},
		{step: "delete", taskID: "XL-123"},
	}
	if !reflect.DeepEqual(fakes.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fakes.calls, want)
	}
}

func TestCleanupContinuesAfterFailureAndKeepsStateForRetry(t *testing.T) {
	containerErr := errors.New("docker remove failed")
	fakes := &cleanupFakes{containerErr: containerErr}
	service := NewService(fakes.deps())
	task := cleanupTestTask()

	err := service.Cleanup(context.Background(), Request{
		TaskID:              "XL-123",
		Task:                task,
		RepoPath:            "/repo/backend",
		StateDir:            "/state",
		GitLabBaseURL:       "https://gitlab.example.com",
		ControlPAT:          "control-pat",
		ExpectedTmuxSession: "agentctl-XL-123",
	})
	if !errors.Is(err, containerErr) {
		t.Fatalf("error = %v, want container error", err)
	}
	if !strings.Contains(err.Error(), "docker container cleanup failed") {
		t.Fatalf("error = %v, want docker cleanup label", err)
	}

	want := []cleanupCall{
		{step: "revoke", baseURL: "https://gitlab.example.com", controlPAT: "control-pat", projectID: "123", tokenID: "98765"},
		{step: "container", containerName: "agent-XL-123"},
		{step: "tmux", taskID: "XL-123"},
		{step: "worktree", repoPath: "/repo/backend", worktree: "/worktrees/XL-123"},
	}
	if !reflect.DeepEqual(fakes.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fakes.calls, want)
	}
}

func TestCleanupSkipsTokenRevokeWhenTokenIDIsEmpty(t *testing.T) {
	fakes := &cleanupFakes{}
	service := NewService(fakes.deps())
	task := cleanupTestTask()
	task.TokenID = ""

	err := service.Cleanup(context.Background(), Request{
		TaskID:              "XL-123",
		Task:                task,
		RepoPath:            "/repo/backend",
		StateDir:            "/state",
		GitLabBaseURL:       "https://gitlab.example.com",
		ExpectedTmuxSession: "agentctl-XL-123",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []cleanupCall{
		{step: "container", containerName: "agent-XL-123"},
		{step: "tmux", taskID: "XL-123"},
		{step: "worktree", repoPath: "/repo/backend", worktree: "/worktrees/XL-123"},
		{step: "delete", taskID: "XL-123"},
	}
	if !reflect.DeepEqual(fakes.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fakes.calls, want)
	}
}

func TestCleanupRequiresControlPATWhenTokenIDExistsButStillContinues(t *testing.T) {
	fakes := &cleanupFakes{}
	service := NewService(fakes.deps())
	task := cleanupTestTask()

	err := service.Cleanup(context.Background(), Request{
		TaskID:              "XL-123",
		Task:                task,
		RepoPath:            "/repo/backend",
		StateDir:            "/state",
		GitLabBaseURL:       "https://gitlab.example.com",
		ExpectedTmuxSession: "agentctl-XL-123",
	})
	if err == nil {
		t.Fatal("error = nil, want missing PAT error")
	}
	if !strings.Contains(err.Error(), "GITLAB_CONTROL_PAT is required") {
		t.Fatalf("error = %v, want missing PAT message", err)
	}

	want := []cleanupCall{
		{step: "container", containerName: "agent-XL-123"},
		{step: "tmux", taskID: "XL-123"},
		{step: "worktree", repoPath: "/repo/backend", worktree: "/worktrees/XL-123"},
	}
	if !reflect.DeepEqual(fakes.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fakes.calls, want)
	}
}

func TestCleanupRejectsMismatchedStateTaskIDBeforeCleanup(t *testing.T) {
	fakes := &cleanupFakes{}
	service := NewService(fakes.deps())
	task := cleanupTestTask()
	task.TaskID = "OTHER-1"

	err := service.Cleanup(context.Background(), Request{
		TaskID:              "XL-123",
		Task:                task,
		RepoPath:            "/repo/backend",
		StateDir:            "/state",
		GitLabBaseURL:       "https://gitlab.example.com",
		ControlPAT:          "control-pat",
		ExpectedTmuxSession: "agentctl-XL-123",
	})
	if err == nil {
		t.Fatal("error = nil, want task mismatch")
	}
	if !strings.Contains(err.Error(), "state task id") {
		t.Fatalf("error = %v, want task mismatch message", err)
	}
	if len(fakes.calls) != 0 {
		t.Fatalf("calls = %#v, want none", fakes.calls)
	}
}

func TestCleanupSkipsTmuxKillWhenStateSessionMismatchesExpected(t *testing.T) {
	fakes := &cleanupFakes{}
	service := NewService(fakes.deps())
	task := cleanupTestTask()
	task.TmuxSession = "unexpected-session"

	err := service.Cleanup(context.Background(), Request{
		TaskID:              "XL-123",
		Task:                task,
		RepoPath:            "/repo/backend",
		StateDir:            "/state",
		GitLabBaseURL:       "https://gitlab.example.com",
		ControlPAT:          "control-pat",
		ExpectedTmuxSession: "agentctl-XL-123",
	})
	if err == nil {
		t.Fatal("error = nil, want tmux session mismatch")
	}
	if !strings.Contains(err.Error(), "tmux session") {
		t.Fatalf("error = %v, want tmux session mismatch message", err)
	}

	want := []cleanupCall{
		{step: "revoke", baseURL: "https://gitlab.example.com", controlPAT: "control-pat", projectID: "123", tokenID: "98765"},
		{step: "container", containerName: "agent-XL-123"},
		{step: "worktree", repoPath: "/repo/backend", worktree: "/worktrees/XL-123"},
	}
	if !reflect.DeepEqual(fakes.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fakes.calls, want)
	}
}

func cleanupTestTask() state.Task {
	return state.Task{
		TaskID:          "XL-123",
		Repo:            "backend",
		Worktree:        "/worktrees/XL-123",
		TmuxSession:     "agentctl-XL-123",
		ContainerName:   "agent-XL-123",
		TokenID:         "98765",
		TokenName:       "agent-XL-123-1780662896",
		GitLabProjectID: "123",
		GitLabHost:      "gitlab.example.com",
		Status:          "running",
	}
}

type cleanupFakes struct {
	calls        []cleanupCall
	revokeErr    error
	containerErr error
	tmuxErr      error
	worktreeErr  error
	deleteErr    error
}

func (f *cleanupFakes) deps() Deps {
	return Deps{
		RevokeToken: func(_ context.Context, baseURL, controlPAT, projectID, tokenID string) error {
			f.calls = append(f.calls, cleanupCall{
				step:       "revoke",
				baseURL:    baseURL,
				controlPAT: controlPAT,
				projectID:  projectID,
				tokenID:    tokenID,
			})
			return f.revokeErr
		},
		RemoveContainer: func(_ context.Context, containerName string) error {
			f.calls = append(f.calls, cleanupCall{step: "container", containerName: containerName})
			return f.containerErr
		},
		KillSession: func(_ context.Context, taskID string) error {
			f.calls = append(f.calls, cleanupCall{step: "tmux", taskID: taskID})
			return f.tmuxErr
		},
		RemoveWorktree: func(_ context.Context, repoPath, worktree string) error {
			f.calls = append(f.calls, cleanupCall{step: "worktree", repoPath: repoPath, worktree: worktree})
			return f.worktreeErr
		},
		DeleteState: func(_ string, taskID string) error {
			f.calls = append(f.calls, cleanupCall{step: "delete", taskID: taskID})
			return f.deleteErr
		},
	}
}

type cleanupCall struct {
	step          string
	baseURL       string
	controlPAT    string
	projectID     string
	tokenID       string
	containerName string
	taskID        string
	repoPath      string
	worktree      string
}
