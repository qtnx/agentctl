package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qtnx/agentctl/internal/config"
	"github.com/qtnx/agentctl/internal/remote"
	"github.com/qtnx/agentctl/internal/runtime"
	"github.com/qtnx/agentctl/internal/state"
	"github.com/qtnx/agentctl/internal/tokenbroker"
)

func TestRunStartsLocalAgentWorkspace(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	hostHome := createAgentAuthFixtures(t, tmp)
	now := time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC)
	tokenValue := "glpat-created-'secret'"
	fakes := newRunFakes(cfg, now)
	fakes.userHome = hostHome
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.createdToken = tokenbroker.CreatedToken{
		ID:    "98765",
		Token: tokenValue,
		Name:  "agent-XL-123-1780662896",
	}

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"XL-123",
		"--repo", "backend",
		"--config", filepath.Join(tmp, "config.yaml"),
		"--agent", "codex",
		"--risk", "untrusted",
		"--template", "generic",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	wantWorktree := filepath.Join(cfg.BaseDir, "XL-123")
	wantGitDir := filepath.Join(cfg.Repos["backend"].Path, ".git")
	if !reflect.DeepEqual(fakes.prepareCalls, []prepareWorktreeCall{{
		repoPath:      cfg.Repos["backend"].Path,
		defaultBranch: "main",
		taskID:        "XL-123",
		worktree:      wantWorktree,
	}}) {
		t.Fatalf("prepare calls = %#v", fakes.prepareCalls)
	}

	if len(fakes.tokenClients) != 1 {
		t.Fatalf("token clients = %d, want 1", len(fakes.tokenClients))
	}
	tokenClient := fakes.tokenClients[0]
	if tokenClient.baseURL != "https://gitlab.example.com" {
		t.Fatalf("token client base URL = %q", tokenClient.baseURL)
	}
	if tokenClient.controlPAT != "control-pat" {
		t.Fatalf("token client control PAT = %q", tokenClient.controlPAT)
	}
	if !reflect.DeepEqual(tokenClient.createCalls, []createTokenCall{{
		projectID: "123",
		name:      "agent-XL-123-1780662896",
		expiresAt: now.Add(24 * time.Hour),
	}}) {
		t.Fatalf("create token calls = %#v", tokenClient.createCalls)
	}
	if !strings.HasPrefix(tokenClient.createCalls[0].name, "agent-XL-123-") {
		t.Fatalf("token name = %q, want agent task prefix", tokenClient.createCalls[0].name)
	}

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one call", fakes.dockerOptions)
	}
	dockerOpts := fakes.dockerOptions[0]
	if dockerOpts.TaskID != "XL-123" ||
		dockerOpts.Worktree != wantWorktree ||
		dockerOpts.GitDir != wantGitDir ||
		dockerOpts.GitLabToken != tokenValue ||
		dockerOpts.GitLabHost != "gitlab.example.com" {
		t.Fatalf("docker options = %#v", dockerOpts)
	}
	if dockerOpts.Image != "node:22-bookworm" {
		t.Fatalf("docker image = %q, want node default", dockerOpts.Image)
	}
	if !reflect.DeepEqual(dockerOpts.Mounts, wantAgentAuthMounts(hostHome)) {
		t.Fatalf("docker mounts = %#v, want agent auth mounts", dockerOpts.Mounts)
	}
	assertRunCommandContains(t, dockerOpts.Command, "command -v codex", "exec codex")
	if strings.Contains(strings.Join(dockerOpts.Command, "\x00"), tokenValue) {
		t.Fatalf("docker command contains token value: %#v", dockerOpts.Command)
	}

	if len(fakes.tmuxStarts) != 1 {
		t.Fatalf("tmux starts = %d, want 1", len(fakes.tmuxStarts))
	}
	start := fakes.tmuxStarts[0]
	if start.taskID != "XL-123" || start.worktree != wantWorktree {
		t.Fatalf("tmux start = %#v", start)
	}
	if len(start.command) < 6 {
		t.Fatalf("tmux command too short: %#v", start.command)
	}
	if !reflect.DeepEqual(start.command[:4], []string{"bash", "-lc", start.command[2], "--"}) {
		t.Fatalf("tmux wrapper prefix = %#v", start.command[:4])
	}
	envFile := start.command[4]
	if got := start.command[5:]; !reflect.DeepEqual(got, []string{"docker", "run", "--name", "agent-XL-123"}) {
		t.Fatalf("wrapped docker argv = %#v", got)
	}
	if strings.Contains(strings.Join(start.command, "\x00"), tokenValue) {
		t.Fatalf("tmux command contains token value: %#v", start.command)
	}
	wrapperScript := start.command[2]
	for _, want := range []string{
		"set -euo pipefail",
		"set -a",
		`. "$env_file"`,
		`rm -f "$env_file"`,
		`exec "$@"`,
	} {
		if !strings.Contains(wrapperScript, want) {
			t.Fatalf("wrapper script = %q, want %q", wrapperScript, want)
		}
	}
	if strings.Contains(wrapperScript, tokenValue) {
		t.Fatalf("wrapper script includes token value: %q", wrapperScript)
	}

	wantEnvFile := filepath.Join(cfg.StateDir, "env", "XL-123.env")
	if envFile != wantEnvFile {
		t.Fatalf("env file = %q, want %q", envFile, wantEnvFile)
	}
	envDirInfo, err := os.Stat(filepath.Dir(envFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := envDirInfo.Mode().Perm(); got != 0700 {
		t.Fatalf("env dir mode = %o, want %o", got, 0700)
	}
	envInfo, err := os.Stat(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := envInfo.Mode().Perm(); got != 0600 {
		t.Fatalf("env file mode = %o, want %o", got, 0600)
	}
	envData, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	envText := string(envData)
	wantEscapedToken := `export GITLAB_TOKEN='glpat-created-'\''secret'\'''`
	if !strings.Contains(envText, wantEscapedToken) {
		t.Fatalf("env file = %q, want shell-escaped token line %q", envText, wantEscapedToken)
	}
	if !strings.Contains(envText, `export GITLAB_HOST='gitlab.example.com'`) {
		t.Fatalf("env file = %q, want GitLab host", envText)
	}

	if len(fakes.savedTasks) != 1 {
		t.Fatalf("saved tasks = %d, want 1", len(fakes.savedTasks))
	}
	saved := fakes.savedTasks[0]
	wantState := state.Task{
		TaskID:          "XL-123",
		Repo:            "backend",
		RepoPath:        cfg.Repos["backend"].Path,
		SessionKind:     "tmux",
		WorktreeManaged: true,
		Branch:          "agent/XL-123",
		Worktree:        wantWorktree,
		TmuxSession:     "agentctl-XL-123",
		ContainerName:   "agent-XL-123",
		TokenID:         "98765",
		TokenName:       "agent-XL-123-1780662896",
		GitLabProjectID: "123",
		GitLabHost:      "gitlab.example.com",
		Status:          "running",
		CreatedAt:       now,
	}
	if !reflect.DeepEqual(saved, wantState) {
		t.Fatalf("saved state = %#v, want %#v", saved, wantState)
	}
	stateJSON, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateJSON), tokenValue) {
		t.Fatalf("saved state contains token value: %s", stateJSON)
	}
}

