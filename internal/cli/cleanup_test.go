package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cleanuppkg "github.com/your-org/agentctl/internal/cleanup"
	"github.com/your-org/agentctl/internal/config"
	"github.com/your-org/agentctl/internal/remote"
	"github.com/your-org/agentctl/internal/state"
)

func TestCleanupLoadsConfigStateAndCallsCleanupService(t *testing.T) {
	tmp := t.TempDir()
	cfg := testCleanupConfig(tmp)
	task := cleanupCLITestTask()
	task.Worktree = filepath.Join(cfg.BaseDir, "XL-123")
	fakes := &cleanupCLIFakes{
		cfg:  cfg,
		task: task,
		env:  map[string]string{"GITLAB_CONTROL_PAT": "control-pat"},
	}

	cmd := newCleanupCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{filepath.Join(tmp, "config.yaml")}) {
		t.Fatalf("config paths = %#v", fakes.loadConfigPaths)
	}
	if !reflect.DeepEqual(fakes.loadTaskCalls, []loadCleanupTaskCall{{
		stateDir: cfg.StateDir,
		taskID:   "XL-123",
	}}) {
		t.Fatalf("load task calls = %#v", fakes.loadTaskCalls)
	}
	if len(fakes.cleanupCalls) != 1 {
		t.Fatalf("cleanup calls = %#v, want one", fakes.cleanupCalls)
	}
	req := fakes.cleanupCalls[0]
	if req.TaskID != "XL-123" {
		t.Fatalf("request task id = %q", req.TaskID)
	}
	if !reflect.DeepEqual(req.Task, task) {
		t.Fatalf("request task = %#v, want %#v", req.Task, task)
	}
	if req.RepoPath != cfg.Repos["backend"].Path {
		t.Fatalf("repo path = %q, want %q", req.RepoPath, cfg.Repos["backend"].Path)
	}
	if req.StateDir != cfg.StateDir {
		t.Fatalf("state dir = %q, want %q", req.StateDir, cfg.StateDir)
	}
	if req.GitLabBaseURL != "https://gitlab.saved.example.com" {
		t.Fatalf("GitLab base URL = %q", req.GitLabBaseURL)
	}
	if req.ControlPAT != "control-pat" {
		t.Fatalf("control PAT = %q", req.ControlPAT)
	}
	if req.ExpectedTmuxSession != "agentctl-XL-123" {
		t.Fatalf("expected tmux session = %q", req.ExpectedTmuxSession)
	}
	if req.ExpectedWorktree != filepath.Join(cfg.BaseDir, "XL-123") {
		t.Fatalf("expected worktree = %q, want %q", req.ExpectedWorktree, filepath.Join(cfg.BaseDir, "XL-123"))
	}
	if req.RepoLookupError != nil {
		t.Fatalf("repo lookup error = %v, want nil", req.RepoLookupError)
	}
}

func TestCleanupRejectsInvalidTaskIDBeforeLoadingConfigOrState(t *testing.T) {
	fakes := &cleanupCLIFakes{
		cfg:  testCleanupConfig(t.TempDir()),
		task: cleanupCLITestTask(),
		env:  map[string]string{},
	}

	cmd := newCleanupCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"../other", "--config", "/tmp/config.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want invalid task id")
	}
	if !strings.Contains(err.Error(), "invalid task id") {
		t.Fatalf("error = %v, want invalid task id message", err)
	}
	if len(fakes.loadConfigPaths) != 0 {
		t.Fatalf("config paths = %#v, want none", fakes.loadConfigPaths)
	}
	if len(fakes.loadTaskCalls) != 0 {
		t.Fatalf("load task calls = %#v, want none", fakes.loadTaskCalls)
	}
	if len(fakes.cleanupCalls) != 0 {
		t.Fatalf("cleanup calls = %#v, want none", fakes.cleanupCalls)
	}
}

func TestCleanupReturnsMissingStateErrorWithoutCleanup(t *testing.T) {
	loadErr := os.ErrNotExist
	cfg := testCleanupConfig(t.TempDir())
	fakes := &cleanupCLIFakes{
		cfg:         cfg,
		env:         map[string]string{},
		loadTaskErr: loadErr,
	}

	cmd := newCleanupCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--config", "/tmp/config.yaml"})

	err := cmd.Execute()
	if !errors.Is(err, loadErr) {
		t.Fatalf("error = %v, want %v", err, loadErr)
	}
	if !reflect.DeepEqual(fakes.loadTaskCalls, []loadCleanupTaskCall{{
		stateDir: cfg.StateDir,
		taskID:   "XL-123",
	}}) {
		t.Fatalf("load task calls = %#v", fakes.loadTaskCalls)
	}
	if len(fakes.cleanupCalls) != 0 {
		t.Fatalf("cleanup calls = %#v, want none", fakes.cleanupCalls)
	}
}

