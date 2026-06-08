package cli

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/qtnx/agentctl/internal/config"
	"github.com/qtnx/agentctl/internal/execx"
	"github.com/qtnx/agentctl/internal/remote"
	"github.com/qtnx/agentctl/internal/runtime"
	watchx "github.com/qtnx/agentctl/internal/watch"
	"github.com/spf13/cobra"
)

type devDeps struct {
	loadConfig                func(path string) (*config.Config, error)
	saveConfig                func(path string, cfg *config.Config) error
	confirmSaveRemote         func(prompt string) (bool, error)
	getenv                    func(key string) string
	workingDir                func() (string, error)
	localPortAvailable        func(port string) bool
	readFile                  func(path string) ([]byte, error)
	writeFile                 func(path string, data []byte, perm os.FileMode) error
	userHome                  func() (string, error)
	gitMetadataPaths          func(ctx context.Context, worktree string) []string
	dockerInvocationFor       func(opts runtime.DockerOptions) (runtime.DockerInvocation, error)
	dockerAvailable           func() bool
	dockerReady               func(ctx context.Context) error
	goos                      func() string
	sandboxExecAvailable      func() bool
	confirmSandboxFallback    func(prompt string) (bool, error)
	macOSSandboxInvocationFor func(opts runtime.MacOSSandboxOptions) (runtime.DockerInvocation, error)
	runCommand                func(ctx context.Context, command []string) error
	remoteEnsureDir           func(ctx context.Context, target remote.Target, remoteDir string) error
	remoteSync                func(ctx context.Context, opts remote.SyncOptions) error
	remoteEnsureWritable      func(ctx context.Context, target remote.Target, remoteDir string) error
	remoteRemoveContainer     func(ctx context.Context, target remote.Target, taskID string) error
	remoteFindAvailablePort   func(ctx context.Context, target remote.Target, start string) (string, error)
	remoteRunDockerStream     func(ctx context.Context, opts remote.DockerStreamOptions) error
	watch                     func(ctx context.Context, root string, onChange func(context.Context) error) error
}

func newDevCommand() *cobra.Command {
	return newDevCommandWithDeps(defaultDevDeps())
}

func defaultDevDeps() devDeps {
	exec := execx.LocalExecutor{}
	remoteService := remote.NewService(exec)
	return devDeps{
		loadConfig:          config.Load,
		saveConfig:          config.Save,
		confirmSaveRemote:   promptSaveRemoteConfig,
		getenv:              os.Getenv,
		workingDir:          os.Getwd,
		localPortAvailable:  localPortAvailable,
		readFile:            os.ReadFile,
		writeFile:           os.WriteFile,
		userHome:            os.UserHomeDir,
		gitMetadataPaths:    discoverGitMetadataPaths,
		dockerInvocationFor: runtime.DockerInvocationFor,
		dockerAvailable: func() bool {
			_, err := osexec.LookPath("docker")
			return err == nil
		},
		dockerReady: dockerInfoReady,
		goos: func() string {
			return goruntime.GOOS
		},
		sandboxExecAvailable: func() bool {
			_, err := osexec.LookPath("sandbox-exec")
			return err == nil
		},
		confirmSandboxFallback:    promptMacOSSandboxFallback,
		macOSSandboxInvocationFor: runtime.MacOSSandboxInvocationFor,
		runCommand: func(ctx context.Context, command []string) error {
			if len(command) == 0 {
				return fmt.Errorf("command is empty")
			}
			return exec.Run(ctx, command[0], command[1:]...)
		},
		remoteEnsureDir:         remoteService.EnsureDir,
		remoteSync:              remoteService.Sync,
		remoteEnsureWritable:    remoteService.EnsureWritable,
		remoteRemoveContainer:   remoteService.RemoveDockerContainer,
		remoteFindAvailablePort: remoteService.FindAvailablePort,
		remoteRunDockerStream:   remoteService.RunDockerStream,
		watch: func(ctx context.Context, root string, onChange func(context.Context) error) error {
			return watchx.Poll(ctx, root, 750*time.Millisecond, defaultRemoteDevExcludes(), onChange)
		},
	}
}

