# macOS Strict Sandbox Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make macOS `sandbox-exec` fallback deny reads outside an allowlist by default while still letting Go, Node, and Rust tools run through configurable system/toolchain paths and task-local caches.

**Architecture:** Add a `sandbox.macos` config section, normalize it into runtime sandbox options, and generate either a strict read/write allowlist profile or the older write-only compatibility profile. Keep structured custom rules as path allowlists; avoid raw SBPL by default.

**Tech Stack:** Go, YAML config, macOS `sandbox-exec` SBPL profiles, existing Cobra commands.

---

### Task 1: Config Defaults And Parsing

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Step 1:** Write failing tests for default `sandbox.macos.mode: strict`, network enabled, toolchain read allowlist, task-local env defaults, and custom allowlist expansion.

**Step 2:** Run `go test ./internal/config` and confirm failure.

**Step 3:** Add config structs and path/env normalization.

**Step 4:** Run `go test ./internal/config`.

### Task 2: Runtime Profile Generation

**Files:**
- Modify: `internal/runtime/macos_sandbox.go`
- Test: `internal/runtime/macos_sandbox_test.go`

**Step 1:** Write failing tests that strict profiles deny `file-read*`, allow only configured read/write paths, apply deny-read overrides, and export task-local Go/Node/Rust cache env vars.

**Step 2:** Run `go test ./internal/runtime` and confirm failure.

**Step 3:** Implement strict profile generation and preserve `write_only` compatibility mode.

**Step 4:** Run `go test ./internal/runtime`.

### Task 3: CLI Wiring

**Files:**
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/dev.go`
- Test: `internal/cli/run_test.go`
- Test: `internal/cli/dev_test.go`

**Step 1:** Write failing tests that `run` and `dev` pass config-derived macOS sandbox policy into `runtime.MacOSSandboxInvocationFor`.

**Step 2:** Run `go test ./internal/cli` and confirm failure.

**Step 3:** Map config sandbox settings into runtime options.

**Step 4:** Run `go test ./internal/cli`.

### Task 4: Docs And Verification

**Files:**
- Modify: `README.md`

**Step 1:** Document strict vs write-only modes, custom allowlists, and toolchain cache env behavior.

**Step 2:** Run `go test ./...`, `bash scripts/smoke-local.sh`, and `go build -o bin/agentctl ./cmd/agentctl`.
