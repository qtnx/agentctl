package remote

import (
	"context"
	"reflect"
	"testing"
)

func TestRemoteRunBuildsSSHArgvWithUser(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host:         "buildbox-1.example.com",
		User:         "deploy",
		AgentctlPath: "/usr/local/bin/agentctl",
	}, false, "run", "XL-123", "--repo", "backend")
	if err != nil {
		t.Fatal(err)
	}

	assertSSHRun(t, exec, []string{
		"deploy@buildbox-1.example.com",
		"/usr/local/bin/agentctl",
		"run",
		"XL-123",
		"--repo",
		"backend",
	})
}

func TestRemoteRunBuildsSSHArgvWithoutUser(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host:         "buildbox-1.example.com",
		AgentctlPath: "/usr/local/bin/agentctl",
	}, false, "attach", "XL-123")
	if err != nil {
		t.Fatal(err)
	}

	assertSSHRun(t, exec, []string{
		"buildbox-1.example.com",
		"/usr/local/bin/agentctl",
		"attach",
		"XL-123",
	})
}

func TestRemoteRunDefaultsAgentctlPath(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}, false, "detach", "XL-123")
	if err != nil {
		t.Fatal(err)
	}

	assertSSHRun(t, exec, []string{
		"deploy@buildbox-1.example.com",
		"agentctl",
		"detach",
		"XL-123",
	})
}

func TestRemoteRunAddsTTYForInteractiveCommand(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host:         "buildbox-1.example.com",
		User:         "deploy",
		AgentctlPath: "/usr/local/bin/agentctl",
	}, true, "shell", "XL-123", "--config", "/tmp/config.yaml")
	if err != nil {
		t.Fatal(err)
	}

	assertSSHRun(t, exec, []string{
		"-t",
		"deploy@buildbox-1.example.com",
		"/usr/local/bin/agentctl",
		"shell",
		"XL-123",
		"--config",
		"/tmp/config.yaml",
	})
}

func TestRemoteRunPreservesArgvOrder(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host:         "buildbox-1.example.com",
		User:         "deploy",
		AgentctlPath: "/usr/local/bin/agentctl",
	}, false,
		"run",
		"XL-123",
		"--repo",
		"backend",
		"--agent",
		"codex",
		"--risk",
		"untrusted",
		"--template",
		"golang",
		"--config",
		"/tmp/config.yaml",
	)
	if err != nil {
		t.Fatal(err)
	}

	assertSSHRun(t, exec, []string{
		"deploy@buildbox-1.example.com",
		"/usr/local/bin/agentctl",
		"run",
		"XL-123",
		"--repo",
		"backend",
		"--agent",
		"codex",
		"--risk",
		"untrusted",
		"--template",
		"golang",
		"--config",
		"/tmp/config.yaml",
	})
}

func assertSSHRun(t *testing.T, exec *fakeExecutor, wantArgs []string) {
	t.Helper()

	if exec.name != "ssh" {
		t.Fatalf("executor name = %q, want ssh", exec.name)
	}
	if !reflect.DeepEqual(exec.args, wantArgs) {
		t.Fatalf("ssh args = %#v, want %#v", exec.args, wantArgs)
	}
}

type fakeExecutor struct {
	name string
	args []string
}

func (f *fakeExecutor) Run(_ context.Context, name string, args ...string) error {
	f.name = name
	f.args = append([]string(nil), args...)
	return nil
}
