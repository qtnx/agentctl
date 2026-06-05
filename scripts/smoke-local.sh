#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

agentctl_bin="$repo_root/bin/agentctl"
temp_repo="$tmp_dir/backend"
base_dir="$tmp_dir/workspaces"
state_dir="$tmp_dir/state"
config_file="$tmp_dir/config.yaml"

mkdir -p "$repo_root/bin" "$base_dir" "$state_dir"

if git init -b main "$temp_repo" >/dev/null 2>&1; then
  :
else
  git init "$temp_repo" >/dev/null
  git -C "$temp_repo" checkout -b main >/dev/null
fi
git -C "$temp_repo" config user.name "agentctl smoke"
git -C "$temp_repo" config user.email "agentctl-smoke@example.invalid"
git -C "$temp_repo" config commit.gpgsign false
printf '# smoke\n' >"$temp_repo/README.md"
git -C "$temp_repo" add README.md
git -C "$temp_repo" commit -m "initial smoke commit" >/dev/null

cat >"$config_file" <<YAML
base_dir: "$base_dir"
state_dir: "$state_dir"
gitlab:
  host: gitlab.example.com
repos:
  backend:
    path: "$temp_repo"
    project_id: "12345678"
    default_branch: main
    remote: git@gitlab.example.com:example-group/backend.git
templates:
  default: node
YAML

go test ./...
go build -o "$agentctl_bin" ./cmd/agentctl

"$agentctl_bin" --help >/dev/null
"$agentctl_bin" run --help >/dev/null

list_output="$("$agentctl_bin" list --config "$config_file")"
case "$list_output" in
  *TASK_ID*REPO*STATUS*WORKTREE*TMUX_SESSION*) ;;
  *)
    printf 'agentctl list output did not include the expected header:\n%s\n' "$list_output" >&2
    exit 1
    ;;
esac

if "$agentctl_bin" run BAD/ID --repo backend --config "$config_file" >"$tmp_dir/bad-run.out" 2>"$tmp_dir/bad-run.err"; then
  printf 'agentctl run BAD/ID succeeded; expected task ID validation failure\n' >&2
  exit 1
fi

if ! grep -q 'invalid task id' "$tmp_dir/bad-run.err"; then
  printf 'agentctl run BAD/ID failed for an unexpected reason:\n' >&2
  cat "$tmp_dir/bad-run.err" >&2
  exit 1
fi

if [ -n "$(find "$base_dir" "$state_dir" -mindepth 1 -print -quit)" ]; then
  printf 'invalid task ID smoke check left unexpected files under temp base/state dirs\n' >&2
  find "$base_dir" "$state_dir" -mindepth 1 -print >&2
  exit 1
fi
