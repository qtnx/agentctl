# agentctl Design

## Goal

Build `agentctl`, a Go single-binary CLI for spawning isolated agent workspaces with git worktrees, short-lived GitLab tokens, tmux-backed attach/detach sessions, and optional remote-machine execution.

## Recommended Approach

Use Go as the CLI/runtime and shell out to proven system tools:

- `git` for fetch, branch creation, and worktree lifecycle.
- `docker` for risk-isolated task containers.
- `tmux` as the long-running session/server layer.
- `ssh` for remote orchestration.
- `curl`-equivalent Go HTTP calls for GitLab access token management.

This keeps v1 pragmatic and debuggable while avoiding premature native integrations with Git, Docker, or SSH libraries.

## Command Surface

```text
agentctl run XL-123 --repo backend --agent codex --risk untrusted
agentctl shell XL-123
agentctl attach XL-123
agentctl detach XL-123
agentctl cleanup XL-123
agentctl list
agentctl token-broker ...
```

`shell` is a convenience alias for interactive attachment. `attach` connects to the tmux session. `detach` sends a detach command or prints the tmux detach binding depending on whether the current process is attached.

## Directory Layout

```text
agentctl/
  cmd/agentctl/
  internal/cli/
  internal/config/
  internal/gitx/
  internal/runtime/
  internal/state/
  internal/tokenbroker/
  internal/tmux/
  internal/remote/
  templates/
    node/
    golang/
    python/
    generic/
```

## Runtime Flow

`agentctl run` will:

1. Load config from `~/.config/agentctl/config.yaml`.
2. Resolve the named repo, including local path, GitLab project ID, default branch, and remote URL.
3. Create branch `agent/<TASK_ID>`.
4. Create worktree under `~/agent-workspaces/<TASK_ID>` unless overridden.
5. Request a short-lived GitLab project access token through the token broker.
6. Start a named tmux session such as `agentctl-XL-123`.
7. Run the selected runtime template inside tmux.
8. Persist task state to `~/.local/state/agentctl/tasks/<TASK_ID>.json`.

For `--risk untrusted`, the default runtime is Docker with restricted capabilities:

- Drop all Linux capabilities.
- Enable `no-new-privileges`.
- Set CPU, memory, and PID limits.
- Mount only the worktree into `/workspace`.
- Pass only the scoped agent token and selected allowlisted environment variables.

## Remote Support

Remote v1 uses SSH orchestration rather than a daemon.

```text
agentctl run XL-123 --repo backend --remote buildbox-1
agentctl attach XL-123 --remote buildbox-1
agentctl cleanup XL-123 --remote buildbox-1
```

The local CLI will execute `agentctl` on the remote machine over SSH. The remote must have the binary installed and configured, or the CLI can later gain an install/copy helper. Tmux sessions and task state live on the remote host, which makes attach/detach stable across local terminal disconnects.

## Config Model

Example config:

```yaml
base_dir: ~/agent-workspaces
state_dir: ~/.local/state/agentctl
gitlab:
  host: gitlab.com
repos:
  backend:
    path: ~/code/backend
    project_id: "123456"
    default_branch: main
    remote: git@gitlab.com:your_group/backend.git
remotes:
  buildbox-1:
    host: buildbox-1.example.com
    user: deploy
    agentctl_path: /usr/local/bin/agentctl
templates:
  default: generic
```

Secrets such as `GITLAB_CONTROL_PAT` are read from the environment for v1. The config may reference environment variable names, but should not store secret values.

## State Model

Each task has one JSON state file:

```json
{
  "task_id": "XL-123",
  "repo": "backend",
  "branch": "agent/XL-123",
  "worktree": "/home/user/agent-workspaces/XL-123",
  "tmux_session": "agentctl-XL-123",
  "container_name": "agent-XL-123",
  "token_id": "98765",
  "token_name": "agent-XL-123-1710000000",
  "gitlab_project_id": "123456",
  "gitlab_host": "gitlab.com",
  "status": "running",
  "created_at": "2026-06-05T09:00:00Z"
}
```

The token value is never persisted. Only the token ID and metadata are saved so cleanup can revoke it.

## Error Handling

The CLI should fail fast with clear messages and keep cleanup idempotent:

- If worktree creation fails, no state file is written.
- If token creation succeeds but runtime startup fails, revoke the token.
- If cleanup partially fails, continue the remaining cleanup steps and report all failures.
- If state is missing but resources exist, `cleanup --worktree PATH` or future discovery flags can handle manual recovery.

## Testing

Unit tests should cover config loading, state persistence, command construction, GitLab token broker requests, and cleanup ordering. Integration tests can use fake executors instead of real Docker/tmux/git. A small set of manual smoke tests should run against a disposable local repo and tmux session.

