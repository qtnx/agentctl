package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/qtnx/agentctl/internal/config"
	"github.com/qtnx/agentctl/internal/execx"
	"github.com/qtnx/agentctl/internal/gitx"
	"github.com/qtnx/agentctl/internal/remote"
	"github.com/qtnx/agentctl/internal/runtime"
	"github.com/qtnx/agentctl/internal/state"
	"github.com/qtnx/agentctl/internal/tmux"
	"github.com/qtnx/agentctl/internal/tokenbroker"
	"github.com/spf13/cobra"
)

const defaultConfigPath = "~/.config/agentctl/config.yaml"

var validRunTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type runTokenClient interface {
	CreateProjectToken(ctx context.Context, projectID, name string, expiresAt time.Time) (tokenbroker.CreatedToken, error)
	RevokeProjectToken(ctx context.Context, projectID, tokenID string) error
}

type runDeps struct {
	loadConfig                func(path string) (*config.Config, error)
	getenv                    func(key string) string
	now                       func() time.Time
	workingDir                func() (string, error)
	userHome                  func() (string, error)
	originRemote              func(ctx context.Context, repoPath string) (string, error)
	gitMetadataPaths          func(ctx context.Context, worktree string) []string
	ensureRepoClone           func(ctx context.Context, remote, repoPath string) error
	prepareWorktree           func(ctx context.Context, repoPath, defaultBranch, taskID, worktree string) error
	removeWorktree            func(ctx context.Context, repoPath, worktree string) error
	newTokenClient            func(baseURL, controlPAT string) runTokenClient
	dockerInvocationFor       func(opts runtime.DockerOptions) (runtime.DockerInvocation, error)
	dockerAvailable           func() bool
	dockerReady               func(ctx context.Context) error
	goos                      func() string
	sandboxExecAvailable      func() bool
	confirmSandboxFallback    func(prompt string) (bool, error)
	macOSSandboxInvocationFor func(opts runtime.MacOSSandboxOptions) (runtime.DockerInvocation, error)
	tmuxAvailable             func() bool
	startTmux                 func(ctx context.Context, taskID, worktree string, command []string) error
	attachTmux                func(ctx context.Context, taskID string) error
	stopTmux                  func(ctx context.Context, taskID string) error
	startDocker               func(ctx context.Context, command []string) error
	attachDocker              func(ctx context.Context, containerName string) error
	stopDocker                func(ctx context.Context, containerName string) error
	removeEnvFile             func(path string) error
	saveState                 func(stateDir string, task state.Task) error
	forwardRemote             func(ctx context.Context, target remote.Target, interactive bool, args ...string) error
}

func newRunCommand() *cobra.Command {
	return newRunCommandWithDeps(defaultRunDeps())
}

func defaultRunDeps() runDeps {
	exec := execx.LocalExecutor{}
	gitService := gitx.NewService(exec)
	tmuxService := tmux.NewService(exec)
	remoteService := remote.NewService(exec)

	return runDeps{
		loadConfig:       config.Load,
		getenv:           os.Getenv,
		now:              time.Now,
		workingDir:       os.Getwd,
		userHome:         os.UserHomeDir,
		originRemote:     gitOriginRemote,
		gitMetadataPaths: discoverGitMetadataPaths,
		ensureRepoClone:  gitService.EnsureRepoClone,
		prepareWorktree:  gitService.PrepareWorktree,
		removeWorktree:   gitService.RemoveWorktree,
		newTokenClient: func(baseURL, controlPAT string) runTokenClient {
			return tokenbroker.NewGitLabClient(baseURL, controlPAT, nil)
		},
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
		tmuxAvailable: func() bool {
			_, err := osexec.LookPath("tmux")
			return err == nil
		},
		startTmux:  tmuxService.Start,
		attachTmux: tmuxService.Attach,
		stopTmux:   tmuxService.Kill,
		startDocker: func(ctx context.Context, command []string) error {
			if len(command) == 0 {
				return fmt.Errorf("docker command is empty")
			}
			return exec.Run(ctx, command[0], command[1:]...)
		},
		attachDocker: func(ctx context.Context, containerName string) error {
			return exec.Run(ctx, "docker", "attach", containerName)
		},
		stopDocker: func(ctx context.Context, containerName string) error {
			return exec.Run(ctx, "docker", "rm", "-f", containerName)
		},
		removeEnvFile: cleanupEnvFile,
		saveState: func(stateDir string, task state.Task) error {
			return state.NewStore(stateDir).Save(task)
		},
		forwardRemote: remoteService.Run,
	}
}

func newRunCommandWithDeps(deps runDeps) *cobra.Command {
	var repoName string
	var configPath string
	var agent string
	var risk string
	var templateName string
	var remoteName string
	var detach bool
	var noTmux bool

	cmd := &cobra.Command{
		Use:   "run TASK_ID",
		Short: "Start an agent workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := runOptions{
				taskID:       args[0],
				repoName:     repoName,
				configPath:   configPath,
				agent:        agent,
				risk:         risk,
				templateName: templateName,
				remoteName:   remoteName,
				detach:       detach,
				noTmux:       noTmux,
				out:          cmd.OutOrStdout(),
			}
			return runLocalOrRemote(cmd.Context(), deps, opts)
		},
	}

	cmd.Flags().StringVar(&repoName, "repo", "", "Repository name from config or GitLab repo URL")
	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "Path to config file")
	cmd.Flags().StringVar(&agent, "agent", "codex", "Agent to run")
	cmd.Flags().StringVar(&risk, "risk", "untrusted", "Risk profile")
	cmd.Flags().StringVar(&templateName, "template", "", "Template name")
	cmd.Flags().StringVar(&remoteName, "remote", "", "Remote runner name")
	cmd.Flags().BoolVar(&detach, "detach", false, "Start the session without attaching")
	cmd.Flags().BoolVar(&noTmux, "no-tmux", false, "Start Docker directly without tmux")

	return cmd
}