func TestCleanupMissingRepoInConfigSurfacesAndStillCallsCleanupService(t *testing.T) {
	cfg := testCleanupConfig(t.TempDir())
	cfg.Repos = map[string]config.Repo{}
	task := cleanupCLITestTask()
	task.Worktree = filepath.Join(cfg.BaseDir, "XL-123")
	fakes := &cleanupCLIFakes{
		cfg:  cfg,
		task: task,
		env:  map[string]string{},
	}

	cmd := newCleanupCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--config", "/tmp/config.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want missing repo error")
	}
	if !strings.Contains(err.Error(), `repo "backend" not found in config`) {
		t.Fatalf("error = %v, want missing repo message", err)
	}
	if len(fakes.cleanupCalls) != 1 {
		t.Fatalf("cleanup calls = %#v, want one", fakes.cleanupCalls)
	}
	req := fakes.cleanupCalls[0]
	if req.RepoPath != "" {
		t.Fatalf("repo path = %q, want empty when repo is missing", req.RepoPath)
	}
	if req.ExpectedWorktree != filepath.Join(cfg.BaseDir, "XL-123") {
		t.Fatalf("expected worktree = %q, want %q", req.ExpectedWorktree, filepath.Join(cfg.BaseDir, "XL-123"))
	}
	if req.RepoLookupError == nil {
		t.Fatal("repo lookup error = nil, want missing repo error")
	}
}

func TestRemoteCleanupForwardsNonInteractiveSSHWithoutLocalCleanup(t *testing.T) {
	tmp := t.TempDir()
	cfg := testCleanupConfig(tmp)
	configPath := filepath.Join(tmp, "config.yaml")
	cfg.Remotes = map[string]config.Remote{
		"buildbox-1": {
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
	}
	fakes := &cleanupCLIFakes{
		cfg:  cfg,
		task: cleanupCLITestTask(),
		env:  map[string]string{"GITLAB_CONTROL_PAT": "control-pat"},
	}

	cmd := newCleanupCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--remote", "buildbox-1", "--config", configPath})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{configPath}) {
		t.Fatalf("config paths = %#v, want remote config load", fakes.loadConfigPaths)
	}
	wantCall := cleanupRemoteCall{
		target: remote.Target{
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
		interactive: false,
		args:        []string{"cleanup", "XL-123"},
	}
	if !reflect.DeepEqual(fakes.remoteCalls, []cleanupRemoteCall{wantCall}) {
		t.Fatalf("remote calls = %#v, want %#v", fakes.remoteCalls, []cleanupRemoteCall{wantCall})
	}
	assertNoForwardedRemoteFlag(t, fakes.remoteCalls[0].args)
	if len(fakes.loadTaskCalls) != 0 {
		t.Fatalf("load task calls = %#v, want none", fakes.loadTaskCalls)
	}
	if len(fakes.cleanupCalls) != 0 {
		t.Fatalf("cleanup calls = %#v, want none", fakes.cleanupCalls)
	}
}

func TestRemoteCleanupForwardsConfiguredRemoteConfigPath(t *testing.T) {
	tmp := t.TempDir()
	cfg := testCleanupConfig(tmp)
	localConfigPath := filepath.Join(tmp, "local-config.yaml")
	cfg.Remotes = map[string]config.Remote{
		"buildbox-1": {
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
			ConfigPath:   "/etc/agentctl/config.yaml",
		},
	}
	fakes := &cleanupCLIFakes{
		cfg:  cfg,
		task: cleanupCLITestTask(),
		env:  map[string]string{"GITLAB_CONTROL_PAT": "control-pat"},
	}

	cmd := newCleanupCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--remote", "buildbox-1", "--config", localConfigPath})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{localConfigPath}) {
		t.Fatalf("config paths = %#v, want local config load", fakes.loadConfigPaths)
	}
	wantCall := cleanupRemoteCall{
		target: remote.Target{
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
		interactive: false,
		args:        []string{"cleanup", "XL-123", "--config", "/etc/agentctl/config.yaml"},
	}
	if !reflect.DeepEqual(fakes.remoteCalls, []cleanupRemoteCall{wantCall}) {
		t.Fatalf("remote calls = %#v, want %#v", fakes.remoteCalls, []cleanupRemoteCall{wantCall})
	}
	assertNoForwardedRemoteFlag(t, fakes.remoteCalls[0].args)
	if len(fakes.loadTaskCalls) != 0 {
		t.Fatalf("load task calls = %#v, want none", fakes.loadTaskCalls)
	}
	if len(fakes.cleanupCalls) != 0 {
		t.Fatalf("cleanup calls = %#v, want none", fakes.cleanupCalls)
	}
}

func TestCleanupCommandAdaptersMapAlreadyRemovedErrors(t *testing.T) {
	commandErr := errors.New("exit 1")
	tests := []struct {
		name string
		run  func(cleanupCommandRunner) error
		out  string
	}{
		{
			name: "docker missing container",
			run: func(runner cleanupCommandRunner) error {
				return removeDockerContainer(context.Background(), runner, "agent-XL-123")
			},
			out: "Error response from daemon: No such container: agent-XL-123\n",
		},
		{
			name: "tmux missing session",
			run: func(runner cleanupCommandRunner) error {
				return killTmuxSession(context.Background(), runner, "XL-123")
			},
			out: "can't find session: agentctl-XL-123\n",
		},
		{
			name: "tmux no server",
			run: func(runner cleanupCommandRunner) error {
				return killTmuxSession(context.Background(), runner, "XL-123")
			},
			out: "no server running on /tmp/tmux-501/default\n",
		},
		{
			name: "git missing worktree",
			run: func(runner cleanupCommandRunner) error {
				return removeGitWorktree(context.Background(), runner, "/repo/backend", "/worktrees/XL-123")
			},
			out: "fatal: '/worktrees/XL-123' is not a working tree\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run(func(context.Context, string, ...string) ([]byte, error) {
				return []byte(tt.out), commandErr
			})
			if !errors.Is(err, cleanuppkg.ErrAlreadyRemoved) {
				t.Fatalf("error = %v, want %v", err, cleanuppkg.ErrAlreadyRemoved)
			}
		})
	}
}

