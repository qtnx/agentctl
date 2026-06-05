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

	"github.com/your-org/agentctl/internal/config"
	"github.com/your-org/agentctl/internal/runtime"
	"github.com/your-org/agentctl/internal/state"
	"github.com/your-org/agentctl/internal/tokenbroker"
)

func TestRunStartsLocalAgentWorkspace(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	now := time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC)
	tokenValue := "glpat-created-'secret'"
	fakes := newRunFakes(cfg, now)
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
	assertRunCommandContains(t, opts.Command, "go version", "exec bash")
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

func TestRunRemoteReturnsNotImplementedWithoutStartingLocalServices(t *testing.T) {
	tmp := t.TempDir()
	cfg := testRunConfig(tmp)
	fakes := newRunFakes(cfg, time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC))
	fakes.env["GITLAB_CONTROL_PAT"] = "control-pat"

	cmd := newRunCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--repo", "backend", "--remote", "buildbox-1"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("error = nil, want remote not implemented")
	}
	if !strings.Contains(err.Error(), `remote run "buildbox-1" is not implemented`) {
		t.Fatalf("error = %v, want remote not implemented message", err)
	}
	if fakes.loadConfigCalls != 0 {
		t.Fatalf("load config calls = %d, want 0", fakes.loadConfigCalls)
	}
	if len(fakes.prepareCalls) != 0 {
		t.Fatalf("prepare calls = %#v, want none", fakes.prepareCalls)
	}
	if len(fakes.tokenClients) != 0 {
		t.Fatalf("token clients = %d, want none", len(fakes.tokenClients))
	}
	if len(fakes.tmuxStarts) != 0 {
		t.Fatalf("tmux starts = %#v, want none", fakes.tmuxStarts)
	}
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
	cfg             *config.Config
	now             time.Time
	env             map[string]string
	loadConfigCalls int
	loadConfigPaths []string
	prepareCalls    []prepareWorktreeCall
	tokenClients    []*fakeRunTokenClient
	createdToken    tokenbroker.CreatedToken
	dockerOptions   []runtime.DockerOptions
	dockerErr       error
	tmuxStarts      []tmuxStartCall
	tmuxStops       []string
	tmuxErr         error
	stopTmuxErr     error
	saveErr         error
	revokeErr       error
	removeEnvErr    error
	removeEnvCalls  []string
	savedTasks      []state.Task
}

func newRunFakes(cfg *config.Config, now time.Time) *runFakes {
	return &runFakes{
		cfg: cfg,
		now: now,
		env: map[string]string{},
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
		prepareWorktree: func(_ context.Context, repoPath, defaultBranch, taskID, worktree string) error {
			f.prepareCalls = append(f.prepareCalls, prepareWorktreeCall{
				repoPath:      repoPath,
				defaultBranch: defaultBranch,
				taskID:        taskID,
				worktree:      worktree,
			})
			return nil
		},
		newTokenClient: func(baseURL, controlPAT string) runTokenClient {
			client := &fakeRunTokenClient{
				baseURL:     baseURL,
				controlPAT:  controlPAT,
				createToken: f.createdToken,
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
		startTmux: func(_ context.Context, taskID, worktree string, command []string) error {
			f.tmuxStarts = append(f.tmuxStarts, tmuxStartCall{
				taskID:   taskID,
				worktree: worktree,
				command:  append([]string(nil), command...),
			})
			return f.tmuxErr
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
	}
}

func assertNoRunSideEffects(t *testing.T, fakes *runFakes) {
	t.Helper()

	if len(fakes.prepareCalls) != 0 {
		t.Fatalf("prepare calls = %#v, want none", fakes.prepareCalls)
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