func newDevCommandWithDeps(deps devDeps) *cobra.Command {
	var configPath string
	var templateName string
	var remoteName string
	var ports []string
	var debug bool

	cmd := &cobra.Command{
		Use:   "dev [TASK_ID] -- COMMAND",
		Short: "Run a local or remote development command",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, command, err := parseDevArgs(args, cmd.ArgsLenAtDash())
			if err != nil {
				return err
			}
			opts := devOptions{
				taskID:       taskID,
				command:      command,
				configPath:   configPath,
				templateName: templateName,
				remoteName:   remoteName,
				ports:        ports,
				debug:        debug,
			}
			return runDev(cmd.Context(), deps, opts)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "Path to config file")
	cmd.Flags().StringVar(&templateName, "template", "", "Template name")
	cmd.Flags().StringVar(&remoteName, "remote", "", "Remote dev host name")
	cmd.Flags().StringArrayVar(&ports, "port", nil, "Forward/publish a port, either PORT or LOCAL:CONTAINER")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug output")

	return cmd
}

type devOptions struct {
	taskID       string
	command      []string
	configPath   string
	templateName string
	remoteName   string
	ports        []string
	debug        bool
}

func runDev(ctx context.Context, deps devDeps, opts devOptions) error {
	if opts.remoteName != "" {
		return runRemoteDev(ctx, deps, opts)
	}
	runtimeKind, err := selectDevRuntime(ctx, deps)
	if err != nil {
		return err
	}

	cfg, err := deps.loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	worktree, err := deps.workingDir()
	if err != nil {
		return err
	}
	taskID, err := resolveDevTaskID(deps, opts.taskID, worktree)
	if err != nil {
		return err
	}
	image, err := devImageFor(cfg, opts.templateName)
	if err != nil {
		return err
	}
	command := opts.command
	if len(command) == 0 {
		command = []string{"bash"}
	}
	portMappings, err := parseDevPorts(opts.ports)
	if err != nil {
		return err
	}

	invocation, err := buildDevInvocation(ctx, deps, runtimeKind, cfg, taskID, worktree, image, command, portMappings)
	if err != nil {
		return err
	}
	if len(invocation.Env) > 0 {
		envFile, err := writeDockerEnvFile(cfg.StateDir, taskID, invocation.Env)
		if err != nil {
			return err
		}
		invocation.Command = wrapDockerCommandWithEnvFile(envFile, invocation.Command)
	}

	return deps.runCommand(ctx, invocation.Command)
}

func runRemoteDev(ctx context.Context, deps devDeps, opts devOptions) error {
	cfg, err := deps.loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	target, err := devRemoteTargetFromConfig(cfg, opts.configPath, opts.remoteName, deps.confirmSaveRemote, deps.saveConfig)
	if err != nil {
		return err
	}
	worktree, err := deps.workingDir()
	if err != nil {
		return err
	}
	taskID, err := resolveDevTaskID(deps, opts.taskID, worktree)
	if err != nil {
		return err
	}
	portMappings, err := parseDevPorts(opts.ports)
	if err != nil {
		return err
	}
	explicitPortMappings := len(portMappings) > 0
	image, err := devImageFor(cfg, opts.templateName)
	if err != nil {
		return err
	}
	command := opts.command
	if len(command) == 0 {
		command = []string{"bash"}
	}
	if deps.remoteRemoveContainer != nil {
		if err := deps.remoteRemoveContainer(ctx, target, taskID); err != nil {
			return err
		}
	}
	portMappings, command, err = inferRemoteDevPortMappingsAndCommand(ctx, deps, target, worktree, command, portMappings)
	if err != nil {
		return err
	}
	if explicitPortMappings {
		if err := ensureRemoteDevPortMappingsAvailable(ctx, deps, target, portMappings); err != nil {
			return err
		}
	}
	command = exposeRemoteViteCommandForForwardedPorts(deps, worktree, command, portMappings)

	remoteDir := remoteDevDir(taskID)
	if err := deps.remoteEnsureDir(ctx, target, remoteDir); err != nil {
		return err
	}

	debug := opts.debug || debugEnvEnabled(deps.getenv)
	syncOnce := func(syncCtx context.Context, showProgress bool) error {
		if err := deps.remoteSync(syncCtx, remote.SyncOptions{
			Target:       target,
			LocalDir:     worktree,
			RemoteDir:    remoteDir,
			Excludes:     defaultRemoteDevExcludes(),
			ShowProgress: showProgress,
		}); err != nil {
			return err
		}
		return deps.remoteEnsureWritable(syncCtx, target, remoteDir)
	}
	if err := syncOnce(ctx, debug); err != nil {
		return err
	}

	invocation, err := deps.dockerInvocationFor(runtime.DockerOptions{
		TaskID:   taskID,
		Worktree: remoteDir,
		Image:    image,
		Command:  command,
		Ports:    remoteDockerPortMappings(portMappings),
	})
	if err != nil {
		return err
	}

	watchCtx, cancelWatch := context.WithCancel(ctx)
	watchErr := make(chan error, 1)
	go func() {
		watchErr <- deps.watch(watchCtx, worktree, func(syncCtx context.Context) error {
			return syncOnce(syncCtx, false)
		})
	}()

	runErr := deps.remoteRunDockerStream(ctx, remote.DockerStreamOptions{
		Target:          target,
		Invocation:      invocation,
		Ports:           remotePortForwards(portMappings),
		RunAsRemoteUser: true,
	})
	cancelWatch()
	if err := <-watchErr; runErr == nil && err != nil {
		return err
	}
	return runErr
}

