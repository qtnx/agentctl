package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	BaseDir   string            `yaml:"base_dir"`
	StateDir  string            `yaml:"state_dir"`
	GitLab    GitLabConfig      `yaml:"gitlab"`
	Repos     map[string]Repo   `yaml:"repos"`
	Remotes   map[string]Remote `yaml:"remotes"`
	Templates TemplatesConfig   `yaml:"templates"`
}

type GitLabConfig struct {
	Host string `yaml:"host"`
}

type Repo struct {
	Path          string `yaml:"path"`
	ProjectID     string `yaml:"project_id"`
	DefaultBranch string `yaml:"default_branch"`
	Remote        string `yaml:"remote"`
}

type Remote struct {
	Host         string `yaml:"host"`
	User         string `yaml:"user"`
	AgentctlPath string `yaml:"agentctl_path"`
	ConfigPath   string `yaml:"config_path"`
}

type TemplatesConfig struct {
	Default string `yaml:"default"`
}

func Load(path string) (*Config, error) {
	configPath, err := expandTilde(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if err := expandConfigPaths(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func defaultConfig() Config {
	return Config{
		BaseDir:  "~/.agent-workspaces",
		StateDir: "~/.local/state/agentctl",
		GitLab: GitLabConfig{
			Host: "gitlab.com",
		},
		Repos:   map[string]Repo{},
		Remotes: map[string]Remote{},
	}
}

func expandConfigPaths(cfg *Config) error {
	var err error

	cfg.BaseDir, err = expandTilde(cfg.BaseDir)
	if err != nil {
		return err
	}

	cfg.StateDir, err = expandTilde(cfg.StateDir)
	if err != nil {
		return err
	}

	if cfg.Repos == nil {
		cfg.Repos = map[string]Repo{}
	}
	for name, repo := range cfg.Repos {
		repo.Path, err = expandTilde(repo.Path)
		if err != nil {
			return err
		}
		cfg.Repos[name] = repo
	}

	if cfg.Remotes == nil {
		cfg.Remotes = map[string]Remote{}
	}

	return nil
}

func expandTilde(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if path == "~" {
		return home, nil
	}

	return filepath.Join(home, path[2:]), nil
}