type runOptions struct {
	taskID       string
	repoName     string
	configPath   string
	agent        string
	risk         string
	templateName string
	remoteName   string
	detach       bool
	noTmux       bool
	out          io.Writer
}

type runRuntimeSelection struct {
	image   string
	command []string
}

type runTemplateSpec struct {
	image     string
	preflight string
}

type resolvedRunRepo struct {
	displayName   string
	path          string
	projectID     string
	defaultBranch string
	remote        string
	managed       bool
	needsToken    bool
}

type localRuntimeKind string

const (
	localRuntimeDocker       localRuntimeKind = "docker"
	localRuntimeMacOSSandbox localRuntimeKind = "macos-sandbox"
)

func runLocalOrRemote(ctx context.Context, deps runDeps, opts runOptions) error {
	if err := validateRunTaskID(opts.taskID); err != nil {
		return err
	}

	if opts.remoteName != "" {
		return runRemote(ctx, deps, opts)
	}

	if opts.risk != "untrusted" {
		return fmt.Errorf("risk %q is not supported; only untrusted is supported", opts.risk)
	}

	cfg, err := deps.loadConfig(opts.configPath)
	if err != nil {
		return err
	}

	repo, err := resolveRunRepo(ctx, deps, cfg, opts.repoName)
	if err != nil {
		return err
	}

	runRuntime, err := runRuntimeFor(cfg, opts)
	if err != nil {
		return err
	}

	controlPAT := deps.getenv("GITLAB_CONTROL_PAT")
	if repo.needsToken && controlPAT == "" {
		return fmt.Errorf("GITLAB_CONTROL_PAT is required")
	}

	useTmux := !opts.noTmux && (deps.tmuxAvailable == nil || deps.tmuxAvailable())
	runtimeKind, err := selectLocalRuntime(ctx, deps, useTmux)
	if err != nil {
		return err
	}

	worktree := repo.path
	gitDir := ""
	if repo.managed {
		worktree = filepath.Join(cfg.BaseDir, opts.taskID)
		gitDir = filepath.Join(repo.path, ".git")
		if err := deps.prepareWorktree(ctx, repo.path, repo.defaultBranch, opts.taskID, worktree); err != nil {
			return err
		}
	}

	now := deps.now()
	tokenName := ""
	var tokenClient runTokenClient
	var createdToken tokenbroker.CreatedToken
	if repo.needsToken {
		tokenName = fmt.Sprintf("agent-%s-%d", opts.taskID, now.Unix())
		tokenClient = deps.newTokenClient("https://"+cfg.GitLab.Host, controlPAT)
		created, err := tokenClient.CreateProjectToken(ctx, repo.projectID, tokenName, now.Add(24*time.Hour))
		if err != nil {
			return cleanupMaybePreparedWorktree(ctx, deps, repo, worktree, err)
		}
		createdToken = created
	}

	tokenCreated := repo.needsToken
	cleanupAfterTokenFailure := func(failure error) error {
		if failure != nil && tokenCreated {
			if err := tokenClient.RevokeProjectToken(ctx, repo.projectID, createdToken.ID); err != nil {
				failure = joinCleanup(failure, "token revoke cleanup failed", err)
			}
		}
		return cleanupMaybePreparedWorktree(ctx, deps, repo, worktree, failure)
	}

	invocation, err := buildLocalInvocation(ctx, deps, runtimeKind, cfg, opts, runRuntime, repo, worktree, gitDir, createdToken, useTmux)
	if err != nil {
		return cleanupAfterTokenFailure(err)
	}

	envFile, err := writeDockerEnvFile(cfg.StateDir, opts.taskID, invocation.Env)
	if err != nil {
		return cleanupAfterTokenFailure(err)
	}

	sessionKind := "tmux"
	tmuxSession := tmux.SessionName(opts.taskID)
	containerName := "agent-" + opts.taskID
	startedDockerDirect := false
	wrappedCommand := wrapDockerCommandWithEnvFile(envFile, invocation.Command)
	if runtimeKind == localRuntimeMacOSSandbox {
		sessionKind = string(localRuntimeMacOSSandbox)
		containerName = ""
		if err := deps.startTmux(ctx, opts.taskID, worktree, wrappedCommand); err != nil {
			err = joinCleanup(err, "env file remove cleanup failed", deps.removeEnvFile(envFile))
			return cleanupAfterTokenFailure(err)
		}
	} else if useTmux {
		if err := deps.startTmux(ctx, opts.taskID, worktree, wrappedCommand); err != nil {
			err = joinCleanup(err, "env file remove cleanup failed", deps.removeEnvFile(envFile))
			return cleanupAfterTokenFailure(err)
		}
	} else {
		sessionKind = "docker"
		tmuxSession = ""
		if err := deps.startDocker(ctx, wrappedCommand); err != nil {
			err = joinCleanup(err, "env file remove cleanup failed", deps.removeEnvFile(envFile))
			return cleanupAfterTokenFailure(err)
		}
		startedDockerDirect = true
	}

	task := state.Task{
		TaskID:          opts.taskID,
		Repo:            repo.displayName,
		RepoPath:        repo.path,
		SessionKind:     sessionKind,
		WorktreeManaged: repo.managed,
		Worktree:        worktree,
		TmuxSession:     tmuxSession,
		ContainerName:   containerName,
		TokenID:         createdToken.ID,
		TokenName:       tokenName,
		GitLabProjectID: repo.projectID,
		Status:          "running",
		CreatedAt:       now,
	}
	if repo.managed {
		task.Branch = "agent/" + opts.taskID
	}
	if repo.needsToken {
		task.GitLabHost = cfg.GitLab.Host
	}
	if err := deps.saveState(cfg.StateDir, task); err != nil {
		if startedDockerDirect {
			err = joinCleanup(err, "docker container cleanup failed", deps.stopDocker(ctx, task.ContainerName))
			return cleanupAfterTokenFailure(joinCleanup(err, "env file remove cleanup failed", deps.removeEnvFile(envFile)))
		}
		if runtimeKind == localRuntimeMacOSSandbox {
			err = joinCleanup(err, "env file remove cleanup failed", deps.removeEnvFile(envFile))
			err = joinCleanup(err, "tmux session cleanup failed", deps.stopTmux(ctx, opts.taskID))
			if repo.needsToken {
				if revokeErr := tokenClient.RevokeProjectToken(ctx, repo.projectID, createdToken.ID); revokeErr != nil {
					err = joinCleanup(err, "token revoke cleanup failed", revokeErr)
				}
			}
			return cleanupMaybePreparedWorktree(ctx, deps, repo, worktree, err)
		}
		if repo.needsToken {
			return cleanupAfterStateSaveFailure(ctx, deps, repo.path, worktree, envFile, opts.taskID, tokenClient, repo.projectID, createdToken.ID, err)
		}
		return cleanupMaybePreparedWorktree(ctx, deps, repo, worktree, joinCleanup(err, "env file remove cleanup failed", deps.removeEnvFile(envFile)))
	}

	tokenCreated = false
	printRunStarted(opts.out, task)
	if !opts.detach {
		if err := attachRunSession(ctx, deps, task); err != nil {
			return err
		}
	}
	return nil
}

