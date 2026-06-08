# agentctl dev Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build `agentctl dev` for local and remote Docker-backed dev commands with streaming output and automatic remote sync/watch.

**Architecture:** Add a focused CLI command that reuses existing config, task ID validation, template selection, Docker invocation, and remote target validation where possible. For remote dev, introduce direct SSH and rsync helpers because the local process must own streaming, port forwarding, and sync/watch behavior.

**Tech Stack:** Go, Cobra, Docker CLI, SSH, rsync, polling file snapshots for portable watch behavior.

---

### Task 1: Register command and parse dev options

**Files:**
- Modify: `internal/cli/root.go`
- Create: `internal/cli/dev.go`
- Test: `internal/cli/root_test.go`
- Test: `internal/cli/dev_test.go`

**Step 1:** Write failing tests that root exposes `dev` and that `dev TASK -- command` preserves command argv after `--`.

**Step 2:** Run `go test ./internal/cli`.

**Step 3:** Add `newDevCommand()` and minimal option parsing.

**Step 4:** Run `go test ./internal/cli`.

### Task 2: Local Docker dev

**Files:**
- Modify: `internal/cli/dev.go`
- Test: `internal/cli/dev_test.go`

**Step 1:** Write failing tests for local current-dir Docker invocation and default shell command.

**Step 2:** Run `go test ./internal/cli`.

**Step 3:** Build local invocation with `runtime.DockerInvocationFor`, `Detached: false`, and direct executor streaming.

**Step 4:** Run `go test ./internal/cli`.

### Task 3: Local macOS fallback

**Files:**
- Modify: `internal/cli/dev.go`
- Test: `internal/cli/dev_test.go`

**Step 1:** Write failing tests that Docker unavailable on macOS prompts before using `runtime.MacOSSandboxInvocationFor`.

**Step 2:** Run `go test ./internal/cli`.

**Step 3:** Reuse `selectLocalRuntime` and `macOSSandboxFallbackPrompt`.

**Step 4:** Run `go test ./internal/cli`.

### Task 4: Remote sync and Docker stream

**Files:**
- Create: `internal/remote/dev.go`
- Test: `internal/remote/dev_test.go`
- Modify: `internal/cli/dev.go`
- Test: `internal/cli/dev_test.go`

**Step 1:** Write failing tests for rsync initial sync and SSH command that runs Docker on the remote host.

**Step 2:** Run `go test ./internal/remote ./internal/cli`.

**Step 3:** Add helpers for target address, shell quoting reuse, rsync argv, ssh argv with optional port forwards, and remote Docker command.

**Step 4:** Run `go test ./internal/remote ./internal/cli`.

### Task 5: Watch loop

**Files:**
- Create: `internal/watch/watch.go`
- Test: `internal/watch/watch_test.go`
- Modify: `internal/cli/dev.go`
- Test: `internal/cli/dev_test.go`

**Step 1:** Write failing tests for snapshot changes triggering sync.

**Step 2:** Run `go test ./internal/watch ./internal/cli`.

**Step 3:** Add a portable polling watcher with default excludes.

**Step 4:** Run `go test ./internal/watch ./internal/cli`.

### Task 6: Docs and verification

**Files:**
- Modify: `README.md`

**Step 1:** Document `agentctl dev` examples and remote Docker requirement.

**Step 2:** Run `go test ./...`, `bash scripts/smoke-local.sh`, and `go build -o bin/agentctl ./cmd/agentctl`.