func TestRunAcceptsGitLabRepoURL(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	now := time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC)
	fakes := newRunFakes(cfg, now)
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"XL-123",
		"--repo", "https://gitlab.example.com/group/backend.git",
		"--config", filepath.Join(tmp, "config.yaml"),
		"--agent", "shell",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	wantRepoPath := filepath.Join(cfg.StateDir, "repos", "4b97055f-backend")
	wantWorktree := filepath.Join(cfg.BaseDir, "XL-123")
	if !reflect.DeepEqual(fakes.cloneCalls, []cloneRepoCall{{
		remote:   "https://gitlab.example.com/group/backend.git",
		repoPath: wantRepoPath,
	}}) {
		t.Fatalf("clone calls = %#v, want repo cache clone", fakes.cloneCalls)
	}
	if !reflect.DeepEqual(fakes.prepareCalls, []prepareWorktreeCall{{
		repoPath:      wantRepoPath,
		defaultBranch: "main",
		taskID:        "XL-123",
		worktree:      wantWorktree,
	}}) {
		t.Fatalf("prepare calls = %#v", fakes.prepareCalls)
	}
	tokenClient := fakes.tokenClients[0]
	if !reflect.DeepEqual(tokenClient.createCalls, []createTokenCall{{
		projectID: "group/backend",
		name:      "agent-XL-123-1780662896",
		expiresAt: now.Add(24 * time.Hour),
	}}) {
		t.Fatalf("create token calls = %#v", tokenClient.createCalls)
	}

	dockerOpts := singleDockerOptions(t, fakes)
	if dockerOpts.GitDir != filepath.Join(wantRepoPath, ".git") {
		t.Fatalf("git dir = %q, want cached repo git dir", dockerOpts.GitDir)
	}

	if len(fakes.savedTasks) != 1 {
		t.Fatalf("saved tasks = %#v, want one", fakes.savedTasks)
	}
	saved := fakes.savedTasks[0]
	if saved.Repo != "https://gitlab.example.com/group/backend.git" {
		t.Fatalf("state repo = %q, want original URL", saved.Repo)
	}
	if saved.RepoPath != wantRepoPath {
		t.Fatalf("state repo path = %q, want %q", saved.RepoPath, wantRepoPath)
	}
	if saved.GitLabProjectID != "group/backend" {
		t.Fatalf("state project id = %q, want group/backend", saved.GitLabProjectID)
	}
}

func TestRunPrintsAttachAndCleanupHintsOnSuccess(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	var out strings.Builder

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	text := out.String()
	for _, want := range []string{
		"started XL-123",
		"runtime: tmux",
		"workspace: " + filepath.Join(cfg.BaseDir, "XL-123"),
		"attach: agentctl attach XL-123",
		"cleanup: agentctl cleanup XL-123",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output = %q, want %q", text, want)
		}
	}
}

func TestRunAttachesTmuxByDefault(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fakes.tmuxAttachCalls, []string{"XL-123"}) {
		t.Fatalf("tmux attach calls = %#v, want default attach", fakes.tmuxAttachCalls)
	}
}

func TestRunDetachSkipsAutoAttach(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml"), "--detach"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(fakes.tmuxAttachCalls) != 0 {
		t.Fatalf("tmux attach calls = %#v, want none with --detach", fakes.tmuxAttachCalls)
	}
}

func TestRunWithoutRepoUsesCurrentDirectoryOrigin(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	now := time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC)
	currentRepo := filepath.Join(tmp, "current")
	fakes := newRunFakes(cfg, now)
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.currentDir = currentRepo
	fakes.originRemote = "git@gitlab.example.com:group/current.git"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--config", filepath.Join(tmp, "config.yaml"), "--agent", "shell"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	wantWorktree := filepath.Join(cfg.BaseDir, "XL-123")
	if !reflect.DeepEqual(fakes.prepareCalls, []prepareWorktreeCall{{
		repoPath:      currentRepo,
		defaultBranch: "main",
		taskID:        "XL-123",
		worktree:      wantWorktree,
	}}) {
		t.Fatalf("prepare calls = %#v", fakes.prepareCalls)
	}
	tokenClient := fakes.tokenClients[0]
	if tokenClient.createCalls[0].projectID != "group/current" {
		t.Fatalf("project id = %q, want group/current", tokenClient.createCalls[0].projectID)
	}
	if fakes.savedTasks[0].Repo != currentRepo || fakes.savedTasks[0].RepoPath != currentRepo {
		t.Fatalf("saved task = %#v, want current repo path", fakes.savedTasks[0])
	}
}

func TestRunWithoutGitRepoUsesCurrentDirectoryAsPlainWorkspace(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	now := time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC)
	currentDir := filepath.Join(tmp, "plain")
	fakes := newRunFakes(cfg, now)
	fakes.currentDir = currentDir
	fakes.originErr = errors.New("not a git repository")

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--config", filepath.Join(tmp, "config.yaml"), "--agent", "shell"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.prepareCalls) != 0 {
		t.Fatalf("prepare calls = %#v, want none", fakes.prepareCalls)
	}
	if len(fakes.tokenClients) != 0 {
		t.Fatalf("token clients = %#v, want none", fakes.tokenClients)
	}
	dockerOpts := singleDockerOptions(t, fakes)
	if dockerOpts.Worktree != currentDir {
		t.Fatalf("docker worktree = %q, want current dir", dockerOpts.Worktree)
	}
	if dockerOpts.GitDir != "" || dockerOpts.GitLabToken != "" || dockerOpts.GitLabHost != "" {
		t.Fatalf("docker git opts = %#v, want no git/gitlab opts", dockerOpts)
	}

	saved := fakes.savedTasks[0]
	if saved.Repo != currentDir || saved.RepoPath != currentDir || saved.Worktree != currentDir {
		t.Fatalf("saved task = %#v, want plain current workspace", saved)
	}
	if saved.WorktreeManaged || saved.GitLabProjectID != "" || saved.TokenID != "" {
		t.Fatalf("saved task = %#v, want unmanaged workspace without token", saved)
	}
}

func TestRunWithNonGitLabCurrentRepoUsesPlainWorkspace(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	currentDir := filepath.Join(tmp, "github-repo")
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.currentDir = currentDir
	fakes.originRemote = "git@github.com:example/backend.git"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--config", filepath.Join(tmp, "config.yaml"), "--agent", "shell"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.prepareCalls) != 0 {
		t.Fatalf("prepare calls = %#v, want none", fakes.prepareCalls)
	}
	if len(fakes.tokenClients) != 0 {
		t.Fatalf("token clients = %#v, want none", fakes.tokenClients)
	}
	if fakes.savedTasks[0].Worktree != currentDir || fakes.savedTasks[0].WorktreeManaged {
		t.Fatalf("saved task = %#v, want unmanaged current directory", fakes.savedTasks[0])
	}
}