func attachRunSession(ctx context.Context, deps runDeps, task state.Task) error {
	switch task.SessionKind {
	case "docker":
		return deps.attachDocker(ctx, task.ContainerName)
	default:
		return deps.attachTmux(ctx, task.TaskID)
	}
}

func selectLocalRuntime(ctx context.Context, deps runDeps, useTmux bool) (localRuntimeKind, error) {
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
	if !useTmux {
		return "", fmt.Errorf("docker is required but was not found in PATH; macOS sandbox-exec fallback requires tmux")
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

func dockerInfoReady(ctx context.Context) error {
	output, err := osexec.CommandContext(ctx, "docker", "info").CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, message)
}

func printRunStarted(out io.Writer, task state.Task) {
	if out == nil {
		return
	}
	runtimeName := task.SessionKind
	if runtimeName == "" {
		runtimeName = "tmux"
	}
	_, _ = fmt.Fprintf(out, "started %s\n", task.TaskID)
	_, _ = fmt.Fprintf(out, "runtime: %s\n", runtimeName)
	_, _ = fmt.Fprintf(out, "workspace: %s\n", task.Worktree)
	_, _ = fmt.Fprintf(out, "attach: agentctl attach %s\n", task.TaskID)
	_, _ = fmt.Fprintf(out, "cleanup: agentctl cleanup %s\n", task.TaskID)
}

func buildLocalInvocation(ctx context.Context, deps runDeps, runtimeKind localRuntimeKind, cfg *config.Config, opts runOptions, runRuntime runRuntimeSelection, repo resolvedRunRepo, worktree, gitDir string, createdToken tokenbroker.CreatedToken, useTmux bool) (runtime.DockerInvocation, error) {
	switch runtimeKind {
	case localRuntimeDocker:
		authLinks := agentAuthLinks(deps.userHome)
		dockerOpts := runtime.DockerOptions{
			TaskID:   opts.taskID,
			Worktree: worktree,
			GitDir:   gitDir,
			Image:    runRuntime.image,
			Command:  runRuntime.command,
			Mounts:   dockerAgentAuthMounts(authLinks),
			Detached: !useTmux,
		}
		if repo.needsToken {
			dockerOpts.GitLabToken = createdToken.Token
			dockerOpts.GitLabHost = cfg.GitLab.Host
		}
		return deps.dockerInvocationFor(dockerOpts)
	case localRuntimeMacOSSandbox:
		gitPaths := sandboxGitMetadataPaths(ctx, deps.gitMetadataPaths, worktree, gitDir)
		authLinks := agentAuthLinks(deps.userHome)
		if err := ensureAgentAuthLinks(macOSSandboxHomeDir(cfg, opts.taskID), authLinks); err != nil {
			return runtime.DockerInvocation{}, err
		}
		extraPaths := append(gitPaths, agentAuthHostPaths(authLinks)...)
		invocation, err := deps.macOSSandboxInvocationFor(macOSSandboxOptionsFromConfig(cfg, opts.taskID, worktree, runRuntime.command, false, extraPaths))
		if err != nil {
			return runtime.DockerInvocation{}, err
		}
		if repo.needsToken {
			invocation.Env = []string{
				"GITLAB_HOST=" + cfg.GitLab.Host,
				"GITLAB_TOKEN=" + createdToken.Token,
			}
		}
		return invocation, nil
	default:
		return runtime.DockerInvocation{}, fmt.Errorf("unsupported local runtime %q", runtimeKind)
	}
}

func macOSSandboxOptionsFromConfig(cfg *config.Config, taskID, worktree string, command []string, exitAfterCommand bool, extraReadWritePaths []string) runtime.MacOSSandboxOptions {
	homeDir := macOSSandboxHomeDir(cfg, taskID)
	macOSConfig := cfg.Sandbox.MacOS
	allowRead := append([]string{}, macOSConfig.AllowRead...)
	allowRead = append(allowRead, macOSConfig.CustomRules.AllowRead...)
	allowRead = append(allowRead, requiredMacOSSandboxReadPaths()...)
	allowRead = append(allowRead, macOSSandboxToolReadPaths(macOSSandboxTools(macOSConfig.AllowTools))...)
	allowRead = append(allowRead, extraReadWritePaths...)
	allowWrite := append([]string{}, macOSConfig.AllowWrite...)
	allowWrite = append(allowWrite, macOSConfig.CustomRules.AllowWrite...)
	allowWrite = append(allowWrite, extraReadWritePaths...)

	env := map[string]string{}
	for key, value := range macOSConfig.Env {
		env[key] = value
	}
	if developerDir := macOSDeveloperDir(); developerDir != "" {
		env["AGENTCTL_DEVELOPER_DIR"] = developerDir
	}
	if pathPrefix := macOSDeveloperPathPrefix(); pathPrefix != "" {
		env["AGENTCTL_PATH_PREFIX"] = pathPrefix
	}

	return runtime.MacOSSandboxOptions{
		TaskID:           taskID,
		Worktree:         worktree,
		StateDir:         cfg.StateDir,
		HomeDir:          homeDir,
		Command:          command,
		Mode:             macOSConfig.Mode,
		Network:          macOSConfig.Network,
		AllowRead:        resolveMacOSSandboxPaths(allowRead, worktree, homeDir, cfg.StateDir),
		AllowWrite:       resolveMacOSSandboxPaths(allowWrite, worktree, homeDir, cfg.StateDir),
		DenyRead:         resolveMacOSSandboxPaths(macOSConfig.DenyRead, worktree, homeDir, cfg.StateDir),
		Env:              env,
		ExitAfterCommand: exitAfterCommand,
	}
}

func macOSSandboxHomeDir(cfg *config.Config, taskID string) string {
	return filepath.Join(cfg.StateDir, "homes", taskID)
}

func resolveMacOSSandboxPaths(paths []string, worktree, homeDir, stateDir string) []string {
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		switch strings.TrimSpace(path) {
		case "":
			continue
		case "workspace":
			resolved = append(resolved, worktree)
		case "task_home":
			resolved = append(resolved, homeDir)
		case "state_dir":
			resolved = append(resolved, stateDir)
		case "tmp":
			resolved = append(resolved, "/private/tmp", "/tmp")
		default:
			value := strings.ReplaceAll(path, "${WORKSPACE}", worktree)
			value = strings.ReplaceAll(value, "${TASK_HOME}", homeDir)
			value = strings.ReplaceAll(value, "${HOME}", homeDir)
			value = strings.ReplaceAll(value, "${STATE_DIR}", stateDir)
			resolved = append(resolved, value)
		}
	}
	return resolved
}

