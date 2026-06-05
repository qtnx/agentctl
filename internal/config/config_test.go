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

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	wantRepoPath := filepath.Join(home, "code/backend")
	if cfg.Repos["backend"].Path != wantRepoPath {
		t.Fatalf("repo path = %q, want %q", cfg.Repos["backend"].Path, wantRepoPath)
	}

	wantStateDir := filepath.Join(home, ".local/state/agentctl")
	if cfg.StateDir != wantStateDir {
		t.Fatalf("state dir = %q, want %q", cfg.StateDir, wantStateDir)
	}
}