func TestRunStartsDetachedDockerWhenTmuxIsUnavailable(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.tmuxAvailable = false

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.tmuxStarts) != 0 {
		t.Fatalf("tmux starts = %#v, want none", fakes.tmuxStarts)
	}
	if len(fakes.detachedDockerStarts) != 1 {
		t.Fatalf("detached docker starts = %#v, want one", fakes.detachedDockerStarts)
	}
	if !singleDockerOptions(t, fakes).Detached {
		t.Fatalf("docker options = %#v, want detached", fakes.dockerOptions)
	}
	if fakes.savedTasks[0].SessionKind != "docker" || fakes.savedTasks[0].TmuxSession != "" {
		t.Fatalf("saved task = %#v, want docker session kind without tmux session", fakes.savedTasks[0])
	}
}

func TestRunNoTmuxStartsDetachedDockerEvenWhenTmuxAvailable(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.tmuxAvailable = true

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--no-tmux", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.tmuxStarts) != 0 {
		t.Fatalf("tmux starts = %#v, want none with --no-tmux", fakes.tmuxStarts)
	}
	if len(fakes.detachedDockerStarts) != 1 {
		t.Fatalf("detached docker starts = %#v, want one", fakes.detachedDockerStarts)
	}
	if !singleDockerOptions(t, fakes).Detached {
		t.Fatalf("docker options = %#v, want detached", fakes.dockerOptions)
	}
	if fakes.savedTasks[0].SessionKind != "docker" || fakes.savedTasks[0].TmuxSession != "" {
		t.Fatalf("saved task = %#v, want docker session kind without tmux session", fakes.savedTasks[0])
	}
}

func TestRunErrorsBeforeSideEffectsWhenDockerIsUnavailable(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.dockerAvailable = false

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml")})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want docker missing error")
	}
	if !strings.Contains(err.Error(), "docker is required") {
		t.Fatalf("error = %v, want docker is required", err)
	}
	assertNoRunSideEffects(t, fakes)
	if len(fakes.tokenClients) != 0 {
		t.Fatalf("token clients = %#v, want none", fakes.tokenClients)
	}
}

func TestRunErrorsBeforeSideEffectsWhenDockerDaemonIsUnavailable(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.dockerReadyErr = errors.New("cannot connect to Docker daemon")

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml")})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want docker daemon error")
	}
	if !strings.Contains(err.Error(), "Docker daemon is not running or not reachable") {
		t.Fatalf("error = %v, want daemon not reachable", err)
	}
	assertNoRunSideEffects(t, fakes)
	if len(fakes.tokenClients) != 0 {
		t.Fatalf("token clients = %#v, want none", fakes.tokenClients)
	}
}

func TestRunRuntimeErrorDoesNotPrintUsage(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.dockerReadyErr = errors.New("cannot connect to Docker daemon")
	var errOut strings.Builder

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err == nil {
		t.Fatal("error = nil, want docker daemon error")
	}
	if strings.Contains(errOut.String(), "Usage:") {
		t.Fatalf("stderr = %q, want no usage for runtime error", errOut.String())
	}
}

func TestRunPromptsMacOSSandboxWhenDockerDaemonIsUnavailable(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.dockerReadyErr = errors.New("cannot connect to Docker daemon")
	fakes.sandboxExecAvailable = true
	fakes.goos = "darwin"
	fakes.confirmSandboxFallback = true

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(fakes.confirmSandboxFallbackPrompts) != 1 {
		t.Fatalf("prompts = %#v, want sandbox fallback prompt", fakes.confirmSandboxFallbackPrompts)
	}
	if len(fakes.sandboxOptions) != 1 {
		t.Fatalf("sandbox options = %#v, want one", fakes.sandboxOptions)
	}
}

func TestRunPromptsAndStartsMacOSSandboxWhenDockerIsUnavailable(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	hostHome := createAgentAuthFixtures(t, tmp)
	cfg.Sandbox.MacOS = config.MacOSSandboxConfig{
		Mode:    "strict",
		Network: false,
		AllowRead: []string{
			"/opt/homebrew",
		},
		AllowWrite: []string{
			"workspace",
			"task_home",
			"state_dir",
			"tmp",
			"/custom/write",
		},
		DenyRead: []string{
			"/Users/qqq/.ssh",
		},
		AllowTools: []string{
			"node",
			"codex",
			"agentctl",
		},
		Env: map[string]string{
			"GOCACHE": "${TASK_HOME}/.cache/go-build",
		},
		CustomRules: config.MacOSSandboxCustomRules{
			AllowRead:  []string{"/company-sdk"},
			AllowWrite: []string{"/company-cache"},
		},
	}
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.userHome = hostHome
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.dockerAvailable = false
	fakes.sandboxExecAvailable = true
	fakes.goos = "darwin"
	fakes.confirmSandboxFallback = true

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml")})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.confirmSandboxFallbackPrompts) != 1 {
		t.Fatalf("prompts = %#v, want one", fakes.confirmSandboxFallbackPrompts)
	}
	if !strings.Contains(fakes.confirmSandboxFallbackPrompts[0], "sandbox-exec") ||
		!strings.Contains(fakes.confirmSandboxFallbackPrompts[0], "weaker than Docker") {
		t.Fatalf("prompt = %q, want sandbox risk warning", fakes.confirmSandboxFallbackPrompts[0])
	}
	if len(fakes.dockerOptions) != 0 {
		t.Fatalf("docker options = %#v, want none", fakes.dockerOptions)
	}
	if len(fakes.sandboxOptions) != 1 {
		t.Fatalf("sandbox options = %#v, want one", fakes.sandboxOptions)
	}
	wantHome := filepath.Join(cfg.StateDir, "homes", "XL-123")
	sandboxOpts := fakes.sandboxOptions[0]
	if sandboxOpts.Worktree != filepath.Join(cfg.BaseDir, "XL-123") || sandboxOpts.HomeDir != wantHome {
		t.Fatalf("sandbox opts = %#v, want worktree and isolated home", sandboxOpts)
	}
	if sandboxOpts.Mode != "strict" || sandboxOpts.Network {
		t.Fatalf("sandbox opts = %#v, want strict with network disabled", sandboxOpts)
	}
	wantGitDir := filepath.Join(cfg.Repos["backend"].Path, ".git")
	for _, want := range []string{"/opt/homebrew", "/company-sdk", "/private/var/select", "/var/select", "/var/db/xcode_select_link", "/private/var/db/xcode_select_link", "/etc/codex", "/private/etc/codex", wantGitDir, filepath.Join(hostHome, ".codex"), filepath.Join(hostHome, ".claude"), filepath.Join(hostHome, ".claude.json")} {
		if !containsString(sandboxOpts.AllowRead, want) {
			t.Fatalf("sandbox allow read = %#v, want %q", sandboxOpts.AllowRead, want)
		}
	}
	wantWrite := []string{filepath.Join(cfg.BaseDir, "XL-123"), wantHome, cfg.StateDir, "/private/tmp", "/tmp", "/custom/write", "/company-cache", wantGitDir, filepath.Join(hostHome, ".codex"), filepath.Join(hostHome, ".claude"), filepath.Join(hostHome, ".claude.json")}
	if !reflect.DeepEqual(sandboxOpts.AllowWrite, wantWrite) {
		t.Fatalf("sandbox allow write = %#v, want %#v", sandboxOpts.AllowWrite, wantWrite)
	}
	for _, name := range []string{".codex", ".claude", ".claude.json"} {
		link, err := os.Readlink(filepath.Join(wantHome, name))
		if err != nil {
			t.Fatalf("auth link %s: %v", name, err)
		}
		if want := filepath.Join(hostHome, name); link != want {
			t.Fatalf("auth link %s = %q, want %q", name, link, want)
		}
	}
	if !reflect.DeepEqual(sandboxOpts.DenyRead, []string{"/Users/qqq/.ssh"}) {
		t.Fatalf("sandbox deny read = %#v, want configured deny paths", sandboxOpts.DenyRead)
	}
	if sandboxOpts.Env["GOCACHE"] != "${TASK_HOME}/.cache/go-build" {
		t.Fatalf("sandbox env = %#v, want configured GOCACHE", sandboxOpts.Env)
	}
	if len(sandboxOpts.Command) < 2 || !reflect.DeepEqual(sandboxOpts.Command[:2], []string{"bash", "-c"}) {
		t.Fatalf("sandbox command = %#v, want non-login bash -c", sandboxOpts.Command)
	}
	if len(fakes.tmuxStarts) != 1 {
		t.Fatalf("tmux starts = %#v, want sandbox command in tmux", fakes.tmuxStarts)
	}
	if !reflect.DeepEqual(fakes.tmuxStarts[0].command[5:], []string{"sandbox-exec", "-p", "profile", "--", "bash"}) {
		t.Fatalf("tmux command = %#v, want wrapped sandbox-exec", fakes.tmuxStarts[0].command)
	}
	saved := fakes.savedTasks[0]
	if saved.SessionKind != "macos-sandbox" || saved.ContainerName != "" || saved.TmuxSession != "agentctl-XL-123" {
		t.Fatalf("saved task = %#v, want macos-sandbox tmux state without container", saved)
	}
}

