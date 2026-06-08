package remote

import (
	"context"
	"fmt"
	"strings"

	"github.com/qtnx/agentctl/internal/runtime"
)

type SyncOptions struct {
	Target       Target
	LocalDir     string
	RemoteDir    string
	Excludes     []string
	ShowProgress bool
}

type DockerStreamOptions struct {
	Target          Target
	Invocation      runtime.DockerInvocation
	Ports           []PortForward
	RunAsRemoteUser bool
}

type PortForward struct {
	Local  string
	Remote string
}

type outputExecutor interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (s *Service) EnsureDir(ctx context.Context, target Target, remoteDir string) error {
	address, err := targetAddress(target)
	if err != nil {
		return err
	}
	if err := validateRemoteDir(remoteDir); err != nil {
		return err
	}

	return s.exec.Run(ctx, "ssh", address, "mkdir -p "+shellQuoteRemoteArg(remoteDir))
}

func (s *Service) EnsureWritable(ctx context.Context, target Target, remoteDir string) error {
	address, err := targetAddress(target)
	if err != nil {
		return err
	}
	if err := validateRemoteDir(remoteDir); err != nil {
		return err
	}

	command := `find ` + shellQuoteRemoteArg(remoteDir) + ` \( -name 'node_modules' -o -name '.git' -o -name '.worktrees' -o -name '.agentctl' \) -prune -o -user "$(id -u)" -exec chmod a+rwX {} +`
	return s.exec.Run(ctx, "ssh", address, command)
}

func (s *Service) RemoveDockerContainer(ctx context.Context, target Target, taskID string) error {
	address, err := targetAddress(target)
	if err != nil {
		return err
	}
	containerName := "agent-" + strings.TrimSpace(taskID)
	if err := validateRemoteContainerName(containerName); err != nil {
		return err
	}

	return s.exec.Run(ctx, "ssh", address, "docker rm -f "+shellQuote(containerName)+" >/dev/null 2>&1 || true")
}

func (s *Service) FindAvailablePort(ctx context.Context, target Target, start string) (string, error) {
	address, err := targetAddress(target)
	if err != nil {
		return "", err
	}
	if !isPort(start) {
		return "", fmt.Errorf("invalid start port %q", start)
	}
	exec, ok := s.exec.(outputExecutor)
	if !ok {
		return "", fmt.Errorf("remote executor does not support output capture")
	}

	output, err := exec.Output(ctx, "ssh", address, remotePortProbeCommand(start))
	if err != nil {
		return "", err
	}
	port := strings.TrimSpace(string(output))
	if !isPort(port) {
		return "", fmt.Errorf("remote port probe returned invalid port %q", port)
	}
	return port, nil
}

func remotePortProbeCommand(start string) string {
	return strings.Join([]string{
		"p=" + start,
		`while [ "$p" -le 65535 ]; do`,
		`  if (ss -ltn 2>/dev/null || netstat -ltn 2>/dev/null || true) | awk '{print $4}' | grep -Eq '(^|[:.])'"$p"'$'; then`,
		`    p=$((p + 1))`,
		`  else`,
		`    echo "$p"`,
		`    exit 0`,
		`  fi`,
		`done`,
		`exit 1`,
	}, "\n")
}

func (s *Service) Sync(ctx context.Context, opts SyncOptions) error {
	address, err := targetAddress(opts.Target)
	if err != nil {
		return err
	}
	if err := validateLocalDir(opts.LocalDir); err != nil {
		return err
	}
	if err := validateRemoteDir(opts.RemoteDir); err != nil {
		return err
	}

	args := []string{"-az", "--delete"}
	if opts.ShowProgress {
		args = append(args, "--progress", "--stats")
	}
	for _, exclude := range opts.Excludes {
		if strings.TrimSpace(exclude) == "" {
			return fmt.Errorf("rsync exclude is required")
		}
		args = append(args, "--exclude", exclude)
	}
	args = append(args, trailingSlash(opts.LocalDir), address+":"+trailingSlash(opts.RemoteDir))

	return s.exec.Run(ctx, "rsync", args...)
}

