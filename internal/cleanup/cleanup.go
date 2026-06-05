package cleanup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/your-org/agentctl/internal/state"
)

// ErrAlreadyRemoved marks cleanup targets that are already gone and safe to treat as cleaned.
var ErrAlreadyRemoved = errors.New("resource already removed")

type Request struct {
	TaskID              string
	Task                state.Task
	RepoPath            string
	StateDir            string
	GitLabBaseURL       string
	ControlPAT          string
	ExpectedTmuxSession string
	ExpectedWorktree    string
	RepoLookupError     error
}

type Deps struct {
	RevokeToken     func(ctx context.Context, baseURL, controlPAT, projectID, tokenID string) error
	RemoveContainer func(ctx context.Context, containerName string) error
	KillSession     func(ctx context.Context, taskID string) error
	RemoveWorktree  func(ctx context.Context, repoPath, worktree string) error
	DeleteState     func(stateDir, taskID string) error
}

type Service struct {
	deps Deps
}

func NewService(deps Deps) *Service {
	return &Service{deps: deps}
}

func (s *Service) Cleanup(ctx context.Context, req Request) error {
	if req.Task.TaskID != req.TaskID {
		return fmt.Errorf("state task id %q does not match requested task id %q", req.Task.TaskID, req.TaskID)
	}

	var errs []error

	if strings.TrimSpace(req.Task.TokenID) != "" {
		if strings.TrimSpace(req.ControlPAT) == "" {
			errs = append(errs, fmt.Errorf("token revoke cleanup failed: GITLAB_CONTROL_PAT is required"))
		} else if err := s.deps.RevokeToken(ctx, req.GitLabBaseURL, req.ControlPAT, req.Task.GitLabProjectID, req.Task.TokenID); err != nil {
			errs = append(errs, fmt.Errorf("token revoke cleanup failed: %w", err))
		}
	}

	expectedContainerName := "agent-" + req.TaskID
	if req.Task.ContainerName != expectedContainerName {
		errs = append(errs, fmt.Errorf("docker container cleanup failed: state container name %q does not match expected %q", req.Task.ContainerName, expectedContainerName))
	} else if err := s.deps.RemoveContainer(ctx, req.Task.ContainerName); err != nil {
		if !errors.Is(err, ErrAlreadyRemoved) {
			errs = append(errs, fmt.Errorf("docker container cleanup failed: %w", err))
		}
	}

	if strings.TrimSpace(req.Task.TmuxSession) == "" || req.Task.TmuxSession != req.ExpectedTmuxSession {
		errs = append(errs, fmt.Errorf("tmux session cleanup failed: state tmux session %q does not match expected %q", req.Task.TmuxSession, req.ExpectedTmuxSession))
	} else if err := s.deps.KillSession(ctx, req.TaskID); err != nil {
		if !errors.Is(err, ErrAlreadyRemoved) {
			errs = append(errs, fmt.Errorf("tmux session cleanup failed: %w", err))
		}
	}

	if strings.TrimSpace(req.Task.Worktree) == "" || req.Task.Worktree != req.ExpectedWorktree {
		errs = append(errs, fmt.Errorf("git worktree cleanup failed: state worktree %q does not match expected %q", req.Task.Worktree, req.ExpectedWorktree))
	} else if req.RepoLookupError != nil {
		errs = append(errs, fmt.Errorf("git worktree cleanup failed: %w", req.RepoLookupError))
	} else if strings.TrimSpace(req.RepoPath) == "" {
		errs = append(errs, fmt.Errorf("git worktree cleanup failed: repo path is required"))
	} else if err := s.deps.RemoveWorktree(ctx, req.RepoPath, req.Task.Worktree); err != nil {
		if !errors.Is(err, ErrAlreadyRemoved) {
			errs = append(errs, fmt.Errorf("git worktree cleanup failed: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	if err := s.deps.DeleteState(req.StateDir, req.TaskID); err != nil {
		return fmt.Errorf("state delete cleanup failed: %w", err)
	}

	return nil
}