func TestMacOSSandboxToolReadPathsResolveNamesSymlinksAndNodePackages(t *testing.T) {
	tmp := t.TempDir()
	nodeBin := filepath.Join(tmp, ".n", "bin")
	codexPackage := filepath.Join(tmp, ".n", "lib", "node_modules", "@openai", "codex")
	if err := os.MkdirAll(nodeBin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(codexPackage, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	nodePath := filepath.Join(nodeBin, "node")
	if err := os.WriteFile(nodePath, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	codexTarget := filepath.Join(codexPackage, "bin", "codex.js")
	if err := os.WriteFile(codexTarget, []byte("#!/usr/bin/env node\n"), 0700); err != nil {
		t.Fatal(err)
	}
	codexLink := filepath.Join(nodeBin, "codex")
	if err := os.Symlink(codexTarget, codexLink); err != nil {
		t.Fatal(err)
	}
	selfPath := filepath.Join(tmp, "bin", "agentctl")
	if err := os.MkdirAll(filepath.Dir(selfPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(selfPath, []byte("agentctl"), 0700); err != nil {
		t.Fatal(err)
	}

	got := macOSSandboxToolReadPathsWithResolvers(
		[]string{"node", "codex", "agentctl", "missing"},
		func(name string) (string, error) {
			switch name {
			case "node":
				return nodePath, nil
			case "codex":
				return codexLink, nil
			case "agentctl":
				return "", errors.New("not in PATH")
			default:
				return "", errors.New("missing")
			}
		},
		filepath.EvalSymlinks,
		func() (string, error) {
			return selfPath, nil
		},
	)
	realCodexPackage, err := filepath.EvalSymlinks(codexPackage)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		nodeBin,
		realCodexPackage,
		filepath.Dir(selfPath),
	} {
		if !containsString(got, want) {
			t.Fatalf("tool read paths = %#v, want %q", got, want)
		}
	}
}

func TestRunDeclinesMacOSSandboxFallbackBeforeSideEffects(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.dockerAvailable = false
	fakes.sandboxExecAvailable = true
	fakes.goos = "darwin"
	fakes.confirmSandboxFallback = false

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--config", filepath.Join(tmp, "config.yaml")})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want declined sandbox fallback")
	}
	if !strings.Contains(err.Error(), "sandbox-exec fallback declined") {
		t.Fatalf("error = %v, want declined fallback", err)
	}
	if len(fakes.confirmSandboxFallbackPrompts) != 1 {
		t.Fatalf("prompts = %#v, want one", fakes.confirmSandboxFallbackPrompts)
	}
	assertNoRunSideEffects(t, fakes)
	if len(fakes.tokenClients) != 0 || len(fakes.sandboxOptions) != 0 {
		t.Fatalf("token clients = %#v sandbox options = %#v, want none", fakes.tokenClients, fakes.sandboxOptions)
	}
}

func TestRunTemplateNodeAgentCodexSelectsDockerImageAndCommand(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--template", "node", "--agent", "codex"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	opts := singleDockerOptions(t, fakes)
	if opts.Image != "node:22-bookworm" {
		t.Fatalf("docker image = %q, want node image", opts.Image)
	}
	assertRunCommandContains(t, opts.Command, "node --version", "command -v codex", "exec codex")
}

func TestRunTemplateNodeAgentClaudeSelectsDockerImageAndCommand(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--template", "node", "--agent", "claude"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	opts := singleDockerOptions(t, fakes)
	if opts.Image != "node:22-bookworm" {
		t.Fatalf("docker image = %q, want node image", opts.Image)
	}
	assertRunCommandContains(t, opts.Command, "node --version", "command -v claude", "exec claude")
}

func TestRunTemplateGolangAgentShellSelectsDockerImageAndCommand(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--template", "golang", "--agent", "shell"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	opts := singleDockerOptions(t, fakes)
	if opts.Image != "golang:1.22-bookworm" {
		t.Fatalf("docker image = %q, want golang image", opts.Image)
	}
	assertRunCommandContains(t, opts.Command, "go version", "exec zsh -l", "exec bash -l")
}

func TestRunEmptyTemplateUsesConfigDefault(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	cfg.Templates.Default = "python"
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	opts := singleDockerOptions(t, fakes)
	if opts.Image != "python:3.12-bookworm" {
		t.Fatalf("docker image = %q, want config default python image", opts.Image)
	}
	assertRunCommandContains(t, opts.Command, "python --version")
}

func TestRunUnsupportedTemplateErrorsBeforeSideEffects(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--template", "ruby"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want unsupported template")
	}
	if !strings.Contains(err.Error(), `unsupported template "ruby"`) {
		t.Fatalf("error = %v, want unsupported template message", err)
	}
	assertNoRunSideEffects(t, fakes)

	envPath := filepath.Join(cfg.StateDir, "env", "XL-123.env")
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatalf("env file stat = %v, want not exists", err)
	}
}