func ensureRemoteDevPortMappingsAvailable(ctx context.Context, deps devDeps, target remote.Target, ports []runtime.PortMapping) error {
	for _, port := range ports {
		if !isLocalPortAvailable(deps, port.Local) {
			return fmt.Errorf("local port %s is already in use; choose another --port", port.Local)
		}
		if deps.remoteFindAvailablePort == nil {
			continue
		}
		found, err := deps.remoteFindAvailablePort(ctx, target, port.Container)
		if err != nil {
			return err
		}
		if found != port.Container {
			return fmt.Errorf("remote port %s is already in use; choose another --port", port.Container)
		}
	}
	return nil
}

func remoteDevDir(taskID string) string {
	return "$HOME/.agent-workspaces/dev/" + taskID
}

func debugEnvEnabled(getenv func(key string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch strings.ToLower(strings.TrimSpace(getenv("AGENTCTL_DEBUG"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func defaultRemoteDevExcludes() []string {
	return []string{".git", ".worktrees", "node_modules", ".agentctl"}
}

func remoteDockerPortMappings(ports []runtime.PortMapping) []runtime.PortMapping {
	mappings := make([]runtime.PortMapping, 0, len(ports))
	for _, port := range ports {
		mappings = append(mappings, runtime.PortMapping{
			Local:     port.Container,
			Container: port.Container,
		})
	}
	return mappings
}

func inferRemoteDevPortMappingsAndCommand(ctx context.Context, deps devDeps, target remote.Target, worktree string, command []string, ports []runtime.PortMapping) ([]runtime.PortMapping, []string, error) {
	command = append([]string(nil), command...)
	if len(ports) > 0 || len(command) == 0 || !isViteDevEntrypoint(command) || !packageJSONUsesVite(deps.readFile, worktree) {
		return append([]runtime.PortMapping(nil), ports...), command, nil
	}
	port := inferViteCommandPort(command)
	if port == "" {
		port = "5173"
		port, err := firstAvailableDevPort(ctx, deps, target, port)
		if err != nil {
			return nil, nil, err
		}
		command = setVitePortFlag(command, port)
		return []runtime.PortMapping{{Local: port, Container: port}}, command, nil
	}
	port, err := firstAvailableDevPort(ctx, deps, target, port)
	if err != nil {
		return nil, nil, err
	}
	command = setVitePortFlag(command, port)
	return []runtime.PortMapping{{Local: port, Container: port}}, command, nil
}

func firstAvailableDevPort(ctx context.Context, deps devDeps, target remote.Target, start string) (string, error) {
	port := start
	for i := 0; i < 100; i++ {
		remotePort := port
		if deps.remoteFindAvailablePort != nil {
			found, err := deps.remoteFindAvailablePort(ctx, target, port)
			if err != nil {
				return "", err
			}
			remotePort = found
		}
		if isLocalPortAvailable(deps, remotePort) {
			return remotePort, nil
		}
		next, ok := nextDevPort(remotePort)
		if !ok {
			return start, nil
		}
		port = next
	}
	return start, nil
}

func isLocalPortAvailable(deps devDeps, port string) bool {
	if deps.localPortAvailable == nil {
		return true
	}
	return deps.localPortAvailable(port)
}

func nextDevPort(port string) (string, bool) {
	value := 0
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", false
		}
		value = value*10 + int(r-'0')
	}
	if value >= 65535 {
		return "", false
	}
	return fmt.Sprintf("%d", value+1), true
}

func localPortAvailable(port string) bool {
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func inferViteCommandPort(command []string) string {
	for i, arg := range command {
		if arg == "--port" && i+1 < len(command) && isDevPort(command[i+1]) {
			return command[i+1]
		}
		if value, ok := strings.CutPrefix(arg, "--port="); ok && isDevPort(value) {
			return value
		}
	}
	return ""
}

func remotePortForwards(ports []runtime.PortMapping) []remote.PortForward {
	forwards := make([]remote.PortForward, 0, len(ports))
	for _, port := range ports {
		forwards = append(forwards, remote.PortForward{
			Local:  port.Local,
			Remote: port.Container,
		})
	}
	return forwards
}

func selectDevRuntime(ctx context.Context, deps devDeps) (localRuntimeKind, error) {
	if deps.dockerAvailable == nil || deps.dockerAvailable() {
		if deps.dockerReady == nil {
			return localRuntimeDocker, nil
		}
		if err := deps.dockerReady(ctx); err == nil {
			return localRuntimeDocker, nil
		} else if deps.goos == nil || deps.goos() != "darwin" {
			return "", fmt.Errorf("Docker daemon is not running or not reachable: %w", err)
		}
	}

	if deps.goos == nil || deps.goos() != "darwin" {
		return "", fmt.Errorf("docker is required but was not found in PATH")
	}
	if deps.sandboxExecAvailable == nil || !deps.sandboxExecAvailable() {
		return "", fmt.Errorf("docker is required but was not found in PATH; macOS sandbox-exec fallback is unavailable")
	}
	confirmed, err := deps.confirmSandboxFallback(macOSSandboxFallbackPrompt())
	if err != nil {
		return "", err
	}
	if !confirmed {
		return "", fmt.Errorf("sandbox-exec fallback declined")
	}
	return localRuntimeMacOSSandbox, nil
}

func buildDevInvocation(ctx context.Context, deps devDeps, runtimeKind localRuntimeKind, cfg *config.Config, taskID, worktree, image string, command []string, ports []runtime.PortMapping) (runtime.DockerInvocation, error) {
	switch runtimeKind {
	case localRuntimeDocker:
		authLinks := agentAuthLinks(deps.userHome)
		return deps.dockerInvocationFor(runtime.DockerOptions{
			TaskID:   taskID,
			Worktree: worktree,
			Image:    image,
			Command:  command,
			Ports:    ports,
			Mounts:   dockerAgentAuthMounts(authLinks),
		})
	case localRuntimeMacOSSandbox:
		gitPaths := sandboxGitMetadataPaths(ctx, deps.gitMetadataPaths, worktree, "")
		authLinks := agentAuthLinks(deps.userHome)
		if err := ensureAgentAuthLinks(macOSSandboxHomeDir(cfg, taskID), authLinks); err != nil {
			return runtime.DockerInvocation{}, err
		}
		extraPaths := append(gitPaths, agentAuthHostPaths(authLinks)...)
		return deps.macOSSandboxInvocationFor(macOSSandboxOptionsFromConfig(cfg, taskID, worktree, command, true, extraPaths))
	default:
		return runtime.DockerInvocation{}, fmt.Errorf("unsupported local runtime %q", runtimeKind)
	}
}

func parseDevArgs(args []string, argsLenAtDash int) (string, []string, error) {
	switch {
	case argsLenAtDash == 0:
		return "", append([]string(nil), args...), nil
	case argsLenAtDash == 1:
		return args[0], append([]string(nil), args[1:]...), nil
	case argsLenAtDash > 1:
		return "", nil, fmt.Errorf("dev accepts at most one task id before --")
	}

	if len(args) == 0 {
		return "", nil, fmt.Errorf("dev requires a task id or -- COMMAND")
	}
	return args[0], append([]string(nil), args[1:]...), nil
}

type agentctlDevFile struct {
	DevTaskID string `json:"dev_task_id"`
}

func resolveDevTaskID(deps devDeps, taskID, worktree string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID != "" {
		if err := validateRunTaskID(taskID); err != nil {
			return "", err
		}
		return taskID, nil
	}

	persistedTaskID, err := readPersistedDevTaskID(deps.readFile, worktree)
	if err == nil && persistedTaskID != "" {
		if err := validateRunTaskID(persistedTaskID); err != nil {
			return "", err
		}
		return persistedTaskID, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	taskID = generatedDevTaskID(worktree)
	if err := validateRunTaskID(taskID); err != nil {
		return "", err
	}
	if err := writePersistedDevTaskID(deps.writeFile, worktree, taskID); err != nil {
		return "", err
	}
	return taskID, nil
}

func readPersistedDevTaskID(readFile func(path string) ([]byte, error), worktree string) (string, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile(agentctlDevFilePath(worktree))
	if err != nil {
		return "", err
	}
	var file agentctlDevFile
	if err := json.Unmarshal(data, &file); err != nil {
		return "", fmt.Errorf("read %s: %w", agentctlDevFilePath(worktree), err)
	}
	return strings.TrimSpace(file.DevTaskID), nil
}

func writePersistedDevTaskID(writeFile func(path string, data []byte, perm os.FileMode) error, worktree, taskID string) error {
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	data, err := json.MarshalIndent(agentctlDevFile{DevTaskID: taskID}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeFile(agentctlDevFilePath(worktree), data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", agentctlDevFilePath(worktree), err)
	}
	return nil
}

func agentctlDevFilePath(worktree string) string {
	return filepath.Join(worktree, ".agentctl")
}

func generatedDevTaskID(worktree string) string {
	clean := filepath.Clean(worktree)
	base := sanitizeDevTaskIDComponent(filepath.Base(clean))
	if base == "" {
		base = "workspace"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	sum := sha1.Sum([]byte(clean))
	return "dev-" + base + "-" + hex.EncodeToString(sum[:])[:8]
}

func sanitizeDevTaskIDComponent(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), ".-_")
}

func devImageFor(cfg *config.Config, templateName string) (string, error) {
	name := strings.TrimSpace(templateName)
	if name == "" && cfg != nil {
		name = strings.TrimSpace(cfg.Templates.Default)
	}
	if name == "" {
		name = "generic"
	}

	template, ok := supportedRunTemplates()[name]
	if !ok {
		return "", fmt.Errorf("unsupported template %q; supported templates: generic, node, golang, python", name)
	}
	return template.image, nil
}

func parseDevPorts(values []string) ([]runtime.PortMapping, error) {
	mappings := make([]runtime.PortMapping, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("port is required")
		}
		local, container, ok := strings.Cut(value, ":")
		if !ok {
			local = value
			container = value
		}
		local = strings.TrimSpace(local)
		container = strings.TrimSpace(container)
		if !isDevPort(local) {
			return nil, fmt.Errorf("invalid local port %q", local)
		}
		if !isDevPort(container) {
			return nil, fmt.Errorf("invalid container port %q", container)
		}
		mappings = append(mappings, runtime.PortMapping{
			Local:     local,
			Container: container,
		})
	}
	return mappings, nil
}

func isDevPort(value string) bool {
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

type devPackageJSON struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func exposeRemoteViteCommandForForwardedPorts(deps devDeps, worktree string, command []string, ports []runtime.PortMapping) []string {
	if len(ports) == 0 || len(command) == 0 || commandHasHostFlag(command) || !isViteDevEntrypoint(command) {
		return append([]string(nil), command...)
	}
	if !packageJSONUsesVite(deps.readFile, worktree) {
		return append([]string(nil), command...)
	}
	return appendViteHostFlag(command)
}

func packageJSONUsesVite(readFile func(path string) ([]byte, error), worktree string) bool {
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile(filepath.Join(worktree, "package.json"))
	if err != nil {
		return false
	}
	var pkg devPackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	if _, ok := pkg.Dependencies["vite"]; ok {
		return true
	}
	if _, ok := pkg.DevDependencies["vite"]; ok {
		return true
	}
	return strings.Contains(pkg.Scripts["dev"], "vite")
}

func isViteDevEntrypoint(command []string) bool {
	if len(command) == 0 {
		return false
	}
	switch command[0] {
	case "npm":
		return len(command) >= 3 && command[1] == "run" && command[2] == "dev"
	case "pnpm", "yarn":
		return len(command) >= 2 && (command[1] == "dev" || len(command) >= 3 && command[1] == "run" && command[2] == "dev")
	case "bun":
		return len(command) >= 2 && (command[1] == "dev" || len(command) >= 3 && command[1] == "run" && command[2] == "dev")
	case "vite":
		return true
	default:
		return strings.HasSuffix(command[0], "/vite")
	}
}

func commandHasHostFlag(command []string) bool {
	for _, arg := range command {
		if arg == "--host" || strings.HasPrefix(arg, "--host=") {
			return true
		}
	}
	return false
}

func appendViteHostFlag(command []string) []string {
	out := append([]string(nil), command...)
	if len(out) >= 3 && out[0] == "npm" && out[1] == "run" && out[2] == "dev" && !containsDevArg(out, "--") {
		out = append(out, "--")
	}
	return append(out, "--host", "0.0.0.0")
}

func setVitePortFlag(command []string, port string) []string {
	out := append([]string(nil), command...)
	for i, arg := range out {
		if arg == "--port" && i+1 < len(out) {
			out[i+1] = port
			return out
		}
		if strings.HasPrefix(arg, "--port=") {
			out[i] = "--port=" + port
			return out
		}
	}
	if len(out) >= 3 && out[0] == "npm" && out[1] == "run" && out[2] == "dev" && !containsDevArg(out, "--") {
		out = append(out, "--")
	}
	return append(out, "--port", port)
}

func containsDevArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