type agentAuthLink struct {
	name     string
	hostPath string
}

func agentAuthLinks(userHome func() (string, error)) []agentAuthLink {
	if userHome == nil {
		userHome = os.UserHomeDir
	}
	home, err := userHome()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}

	links := []agentAuthLink{}
	for _, name := range []string{".codex", ".claude", ".claude.json"} {
		hostPath := filepath.Join(home, name)
		if _, err := os.Lstat(hostPath); err == nil {
			links = append(links, agentAuthLink{name: name, hostPath: hostPath})
		}
	}
	return links
}

func agentAuthHostPaths(links []agentAuthLink) []string {
	paths := make([]string, 0, len(links))
	for _, link := range links {
		if strings.TrimSpace(link.hostPath) != "" {
			paths = append(paths, link.hostPath)
		}
	}
	return uniqueStrings(paths)
}

func dockerAgentAuthMounts(links []agentAuthLink) []runtime.Mount {
	mounts := make([]runtime.Mount, 0, len(links))
	for _, link := range links {
		if strings.TrimSpace(link.hostPath) == "" || strings.TrimSpace(link.name) == "" {
			continue
		}
		mounts = append(mounts, runtime.Mount{
			HostPath:      link.hostPath,
			ContainerPath: filepath.Join("/root", link.name),
			Mode:          "rw",
		})
	}
	return mounts
}

func ensureAgentAuthLinks(homeDir string, links []agentAuthLink) error {
	if len(links) == 0 {
		return nil
	}
	if err := os.MkdirAll(homeDir, 0700); err != nil {
		return err
	}

	for _, link := range links {
		if strings.TrimSpace(link.name) == "" || strings.TrimSpace(link.hostPath) == "" {
			continue
		}
		dest := filepath.Join(homeDir, link.name)
		info, err := os.Lstat(dest)
		if err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			current, err := os.Readlink(dest)
			if err == nil && current == link.hostPath {
				continue
			}
			if err := os.Remove(dest); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Symlink(link.hostPath, dest); err != nil {
			return err
		}
	}
	return nil
}

func requiredMacOSSandboxReadPaths() []string {
	return uniqueStrings(append([]string{
		"/private/var/select",
		"/var/select",
		"/var/db/xcode_select_link",
		"/private/var/db/xcode_select_link",
		"/etc/codex",
		"/private/etc/codex",
	}, macOSDeveloperDirReadPaths()...))
}

