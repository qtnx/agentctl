# agentctl Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Go single-binary CLI that spawns isolated git-worktree agent sessions, manages short-lived GitLab tokens, supports tmux attach/detach, and can orchestrate runs on remote machines over SSH.

**Architecture:** The CLI uses Cobra for commands and shells out through an injectable command executor for `git`, `docker`, `tmux`, and `ssh`. Config and state are plain YAML/JSON files, while GitLab token creation/revocation uses a small HTTP client that never persists token values.

**Tech Stack:** Go 1.22+, Cobra, yaml.v3, standard library HTTP/JSON, standard library testing with fake executors.

---

### Task 1: Initialize Go CLI Skeleton

**Files:**
- Create: `go.mod`
- Create: `cmd/agentctl/main.go`
- Create: `internal/cli/root.go`
- Create: `internal/cli/root_test.go`

**Step 1: Write the failing test**

Create `internal/cli/root_test.go`:

```go
package cli

import "testing"

func TestRootCommandHasExpectedSubcommands(t *testing.T) {
	cmd := NewRootCommand()
	names := map[string]bool{}
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}

	for _, want := range []string{"run", "shell", "attach", "detach", "cleanup", "list", "token-broker"} {
		if !names[want] {
			t.Fatalf("missing subcommand %q", want)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/cli -run TestRootCommandHasExpectedSubcommands -v`

Expected: FAIL because no module or CLI package exists.

**Step 3: Write minimal implementation**

Create `go.mod`:

```go
module github.com/your-org/agentctl

go 1.22

require github.com/spf13/cobra v1.8.1
```

Create `cmd/agentctl/main.go`:

```go
package main

import (
	"os"

	"github.com/your-org/agentctl/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
```

Create `internal/cli/root.go`:

```go
package cli

import "github.com/spf13/cobra"

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agentctl",
		Short: "Spawn isolated agent workspaces",
	}

	for _, sub := range []*cobra.Command{
		newRunCommand(),
		newShellCommand(),
		newAttachCommand(),
		newDetachCommand(),
		newCleanupCommand(),
		newListCommand(),
		newTokenBrokerCommand(),
	} {
		cmd.AddCommand(sub)
	}

	return cmd
}

func newRunCommand() *cobra.Command {
	return &cobra.Command{Use: "run TASK_ID", Short: "Start an agent workspace"}
}

func newShellCommand() *cobra.Command {
	return &cobra.Command{Use: "shell TASK_ID", Short: "Open a shell in an agent session"}
}

func newAttachCommand() *cobra.Command {
	return &cobra.Command{Use: "attach TASK_ID", Short: "Attach to an agent tmux session"}
}

func newDetachCommand() *cobra.Command {
	return &cobra.Command{Use: "detach TASK_ID", Short: "Detach from an agent tmux session"}
}

func newCleanupCommand() *cobra.Command {
	return &cobra.Command{Use: "cleanup TASK_ID", Short: "Clean up an agent workspace"}
}

func newListCommand() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List agent workspaces"}
}

func newTokenBrokerCommand() *cobra.Command {
	return &cobra.Command{Use: "token-broker", Short: "Manage short-lived agent tokens"}
}
```

**Step 4: Run test to verify it passes**

Run: `go mod tidy && go test ./internal/cli -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add go.mod go.sum cmd/agentctl/main.go internal/cli/root.go internal/cli/root_test.go
git commit -m "feat: initialize agentctl cli"
```

### Task 2: Add Config Loading

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigExpandsRepoAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(`
base_dir: ~/agent-workspaces
gitlab:
  host: gitlab.example.com
repos:
  backend:
    path: ~/code/backend
    project_id: "123"
    default_branch: main
    remote: git@gitlab.example.com:team/backend.git
`), 0600)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.GitLab.Host != "gitlab.example.com" {
		t.Fatalf("host = %q", cfg.GitLab.Host)
	}
	if cfg.Repos["backend"].ProjectID != "123" {
		t.Fatalf("project id = %q", cfg.Repos["backend"].ProjectID)
	}
	if cfg.StateDir == "" {
		t.Fatal("expected default state dir")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestLoadConfigExpandsRepoAndDefaults -v`

Expected: FAIL because config package does not exist.

**Step 3: Write minimal implementation**

Create `internal/config/config.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	BaseDir   string              `yaml:"base_dir"`
	StateDir  string              `yaml:"state_dir"`
	GitLab    GitLabConfig        `yaml:"gitlab"`
	Repos     map[string]Repo     `yaml:"repos"`
	Remotes   map[string]Remote   `yaml:"remotes"`
	Templates TemplatesConfig     `yaml:"templates"`
}