func TestRunUnsupportedAgentErrorsBeforeSideEffects(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--agent", "cursor"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want unsupported agent")
	}
	if !strings.Contains(err.Error(), `unsupported agent "cursor"`) {
		t.Fatalf("error = %v, want unsupported agent message", err)
	}
	assertNoRunSideEffects(t, fakes)
}

func TestRunRejectsInvalidTaskIDBeforeSideEffects(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	invalidTaskIDs := []string{
		"",
		" ",
		"foo/bar",
		"../other",
		"bad id",
		"bad:id",
		filepath.Join(tmp, "absolute"),
		`foo\bar`,
	}

	for _, taskID := range invalidTaskIDs {
		t.Run(taskID, func(t *testing.T) {
			fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
			fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

			cmd := newRunCommandWithDeps(fakes.deps())
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{taskID, "--repo", "backend"})

			err := cmd.Execute()
			if err == nil {
				t.Fatal("error = nil, want invalid task id")
			}
			if !strings.Contains(err.Error(), "invalid task id") {
				t.Fatalf("error = %v, want invalid task id message", err)
			}
			if fakes.loadConfigCalls != 0 {
				t.Fatalf("load config calls = %d, want 0", fakes.loadConfigCalls)
			}
			assertNoRunSideEffects(t, fakes)

			envPath := filepath.Join(cfg.StateDir, "env", taskID+".env")
			if _, err := os.Stat(envPath); !os.IsNotExist(err) {
				t.Fatalf("env file stat = %v, want not exists", err)
			}
		})
	}
}

func TestRunAcceptsValidTaskIDs(t *testing.T) {
	for _, taskID := range []string{"XL-123", "abc_123", "abc.123"} {
		t.Run(taskID, func(t *testing.T) {
			tmp := t.TempDir()
			cfg := testRunConfig(tmp)
			fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
			fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
			fakes.createdToken = tokenbroker.CreatedToken{
				ID:    "98765",
				Token: "glpat-created-secret",
				Name:  "agent-" + taskID + "-1780662896",
			}

			cmd := newRunCommandWithDeps(fakes.deps())
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{taskID, "--repo", "backend"})

			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunRevokesTokenWhenLaterStepFails(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.createdToken = tokenbroker.CreatedToken{
		ID:    "98765",
		Token: "glpat-created-secret",
		Name:  "agent-XL-123-1780662896",
	}
	fakes.tmuxErr = errors.New("tmux failed")

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want tmux failure")
	}
	if !strings.Contains(err.Error(), "tmux failed") {
		t.Fatalf("error = %v, want tmux failure", err)
	}

	if len(fakes.tokenClients) != 1 {
		t.Fatalf("token clients = %d, want 1", len(fakes.tokenClients))
	}
	tokenClient := fakes.tokenClients[0]
	if !reflect.DeepEqual(tokenClient.revokeCalls, []revokeTokenCall{{
		projectID: "123",
		tokenID:   "98765",
	}}) {
		t.Fatalf("revoke calls = %#v", tokenClient.revokeCalls)
	}
	envPath := filepath.Join(cfg.StateDir, "env", "XL-123.env")
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatalf("env file stat after tmux failure = %v, want not exists", err)
	}
}

func TestRunTokenCreationFailureRemovesPreparedWorktree(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	createErr := errors.New("token creation failed")
	fakes.createTokenErr = createErr

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	err := cmd.Execute()
	if !errors.Is(err, createErr) {
		t.Fatalf("error = %v, want token creation error", err)
	}

	wantWorktree := filepath.Join(cfg.BaseDir, "XL-123")
	if !reflect.DeepEqual(fakes.removeWorktreeCalls, []removeWorktreeCall{{
		repoPath: cfg.Repos["backend"].Path,
		worktree: wantWorktree,
	}}) {
		t.Fatalf("remove worktree calls = %#v", fakes.removeWorktreeCalls)
	}
	if len(fakes.tokenClients) != 1 {
		t.Fatalf("token clients = %d, want 1", len(fakes.tokenClients))
	}
	if len(fakes.tokenClients[0].revokeCalls) != 0 {
		t.Fatalf("revoke calls = %#v, want none", fakes.tokenClients[0].revokeCalls)
	}
	if len(fakes.dockerOptions) != 0 {
		t.Fatalf("docker options = %#v, want none", fakes.dockerOptions)
	}
}

func TestRunDockerInvocationFailureReportsRevokeFailure(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	dockerErr := errors.New("docker invocation failed")
	revokeErr := errors.New("token revoke failed")
	fakes.dockerErr = dockerErr
	fakes.revokeErr = revokeErr

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	err := cmd.Execute()
	if !errors.Is(err, dockerErr) {
		t.Fatalf("error = %v, want docker error", err)
	}
	if !errors.Is(err, revokeErr) {
		t.Fatalf("error = %v, want revoke error", err)
	}
	if !strings.Contains(err.Error(), "docker invocation failed") || !strings.Contains(err.Error(), "token revoke failed") {
		t.Fatalf("error = %v, want docker and revoke messages", err)
	}
	if len(fakes.tokenClients) != 1 {
		t.Fatalf("token clients = %d, want 1", len(fakes.tokenClients))
	}
	tokenClient := fakes.tokenClients[0]
	if !reflect.DeepEqual(tokenClient.revokeCalls, []revokeTokenCall{{
		projectID: "123",
		tokenID:   "98765",
	}}) {
		t.Fatalf("revoke calls = %#v", tokenClient.revokeCalls)
	}
	if len(fakes.tmuxStarts) != 0 {
		t.Fatalf("tmux starts = %#v, want none", fakes.tmuxStarts)
	}
}

func TestRunDockerInvocationFailureRemovesWorktreeAndReportsRemovalFailure(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	dockerErr := errors.New("docker invocation failed")
	removeWorktreeErr := errors.New("worktree remove failed")
	fakes.dockerErr = dockerErr
	fakes.removeWorktreeErr = removeWorktreeErr

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	err := cmd.Execute()
	if !errors.Is(err, dockerErr) {
		t.Fatalf("error = %v, want docker error", err)
	}
	if !errors.Is(err, removeWorktreeErr) {
		t.Fatalf("error = %v, want worktree remove error", err)
	}
	if !strings.Contains(err.Error(), "docker invocation failed") || !strings.Contains(err.Error(), "git worktree cleanup failed") {
		t.Fatalf("error = %v, want docker and worktree cleanup messages", err)
	}

	if len(fakes.tokenClients) != 1 {
		t.Fatalf("token clients = %d, want 1", len(fakes.tokenClients))
	}
	tokenClient := fakes.tokenClients[0]
	if !reflect.DeepEqual(tokenClient.revokeCalls, []revokeTokenCall{{
		projectID: "123",
		tokenID:   "98765",
	}}) {
		t.Fatalf("revoke calls = %#v", tokenClient.revokeCalls)
	}
	wantWorktree := filepath.Join(cfg.BaseDir, "XL-123")
	if !reflect.DeepEqual(fakes.removeWorktreeCalls, []removeWorktreeCall{{
		repoPath: cfg.Repos["backend"].Path,
		worktree: wantWorktree,
	}}) {
		t.Fatalf("remove worktree calls = %#v", fakes.removeWorktreeCalls)
	}
}

func TestRunTmuxStartFailureReportsEnvFileRemoveFailure(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	tmuxErr := errors.New("tmux failed")
	removeErr := errors.New("env file remove failed")
	fakes.tmuxErr = tmuxErr
	fakes.removeEnvErr = removeErr

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	err := cmd.Execute()
	if !errors.Is(err, tmuxErr) {
		t.Fatalf("error = %v, want tmux error", err)
	}
	if !errors.Is(err, removeErr) {
		t.Fatalf("error = %v, want remove error", err)
	}
	if !strings.Contains(err.Error(), "tmux failed") || !strings.Contains(err.Error(), "env file remove failed") {
		t.Fatalf("error = %v, want tmux and remove messages", err)
	}
}

func TestRunErrorsWhenRepoIsMissing(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "frontend"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want missing repo")
	}
	if !strings.Contains(err.Error(), `repo "frontend" not found`) {
		t.Fatalf("error = %v, want missing repo message", err)
	}
	if len(fakes.prepareCalls) != 0 {
		t.Fatalf("prepare calls = %#v, want none", fakes.prepareCalls)
	}
	if len(fakes.tokenClients) != 0 {
		t.Fatalf("token clients = %d, want none", len(fakes.tokenClients))
	}
}

