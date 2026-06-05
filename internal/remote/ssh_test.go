package remote

import (
	"context"
	"reflect"
	"strings"
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
		"'/usr/local/bin/agentctl' 'run' 'XL-123' '--repo' 'backend'",
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
		"'/usr/local/bin/agentctl' 'attach' 'XL-123'",
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
		"'agentctl' 'detach' 'XL-123'",
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
		"'/usr/local/bin/agentctl' 'shell' 'XL-123' '--config' '/tmp/config.yaml'",
	})
}

func TestRemoteRunQuotesRemoteCommandAndPreservesArgvOrder(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host:         "buildbox-1.example.com",
		User:         "deploy",
		AgentctlPath: "/Applications/Agentctl Bin/agentctl",
	}, false,
		"run",
		"XL 123",
		"--repo",
		"backend",
		"--agent",
		"codex",
		"--risk",
		"untrusted",
		"--template",
		"go'lang; rm -rf /",
		"--config",
		"/tmp/config.yaml",
	)
	if err != nil {
		t.Fatal(err)
	}

	assertSSHRun(t, exec, []string{
		"deploy@buildbox-1.example.com",
		"'/Applications/Agentctl Bin/agentctl' 'run' 'XL 123' '--repo' 'backend' '--agent' 'codex' '--risk' 'untrusted' '--template' 'go'\\''lang; rm -rf /' '--config' '/tmp/config.yaml'",
	})
}

func TestRemoteRunRejectsHostOptionInjectionWithoutExecutorCall(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host: "-oProxyCommand=touch /tmp/pwned",
		User: "deploy",
	}, false, "run", "XL-123")
	if err == nil {
		t.Fatal("error = nil, want invalid host")
	}
	if !strings.Contains(err.Error(), "remote host") {
		t.Fatalf("error = %v, want remote host validation message", err)
	}
	assertNoExecutorCall(t, exec)
}

func TestRemoteRunRejectsHostControlCharactersWithoutExecutorCall(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host: "buildbox-1.example.com\n",
		User: "deploy",
	}, false, "run", "XL-123")
	if err == nil {
		t.Fatal("error = nil, want invalid host")
	}
	if !strings.Contains(err.Error(), "remote host") {
		t.Fatalf("error = %v, want remote host validation message", err)
	}
	assertNoExecutorCall(t, exec)
}

func TestRemoteRunRejectsUserOptionInjectionWithoutExecutorCall(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host: "buildbox-1.example.com",
		User: "-lroot",
	}, false, "run", "XL-123")
	if err == nil {
		t.Fatal("error = nil, want invalid user")
	}
	if !strings.Contains(err.Error(), "remote user") {
		t.Fatalf("error = %v, want remote user validation message", err)
	}
	assertNoExecutorCall(t, exec)
}

func TestRemoteRunRejectsUserWhitespaceWithoutExecutorCall(t *testing.T) {
	exec := &fakeExecutor{}
	service := NewService(exec)

	err := service.Run(context.Background(), Target{
		Host: "buildbox-1.example.com",
		User: "deploy ",
	}, false, "run", "XL-123")
	if err == nil {
		t.Fatal("error = nil, want invalid user")
	}
	if !strings.Contains(err.Error(), "remote user") {
		t.Fatalf("error = %v, want remote user validation message", err)
	}
	assertNoExecutorCall(t, exec)
}

func TestRemoteRunRejectsInvalidAgentctlPathWithoutExecutorCall(t *testing.T) {
	for _, path := range []string{"-bad-agentctl", " \t", "agentctl\n"} {
		t.Run(path, func(t *testing.T) {
			exec := &fakeExecutor{}
			service := NewService(exec)

			err := service.Run(context.Background(), Target{
				Host:         "buildbox-1.example.com",
				User:         "deploy",
				AgentctlPath: path,
			}, false, "run", "XL-123")
			if err == nil {
				t.Fatal("error = nil, want invalid agentctl path")
			}
			if !strings.Contains(err.Error(), "agentctl path") {
				t.Fatalf("error = %v, want agentctl path validation message", err)
			}
			assertNoExecutorCall(t, exec)
		})
	}
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

func assertNoExecutorCall(t *testing.T, exec *fakeExecutor) {
	t.Helper()

	if exec.called {
		t.Fatalf("executor called with name=%q args=%#v, want no call", exec.name, exec.args)
	}
}

type fakeExecutor struct {
	called bool
	name   string
	args   []string
}

func (f *fakeExecutor) Run(_ context.Context, name string, args ...string) error {
	f.called = true
	f.name = name
	f.args = append([]string(nil), args...)
	return nil
}