func macOSSandboxTools(configured []string) []string {
	tools := []string{
		"git",
		"ssh",
		"make",
		"cmake",
		"clang",
		"clang++",
		"gcc",
		"g++",
		"python",
		"python3",
		"pip",
		"pip3",
		"uv",
		"poetry",
		"node",
		"npm",
		"npx",
		"pnpm",
		"yarn",
		"bun",
		"deno",
		"go",
		"rustup",
		"cargo",
		"rustc",
		"java",
		"javac",
		"mvn",
		"gradle",
		"jq",
		"rg",
		"zsh",
		"codex",
		"claude",
		"agentctl",
	}
	tools = append(tools, configured...)
	return uniqueStrings(tools)
}

func macOSDeveloperDirReadPaths() []string {
	path := macOSDeveloperDir()
	paths := []string{}
	if path != "" {
		paths = append(paths, path)
	}
	paths = append(paths, macOSDeveloperToolDirs()...)
	return uniqueStrings(paths)
}

func macOSDeveloperDir() string {
	path, err := filepath.EvalSymlinks("/var/select/developer_dir")
	if err != nil || strings.TrimSpace(path) == "" {
		return ""
	}
	return path
}

func macOSDeveloperPathPrefix() string {
	return strings.Join(macOSDeveloperToolDirs(), ":")
}

func macOSDeveloperToolDirs() []string {
	candidates := []string{
		"/Library/Developer/CommandLineTools/usr/bin",
	}
	if developerDir := macOSDeveloperDir(); developerDir != "" {
		candidates = append(candidates,
			filepath.Join(developerDir, "usr", "bin"),
			filepath.Join(developerDir, "Toolchains", "XcodeDefault.xctoolchain", "usr", "bin"),
		)
	}

	paths := []string{}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			paths = append(paths, path)
		}
	}
	return uniqueStrings(paths)
}

func sandboxGitMetadataPaths(ctx context.Context, resolver func(context.Context, string) []string, worktree, gitDir string) []string {
	paths := []string{}
	if strings.TrimSpace(gitDir) != "" {
		paths = append(paths, gitDir)
	}
	if resolver != nil {
		paths = append(paths, resolver(ctx, worktree)...)
	}
	return uniqueStrings(paths)
}

func discoverGitMetadataPaths(ctx context.Context, worktree string) []string {
	output, err := osexec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir").Output()
	if err == nil {
		paths := []string{}
		for _, line := range strings.Split(string(output), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				paths = append(paths, line)
			}
		}
		return uniqueStrings(paths)
	}
	return discoverGitMetadataPathsFromDotGit(worktree)
}

func discoverGitMetadataPathsFromDotGit(worktree string) []string {
	dotGit := filepath.Join(worktree, ".git")
	if info, err := os.Stat(dotGit); err == nil && info.IsDir() {
		return []string{dotGit}
	}

	data, err := os.ReadFile(dotGit)
	if err != nil {
		return nil
	}
	gitDirLine := strings.TrimSpace(string(data))
	gitDir, ok := strings.CutPrefix(gitDirLine, "gitdir:")
	if !ok {
		return nil
	}
	gitDir = strings.TrimSpace(gitDir)
	if gitDir == "" {
		return nil
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Clean(filepath.Join(worktree, gitDir))
	}

	paths := []string{gitDir}
	if commonDir := discoverGitCommonDir(gitDir); commonDir != "" {
		paths = append(paths, commonDir)
	}
	return uniqueStrings(paths)
}

func discoverGitCommonDir(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return ""
	}
	commonDir := strings.TrimSpace(string(data))
	if commonDir == "" {
		return ""
	}
	if filepath.IsAbs(commonDir) {
		return filepath.Clean(commonDir)
	}
	return filepath.Clean(filepath.Join(gitDir, commonDir))
}

func macOSSandboxToolReadPaths(tools []string) []string {
	return macOSSandboxToolReadPathsWithResolvers(tools, osexec.LookPath, filepath.EvalSymlinks, os.Executable)
}

func macOSSandboxToolReadPathsWithResolvers(tools []string, lookPath func(string) (string, error), evalSymlinks func(string) (string, error), executable func() (string, error)) []string {
	paths := []string{}
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}

		path := tool
		var err error
		if !strings.Contains(path, "/") {
			path, err = lookPath(tool)
			if err != nil {
				if tool != "agentctl" {
					continue
				}
				path, err = executable()
				if err != nil {
					continue
				}
			}
		}
		paths = append(paths, toolReadPathsForPath(path, evalSymlinks)...)
	}
	return uniqueStrings(paths)
}

func toolReadPathsForPath(path string, evalSymlinks func(string) (string, error)) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	paths := []string{filepath.Dir(path)}
	if realPath, err := evalSymlinks(path); err == nil && realPath != "" {
		paths = append(paths, filepath.Dir(realPath))
		if packageRoot := nodePackageRoot(realPath); packageRoot != "" {
			paths = append(paths, packageRoot)
		}
	}
	if packageRoot := nodePackageRoot(path); packageRoot != "" {
		paths = append(paths, packageRoot)
	}
	return paths
}

