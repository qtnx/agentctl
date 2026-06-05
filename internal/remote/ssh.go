package remote

import (
	"context"
	"fmt"
	"strings"
	"unicode"

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
	address, err := targetAddress(target)
	if err != nil {
		return err
	}
	agentctlPath, err := resolveAgentctlPath(target.AgentctlPath)
	if err != nil {
		return err
	}

	command := shellQuoteCommand(append([]string{agentctlPath}, args...)...)
	sshArgs := make([]string, 0, 3)
	if interactive {
		sshArgs = append(sshArgs, "-t")
	}
	sshArgs = append(sshArgs, address, command)

	return s.exec.Run(ctx, "ssh", sshArgs...)
}

func targetAddress(target Target) (string, error) {
	if err := validateHost(target.Host); err != nil {
		return "", err
	}

	if target.User == "" {
		return target.Host, nil
	}
	if err := validateUser(target.User); err != nil {
		return "", err
	}

	return target.User + "@" + target.Host, nil
}

func resolveAgentctlPath(path string) (string, error) {
	if path == "" {
		return "agentctl", nil
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("agentctl path must not contain control characters")
		}
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("agentctl path is required")
	}
	if strings.HasPrefix(path, "-") {
		return "", fmt.Errorf("agentctl path must not start with '-'")
	}

	return path, nil
}

func validateHost(host string) error {
	if host == "" {
		return fmt.Errorf("remote host is required")
	}
	if strings.HasPrefix(host, "-") {
		return fmt.Errorf("remote host must not start with '-'")
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("remote host must not contain whitespace or control characters")
		}
		if !isSafeHostRune(r) {
			return fmt.Errorf("remote host contains invalid character %q", r)
		}
	}

	return nil
}

func validateUser(user string) error {
	if strings.HasPrefix(user, "-") {
		return fmt.Errorf("remote user must not start with '-'")
	}
	for _, r := range user {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '@' {
			return fmt.Errorf("remote user must not contain whitespace, control characters, or '@'")
		}
		if !isSafeUserRune(r) {
			return fmt.Errorf("remote user contains invalid character %q", r)
		}
	}

	return nil
}

func isSafeHostRune(r rune) bool {
	return r >= 'A' && r <= 'Z' ||
		r >= 'a' && r <= 'z' ||
		r >= '0' && r <= '9' ||
		r == '.' || r == '-' || r == '_' || r == ':' || r == '[' || r == ']'
}

func isSafeUserRune(r rune) bool {
	return r >= 'A' && r <= 'Z' ||
		r >= 'a' && r <= 'z' ||
		r >= '0' && r <= '9' ||
		r == '.' || r == '-' || r == '_'
}

func shellQuoteCommand(args ...string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}

	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
