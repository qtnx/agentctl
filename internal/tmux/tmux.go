package tmux

import (
	"context"
	"errors"
	"strings"

	"github.com/your-org/agentctl/internal/execx"
)

var ErrEmptyCommand = errors.New("tmux command is empty")

type Service struct {
	exec execx.Executor
}

func NewService(exec execx.Executor) *Service {
	return &Service{exec: exec}
}

func SessionName(taskID string) string {
	return "agentctl-" + taskID
}

func (s *Service) Start(ctx context.Context, taskID, worktree string, command []string) error {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return ErrEmptyCommand
	}

	args := []string{"new-session", "-d", "-s", SessionName(taskID), "-c", worktree}
	args = append(args, command...)

	return s.exec.Run(ctx, "tmux", args...)
}

func (s *Service) Attach(ctx context.Context, taskID string) error {
	return s.exec.Run(ctx, "tmux", "attach-session", "-t", SessionName(taskID))
}

func (s *Service) Detach(ctx context.Context, taskID string) error {
	return s.exec.Run(ctx, "tmux", "detach-client", "-s", SessionName(taskID))
}
