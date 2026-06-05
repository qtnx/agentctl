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
	if cfg.StateDir == "" {
		t.Fatal("expected default state dir")
	}
}