func TestCleanupCommandAdaptersKeepUnknownErrors(t *testing.T) {
	commandErr := errors.New("exit 1")
	err := removeDockerContainer(context.Background(), func(context.Context, string, ...string) ([]byte, error) {
		return []byte("permission denied\n"), commandErr
	}, "agent-XL-123")

	if !errors.Is(err, commandErr) {
		t.Fatalf("error = %v, want command error", err)
	}
	if errors.Is(err, cleanuppkg.ErrAlreadyRemoved) {
		t.Fatalf("error = %v, must not be already removed", err)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v, want command output", err)
	}
}

func testCleanupConfig(tmp string) *config.Config {
	return &config.Config{
		BaseDir:  filepath.Join(tmp, "worktrees"),
		StateDir: filepath.Join(tmp, "state"),
		GitLab: config.GitLabConfig{
			Host: "gitlab.config.example.com",
		},
		Repos: map[string]config.Repo{
			"backend": {
				Path:      filepath.Join(tmp, "backend"),
				ProjectID: "123",
			},
		},
	}
}

func cleanupCLITestTask() state.Task {
	return state.Task{
		TaskID:          "XL-123",
		Repo:            "backend",
		Worktree:        "/worktrees/XL-123",
		TmuxSession:     "agentctl-XL-123",
		ContainerName:   "agent-XL-123",
		TokenID:         "98765",
		TokenName:       "agent-XL-123-1780662896",
		GitLabProjectID: "123",
		GitLabHost:      "gitlab.saved.example.com",
		Status:          "running",
	}
}

type cleanupCLIFakes struct {
	cfg             *config.Config
	env             map[string]string
	task            state.Task
	loadConfigPaths []string
	loadTaskCalls   []loadCleanupTaskCall
	loadTaskErr     error
	cleanupCalls    []cleanuppkg.Request
	cleanupErr      error
	remoteCalls     []cleanupRemoteCall
	remoteErr       error
}

func (f *cleanupCLIFakes) deps() cleanupDeps {
	return cleanupDeps{
		loadConfig: func(path string) (*config.Config, error) {
			f.loadConfigPaths = append(f.loadConfigPaths, path)
			return f.cfg, nil
		},
		getenv: func(key string) string {
			return f.env[key]
		},
		loadTask: func(stateDir, taskID string) (state.Task, error) {
			f.loadTaskCalls = append(f.loadTaskCalls, loadCleanupTaskCall{
				stateDir: stateDir,
				taskID:   taskID,
			})
			return f.task, f.loadTaskErr
		},
		runCleanup: func(_ context.Context, req cleanuppkg.Request) error {
			f.cleanupCalls = append(f.cleanupCalls, req)
			if f.cleanupErr != nil {
				return f.cleanupErr
			}
			return req.RepoLookupError
		},
		forwardRemote: func(_ context.Context, target remote.Target, interactive bool, args ...string) error {
			f.remoteCalls = append(f.remoteCalls, cleanupRemoteCall{
				target:      target,
				interactive: interactive,
				args:        append([]string(nil), args...),
			})
			return f.remoteErr
		},
	}
}

type loadCleanupTaskCall struct {
	stateDir string
	taskID   string
}

type cleanupRemoteCall struct {
	target      remote.Target
	interactive bool
	args        []string
}
