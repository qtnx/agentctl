package remote

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/qtnx/agentctl/internal/runtime"
)

func TestRemoteEnsureDirBuildsSSHMkdir(t *testing.T) {
	exec := &recordingExecutor{}
	service := NewService(exec)

	err := service.EnsureDir(context.Background(), Target{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}, "$HOME/.agent-workspaces/dev/XL-123")
	if err != nil {
		t.Fatal(err)
	}

	assertRecordedCalls(t, exec, []recordedCall{
		{
			name: "ssh",
			args: []string{
				"deploy@buildbox-1.example.com",
				`mkdir -p "$HOME/.agent-workspaces/dev/XL-123"`,
			},
		},
	})
}

func TestRemoteEnsureWritableBuildsSSHChmod(t *testing.T) {
	exec := &recordingExecutor{}
	service := NewService(exec)

	err := service.EnsureWritable(context.Background(), Target{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}, "$HOME/.agent-workspaces/dev/XL-123")
	if err != nil {
		t.Fatal(err)
	}

	assertRecordedCalls(t, exec, []recordedCall{
		{
			name: "ssh",
			args: []string{
				"deploy@buildbox-1.example.com",
				`find "$HOME/.agent-workspaces/dev/XL-123" \( -name 'node_modules' -o -name '.git' -o -name '.worktrees' -o -name '.agentctl' \) -prune -o -user "$(id -u)" -exec chmod a+rwX {} +`,
			},
		},
	})
}

func TestRemoteRemoveDockerContainerBuildsSSHDockerRm(t *testing.T) {
	exec := &recordingExecutor{}
	service := NewService(exec)

	err := service.RemoveDockerContainer(context.Background(), Target{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}, "XL-123")
	if err != nil {
		t.Fatal(err)
	}

	assertRecordedCalls(t, exec, []recordedCall{
		{
			name: "ssh",
			args: []string{
				"deploy@buildbox-1.example.com",
				`docker rm -f 'agent-XL-123' >/dev/null 2>&1 || true`,
			},
		},
	})
}

func TestRemoteFindAvailablePortBuildsSSHProbe(t *testing.T) {
	exec := &recordingExecutor{
		output: []byte("5174\n"),
	}
	service := NewService(exec)

	port, err := service.FindAvailablePort(context.Background(), Target{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}, "5173")
	if err != nil {
		t.Fatal(err)
	}

	if port != "5174" {
		t.Fatalf("port = %q, want 5174", port)
	}
	if len(exec.calls) != 1 {
		t.Fatalf("calls = %#v, want one ssh probe", exec.calls)
	}
	call := exec.calls[0]
	if call.name != "ssh" || len(call.args) != 2 || call.args[0] != "deploy@buildbox-1.example.com" {
		t.Fatalf("call = %#v, want ssh target command", call)
	}
	for _, want := range []string{"p=5173", "ss -ltn", "netstat -ltn", "echo \"$p\""} {
		if !strings.Contains(call.args[1], want) {
			t.Fatalf("probe command = %q, want %q", call.args[1], want)
		}
	}
}

func TestRemoteSyncBuildsRsyncArgs(t *testing.T) {
	exec := &recordingExecutor{}
	service := NewService(exec)

	err := service.Sync(context.Background(), SyncOptions{
		Target: Target{
			Host: "buildbox-1.example.com",
			User: "deploy",
		},
		LocalDir:  "/repo/current",
		RemoteDir: "$HOME/.agent-workspaces/dev/XL-123",
		Excludes:  []string{".git", "node_modules"},
	})
	if err != nil {
		t.Fatal(err)
	}

	assertRecordedCalls(t, exec, []recordedCall{
		{
			name: "rsync",
			args: []string{
				"-az",
				"--delete",
				"--exclude", ".git",
				"--exclude", "node_modules",
				"/repo/current/",
				"deploy@buildbox-1.example.com:$HOME/.agent-workspaces/dev/XL-123/",
			},
		},
	})
}

