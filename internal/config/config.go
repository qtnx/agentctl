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
	Sandbox   SandboxConfig     `yaml:"sandbox"`
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

type SandboxConfig struct {
	MacOS MacOSSandboxConfig `yaml:"macos"`
}

type MacOSSandboxConfig struct {
	Mode        string                  `yaml:"mode"`
	Network     bool                    `yaml:"network"`
	AllowRead   []string                `yaml:"allow_read"`
	AllowWrite  []string                `yaml:"allow_write"`
	DenyRead    []string                `yaml:"deny_read"`
	AllowTools  []string                `yaml:"allow_tools"`
	Env         map[string]string       `yaml:"env"`
	CustomRules MacOSSandboxCustomRules `yaml:"custom_rules"`
}

type MacOSSandboxCustomRules struct {
	AllowRead  []string `yaml:"allow_read"`
	AllowWrite []string `yaml:"allow_write"`
}

func Load(path string) (*Config, error) {
	configPath, err := expandTilde(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := createDefaultConfig(configPath); err != nil {
			return nil, err
		}
		data, err = os.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
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

func Save(path string, cfg *Config) error {
	configPath, err := expandTilde(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return err
	}
	return os.Chmod(configPath, 0600)
}

func defaultConfig() Config {
	return Config{
		BaseDir:  "~/.agent-workspaces",
		StateDir: "~/.local/state/agentctl",
		GitLab: GitLabConfig{
			Host: "gitlab.com",
		},
		Templates: TemplatesConfig{
			Default: "node",
		},
		Sandbox: SandboxConfig{
			MacOS: MacOSSandboxConfig{
				Mode:    "strict",
				Network: true,
				AllowRead: []string{
					"/bin",
					"/sbin",
					"/usr",
					"/System",
					"/Library",
					"/opt/homebrew",
					"/usr/local",
					"/private/var/select",
					"/var/select",
					"/var/db/xcode_select_link",
					"/private/var/db/xcode_select_link",
					"/etc/codex",
					"/private/etc/codex",
				},
				AllowWrite: []string{
					"workspace",
					"task_home",
					"state_dir",
					"tmp",
				},
				DenyRead: []string{
					"~/.ssh",
					"~/.aws",
					"~/.config",
					"~/.gitconfig",
					"~/Desktop",
					"~/Documents",
				},
				AllowTools: []string{
					"git",
					"ssh",
					"make",
					"cmake",
					"clang",
					"clang++",
					"gcc",
					"g++",
					"python",
					"python3",
					"pip",
					"pip3",
					"uv",
					"poetry",
					"node",
					"npm",
					"npx",
					"pnpm",
					"yarn",
					"bun",
					"deno",
					"go",
					"rustup",
					"cargo",
					"rustc",
					"java",
					"javac",
					"mvn",
					"gradle",
					"jq",
					"rg",
					"zsh",
					"codex",
					"claude",
					"omx",
					"agentctl",
				},
				Env: map[string]string{
					"GOPATH":           "${TASK_HOME}/go",
					"GOCACHE":          "${TASK_HOME}/.cache/go-build",
					"GOMODCACHE":       "${TASK_HOME}/go/pkg/mod",
					"npm_config_cache": "${TASK_HOME}/.npm",
					"PNPM_HOME":        "${TASK_HOME}/.pnpm",
					"CARGO_HOME":       "${TASK_HOME}/.cargo",
					"RUSTUP_HOME":      "${TASK_HOME}/.rustup",
				},
			},
		},
		Repos:   map[string]Repo{},
		Remotes: map[string]Remote{},
	}
}

func createDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	data := []byte(strings.Join([]string{
		"base_dir: ~/.agent-workspaces",
		"state_dir: ~/.local/state/agentctl",
		"gitlab:",
		"  host: gitlab.com",
		"repos: {}",
		"remotes: {}",
		"templates:",
		"  default: node",
		"sandbox:",
		"  macos:",
		"    mode: strict",
		"    network: true",
		"    allow_read:",
		"      - /bin",
		"      - /sbin",
		"      - /usr",
		"      - /System",
		"      - /Library",
		"      - /opt/homebrew",
		"      - /usr/local",
		"      - /private/var/select",
		"      - /var/select",
		"      - /var/db/xcode_select_link",
		"      - /private/var/db/xcode_select_link",
		"      - /etc/codex",
		"      - /private/etc/codex",
		"    allow_write:",
		"      - workspace",
		"      - task_home",
		"      - state_dir",
		"      - tmp",
		"    deny_read:",
		"      - ~/.ssh",
		"      - ~/.aws",
		"      - ~/.config",
		"      - ~/.gitconfig",
		"      - ~/Desktop",
		"      - ~/Documents",
		"    allow_tools:",
		"      - git",
		"      - ssh",
		"      - make",
		"      - cmake",
		"      - clang",
		"      - clang++",
		"      - gcc",
		"      - g++",
		"      - python",
		"      - python3",
		"      - pip",
		"      - pip3",
		"      - uv",
		"      - poetry",
		"      - node",
		"      - npm",
		"      - npx",
		"      - pnpm",
		"      - yarn",
		"      - bun",
		"      - deno",
		"      - go",
		"      - rustup",
		"      - cargo",
		"      - rustc",
		"      - java",
		"      - javac",
		"      - mvn",
		"      - gradle",
		"      - jq",
		"      - rg",
		"      - zsh",
		"      - codex",
		"      - claude",
		"      - omx",
		"      - agentctl",
		"    env:",
		"      GOPATH: ${TASK_HOME}/go",
		"      GOCACHE: ${TASK_HOME}/.cache/go-build",
		"      GOMODCACHE: ${TASK_HOME}/go/pkg/mod",
		"      npm_config_cache: ${TASK_HOME}/.npm",
		"      PNPM_HOME: ${TASK_HOME}/.pnpm",
		"      CARGO_HOME: ${TASK_HOME}/.cargo",
		"      RUSTUP_HOME: ${TASK_HOME}/.rustup",
		"    custom_rules:",
		"      allow_read: []",
		"      allow_write: []",
		"",
	}, "\n"))

	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
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

	cfg.Sandbox.MacOS.AllowRead, err = expandPathList(cfg.Sandbox.MacOS.AllowRead)
	if err != nil {
		return err
	}
	cfg.Sandbox.MacOS.AllowWrite, err = expandPathList(cfg.Sandbox.MacOS.AllowWrite)
	if err != nil {
		return err
	}
	cfg.Sandbox.MacOS.DenyRead, err = expandPathList(cfg.Sandbox.MacOS.DenyRead)
	if err != nil {
		return err
	}
	cfg.Sandbox.MacOS.CustomRules.AllowRead, err = expandPathList(cfg.Sandbox.MacOS.CustomRules.AllowRead)
	if err != nil {
		return err
	}
	cfg.Sandbox.MacOS.CustomRules.AllowWrite, err = expandPathList(cfg.Sandbox.MacOS.CustomRules.AllowWrite)
	if err != nil {
		return err
	}
	if cfg.Sandbox.MacOS.Env == nil {
		cfg.Sandbox.MacOS.Env = map[string]string{}
	}
	for key, value := range cfg.Sandbox.MacOS.Env {
		expanded, err := expandTilde(value)
		if err != nil {
			return err
		}
		cfg.Sandbox.MacOS.Env[key] = expanded
	}

	return nil
}

func expandPathList(paths []string) ([]string, error) {
	expanded := make([]string, 0, len(paths))
	for _, path := range paths {
		value, err := expandTilde(path)
		if err != nil {
			return nil, err
		}
		expanded = append(expanded, value)
	}
	return expanded, nil
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
