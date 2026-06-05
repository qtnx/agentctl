package gitx

import (
	"context"

	"github.com/your-org/agentctl/internal/execx"
)

type Service struct {
	exec execx.Executor
}

func NewService(exec execx.Executor) *Service {
	return &Service{exec: exec}
}

func (s *Service) PrepareWorktree(ctx context.Context, repoPath, defaultBranch, taskID, worktree string) error {
	if err := s.exec.Run(ctx, "git", "-C", repoPath, "fetch", "origin", defaultBranch); err != nil {
		return err
	}

	return s.exec.Run(ctx, "git", "-C", repoPath, "worktree", "add", worktree, "-b", "agent/"+taskID, "origin/"+defaultBranch)
}

func (s *Service) RemoveWorktree(ctx context.Context, repoPath, worktree string) error {
	return s.exec.Run(ctx, "git", "-C", repoPath, "worktree", "remove", worktree, "--force")
}
