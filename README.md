# agentctl

`agentctl` is a Go CLI for starting isolated agent workspaces from local Git repositories. A run creates a Git worktree, issues a short-lived GitLab project token, starts the agent command inside Docker, and keeps the session attachable through tmux. The same commands can be forwarded to a configured remote runner over SSH.

## Install From Source

Build-time requirement:

- Go 1.22 or newer

Runtime requirements:

- `git`
- `tmux`
- `docker`
- `ssh` for remote runners

Build and test:

```bash
git clone <repo-url> agentctl
cd agentctl
go test ./...
go build -o bin/agentctl ./cmd/agentctl
```

Put `bin/agentctl` on your `PATH`, or copy it to a standard location such as `/usr/local/bin/agentctl`.

## GitLab Control Token

`agentctl run` and `agentctl cleanup` require `GITLAB_CONTROL_PAT` when they need to create or revoke GitLab project access tokens:

```bash
export GITLAB_CONTROL_PAT=glpat-...
```

The control PAT is read from the environment and is not stored in state. The short-lived project token value returned by GitLab is also not persisted in task state; state stores only token metadata such as token ID, token name, GitLab host, and project ID so cleanup can revoke it.

## Configuration

By default, commands read `~/.config/agentctl/config.yaml`. You can pass `--config` on individual commands.

```yaml
base_dir: ~/agent-workspaces
state_dir: ~/.local/state/agentctl
gitlab:
  host: gitlab.example.com
repos:
  backend:
    path: ~/code/example-group/backend
    project_id: "12345678"
    default_branch: main
    remote: git@gitlab.example.com:example-group/backend.git
remotes:
  buildbox-1:
    host: buildbox-1.example.com
    user: agent
    agentctl_path: /usr/local/bin/agentctl
    config_path: /etc/agentctl/config.yaml
templates:
  default: node
```

Fields:

- `base_dir`: directory where task worktrees are created. A task `XL-123` uses `<base_dir>/XL-123`.
- `state_dir`: directory where JSON task state and temporary env files are written.
- `gitlab.host`: GitLab host used for project token API calls and in-container Git URL rewriting.
- `repos.<name>.path`: local path to an existing Git repository.
- `repos.<name>.project_id`: GitLab project ID used for project token creation and revocation.
- `repos.<name>.default_branch`: branch fetched from `origin` and used as the base for `agent/<TASK_ID>`.
- `repos.<name>.remote`: repository URL for documentation/config completeness; keep it aligned with the repo's `origin`.
- `remotes.<name>.host`: SSH host for a remote runner.
- `remotes.<name>.user`: optional SSH user.
- `remotes.<name>.agentctl_path`: remote binary path. Defaults to `agentctl` when omitted.
- `remotes.<name>.config_path`: config path passed to the remote `agentctl` command.
- `templates.default`: default runtime template when `--template` is not provided. Supported templates are `generic`, `node`, `golang`, and `python`.

See `examples/config.yaml` for a complete sample.

## Local Usage

Start a local task:

```bash
agentctl run XL-123 --repo backend --agent codex --risk untrusted --template node
```

Then attach, list, and clean up:

```bash
agentctl attach XL-123
agentctl list
agentctl cleanup XL-123
```

`run` creates branch `agent/XL-123`, worktree `<base_dir>/XL-123`, tmux session `agentctl-XL-123`, and Docker container `agent-XL-123`. Only the `untrusted` risk profile is currently supported.

## Remote Usage

Remote commands are forwarded over SSH to a configured runner. The remote machine must already have `agentctl`, its config file, and required runtime tools installed. For `run` and token-revoking `cleanup`, `GITLAB_CONTROL_PAT` must be available in the remote command environment.

```bash
agentctl run XL-123 --repo backend --remote buildbox-1
agentctl attach XL-123 --remote buildbox-1
agentctl cleanup XL-123 --remote buildbox-1
```

When `config_path` is set on the remote, the local CLI appends `--config <config_path>` to the remote command.

## Cleanup And Retry Behavior

`agentctl cleanup TASK_ID` loads task state, then tries to:

- revoke the GitLab project token when a token ID is present;
- remove Docker container `agent-<TASK_ID>`;
- kill tmux session `agentctl-<TASK_ID>`;
- remove worktree `<base_dir>/<TASK_ID>`;
- delete the task state file.

Cleanup validates state-derived resource names before removing them. If any resource cleanup step fails, errors are reported together and state is not deleted. Fix the underlying issue and rerun the same cleanup command. State is deleted only after token, Docker, tmux, and worktree cleanup all succeed.

If `run` fails after creating a GitLab project token, it attempts to revoke that token before returning the original error. If state saving fails after tmux startup, it also removes the temporary env file, stops the tmux session, and revokes the token.

## Security Notes

- GitLab project tokens are short-lived and scoped to the configured project with repository read/write access.
- `GITLAB_CONTROL_PAT` is read from the environment and is never written to task state.
- Runtime project token values are not persisted in task state. They are written to a `0600` temporary env file under `state_dir/env`, sourced immediately before Docker starts, and removed before the Docker command is executed.
- Docker runs with `--cap-drop=ALL`, `--security-opt no-new-privileges`, pids, memory, and CPU limits. The worktree and repository `.git` directory are mounted explicitly.
- tmux sessions keep agent processes attachable and detachable; session names are derived from validated task IDs.
- Remote SSH forwarding validates host/user/path inputs and shell-quotes the remote `agentctl` command arguments.

## Limitations

- There is no daemon. Commands coordinate Git, Docker, tmux, state files, GitLab API calls, and SSH directly.
- Remote runners are not bootstrapped automatically; the remote binary, config, tools, and environment must already exist.
- The included smoke script intentionally avoids real GitLab, Docker, and tmux side effects. Full Docker/tmux/git integration smoke tests still require a prepared manual environment.
