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
remotes:
  buildbox-1:
    host: buildbox-1.example.com
    user: deploy
    agentctl_path: /usr/local/bin/agentctl
    config_path: /etc/agentctl/config.yaml
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

	wantStateDir := filepath.Join(home, ".local/state/agentctl")
	if cfg.StateDir != wantStateDir {
		t.Fatalf("state dir = %q, want %q", cfg.StateDir, wantStateDir)
	}
}