func nodePackageRoot(path string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i, part := range parts {
		if part != "node_modules" || i+1 >= len(parts) {
			continue
		}
		if strings.HasPrefix(parts[i+1], "@") {
			if i+2 >= len(parts) {
				return ""
			}
			return filepath.FromSlash(strings.Join(parts[:i+3], "/"))
		}
		return filepath.FromSlash(strings.Join(parts[:i+2], "/"))
	}
	return ""
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func macOSSandboxFallbackPrompt() string {
	return strings.Join([]string{
		"Docker is not available. Fallback to macOS sandbox-exec?",
		"Risk: sandbox-exec is deprecated by Apple and weaker than Docker for untrusted code.",
		"It has no container filesystem, CPU, memory, or PID isolation.",
		"Default strict mode denies file reads outside the configured allowlist and may break local toolchains.",
		"Config mode write_only is weaker: it can read host files and only restricts writes.",
		"Continue with sandbox-exec? [y/N]: ",
	}, "\n")
}

func promptMacOSSandboxFallback(prompt string) (bool, error) {
	if _, err := fmt.Fprint(os.Stderr, prompt); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func resolveRunRepo(ctx context.Context, deps runDeps, cfg *config.Config, repoInput string) (resolvedRunRepo, error) {
	if strings.TrimSpace(repoInput) == "" {
		repoPath, err := deps.workingDir()
		if err != nil {
			return resolvedRunRepo{}, err
		}
		remoteURL, err := deps.originRemote(ctx, repoPath)
		if err != nil {
			return resolvedRunRepo{
				displayName: repoPath,
				path:        repoPath,
			}, nil
		}
		projectPath, err := gitLabProjectPathFromRemote(remoteURL, cfg.GitLab.Host)
		if err != nil {
			return resolvedRunRepo{
				displayName: repoPath,
				path:        repoPath,
			}, nil
		}
		return resolvedRunRepo{
			displayName:   repoPath,
			path:          repoPath,
			projectID:     projectPath,
			defaultBranch: "main",
			remote:        remoteURL,
			managed:       true,
			needsToken:    true,
		}, nil
	}

	if repo, ok := cfg.Repos[repoInput]; ok {
		defaultBranch := strings.TrimSpace(repo.DefaultBranch)
		if defaultBranch == "" {
			defaultBranch = "main"
		}
		return resolvedRunRepo{
			displayName:   repoInput,
			path:          repo.Path,
			projectID:     repo.ProjectID,
			defaultBranch: defaultBranch,
			remote:        repo.Remote,
			managed:       true,
			needsToken:    true,
		}, nil
	}

	projectPath, err := gitLabProjectPathFromRemote(repoInput, cfg.GitLab.Host)
	isGitLabURL := err == nil
	if err != nil && !looksLikeRepoURL(repoInput) {
		return resolvedRunRepo{}, fmt.Errorf("repo %q not found in config", repoInput)
	}

	cachePathName := projectPath
	if !isGitLabURL {
		cachePathName = repoInput
	}
	repoPath := cachedRepoPath(cfg.StateDir, repoInput, cachePathName)
	if err := deps.ensureRepoClone(ctx, repoInput, repoPath); err != nil {
		return resolvedRunRepo{}, err
	}

	resolved := resolvedRunRepo{
		displayName:   repoInput,
		path:          repoPath,
		defaultBranch: "main",
		remote:        repoInput,
		managed:       true,
	}
	if isGitLabURL {
		resolved.projectID = projectPath
		resolved.needsToken = true
	}
	return resolved, nil
}

func gitOriginRemote(ctx context.Context, repoPath string) (string, error) {
	output, err := osexec.CommandContext(ctx, "git", "-C", repoPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func gitLabProjectPathFromRemote(remoteURL, gitLabHost string) (string, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	host := strings.TrimSpace(gitLabHost)
	if host == "" {
		host = "gitlab.com"
	}

	if strings.Contains(remoteURL, "://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return "", err
		}
		if parsed.Hostname() != host {
			return "", fmt.Errorf("repo URL host %q does not match configured GitLab host %q", parsed.Hostname(), host)
		}
		return cleanGitLabProjectPath(parsed.EscapedPath())
	}

	if strings.HasPrefix(remoteURL, "git@") {
		rest := strings.TrimPrefix(remoteURL, "git@")
		hostPart, pathPart, ok := strings.Cut(rest, ":")
		if !ok {
			return "", fmt.Errorf("invalid GitLab SSH repo URL: %s", remoteURL)
		}
		if hostPart != host {
			return "", fmt.Errorf("repo URL host %q does not match configured GitLab host %q", hostPart, host)
		}
		return cleanGitLabProjectPath(pathPart)
	}

	return "", fmt.Errorf("repo %q is not a configured repo name or supported GitLab URL", remoteURL)
}

func cleanGitLabProjectPath(path string) (string, error) {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return "", fmt.Errorf("GitLab repo URL is missing project path")
	}
	path = strings.TrimSuffix(path, ".git")
	if !strings.Contains(path, "/") {
		return "", fmt.Errorf("GitLab repo URL project path %q must include namespace and project", path)
	}
	return path, nil
}

func looksLikeRepoURL(repoInput string) bool {
	return strings.Contains(repoInput, "://") || strings.HasPrefix(repoInput, "git@")
}

func cachedRepoPath(stateDir, remoteURL, projectPath string) string {
	sum := sha256.Sum256([]byte(remoteURL))
	projectName := filepath.Base(projectPath)
	projectName = strings.TrimSuffix(projectName, ".git")
	projectName = validCacheNameChars.ReplaceAllString(projectName, "-")
	if projectName == "" {
		projectName = "repo"
	}
	return filepath.Join(stateDir, "repos", fmt.Sprintf("%x-%s", sum[:4], projectName))
}

var validCacheNameChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func runRemote(ctx context.Context, deps runDeps, opts runOptions) error {
	cfg, err := deps.loadConfig(opts.configPath)
	if err != nil {
		return err
	}

	target, err := remoteTargetFromConfig(cfg, opts.remoteName)
	if err != nil {
		return err
	}

	args := []string{"run", opts.taskID}
	if opts.repoName != "" {
		args = append(args, "--repo", opts.repoName)
	}
	if opts.agent != "" {
		args = append(args, "--agent", opts.agent)
	}
	if opts.risk != "" {
		args = append(args, "--risk", opts.risk)
	}
	if opts.templateName != "" {
		args = append(args, "--template", opts.templateName)
	}
	if opts.noTmux {
		args = append(args, "--no-tmux")
	}
	args = append(args, "--detach")
	args = appendRemoteConfigArgs(args, remoteConfigPathFromConfig(cfg, opts.remoteName))

	return deps.forwardRemote(ctx, target, false, args...)
}

func remoteTargetFromConfig(cfg *config.Config, remoteName string) (remote.Target, error) {
	remoteConfig, ok := cfg.Remotes[remoteName]
	if !ok {
		return remote.Target{}, fmt.Errorf("remote %q not found in config", remoteName)
	}

	host := strings.TrimSpace(remoteConfig.Host)
	if host == "" {
		return remote.Target{}, fmt.Errorf("remote %q host is required", remoteName)
	}

	return remote.Target{
		Host:         remoteConfig.Host,
		User:         remoteConfig.User,
		AgentctlPath: remoteConfig.AgentctlPath,
	}, nil
}

func devRemoteTargetFromConfig(cfg *config.Config, configPath, remoteName string, confirm func(string) (bool, error), save func(string, *config.Config) error) (remote.Target, error) {
	target, err := remoteTargetFromConfig(cfg, remoteName)
	if err == nil {
		return target, nil
	}

	saveName, remoteConfig, inlineTarget, inlineErr := inlineRemoteConfig(remoteName)
	if inlineErr != nil {
		return remote.Target{}, err
	}
	if confirm == nil || save == nil {
		return inlineTarget, nil
	}

	shouldSave, confirmErr := confirm(fmt.Sprintf("Remote %q not found in config. Use %s as SSH target and save it as %q in config? [Y/n]: ", remoteName, inlineRemoteDisplay(remoteConfig), saveName))
	if confirmErr != nil {
		return remote.Target{}, confirmErr
	}
	if shouldSave {
		if cfg.Remotes == nil {
			cfg.Remotes = map[string]config.Remote{}
		}
		cfg.Remotes[saveName] = remoteConfig
		if err := save(configPath, cfg); err != nil {
			return remote.Target{}, err
		}
	}

	return inlineTarget, nil
}

func inlineRemoteConfig(value string) (string, config.Remote, remote.Target, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", config.Remote{}, remote.Target{}, fmt.Errorf("remote is required")
	}
	if strings.Contains(value, "://") || strings.Contains(value, "/") {
		return "", config.Remote{}, remote.Target{}, fmt.Errorf("remote %q is not an SSH target", value)
	}

	user := ""
	host := value
	if strings.Contains(value, "@") {
		parts := strings.Split(value, "@")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return "", config.Remote{}, remote.Target{}, fmt.Errorf("invalid remote SSH target %q", value)
		}
		user = strings.TrimSpace(parts[0])
		host = strings.TrimSpace(parts[1])
	}
	if err := validateInlineRemotePart("host", host, false); err != nil {
		return "", config.Remote{}, remote.Target{}, err
	}
	if err := validateInlineRemotePart("user", user, true); err != nil {
		return "", config.Remote{}, remote.Target{}, err
	}

	name := remoteConfigName(host)
	remoteConfig := config.Remote{Host: host, User: user}
	target := remote.Target{Host: host, User: user}
	return name, remoteConfig, target, nil
}