func TestRunErrorsWhenControlPATIsMissingBeforeTokenCreation(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want missing PAT")
	}
	if !strings.Contains(err.Error(), "GITLAB_CONTROL_PAT is required") {
		t.Fatalf("error = %v, want missing PAT message", err)
	}
	if len(fakes.prepareCalls) != 0 {
		t.Fatalf("prepare calls = %#v, want none", fakes.prepareCalls)
	}
	if len(fakes.tokenClients) != 0 {
		t.Fatalf("token clients = %d, want none", len(fakes.tokenClients))
	}
}

func TestRunStopsTmuxAndRevokesTokenWhenStateSaveFails(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	saveErr := errors.New("state save failed")
	fakes.saveErr = saveErr

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	err := cmd.Execute()
	if !errors.Is(err, saveErr) {
		t.Fatalf("error = %v, want save error", err)
	}
	if !reflect.DeepEqual(fakes.tmuxStops, []string{"XL-123"}) {
		t.Fatalf("tmux stops = %#v, want task stop", fakes.tmuxStops)
	}
	if len(fakes.tokenClients) != 1 {
		t.Fatalf("token clients = %d, want 1", len(fakes.tokenClients))
	}
	tokenClient := fakes.tokenClients[0]
	if !reflect.DeepEqual(tokenClient.revokeCalls, []revokeTokenCall{{
		projectID: "123",
		tokenID:   "98765",
	}}) {
		t.Fatalf("revoke calls = %#v", tokenClient.revokeCalls)
	}
	envPath := filepath.Join(cfg.StateDir, "env", "XL-123.env")
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatalf("env file stat after state save failure = %v, want not exists", err)
	}
	wantWorktree := filepath.Join(cfg.BaseDir, "XL-123")
	if !reflect.DeepEqual(fakes.removeWorktreeCalls, []removeWorktreeCall{{
		repoPath: cfg.Repos["backend"].Path,
		worktree: wantWorktree,
	}}) {
		t.Fatalf("remove worktree calls = %#v", fakes.removeWorktreeCalls)
	}
}

func TestRunStateSaveFailureReportsRevokeFailure(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	saveErr := errors.New("state save failed")
	revokeErr := errors.New("token revoke failed")
	fakes.saveErr = saveErr
	fakes.revokeErr = revokeErr

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	err := cmd.Execute()
	if !errors.Is(err, saveErr) {
		t.Fatalf("error = %v, want save error", err)
	}
	if !errors.Is(err, revokeErr) {
		t.Fatalf("error = %v, want revoke error", err)
	}
	if !strings.Contains(err.Error(), "state save failed") || !strings.Contains(err.Error(), "token revoke failed") {
		t.Fatalf("error = %v, want save and revoke messages", err)
	}
}

func TestRunWithRealDockerInvocationDoesNotPutTokenInTmuxArgv(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	tokenValue := "glpat-real-runtime-secret"
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"
	fakes.createdToken = tokenbroker.CreatedToken{
		ID:    "98765",
		Token: tokenValue,
		Name:  "agent-XL-123-1780662896",
	}
	deps := fakes.deps()
	deps.dockerInvocationFor = runtime.DockerInvocationFor

	cmd := newRunCommandWithDeps(deps)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(fakes.tmuxStarts) != 1 {
		t.Fatalf("tmux starts = %d, want 1", len(fakes.tmuxStarts))
	}
	if strings.Contains(strings.Join(fakes.tmuxStarts[0].command, "\x00"), tokenValue) {
		t.Fatalf("tmux command contains token value: %#v", fakes.tmuxStarts[0].command)
	}
}

func TestRemoteRunForwardsResolvedConfigWithoutLocalServices(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	configPath := filepath.Join(tmp, "config.yaml")
	cfg.Remotes = map[string]config.Remote{
		"buildbox-1": {
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
	}
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"XL-123",
		"--repo", "backend",
		"--remote", "buildbox-1",
		"--config", configPath,
		"--template", "golang",
		"--no-tmux",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{configPath}) {
		t.Fatalf("config paths = %#v, want remote config load", fakes.loadConfigPaths)
	}
	wantCall := runRemoteCall{
		target: remote.Target{
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
		interactive: false,
		args: []string{
			"run",
			"XL-123",
			"--repo",
			"backend",
			"--agent",
			"codex",
			"--risk",
			"untrusted",
			"--template",
			"golang",
			"--no-tmux",
			"--detach",
		},
	}
	if !reflect.DeepEqual(fakes.remoteCalls, []runRemoteCall{wantCall}) {
		t.Fatalf("remote calls = %#v, want %#v", fakes.remoteCalls, []runRemoteCall{wantCall})
	}
	assertNoForwardedRemoteFlag(t, fakes.remoteCalls[0].args)
	assertNoRunSideEffects(t, fakes)
}

func TestRemoteRunForwardsConfiguredRemoteConfigPath(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	localConfigPath := filepath.Join(tmp, "local-config.yaml")
	cfg.Remotes = map[string]config.Remote{
		"buildbox-1": {
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
			ConfigPath:   "/etc/agentctl/config.yaml",
		},
	}
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"XL-123",
		"--repo", "backend",
		"--remote", "buildbox-1",
		"--config", localConfigPath,
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{localConfigPath}) {
		t.Fatalf("config paths = %#v, want local config load", fakes.loadConfigPaths)
	}
	wantCall := runRemoteCall{
		target: remote.Target{
			Host:         "buildbox-1.example.com",
			User:         "deploy",
			AgentctlPath: "/usr/local/bin/agentctl",
		},
		interactive: false,
		args: []string{
			"run",
			"XL-123",
			"--repo",
			"backend",
			"--agent",
			"codex",
			"--risk",
			"untrusted",
			"--detach",
			"--config",
			"/etc/agentctl/config.yaml",
		},
	}
	if !reflect.DeepEqual(fakes.remoteCalls, []runRemoteCall{wantCall}) {
		t.Fatalf("remote calls = %#v, want %#v", fakes.remoteCalls, []runRemoteCall{wantCall})
	}
	assertNoForwardedRemoteFlag(t, fakes.remoteCalls[0].args)
	assertNoRunSideEffects(t, fakes)
}

