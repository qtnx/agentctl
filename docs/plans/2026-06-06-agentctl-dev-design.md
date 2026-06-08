# agentctl dev Design

## Goal

Add a `agentctl dev` command for running a development command with local-command ergonomics: stream output to the caller, keep the loop alive, and for remote dev hosts automatically sync local file changes before and during execution.

## Command Shape

```bash
agentctl dev TASK_ID -- pnpm dev
agentctl dev TASK_ID --remote devbox -- pnpm dev
agentctl dev TASK_ID --remote devbox --port 3000 -- pnpm dev
agentctl dev TASK_ID --remote devbox --port 5173:5173 -- pnpm dev
```

`dev` is intentionally the primary name instead of `exec` because the command is a dev loop, not a one-shot process. It watches/syncs by default for remote runs.

## Behavior

Local runs use the current working directory as the workspace and stream command output. Untrusted local code prefers Docker. On macOS, if Docker is unavailable, the existing prompted `sandbox-exec` fallback is allowed with the same risk warning used by `run`.

Remote runs do not forward to remote `agentctl run`. The local CLI owns the sync loop and SSH stream. It mirrors the current directory to `$HOME/.agent-workspaces/dev/<TASK_ID>` on the remote host with `rsync`, starts a lightweight polling watch loop that repeats rsync when files change, then opens an interactive SSH command that runs Docker on the dev host. The user process always runs in Docker on the dev host.

## Docker Contract

For both local Docker and remote Docker, the command runs with:

- workspace mounted at `/workspace`
- working directory `/workspace`
- hardened flags consistent with the existing Docker runtime where practical
- `--rm -it`
- default image from template/config, overridable by `--template`

Remote dev requires Docker on the remote host. There is no remote `sandbox-exec` fallback.

## Error Handling

Invalid task IDs fail before side effects. Missing remote config fails before sync. Missing command defaults to an interactive shell. If initial rsync fails, the Docker command is not started. If the watch loop fails after the command starts, the command keeps streaming and the sync error is reported on stderr.

## Testing

Tests cover CLI registration, command parsing after `--`, local Docker invocation, local macOS fallback prompt path, remote initial rsync, remote SSH Docker command construction, watch-enabled rsync loop wiring, and port forwarding argument construction.