type GitLabConfig struct {
	Host string `yaml:"host"`
}

type Repo struct {
	Path          string `yaml:"path"`
	ProjectID     string `yaml:"project_id"`
	DefaultBranch string `yaml:"default_branch"`
	Remote        string `yaml:"remote"`
}

type Remote struct {
	Host         string `yaml:"host"`
	User         string `yaml:"user"`
	AgentctlPath string `yaml:"agentctl_path"`
}

type TemplatesConfig struct {
	Default string `yaml:"default"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(expand(path))
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.BaseDir == "" {
		cfg.BaseDir = "~/.agent-workspaces"
	}
	if cfg.StateDir == "" {
		cfg.StateDir = "~/.local/state/agentctl"
	}
	if cfg.GitLab.Host == "" {
		cfg.GitLab.Host = "gitlab.com"
	}
	if cfg.Repos == nil {
		cfg.Repos = map[string]Repo{}
	}
	if cfg.Remotes == nil {
		cfg.Remotes = map[string]Remote{}
	}

	cfg.BaseDir = expand(cfg.BaseDir)
	cfg.StateDir = expand(cfg.StateDir)
	for name, repo := range cfg.Repos {
		repo.Path = expand(repo.Path)
		cfg.Repos[name] = repo
	}

	return &cfg, nil
}

func expand(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
```

Add dependency:

```bash
go get gopkg.in/yaml.v3
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add go.mod go.sum internal/config/config.go internal/config/config_test.go
git commit -m "feat: load agentctl config"
```

### Task 3: Add Task State Store

**Files:**
- Create: `internal/state/state.go`
- Create: `internal/state/state_test.go`

**Step 1: Write the failing test**

Create `internal/state/state_test.go`:

```go
package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSaveLoadListDelete(t *testing.T) {
	store := NewStore(t.TempDir())
	task := Task{
		TaskID:       "XL-123",
		Repo:         "backend",
		Branch:       "agent/XL-123",
		Worktree:     "/tmp/worktree",
		TmuxSession:  "agentctl-XL-123",
		ContainerName:"agent-XL-123",
		TokenID:      "42",
		Status:       "running",
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
	}

	if err := store.Save(task); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load("XL-123")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TaskID != task.TaskID || loaded.TokenID != "42" {
		t.Fatalf("loaded = %+v", loaded)
	}

	tasks, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks len = %d", len(tasks))
	}

	if err := store.Delete("XL-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("XL-123"); err == nil {
		t.Fatal("expected load after delete to fail")
	}

	_ = filepath.Separator
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/state -run TestStoreSaveLoadListDelete -v`

Expected: FAIL because state package does not exist.

**Step 3: Write minimal implementation**

Create `internal/state/state.go` with a JSON-backed store that writes files as `<state_dir>/tasks/<TASK_ID>.json`, creates directories with `0700`, writes files with `0600`, and supports `Save`, `Load`, `List`, and `Delete`.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/state -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/state/state.go internal/state/state_test.go
git commit -m "feat: persist task state"
```

### Task 4: Add Command Executor and Git Worktree Service

**Files:**
- Create: `internal/execx/exec.go`
- Create: `internal/gitx/worktree.go`
- Create: `internal/gitx/worktree_test.go`

**Step 1: Write the failing test**

Create tests that verify `PrepareWorktree` runs:

```text
git -C <repo-path> fetch origin <default-branch>
git -C <repo-path> worktree add <worktree> -b agent/XL-123 origin/<default-branch>
```

Use a fake executor that records command invocations.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/gitx -v`

Expected: FAIL because packages do not exist.

**Step 3: Write minimal implementation**

Implement:

```go
type Executor interface {
	Run(ctx context.Context, name string, args ...string) error
}
```

Then implement `gitx.Service.PrepareWorktree(ctx, repoPath, defaultBranch, taskID, worktree string)`.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/gitx -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/execx/exec.go internal/gitx/worktree.go internal/gitx/worktree_test.go
git commit -m "feat: prepare git worktrees"
```

### Task 5: Add GitLab Token Broker

**Files:**
- Create: `internal/tokenbroker/gitlab.go`
- Create: `internal/tokenbroker/gitlab_test.go`

**Step 1: Write the failing test**

Use `httptest.Server` to verify `CreateProjectToken` sends:

- `PRIVATE-TOKEN` header from control PAT.
- `name=<token-name>`.
- `scopes[]=read_repository`.
- `scopes[]=write_repository`.
- `access_level=30`.
- `expires_at=<YYYY-MM-DD>`.

Also verify `RevokeProjectToken` sends `DELETE /api/v4/projects/<id>/access_tokens/<token_id>`.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/tokenbroker -v`

Expected: FAIL because token broker package does not exist.

**Step 3: Write minimal implementation**

Implement a GitLab client with:

```go
type CreatedToken struct {
	ID    string
	Token string
	Name  string
}

func (c *GitLabClient) CreateProjectToken(ctx context.Context, projectID, name string, expiresAt time.Time) (CreatedToken, error)
func (c *GitLabClient) RevokeProjectToken(ctx context.Context, projectID, tokenID string) error
```

Never log or persist `CreatedToken.Token`.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/tokenbroker -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tokenbroker/gitlab.go internal/tokenbroker/gitlab_test.go
git commit -m "feat: manage gitlab project tokens"
```

### Task 6: Add Tmux Runtime Service

**Files:**
- Create: `internal/tmux/tmux.go`
- Create: `internal/tmux/tmux_test.go`

**Step 1: Write the failing test**

Verify that starting a session runs:

```text
tmux new-session -d -s agentctl-XL-123 -c <worktree> <command>
```

Verify attach runs:

```text
tmux attach-session -t agentctl-XL-123
```

Verify detach sends:

```text
tmux detach-client -s agentctl-XL-123
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/tmux -v`

Expected: FAIL because tmux package does not exist.

**Step 3: Write minimal implementation**

Implement `SessionName(taskID string) string`, `Start`, `Attach`, and `Detach` using the executor.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/tmux -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tmux/tmux.go internal/tmux/tmux_test.go
git commit -m "feat: control tmux sessions"
```

### Task 7: Add Docker Template Runtime

**Files:**
- Create: `internal/runtime/docker.go`
- Create: `internal/runtime/docker_test.go`
- Create: `templates/node/command.sh`
- Create: `templates/golang/command.sh`
- Create: `templates/python/command.sh`
- Create: `templates/generic/command.sh`

**Step 1: Write the failing test**

Verify that the untrusted Docker command includes:

- `--rm`
- `--name agent-XL-123`
- `--cap-drop=ALL`
- `--security-opt no-new-privileges`
- `--pids-limit=1024`
- `--memory=8g`
- `--cpus=4`
- `-e GITLAB_TOKEN=<token>`
- `-v <worktree>:/workspace:rw`
- `-w /workspace`

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -v`

Expected: FAIL because runtime package does not exist.

**Step 3: Write minimal implementation**

Implement a `DockerCommand` builder that returns shell-safe argument slices. It should set remote URL inside the container using the scoped token and then run the selected agent command.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/docker.go internal/runtime/docker_test.go templates
git commit -m "feat: build isolated docker runtime commands"
```

### Task 8: Wire Local `run`

**Files:**
- Modify: `internal/cli/root.go`
- Create: `internal/cli/run.go`
- Create: `internal/cli/run_test.go`

**Step 1: Write the failing test**

Test `agentctl run XL-123 --repo backend --config <path>` with fake services and assert:

- Worktree is prepared.
- Token is created.
- Tmux session is started.
- State file is saved.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/cli -run TestRunCommandStartsWorkspace -v`

Expected: FAIL because `run` is not wired.

**Step 3: Write minimal implementation**

Wire `run` flags:

```text
--config
--repo
--agent
--risk
--template
--remote
```

For local runs, orchestrate config, worktree, token, runtime command, tmux start, and state save. If a later step fails after token creation, revoke the token before returning the error.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/cli -run TestRunCommandStartsWorkspace -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/cli/root.go internal/cli/run.go internal/cli/run_test.go
git commit -m "feat: run local agent workspaces"
```

### Task 9: Wire `list`, `attach`, `shell`, and `detach`

**Files:**
- Modify: `internal/cli/root.go`
- Create: `internal/cli/list.go`
- Create: `internal/cli/session.go`
- Create: `internal/cli/list_test.go`
- Create: `internal/cli/session_test.go`

**Step 1: Write the failing tests**

Verify:

- `list` prints task ID, repo, status, worktree, and tmux session.
- `attach XL-123` loads state and calls tmux attach.
- `shell XL-123` behaves like `attach`.
- `detach XL-123` calls tmux detach.

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli -run 'Test(List|Attach|Shell|Detach)' -v`

Expected: FAIL.

**Step 3: Write minimal implementation**

Implement the commands through state store and tmux service.

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli -run 'Test(List|Attach|Shell|Detach)' -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/cli/list.go internal/cli/session.go internal/cli/list_test.go internal/cli/session_test.go
git commit -m "feat: manage active agent sessions"
```

### Task 10: Wire Cleanup

**Files:**
- Create: `internal/cleanup/cleanup.go`
- Create: `internal/cleanup/cleanup_test.go`
- Create: `internal/cli/cleanup.go`
- Create: `internal/cli/cleanup_test.go`

**Step 1: Write the failing test**

Verify cleanup continues through all steps even when one fails:

1. Revoke GitLab token.
2. Stop Docker container.
3. Kill tmux session.
4. Remove git worktree.
5. Delete state file only when cleanup is complete enough.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/cleanup ./internal/cli -run TestCleanup -v`

Expected: FAIL.

**Step 3: Write minimal implementation**

Implement cleanup as an ordered, idempotent service that aggregates errors and prints each failure.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/cleanup ./internal/cli -run TestCleanup -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/cleanup internal/cli/cleanup.go internal/cli/cleanup_test.go
git commit -m "feat: clean up agent workspaces"
```

### Task 11: Add Remote SSH Orchestration

**Files:**
- Create: `internal/remote/ssh.go`
- Create: `internal/remote/ssh_test.go`
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/session.go`
- Modify: `internal/cli/cleanup.go`

**Step 1: Write the failing test**

Verify `agentctl run XL-123 --repo backend --remote buildbox-1` resolves remote config and executes:

```text
ssh deploy@buildbox-1.example.com /usr/local/bin/agentctl run XL-123 --repo backend --agent codex --risk untrusted
```

Verify remote attach uses:

```text
ssh -t deploy@buildbox-1.example.com /usr/local/bin/agentctl attach XL-123
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/remote ./internal/cli -run TestRemote -v`

Expected: FAIL.

**Step 3: Write minimal implementation**

Implement remote command forwarding over SSH using the executor. Avoid forwarding local-only flags that would cause recursion bugs.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/remote ./internal/cli -run TestRemote -v`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/remote internal/cli/run.go internal/cli/session.go internal/cli/cleanup.go
git commit -m "feat: run agentctl on remote hosts"
```

### Task 12: Add CLI Documentation and Smoke Test

**Files:**
- Create: `README.md`
- Create: `examples/config.yaml`
- Create: `scripts/smoke-local.sh`

**Step 1: Write README**

Document:

- Install from source.
- Required tools: `git`, `tmux`, `docker`, `ssh`.
- Required env: `GITLAB_CONTROL_PAT`.
- Config format.
- Local run example.
- Remote run example.
- Cleanup behavior.

**Step 2: Add example config**

Create a complete config with `backend`, `buildbox-1`, GitLab host, and templates.

**Step 3: Add smoke script**

Create `scripts/smoke-local.sh` that:

1. Builds `agentctl`.
2. Creates a disposable git repo.
3. Creates a config pointing to that repo.
4. Runs config validation and command construction tests where possible.

Do not require a real GitLab token for the smoke script.

**Step 4: Run verification**

Run:

```bash
go test ./...
go build ./cmd/agentctl
```

Expected: all tests pass and binary builds.

**Step 5: Commit**

```bash
git add README.md examples/config.yaml scripts/smoke-local.sh
git commit -m "docs: document agentctl usage"
```

### Task 13: Final Verification

**Files:**
- Modify as needed based on test failures.

**Step 1: Run all tests**

Run: `go test ./...`

Expected: PASS.

**Step 2: Build binary**

Run: `go build -o bin/agentctl ./cmd/agentctl`

Expected: `bin/agentctl` exists.

**Step 3: Run help**

Run: `./bin/agentctl --help`

Expected: Help output lists all core commands.

**Step 4: Run command-level help**

Run:

```bash
./bin/agentctl run --help
./bin/agentctl cleanup --help
./bin/agentctl attach --help
```

Expected: Help output includes documented flags.

**Step 5: Commit final fixes**

```bash
git add .
git commit -m "test: verify agentctl cli"
```

---

Plan complete and saved to `docs/plans/2026-06-05-agentctl-implementation.md`.

Two execution options:

**1. Subagent-Driven (this session)** - dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Parallel Session (separate)** - open a new session with `superpowers:executing-plans`, batch execution with checkpoints.

