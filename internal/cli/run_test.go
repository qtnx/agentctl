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

	if !reflect.DeepEqual(fakes.dockerOptions, []runtime.DockerOptions{{
		TaskID:      "XL-123",
		Worktree:    wantWorktree,
		GitDir:      wantGitDir,
		GitLabToken: tokenValue,
		GitLabHost:  "gitlab.example.com",
	}}) {
		t.Fatalf("docker options = %#v", fakes.dockerOptions)
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
	if len(fakes.tokenClients) != 0 {
		t.Fatalf("token clients = %d, want none", len(fakes.tokenClients))
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
	tmuxStarts      []tmuxStartCall
	tmuxErr         error
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
			}
			f.tokenClients = append(f.tokenClients, client)
			return client
		},
		dockerInvocationFor: func(opts runtime.DockerOptions) (runtime.DockerInvocation, error) {
			f.dockerOptions = append(f.dockerOptions, opts)
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
		saveState: func(stateDir string, task state.Task) error {
			if stateDir != f.cfg.StateDir {
				return errors.New("unexpected state dir")
			}
			f.savedTasks = append(f.savedTasks, task)
			return nil
		},
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
	return nil
}

type tmuxStartCall struct {
	taskID   string
	worktree string
	command  []string
}