func TestRemoteRunMissingRemoteErrorsBeforeLocalServices(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	configPath := filepath.Join(tmp, "config.yaml")
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--remote", "missing", "--config", configPath})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want missing remote")
	}
	if !strings.Contains(err.Error(), `remote "missing" not found in config`) {
		t.Fatalf("error = %v, want missing remote message", err)
	}
	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{configPath}) {
		t.Fatalf("config paths = %#v, want remote config load", fakes.loadConfigPaths)
	}
	if len(fakes.remoteCalls) != 0 {
		t.Fatalf("remote calls = %#v, want none", fakes.remoteCalls)
	}
	assertNoRunSideEffects(t, fakes)
}

func testRunConfig(tmp string) *config.Config {
	return &config.Config{
		BaseDir:  filepath.Join(tmp, "worktrees"),
		StateDir: filepath.Join(tmp, "state"),
		GitLab: config.GitLabConfig{
			Host: "gitlab.example.com",
		},
		Repos: map[string]config.Repo{
			"backend": {
				Path:      filepath.Join(tmp, "backend"),
				ProjectID: "123",
			},
		},
	}
}

type runFakes struct {
	cfg                           *config.Config
	now                           time.Time
	env                           map[string]string
	loadConfigCalls               int
	loadConfigPaths               []string
	currentDir                    string
	userHome                      string
	originRemote                  string
	originErr                     error
	goos                          string
	dockerAvailable               bool
	dockerReadyErr                error
	sandboxExecAvailable          bool
	confirmSandboxFallback        bool
	confirmSandboxFallbackPrompts []string
	tmuxAvailable                 bool
	cloneCalls                    []cloneRepoCall
	prepareCalls                  []prepareWorktreeCall
	removeWorktreeCalls           []removeWorktreeCall
	tokenClients                  []*fakeRunTokenClient
	createdToken                  tokenbroker.CreatedToken
	createTokenErr                error
	dockerOptions                 []runtime.DockerOptions
	sandboxOptions                []runtime.MacOSSandboxOptions
	dockerErr                     error
	tmuxStarts                    []tmuxStartCall
	tmuxAttachCalls               []string
	containerAttachCalls          []string
	detachedDockerStarts          [][]string
	tmuxStops                     []string
	tmuxErr                       error
	stopTmuxErr                   error
	saveErr                       error
	revokeErr                     error
	removeWorktreeErr             error
	removeEnvErr                  error
	removeEnvCalls                []string
	savedTasks                    []state.Task
	remoteCalls                   []runRemoteCall
	remoteErr                     error
}

func newRunFakes(cfg *config.Config, now time.Time) *runFakes {
	return &runFakes{
		cfg:             cfg,
		now:             now,
		env:             map[string]string{},
		goos:            "linux",
		dockerAvailable: true,
		tmuxAvailable:   true,
		createdToken: tokenbroker.CreatedToken{
			ID:    "98765",
			Token: "glpat-created-secret",
			Name:  "agent-XL-123-1780662896",
		},
	}
}