func (s *Service) RunDockerStream(ctx context.Context, opts DockerStreamOptions) error {
	address, err := targetAddress(opts.Target)
	if err != nil {
		return err
	}
	if len(opts.Invocation.Command) == 0 {
		return fmt.Errorf("Docker command is empty")
	}

	args := []string{"-t"}
	for _, port := range opts.Ports {
		local, remote, err := validatePortForward(port)
		if err != nil {
			return err
		}
		args = append(args, "-L", local+":127.0.0.1:"+remote)
	}
	command, err := remoteDockerCommand(opts.Invocation, opts.RunAsRemoteUser)
	if err != nil {
		return err
	}
	args = append(args, address, command)

	return s.exec.Run(ctx, "ssh", args...)
}

func remoteDockerCommand(invocation runtime.DockerInvocation, runAsRemoteUser bool) (string, error) {
	prefix := ""
	if len(invocation.Env) > 0 {
		assignments := make([]string, 0, len(invocation.Env))
		for _, pair := range invocation.Env {
			key, value, ok := strings.Cut(pair, "=")
			if !ok || key == "" {
				continue
			}
			assignments = append(assignments, key+"="+shellQuote(value))
		}
		prefix = strings.Join(assignments, " ") + " "
	}

	command := append([]string(nil), invocation.Command...)
	prelude := ""
	if runAsRemoteUser {
		if len(command) < 2 || command[0] != "docker" || command[1] != "run" {
			return "", fmt.Errorf("remote user Docker mode requires a docker run command")
		}
		prelude = `uid="$(id -u)"; gid="$(id -g)"; `
		withUser := []string{"docker", "run", remoteUserDockerArg}
		command = append(withUser, command[2:]...)
	}
	return prelude + prefix + "exec " + shellQuoteRemoteCommand(command...), nil
}

func validatePortForward(port PortForward) (string, string, error) {
	local := strings.TrimSpace(port.Local)
	remote := strings.TrimSpace(port.Remote)
	if !isPort(local) {
		return "", "", fmt.Errorf("invalid local port %q", local)
	}
	if !isPort(remote) {
		return "", "", fmt.Errorf("invalid remote port %q", remote)
	}
	return local, remote, nil
}

func isPort(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validateLocalDir(localDir string) error {
	if strings.TrimSpace(localDir) == "" {
		return fmt.Errorf("local dir is required")
	}
	return nil
}

func validateRemoteDir(remoteDir string) error {
	remoteDir = strings.TrimSpace(remoteDir)
	if remoteDir == "" {
		return fmt.Errorf("remote dir is required")
	}
	if strings.HasPrefix(remoteDir, "-") {
		return fmt.Errorf("remote dir must not start with '-'")
	}
	for _, r := range remoteDir {
		if r == '\n' || r == '\r' || r == 0 {
			return fmt.Errorf("remote dir must not contain control characters")
		}
	}
	return nil
}

func validateRemoteContainerName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("container name is required")
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("container name must not start with '-'")
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return fmt.Errorf("container name contains invalid character %q", r)
		}
	}
	return nil
}

func trailingSlash(path string) string {
	return strings.TrimRight(path, "/") + "/"
}

func shellQuoteRemoteCommand(args ...string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == remoteUserDockerArg {
			quoted = append(quoted, "'--user'", `"${uid}:${gid}"`)
			continue
		}
		quoted = append(quoted, shellQuoteRemoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

const remoteUserDockerArg = "__AGENTCTL_REMOTE_DOCKER_USER__"

func shellQuoteRemoteArg(arg string) string {
	if strings.HasPrefix(arg, "$HOME/") && isSafeHomeArg(arg) {
		return `"` + arg + `"`
	}
	return shellQuote(arg)
}

func isSafeHomeArg(arg string) bool {
	for _, r := range strings.TrimPrefix(arg, "$HOME/") {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '/' || r == ':':
		default:
			return false
		}
	}
	return true
}