func validateInlineRemotePart(kind, value string, allowEmpty bool) error {
	if value == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("remote %s is required", kind)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("remote %s must not start with '-'", kind)
	}
	for _, r := range value {
		if r <= 32 || r == 127 || r == '@' && kind == "user" {
			return fmt.Errorf("remote %s contains invalid character %q", kind, r)
		}
		if !(r >= 'A' && r <= 'Z' ||
			r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' ||
			r == '.' || r == '_' || r == '-') {
			return fmt.Errorf("remote %s contains invalid character %q", kind, r)
		}
	}
	return nil
}

func remoteConfigName(host string) string {
	name := strings.Trim(strings.ToLower(host), ".-_")
	if name == "" {
		return "remote"
	}
	return name
}

func inlineRemoteDisplay(remoteConfig config.Remote) string {
	if remoteConfig.User == "" {
		return remoteConfig.Host
	}
	return remoteConfig.User + "@" + remoteConfig.Host
}

func promptSaveRemoteConfig(prompt string) (bool, error) {
	if _, err := fmt.Fprint(os.Stderr, prompt); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "" || answer == "y" || answer == "yes", nil
}

func remoteConfigPathFromConfig(cfg *config.Config, remoteName string) string {
	return strings.TrimSpace(cfg.Remotes[remoteName].ConfigPath)
}

func appendRemoteConfigArgs(args []string, configPath string) []string {
	if configPath == "" {
		return args
	}

	return append(args, "--config", configPath)
}

func runRuntimeFor(cfg *config.Config, opts runOptions) (runRuntimeSelection, error) {
	templateName := strings.TrimSpace(opts.templateName)
	if templateName == "" {
		templateName = strings.TrimSpace(cfg.Templates.Default)
	}
	if templateName == "" {
		templateName = "generic"
	}

	template, ok := supportedRunTemplates()[templateName]
	if !ok {
		return runRuntimeSelection{}, fmt.Errorf("unsupported template %q; supported templates: generic, node, golang, python", templateName)
	}

	agent := strings.TrimSpace(opts.agent)
	if agent == "" {
		agent = "codex"
	}
	switch agent {
	case "codex", "claude", "shell":
	default:
		return runRuntimeSelection{}, fmt.Errorf("unsupported agent %q; supported agents: codex, claude, shell", agent)
	}

	return runRuntimeSelection{
		image:   template.image,
		command: buildRunCommand(template, agent),
	}, nil
}