func (f *runFakes) deps() runDeps {
	return runDeps{
		loadConfig: func(path string) (*config.Config, error) {
			f.loadConfigCalls++
			f.loadConfigPaths = append(f.loadConfigPaths, path)
			return f.cfg, nil
		},
		getenv: func(key string) string {
			return f.env[key]
		},
		now: func() time.Time {
			return f.now
		},
		workingDir: func() (string, error) {
			if f.currentDir == "" {
				return "/current/repo", nil
			}
			return f.currentDir, nil
		},
		userHome: func() (string, error) {
			if f.userHome == "" {
				return os.UserHomeDir()
			}
			return f.userHome, nil
		},
		originRemote: func(_ context.Context, repoPath string) (string, error) {
			if f.originErr != nil {
				return "", f.originErr
			}
			if f.originRemote == "" {
				return "git@gitlab.example.com:example-group/backend.git", nil
			}
			return f.originRemote, nil
		},
		gitMetadataPaths: func(_ context.Context, _ string) []string {
			return nil
		},
		ensureRepoClone: func(_ context.Context, remote, repoPath string) error {
			f.cloneCalls = append(f.cloneCalls, cloneRepoCall{
				remote:   remote,
				repoPath: repoPath,
			})
			return nil
		},
		prepareWorktree: func(_ context.Context, repoPath, defaultBranch, taskID, worktree string) error {
			f.prepareCalls = append(f.prepareCalls, prepareWorktreeCall{
				repoPath:      repoPath,
				defaultBranch: defaultBranch,
				taskID:        taskID,
				worktree:      worktree,
			})
			return nil
		},
		removeWorktree: func(_ context.Context, repoPath, worktree string) error {
			f.removeWorktreeCalls = append(f.removeWorktreeCalls, removeWorktreeCall{
				repoPath: repoPath,
				worktree: worktree,
			})
			return f.removeWorktreeErr
		},
		newTokenClient: func(baseURL, controlPAT string) runTokenClient {
			client := &fakeRunTokenClient{
				baseURL:     baseURL,
				controlPAT:  controlPAT,
				createToken: f.createdToken,
				createErr:   f.createTokenErr,
				revokeErr:   f.revokeErr,
			}
			f.tokenClients = append(f.tokenClients, client)
			return client
		},
		dockerInvocationFor: func(opts runtime.DockerOptions) (runtime.DockerInvocation, error) {
			f.dockerOptions = append(f.dockerOptions, opts)
			if f.dockerErr != nil {
				return runtime.DockerInvocation{}, f.dockerErr
			}
			return runtime.DockerInvocation{
				Command: []string{"docker", "run", "--name", "agent-" + opts.TaskID},
				Env: []string{
					"GITLAB_HOST=" + opts.GitLabHost,
					"GITLAB_TOKEN=" + opts.GitLabToken,
				},
			}, nil
		},
		dockerAvailable: func() bool {
			return f.dockerAvailable
		},
		dockerReady: func(_ context.Context) error {
			return f.dockerReadyErr
		},
		goos: func() string {
			return f.goos
		},
		sandboxExecAvailable: func() bool {
			return f.sandboxExecAvailable
		},
		confirmSandboxFallback: func(prompt string) (bool, error) {
			f.confirmSandboxFallbackPrompts = append(f.confirmSandboxFallbackPrompts, prompt)
			return f.confirmSandboxFallback, nil
		},
		macOSSandboxInvocationFor: func(opts runtime.MacOSSandboxOptions) (runtime.DockerInvocation, error) {
			f.sandboxOptions = append(f.sandboxOptions, opts)
			return runtime.DockerInvocation{
				Command: []string{"sandbox-exec", "-p", "profile", "--", "bash"},
				Env: []string{
					"GITLAB_HOST=gitlab.example.com",
					"GITLAB_TOKEN=glpat-created-secret",
				},
			}, nil
		},
		startTmux: func(_ context.Context, taskID, worktree string, command []string) error {
			f.tmuxStarts = append(f.tmuxStarts, tmuxStartCall{
				taskID:   taskID,
				worktree: worktree,
				command:  append([]string(nil), command...),
			})
			return f.tmuxErr
		},
		attachTmux: func(_ context.Context, taskID string) error {
			f.tmuxAttachCalls = append(f.tmuxAttachCalls, taskID)
			return nil
		},
		tmuxAvailable: func() bool {
			return f.tmuxAvailable
		},
		startDocker: func(_ context.Context, command []string) error {
			f.detachedDockerStarts = append(f.detachedDockerStarts, append([]string(nil), command...))
			return nil
		},
		attachDocker: func(_ context.Context, containerName string) error {
			f.containerAttachCalls = append(f.containerAttachCalls, containerName)
			return nil
		},
		stopTmux: func(_ context.Context, taskID string) error {
			f.tmuxStops = append(f.tmuxStops, taskID)
			return f.stopTmuxErr
		},
		removeEnvFile: func(path string) error {
			f.removeEnvCalls = append(f.removeEnvCalls, path)
			if f.removeEnvErr != nil {
				return f.removeEnvErr
			}
			return os.Remove(path)
		},
		saveState: func(stateDir string, task state.Task) error {
			if stateDir != f.cfg.StateDir {
				return errors.New("unexpected state dir")
			}
			f.savedTasks = append(f.savedTasks, task)
			return f.saveErr
		},
		forwardRemote: func(_ context.Context, target remote.Target, interactive bool, args ...string) error {
			f.remoteCalls = append(f.remoteCalls, runRemoteCall{
				target:      target,
				interactive: interactive,
				args:        append([]string(nil), args...),
			})
			return f.remoteErr
		},
	}
}

func assertNoRunSideEffects(t *testing.T, fakes *runFakes) {
	t.Helper()

	if len(fakes.prepareCalls) != 0 {
		t.Fatalf("prepare calls = %#v, want none", fakes.prepareCalls)
	}
	if len(fakes.removeWorktreeCalls) != 0 {
		t.Fatalf("remove worktree calls = %#v, want none", fakes.removeWorktreeCalls)
	}
	if len(fakes.tokenClients) != 0 {
		t.Fatalf("token clients = %d, want none", len(fakes.tokenClients))
	}
	if len(fakes.dockerOptions) != 0 {
		t.Fatalf("docker options = %#v, want none", fakes.dockerOptions)
	}
	if len(fakes.tmuxStarts) != 0 {
		t.Fatalf("tmux starts = %#v, want none", fakes.tmuxStarts)
	}
	if len(fakes.tmuxStops) != 0 {
		t.Fatalf("tmux stops = %#v, want none", fakes.tmuxStops)
	}
	if len(fakes.savedTasks) != 0 {
		t.Fatalf("saved tasks = %#v, want none", fakes.savedTasks)
	}
}

func assertNoForwardedRemoteFlag(t *testing.T, args []string) {
	t.Helper()

	for _, arg := range args {
		if arg == "--remote" {
			t.Fatalf("forwarded args = %#v, must not include --remote", args)
		}
	}
}

func singleDockerOptions(t *testing.T, fakes *runFakes) runtime.DockerOptions {
	t.Helper()

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one call", fakes.dockerOptions)
	}

	return fakes.dockerOptions[0]
}

func assertRunCommandContains(t *testing.T, command []string, wants ...string) {
	t.Helper()

	commandText := strings.Join(command, "\n")
	for _, want := range wants {
		if !strings.Contains(commandText, want) {
			t.Fatalf("docker command = %#v, want %q", command, want)
		}
	}
}

type prepareWorktreeCall struct {
	repoPath      string
	defaultBranch string
	taskID        string
	worktree      string
}

type removeWorktreeCall struct {
	repoPath string
	worktree string
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func createAgentAuthFixtures(t *testing.T, tmp string) string {
	t.Helper()

	home := filepath.Join(tmp, "host-home")
	for _, name := range []string{".codex", ".claude"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

func wantAgentAuthMounts(home string) []runtime.Mount {
	return []runtime.Mount{
		{HostPath: filepath.Join(home, ".codex"), ContainerPath: "/root/.codex", Mode: "rw"},
		{HostPath: filepath.Join(home, ".claude"), ContainerPath: "/root/.claude", Mode: "rw"},
		{HostPath: filepath.Join(home, ".claude.json"), ContainerPath: "/root/.claude.json", Mode: "rw"},
	}
}

type cloneRepoCall struct {
	remote   string
	repoPath string
}

type createTokenCall struct {
	projectID string
	name      string
	expiresAt time.Time
}

type revokeTokenCall struct {
	projectID string
	tokenID   string
}

type fakeRunTokenClient struct {
	baseURL     string
	controlPAT  string
	createToken tokenbroker.CreatedToken
	createErr   error
	revokeErr   error
	createCalls []createTokenCall
	revokeCalls []revokeTokenCall
}

func (f *fakeRunTokenClient) CreateProjectToken(_ context.Context, projectID, name string, expiresAt time.Time) (tokenbroker.CreatedToken, error) {
	f.createCalls = append(f.createCalls, createTokenCall{
		projectID: projectID,
		name:      name,
		expiresAt: expiresAt,
	})
	if f.createErr != nil {
		return tokenbroker.CreatedToken{}, f.createErr
	}
	return f.createToken, nil
}

func (f *fakeRunTokenClient) RevokeProjectToken(_ context.Context, projectID, tokenID string) error {
	f.revokeCalls = append(f.revokeCalls, revokeTokenCall{
		projectID: projectID,
		tokenID:   tokenID,
	})
	return f.revokeErr
}

type tmuxStartCall struct {
	taskID   string
	worktree string
	command  []string
}

type runRemoteCall struct {
	target      remote.Target
	interactive bool
	args        []string
}
