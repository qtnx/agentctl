package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qtnx/agentctl/internal/execx"
)

type Service struct {
	exec execx.Executor
}

func NewService(exec execx.Executor) *Service {
	return &Service{exec: exec}
}

func (s *Service) EnsureRepoClone(ctx context.Context, remote, repoPath string) error {
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	if _, err := os.Stat(repoPath); err == nil {
		return fmt.Errorf("repo cache path exists but is not a git repository: %s", repoPath)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(repoPath), 0700); err != nil {
		return err
	}

	return s.exec.Run(ctx, "git", "clone", remote, repoPath)
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
