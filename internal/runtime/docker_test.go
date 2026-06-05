package runtime

import (
	"reflect"
	"strings"
	"testing"
)

func TestDockerCommandBuildsUntrustedRunCommand(t *testing.T) {
	args, err := DockerCommand(DockerOptions{
		TaskID:      "XL-123",
		Worktree:    "/tmp/worktree",
		GitLabToken: "glpat-secret",
		GitLabHost:  "gitlab.example.com",
		RemotePath:  "team/project",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(args) < 2 || args[0] != "docker" || args[1] != "run" {
		t.Fatalf("command starts with %#v, want docker run", args[:min(len(args), 2)])
	}

	for _, want := range []string{
		"--rm",
		"--cap-drop=ALL",
		"--pids-limit=1024",
		"--memory=8g",
		"--cpus=4",
	} {
		if !containsArg(args, want) {
			t.Fatalf("command = %#v, want arg %q", args, want)
		}
	}

	for _, want := range [][]string{
		{"--name", "agent-XL-123"},
		{"--security-opt", "no-new-privileges"},
		{"-e", "GITLAB_TOKEN=glpat-secret"},
		{"-v", "/tmp/worktree:/workspace:rw"},
		{"-w", "/workspace"},
	} {
		if !containsSequence(args, want) {
			t.Fatalf("command = %#v, want sequence %#v", args, want)
		}
	}

	imageIndex := indexOf(args, "node:22-bookworm")
	if imageIndex == -1 {
		t.Fatalf("command = %#v, want default node image", args)
	}
	commandSuffix := args[imageIndex+1:]
	if len(commandSuffix) != 3 || commandSuffix[0] != "bash" || commandSuffix[1] != "-lc" {
		t.Fatalf("command suffix = %#v, want bash -lc script", commandSuffix)
	}

	script := commandSuffix[2]
	for _, want := range []string{
		"git remote set-url origin",
		"GITLAB_HOST",
		"GITLAB_REMOTE_PATH",
		"GITLAB_TOKEN",
		"exec bash",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script = %q, want %q", script, want)
		}
	}
	if strings.Contains(script, "glpat-secret") {
		t.Fatalf("script leaked token: %q", script)
	}
	if countContaining(args, "glpat-secret") != 1 {
		t.Fatalf("command = %#v, want token only in GITLAB_TOKEN env", args)
	}
}

func TestDockerCommandRejectsMissingRequiredOptions(t *testing.T) {
	tests := []struct {
		name string
		opts DockerOptions
		want string
	}{
		{
			name: "task id",
			opts: DockerOptions{
				TaskID:      " ",
				Worktree:    "/tmp/worktree",
				GitLabToken: "glpat-secret",
			},
			want: "TaskID",
		},
		{
			name: "worktree",
			opts: DockerOptions{
				TaskID:      "XL-123",
				Worktree:    "",
				GitLabToken: "glpat-secret",
			},
			want: "Worktree",
		},
		{
			name: "gitlab token",
			opts: DockerOptions{
				TaskID:      "XL-123",
				Worktree:    "/tmp/worktree",
				GitLabToken: "\t",
			},
			want: "GitLabToken",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DockerCommand(tt.opts)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want field %q", err, tt.want)
			}
		})
	}
}

func TestDockerCommandOrdersExtraEnvVarsDeterministically(t *testing.T) {
	args, err := DockerCommand(DockerOptions{
		TaskID:      "XL-123",
		Worktree:    "/tmp/worktree",
		GitLabToken: "glpat-secret",
		Env: map[string]string{
			"ZZZ": "last",
			"AAA": "first",
			"MMM": "middle",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, value := range envValues(args) {
		switch {
		case strings.HasPrefix(value, "AAA="):
			got = append(got, value)
		case strings.HasPrefix(value, "MMM="):
			got = append(got, value)
		case strings.HasPrefix(value, "ZZZ="):
			got = append(got, value)
		}
	}

	want := []string{"AAA=first", "MMM=middle", "ZZZ=last"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extra env order = %#v, want %#v", got, want)
	}
}

func TestDockerCommandAppendsCustomCommandAsArgv(t *testing.T) {
	args, err := DockerCommand(DockerOptions{
		TaskID:      "XL-123",
		Worktree:    "/tmp/worktree",
		GitLabToken: "glpat-secret",
		Image:       "golang:1.22-bookworm",
		Command:     []string{"go", "test", "./..."},
	})
	if err != nil {
		t.Fatal(err)
	}

	imageIndex := indexOf(args, "golang:1.22-bookworm")
	if imageIndex == -1 {
		t.Fatalf("command = %#v, want custom image", args)
	}

	got := args[imageIndex+1:]
	want := []string{"go", "test", "./..."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command suffix = %#v, want %#v", got, want)
	}
}

func containsArg(args []string, want string) bool {
	return indexOf(args, want) != -1
}

func containsSequence(args, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for i := 0; i <= len(args)-len(want); i++ {
		if reflect.DeepEqual(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func indexOf(args []string, want string) int {
	for i, arg := range args {
		if arg == want {
			return i
		}
	}
	return -1
}

func envValues(args []string) []string {
	var values []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-e" {
			values = append(values, args[i+1])
			i++
		}
	}
	return values
}

func countContaining(args []string, value string) int {
	count := 0
	for _, arg := range args {
		if strings.Contains(arg, value) {
			count++
		}
	}
	return count
}
