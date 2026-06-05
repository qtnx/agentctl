package remote

import (
	"context"
	"strings"

	"github.com/your-org/agentctl/internal/execx"
)

type Target struct {
	Host         string
	User         string
	AgentctlPath string
}

type Service struct {
	exec execx.Executor
}

func NewService(exec execx.Executor) *Service {
	return &Service{exec: exec}
}

func (s *Service) Run(ctx context.Context, target Target, interactive bool, args ...string) error {
	agentctlPath := strings.TrimSpace(target.AgentctlPath)
	if agentctlPath == "" {
		agentctlPath = "agentctl"
	}

	sshArgs := make([]string, 0, len(args)+3)
	if interactive {
		sshArgs = append(sshArgs, "-t")
	}
	sshArgs = append(sshArgs, targetAddress(target), agentctlPath)
	sshArgs = append(sshArgs, args...)

	return s.exec.Run(ctx, "ssh", sshArgs...)
}

func targetAddress(target Target) string {
	host := strings.TrimSpace(target.Host)
	user := strings.TrimSpace(target.User)
	if user == "" {
		return host
	}

	return user + "@" + host
}
