# agentctl - isolated AI coding agent workspaces for Codex, Claude Code, and OMX

[![Release](https://github.com/qtnx/agentctl/actions/workflows/release.yml/badge.svg)](https://github.com/qtnx/agentctl/actions/workflows/release.yml)
[![GitHub release](https://img.shields.io/github/v/release/qtnx/agentctl)](https://github.com/qtnx/agentctl/releases)

`agentctl` is a Go CLI for running AI coding agents in isolated, repeatable development workspaces. It combines git worktrees, Docker sandboxing, optional tmux sessions, GitLab project tokens, macOS sandbox fallback, and remote devbox execution so tools like Codex, Claude Code, and OMX can work on untrusted code without polluting your main checkout.

Use `agentctl` when you want a practical AI agent runner for:

- isolated Codex, Claude Code, and OMX sessions
- Docker-based untrusted code execution
- per-task git worktrees and cleanup
- short-lived GitLab repository tokens
- remote development machines over SSH
- live synced dev commands with automatic port forwarding
- versioned GitHub Releases for simple install and update flows

## Install

Install the latest `agentctl` release with GitHub CLI. This path is the most reliable when GitHub raw/release CDN returns transient 504s:

```bash
gh release download --repo qtnx/agentctl --pattern install.sh --output - | sh
```

Install or update to a specific version:

```bash
gh release download v0.1.1 --repo qtnx/agentctl --pattern install.sh --output - | sh -s -- v0.1.1
gh release download --repo qtnx/agentctl --pattern install.sh --output - | AGENTCTL_VERSION=v0.1.1 sh
```

Install with curl if you do not use `gh`:

```bash
curl -fsSL --retry 5 --retry-all-errors --retry-delay 2 https://github.com/qtnx/agentctl/releases/latest/download/install.sh | sh
```

Install with Go:

```bash
go install github.com/qtnx/agentctl/cmd/agentctl@latest
go install github.com/qtnx/agentctl/cmd/agentctl@v0.1.1
```

Build from source:

```bash
git clone https://github.com/qtnx/agentctl.git
cd agentctl
go test ./...
go build -o bin/agentctl ./cmd/agentctl
```

Runtime requirements:

- `git`
- `docker` for preferred local untrusted runs
- `tmux` is optional for Docker runs; when absent, local Docker runs use detached Docker directly
- `sandbox-exec` and `tmux` for the prompted macOS fallback when Docker is unavailable
- `ssh` for remote runners
- `rsync` for `agentctl dev --remote`

Check the installed version:

```bash
agentctl version
```

## Versioned GitHub Releases

`agentctl` publishes versioned binaries through GitHub Releases. Push a semver-style tag to build and publish macOS, Linux, and Windows archives with SHA-256 checksums:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow injects version metadata into the binary, so `agentctl version` reports the release tag, commit, and build date. Re-run the same installer to update to the latest release or pass a specific version to pin a machine to a known agentctl build.

## GitLab Control Token

`agentctl run` and `agentctl cleanup` require `GITLAB_CONTROL_PAT` when they need to create or revoke GitLab project access tokens:

```bash
export GITLAB_CONTROL_PAT=glpat-...
```

The control PAT is read from the environment and is not stored in state. The short-lived project token value returned by GitLab is also not persisted in task state; state stores only token metadata such as token ID, token name, GitLab host, and project ID so cleanup can revoke it.

## Configuration

By default, commands read `~/.config/agentctl/config.yaml`. You can pass `--config` on individual commands.
If the config file is missing, `agentctl` creates a minimal default config automatically.

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
sandbox:
  macos:
    mode: strict
    network: true
    allow_read:
      - /bin
      - /sbin
      - /usr
      - /System
      - /Library
      - /opt/homebrew
      - /usr/local
      - /private/var/select
      - /var/select
      - /var/db/xcode_select_link
      - /private/var/db/xcode_select_link
      - /etc/codex
      - /private/etc/codex
    allow_write:
      - workspace
      - task_home
      - state_dir
      - tmp
    deny_read:
      - ~/.ssh
      - ~/.aws
      - ~/.config
      - ~/.gitconfig
      - ~/Desktop
      - ~/Documents
    allow_tools:
      - git
      - ssh
      - make
      - cmake
      - clang
      - clang++
      - gcc
      - g++
      - python
      - python3
      - pip
      - pip3
      - uv
      - poetry
      - node
      - npm
      - npx
      - pnpm
      - yarn
      - bun
      - deno
      - go
      - rustup
      - cargo
      - rustc
      - java
      - javac
      - mvn
      - gradle
      - jq
      - rg
      - zsh
      - codex
      - claude
      - ompx
      - agentctl
    env:
      GOPATH: ${TASK_HOME}/go
      GOCACHE: ${TASK_HOME}/.cache/go-build
      GOMODCACHE: ${TASK_HOME}/go/pkg/mod
      npm_config_cache: ${TASK_HOME}/.npm
      PNPM_HOME: ${TASK_HOME}/.pnpm
      CARGO_HOME: ${TASK_HOME}/.cargo
      RUSTUP_HOME: ${TASK_HOME}/.rustup
    custom_rules:
      allow_read: []
      allow_write: []
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
- `sandbox.macos.mode`: `strict` denies file reads and writes by default, then allows configured paths. `write_only` preserves the older compatibility behavior that only restricts writes.
- `sandbox.macos.network`: enables network access for macOS fallback runs.
- `sandbox.macos.allow_read`: system/toolchain paths readable by strict mode. Add SDKs or custom toolchains here.
- `sandbox.macos.allow_write`: writable paths. Special values are `workspace`, `task_home`, `state_dir`, and `tmp`.
- `sandbox.macos.deny_read`: explicit sensitive read denials. These are useful if you later allow a broader read path.
- `sandbox.macos.allow_tools`: executable names or paths to resolve from host `PATH` and add to the read allowlist. This covers host-installed tools such as `git`, `node`, `codex`, `claude`, `ompx`, `go`, `cargo`, `zsh`, and the current `agentctl` binary. Add local SDKs or CLIs here instead of widening broad home-directory reads.
- `sandbox.macos.env`: environment variables for sandboxed tools. `${TASK_HOME}`, `${WORKSPACE}`, `${STATE_DIR}`, and `${TMPDIR}` are expanded by the runtime.
- `sandbox.macos.custom_rules.allow_read` and `allow_write`: extra structured path allowlists for local overrides. Raw SBPL rules are intentionally not supported by default.

See `examples/config.yaml` for a complete sample.

## Local Usage

Start a local task:

```bash
agentctl run XL-123 --repo backend --agent codex --risk untrusted --template node
```

For quick starts, the root command can run an agent directly and generate the task id:

```bash
agentctl --agent codex
agentctl --agent claude
agentctl --agent ompx
agentctl --agent claude --repo backend --detach
agentctl --agent claude --repo backend --no-tmux
```

`--agent <name>` accepts any safe executable name, not only built-in names. If that command exists in the selected runtime's `PATH`, `agentctl` runs it directly; otherwise it falls back to an interactive login shell. Use `--agent shell` to start the shell intentionally.

Generated task ids use `<agent>-<unix-time>`, for example `claude-1780662896`. You can still pass an explicit id as an optional positional argument: `agentctl --agent claude XL-123`.

When you are already inside a GitLab-backed repository, `--repo` is optional:

```bash
agentctl run XL-123 --agent codex
```

In that mode, `agentctl` uses the current directory. If `origin` points at the configured GitLab host, it creates an isolated git worktree and derives the GitLab project path from `origin`. If the directory is not a Git repo, has no usable `origin`, or points at another host such as GitHub, it runs as a plain workspace: no GitLab token is created, no git worktree is created, and cleanup will not remove the current directory.

You can also pass a GitLab repository URL directly instead of defining it under `repos`:

```bash
agentctl run XL-123 --repo https://gitlab.example.com/example-group/backend.git --agent codex
agentctl run XL-124 --repo git@gitlab.example.com:example-group/backend.git --agent shell
```

For direct URLs, `agentctl` clones or reuses a cached repo under `<state_dir>/repos`, derives the GitLab project path from the URL, and stores the resolved repo path in task state so `cleanup` does not need a matching `repos` config entry. Direct URL runs currently use `main` as the base branch.

Then attach, list, and clean up:

```bash
agentctl attach XL-123
agentctl list
agentctl cleanup XL-123
```

`run` creates branch `agent/XL-123`, worktree `<base_dir>/XL-123`, starts the runtime, then attaches by default. Pass `--detach` to start in the background:

```bash
agentctl run XL-123 --repo backend --agent codex --detach
```

If `tmux` is installed, Docker starts inside tmux session `agentctl-XL-123`. Pass `--no-tmux` to start Docker directly even when tmux is installed. If `tmux` is not installed, Docker starts directly in detached mode and `agentctl attach XL-123` uses `docker attach agent-XL-123`. Docker detach uses Docker's terminal escape sequence, `Ctrl-p Ctrl-q`. tmux sessions are kept open if the agent command exits, so `attach` can show the exit status instead of failing with `no sessions`.

Docker is preferred for `untrusted` code. On macOS only, if Docker is missing but `sandbox-exec` and `tmux` are available, `agentctl run` prompts before falling back to a native macOS sandbox. The prompt explains that `sandbox-exec` is weaker than Docker: it has no container filesystem, CPU, memory, or PID isolation. The default `strict` sandbox denies reads and writes outside the configured allowlists, so it may break local toolchains until their paths are added under `sandbox.macos.allow_read` or their executable names are listed under `sandbox.macos.allow_tools`. Declining the prompt exits before creating worktrees, tokens, or state. Only the `untrusted` risk profile is currently supported.

Codex, Claude, and OMPX auth/config are linked into the isolated task home when they exist on the host:

- `~/.codex` -> `${TASK_HOME}/.codex`
- `~/.claude` -> `${TASK_HOME}/.claude`
- `~/.claude.json` -> `${TASK_HOME}/.claude.json`
- `~/.omp` -> `${TASK_HOME}/.omp`

For local Docker runs, the same paths are bind-mounted under `/root`. This keeps existing CLI login state working, but it also means code running in that task can access those agent credentials.

Run a dev command from the current directory with local-command ergonomics:

```bash
agentctl dev XL-123 -- pnpm dev
agentctl dev XL-123 --port 3000 -- pnpm dev
agentctl dev XL-123 --port 8080:3000 -- pnpm dev
agentctl dev -- npm run dev
```

`dev` does not create a Git worktree or GitLab token. It uses the current directory as the workspace, runs the command in Docker, and streams output to the current terminal. If `TASK_ID` is omitted, `dev` reads `.agentctl` from the current directory or derives a stable id from the current directory, such as `dev-test-repo-472cc118`, then writes it back to `.agentctl` for future runs; use `--` before the command. `--port PORT` publishes `127.0.0.1:PORT` from the container. `--port LOCAL:CONTAINER` publishes `LOCAL` on the host to `CONTAINER` in Docker.

On macOS, if Docker is unavailable, `dev` prompts before falling back to `sandbox-exec`. The fallback has the same risk profile warning as `run`, but `dev` executes the requested command directly and exits with that command instead of keeping a shell open afterward. Go, Node, and Rust caches default into `${TASK_HOME}` through `sandbox.macos.env`, so commands such as `go test`, `pnpm install`, and `cargo build` do not need to read or write host caches.

## Remote Usage

Remote commands are forwarded over SSH to a configured runner. The remote machine must already have `agentctl`, its config file, and required runtime tools installed. For `run` and token-revoking `cleanup`, `GITLAB_CONTROL_PAT` must be available in the remote command environment.

```bash
agentctl run XL-123 --repo backend --remote buildbox-1
agentctl attach XL-123 --remote buildbox-1
agentctl cleanup XL-123 --remote buildbox-1
```

When `config_path` is set on the remote, the local CLI appends `--config <config_path>` to the remote command.

Remote dev uses a different flow: the local CLI owns sync/watch and streams the remote command output over SSH. The remote host needs `docker`, `ssh`, and a writable `$HOME/.agent-workspaces/dev` directory; it does not need `agentctl` for `dev`.

```bash
agentctl dev XL-123 --remote buildbox-1 -- pnpm dev
agentctl dev XL-123 --remote buildbox-1 --port 3000 -- pnpm dev
agentctl dev XL-123 --remote buildbox-1 --port 8080:3000 -- pnpm dev
agentctl dev --remote buildbox-1 -- npm run dev
```

If a dev remote is not in `remotes`, `agentctl dev` accepts an inline SSH target and prompts to save it:

```bash
agentctl dev --remote codemc -- npm run dev
agentctl dev --remote qtnx@codemc -- npm run dev
```

Saving `qtnx@codemc` writes a `remotes.codemc` entry with `host: codemc` and `user: qtnx`; declining the prompt still runs that command using the inline target for the current invocation.

For remote dev, `agentctl` runs an initial `rsync` from the current directory to `$HOME/.agent-workspaces/dev/<TASK_ID>` on the remote host. Sync output is quiet by default; pass `--debug` or set `AGENTCTL_DEBUG=1` to show rsync progress and transfer stats for the initial sync. Watch-triggered syncs stay quiet. After syncing, `agentctl` makes synced user-owned files writable for the Docker container, removes any previous Docker container with the same task id, starts a polling watch loop, then runs the command in Docker on the remote host as the SSH user's UID/GID. Output streams back to the local terminal. Port flags publish the container port on the remote loopback interface and add SSH `-L` forwarding back to the local machine; explicit `--port` values are checked on both the local machine and remote host before sync or Docker starts. For Vite projects, remote dev also infers and forwards the dev server port when no `--port` flag is provided: it uses a command `--port` argument when present, otherwise starts at `5173`, skips ports already busy on the local machine or remote host, and injects the selected `--port` plus `--host 0.0.0.0` into dev scripts so the Docker publish and SSH forward can reach the server.

Install dependencies before running a dev server when the remote workspace is fresh:

```bash
agentctl dev --remote codemc -- npm install
agentctl dev --remote codemc -- npm run dev
```

Node Docker images enable Corepack shims in a writable container temp directory before running your command, so `pnpm` and `yarn` are available without installing them into the synced workspace.

## Cleanup And Retry Behavior

`agentctl cleanup TASK_ID` loads task state, then tries to:

- revoke the GitLab project token when a token ID is present;
- remove Docker container `agent-<TASK_ID>` when the task was started with Docker;
- kill tmux session `agentctl-<TASK_ID>` when the task was started with tmux or macOS sandbox fallback;
- remove worktree `<base_dir>/<TASK_ID>`;
- delete the task state file.

Cleanup validates state-derived resource names before removing them. If any resource cleanup step fails, errors are reported together and state is not deleted. Fix the underlying issue and rerun the same cleanup command. State is deleted only after token, Docker, optional tmux, and worktree cleanup all succeed.

If `run` fails after creating a GitLab project token, it attempts to revoke that token before returning the original error. If state saving fails after tmux startup, it also removes the temporary env file, stops the tmux session, and revokes the token.

## Security Notes

- GitLab project tokens are short-lived and scoped to the configured project with repository read/write access.
- `GITLAB_CONTROL_PAT` is read from the environment and is never written to task state.
- Runtime project token values are not persisted in task state. They are written to a `0600` temporary env file under `state_dir/env`, sourced immediately before Docker starts, and removed before the Docker command is executed.
- Docker runs with `--cap-drop=ALL`, `--security-opt no-new-privileges`, pids, memory, and CPU limits. The worktree and repository `.git` directory are mounted explicitly.
- macOS `sandbox-exec` fallback defaults to `strict`: file reads and writes are denied outside configured allowlists, with system/toolchain paths allowed for common Go, Node, and Rust workflows. Setting `sandbox.macos.mode: write_only` is weaker and may read host files.
- tmux sessions keep agent processes attachable and detachable; session names are derived from validated task IDs.
- Remote SSH forwarding validates host/user/path inputs and shell-quotes the remote `agentctl` command arguments.
- Remote `dev` runs user code in Docker on the remote host as the SSH user's UID/GID. Local sync uses `rsync --delete`, excludes `.git`, `.worktrees`, `node_modules`, and `.agentctl`, then makes synced user-owned files writable so containerized toolchains can write dependency and build output without producing root-owned workspace files. Starting the same task id again replaces the previous `agent-<TASK_ID>` container before running the new command.

## Limitations

- There is no daemon. Commands coordinate Git, Docker, tmux, state files, GitLab API calls, and SSH directly.
- Remote runners are not bootstrapped automatically; the remote binary, config, tools, and environment must already exist.
- `agentctl dev --remote` uses a portable polling watcher instead of platform-specific file notification APIs.
- Strict macOS sandboxing can break tools installed outside configured `allow_read` paths or unresolved by `allow_tools`.
- The included smoke script intentionally avoids real GitLab, Docker, and tmux side effects. Full Docker/tmux/git integration smoke tests still require a prepared manual environment.