func supportedRunTemplates() map[string]runTemplateSpec {
	return map[string]runTemplateSpec{
		"generic": {
			image: "node:22-bookworm",
		},
		"node": {
			image:     "node:22-bookworm",
			preflight: "node --version",
		},
		"golang": {
			image:     "golang:1.22-bookworm",
			preflight: "go version",
		},
		"python": {
			image:     "python:3.12-bookworm",
			preflight: "python --version",
		},
	}
}

func buildRunCommand(template runTemplateSpec, agent string) []string {
	lines := []string{"set -euo pipefail"}
	if template.preflight != "" {
		lines = append(lines, template.preflight)
	}

	switch agent {
	case "shell":
		lines = appendInteractiveLoginShell(lines)
	case "codex":
		lines = append(lines,
			"if command -v codex >/dev/null 2>&1; then",
			"  exec codex",
			"fi",
			`printf '%s\n' 'codex executable not found; falling back to shell' >&2`,
		)
		lines = appendInteractiveLoginShell(lines)
	case "claude":
		lines = append(lines,
			"if command -v claude >/dev/null 2>&1; then",
			"  exec claude",
			"fi",
			`printf '%s\n' 'claude executable not found; falling back to shell' >&2`,
		)
		lines = appendInteractiveLoginShell(lines)
	}

	return []string{"bash", "-c", strings.Join(lines, "\n")}
}

func appendInteractiveLoginShell(lines []string) []string {
	return append(lines,
		"if command -v zsh >/dev/null 2>&1; then",
		"  exec zsh -l",
		"fi",
		"if command -v bash >/dev/null 2>&1; then",
		"  exec bash -l",
		"fi",
		"exec sh",
	)
}

func validateRunTaskID(taskID string) error {
	if !validRunTaskIDPattern.MatchString(taskID) {
		return fmt.Errorf("invalid task id: %q", taskID)
	}

	return nil
}

func cleanupAfterStateSaveFailure(ctx context.Context, deps runDeps, repoPath, worktree, envFile, taskID string, tokenClient runTokenClient, projectID, tokenID string, saveErr error) error {
	errs := []error{saveErr}
	if err := deps.removeEnvFile(envFile); err != nil {
		errs = append(errs, fmt.Errorf("env file remove cleanup failed: %w", err))
	}
	if err := deps.stopTmux(ctx, taskID); err != nil {
		errs = append(errs, fmt.Errorf("tmux cleanup failed: %w", err))
	}
	if err := tokenClient.RevokeProjectToken(ctx, projectID, tokenID); err != nil {
		errs = append(errs, fmt.Errorf("token revoke cleanup failed: %w", err))
	}
	if err := deps.removeWorktree(ctx, repoPath, worktree); err != nil {
		errs = append(errs, fmt.Errorf("git worktree cleanup failed: %w", err))
	}

	return errors.Join(errs...)
}

func cleanupPreparedWorktree(ctx context.Context, deps runDeps, repoPath, worktree string, failure error) error {
	return joinCleanup(failure, "git worktree cleanup failed", deps.removeWorktree(ctx, repoPath, worktree))
}

func cleanupMaybePreparedWorktree(ctx context.Context, deps runDeps, repo resolvedRunRepo, worktree string, failure error) error {
	if !repo.managed {
		return failure
	}
	return cleanupPreparedWorktree(ctx, deps, repo.path, worktree, failure)
}

func joinCleanup(original error, label string, cleanupErr error) error {
	if cleanupErr == nil {
		return original
	}

	return errors.Join(original, fmt.Errorf("%s: %w", label, cleanupErr))
}

func cleanupEnvFile(envFile string) error {
	if envFile == "" {
		return nil
	}

	err := os.Remove(envFile)
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

func writeDockerEnvFile(stateDir, taskID string, env []string) (string, error) {
	envDir := filepath.Join(stateDir, "env")
	if err := os.MkdirAll(envDir, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(envDir, 0700); err != nil {
		return "", err
	}

	var lines []string
	for _, pair := range env {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return "", fmt.Errorf("invalid Docker env pair %q", pair)
		}
		lines = append(lines, "export "+key+"="+shellSingleQuote(value))
	}

	envFile := filepath.Join(envDir, taskID+".env")
	file, err := os.OpenFile(envFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(strings.Join(lines, "\n") + "\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return "", joinCleanup(errors.Join(writeErr, closeErr), "env file cleanup failed", cleanupEnvFile(envFile))
	}
	if err := os.Chmod(envFile, 0600); err != nil {
		return "", joinCleanup(err, "env file cleanup failed", cleanupEnvFile(envFile))
	}

	return envFile, nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func wrapDockerCommandWithEnvFile(envFile string, dockerCommand []string) []string {
	script := strings.Join([]string{
		"set -euo pipefail",
		`env_file="$1"`,
		"shift",
		`cleanup() { rm -f "$env_file"; }`,
		"trap cleanup EXIT",
		"set -a",
		`. "$env_file"`,
		"set +a",
		`rm -f "$env_file"`,
		"trap - EXIT",
		`exec "$@"`,
	}, "\n")

	command := []string{"bash", "-lc", script, "--", envFile}
	command = append(command, dockerCommand...)
	return command
}
