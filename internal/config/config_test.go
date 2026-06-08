package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
remotes:
  buildbox-1:
    host: buildbox-1.example.com
    user: deploy
    agentctl_path: /usr/local/bin/agentctl
    config_path: /etc/agentctl/config.yaml
sandbox:
  macos:
    mode: write_only
    network: false
    allow_read:
      - ~/sdks
    allow_write:
      - ~/agent-cache
    deny_read:
      - ~/.ssh
    allow_tools:
      - node
      - codex
    env:
      FOO_CACHE: ~/cache/foo
    custom_rules:
      allow_read:
        - ~/company-sdk
      allow_write:
        - ~/company-cache
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

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	wantRepoPath := filepath.Join(home, "code/backend")
	if cfg.Repos["backend"].Path != wantRepoPath {
		t.Fatalf("repo path = %q, want %q", cfg.Repos["backend"].Path, wantRepoPath)
	}
	remote := cfg.Remotes["buildbox-1"]
	if remote.Host != "buildbox-1.example.com" {
		t.Fatalf("remote host = %q", remote.Host)
	}
	if remote.User != "deploy" {
		t.Fatalf("remote user = %q", remote.User)
	}
	if remote.AgentctlPath != "/usr/local/bin/agentctl" {
		t.Fatalf("remote agentctl path = %q", remote.AgentctlPath)
	}
	if remote.ConfigPath != "/etc/agentctl/config.yaml" {
		t.Fatalf("remote config path = %q", remote.ConfigPath)
	}
	if cfg.Sandbox.MacOS.Mode != "write_only" || cfg.Sandbox.MacOS.Network {
		t.Fatalf("sandbox macos = %#v, want write_only with network disabled", cfg.Sandbox.MacOS)
	}
	if !reflect.DeepEqual(cfg.Sandbox.MacOS.AllowTools, []string{"node", "codex"}) {
		t.Fatalf("sandbox allow tools = %#v, want node and codex", cfg.Sandbox.MacOS.AllowTools)
	}
	for _, got := range []struct {
		name string
		path string
		want string
	}{
		{"allow read", cfg.Sandbox.MacOS.AllowRead[0], filepath.Join(home, "sdks")},
		{"allow write", cfg.Sandbox.MacOS.AllowWrite[0], filepath.Join(home, "agent-cache")},
		{"deny read", cfg.Sandbox.MacOS.DenyRead[0], filepath.Join(home, ".ssh")},
		{"env", cfg.Sandbox.MacOS.Env["FOO_CACHE"], filepath.Join(home, "cache/foo")},
		{"custom allow read", cfg.Sandbox.MacOS.CustomRules.AllowRead[0], filepath.Join(home, "company-sdk")},
		{"custom allow write", cfg.Sandbox.MacOS.CustomRules.AllowWrite[0], filepath.Join(home, "company-cache")},
	} {
		if got.path != got.want {
			t.Fatalf("%s = %q, want %q", got.name, got.path, got.want)
		}
	}

	wantStateDir := filepath.Join(home, ".local/state/agentctl")
	if cfg.StateDir != wantStateDir {
		t.Fatalf("state dir = %q, want %q", cfg.StateDir, wantStateDir)
	}
}

func TestLoadCreatesDefaultConfigWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".config", "agentctl", "config.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.GitLab.Host != "gitlab.com" {
		t.Fatalf("host = %q, want gitlab.com", cfg.GitLab.Host)
	}
	if cfg.Templates.Default != "node" {
		t.Fatalf("default template = %q, want node", cfg.Templates.Default)
	}
	if cfg.Repos == nil || len(cfg.Repos) != 0 {
		t.Fatalf("repos = %#v, want empty map", cfg.Repos)
	}
	if cfg.Sandbox.MacOS.Mode != "strict" {
		t.Fatalf("sandbox mode = %q, want strict", cfg.Sandbox.MacOS.Mode)
	}
	if !cfg.Sandbox.MacOS.Network {
		t.Fatal("sandbox network = false, want true")
	}
	for _, want := range []string{"/bin", "/sbin", "/usr", "/System", "/Library", "/private/var/select", "/var/select", "/var/db/xcode_select_link", "/private/var/db/xcode_select_link", "/etc/codex", "/private/etc/codex"} {
		if !containsString(cfg.Sandbox.MacOS.AllowRead, want) {
			t.Fatalf("sandbox allow_read = %#v, want %q", cfg.Sandbox.MacOS.AllowRead, want)
		}
	}
	for _, want := range []string{"git", "make", "python3", "uv", "node", "npm", "pnpm", "go", "cargo", "rustc", "zsh", "codex", "claude", "omx", "agentctl"} {
		if !containsString(cfg.Sandbox.MacOS.AllowTools, want) {
			t.Fatalf("sandbox allow_tools = %#v, want %q", cfg.Sandbox.MacOS.AllowTools, want)
		}
	}
	for _, want := range []string{"GOPATH", "GOCACHE", "GOMODCACHE", "npm_config_cache", "PNPM_HOME", "CARGO_HOME", "RUSTUP_HOME"} {
		if cfg.Sandbox.MacOS.Env[want] == "" {
			t.Fatalf("sandbox env %s is empty", want)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"base_dir: ~/.agent-workspaces",
		"state_dir: ~/.local/state/agentctl",
		"host: gitlab.com",
		"default: node",
		"sandbox:",
		"mode: strict",
		"allow_tools:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("created config = %q, want %q", text, want)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("config mode = %o, want 0600", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