func TestRemoteSyncWithProgressBuildsRsyncProgressArgs(t *testing.T) {
	exec := &recordingExecutor{}
	service := NewService(exec)

	err := service.Sync(context.Background(), SyncOptions{
		Target: Target{
			Host: "buildbox-1.example.com",
			User: "deploy",
		},
		LocalDir:     "/repo/current",
		RemoteDir:    "$HOME/.agent-workspaces/dev/XL-123",
		Excludes:     []string{".git", "node_modules"},
		ShowProgress: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertRecordedCalls(t, exec, []recordedCall{
		{
			name: "rsync",
			args: []string{
				"-az",
				"--delete",
				"--progress",
				"--stats",
				"--exclude", ".git",
				"--exclude", "node_modules",
				"/repo/current/",
				"deploy@buildbox-1.example.com:$HOME/.agent-workspaces/dev/XL-123/",
			},
		},
	})
}

func TestRemoteRunDockerStreamBuildsSSHWithPortForwardAndEnv(t *testing.T) {
	exec := &recordingExecutor{}
	service := NewService(exec)

	err := service.RunDockerStream(context.Background(), DockerStreamOptions{
		Target: Target{
			Host: "buildbox-1.example.com",
			User: "deploy",
		},
		Invocation: runtime.DockerInvocation{
			Command: []string{"docker", "run", "--rm", "-it", "--name", "agent-XL-123", "-v", "$HOME/.agent-workspaces/dev/XL-123:/workspace:rw", "node:22-bookworm"},
			Env:     []string{"NPM_CONFIG_IGNORE_SCRIPTS=true"},
		},
		Ports: []PortForward{
			{Local: "8080", Remote: "3000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	assertRecordedCalls(t, exec, []recordedCall{
		{
			name: "ssh",
			args: []string{
				"-t",
				"-L", "8080:127.0.0.1:3000",
				"deploy@buildbox-1.example.com",
				`NPM_CONFIG_IGNORE_SCRIPTS='true' exec 'docker' 'run' '--rm' '-it' '--name' 'agent-XL-123' '-v' "$HOME/.agent-workspaces/dev/XL-123:/workspace:rw" 'node:22-bookworm'`,
			},
		},
	})
}

func TestRemoteRunDockerStreamCanRunDockerAsRemoteUser(t *testing.T) {
	exec := &recordingExecutor{}
	service := NewService(exec)

	err := service.RunDockerStream(context.Background(), DockerStreamOptions{
		Target: Target{
			Host: "buildbox-1.example.com",
			User: "deploy",
		},
		Invocation: runtime.DockerInvocation{
			Command: []string{"docker", "run", "--rm", "-it", "--name", "agent-XL-123", "-v", "$HOME/.agent-workspaces/dev/XL-123:/workspace:rw", "node:22-bookworm"},
			Env:     []string{"NPM_CONFIG_IGNORE_SCRIPTS=true"},
		},
		RunAsRemoteUser: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertRecordedCalls(t, exec, []recordedCall{
		{
			name: "ssh",
			args: []string{
				"-t",
				"deploy@buildbox-1.example.com",
				`uid="$(id -u)"; gid="$(id -g)"; NPM_CONFIG_IGNORE_SCRIPTS='true' exec 'docker' 'run' '--user' "${uid}:${gid}" '--rm' '-it' '--name' 'agent-XL-123' '-v' "$HOME/.agent-workspaces/dev/XL-123:/workspace:rw" 'node:22-bookworm'`,
			},
		},
	})
}

type recordedCall struct {
	name string
	args []string
}

type recordingExecutor struct {
	calls  []recordedCall
	output []byte
}

func (r *recordingExecutor) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, recordedCall{
		name: name,
		args: append([]string(nil), args...),
	})
	return nil
}

func (r *recordingExecutor) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, recordedCall{
		name: name,
		args: append([]string(nil), args...),
	})
	return append([]byte(nil), r.output...), nil
}

func assertRecordedCalls(t *testing.T, exec *recordingExecutor, want []recordedCall) {
	t.Helper()

	if !reflect.DeepEqual(exec.calls, want) {
		t.Fatalf("calls = %#v, want %#v", exec.calls, want)
	}
}
