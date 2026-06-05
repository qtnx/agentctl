package runtime

import (
	"reflect"
	"strings"
	"testing"
)

func TestDockerInvocationBuildsUntrustedRunCommandWithoutLeakingToken(t *testing.T) {
	invocation, err := DockerInvocationFor(validDockerOptions())
	if err != nil {
		t.Fatal(err)
	}
	args := invocation.Command

	if len(args) < 2 || args[0] != "docker" || args[1] != "run" {
		t.Fatalf("command starts with %#v, want docker run", args[:min(len(args), 2)])
	}

	for _, want := range []string{
		"--rm",
		"-i",
		"-t",
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
		{"-e", "GITLAB_TOKEN"},
		{"-v", "/tmp/worktree:/workspace:rw"},
		{"-v", "/tmp/repo/.git:/tmp/repo/.git:rw"},
		{"-w", "/workspace"},
	} {
		if !containsSequence(args, want) {
			t.Fatalf("command = %#v, want sequence %#v", args, want)
		}
	}

	if containsArg(args, "GITLAB_TOKEN=glpat-secret") {
		t.Fatalf("command = %#v, must pass GITLAB_TOKEN by name only", args)
	}
	if countContaining(args, "glpat-secret") != 0 {
		t.Fatalf("command = %#v, must not include literal token", args)
	}
	if !containsArg(invocation.Env, "GITLAB_TOKEN=glpat-secret") {
		t.Fatalf("env = %#v, want sidecar token env", invocation.Env)
	}

	imageIndex := indexOf(args, "node:22-bookworm")
	if imageIndex == -1 {
		t.Fatalf("command = %#v, want default node image", args)
	}
	entrypointIndex := indexOf(args, "--entrypoint")
	if entrypointIndex == -1 || entrypointIndex+1 >= len(args) || args[entrypointIndex+1] != "bash" {
		t.Fatalf("command = %#v, want --entrypoint bash", args)
	}
	if entrypointIndex > imageIndex {
		t.Fatalf("command = %#v, want --entrypoint bash before image", args)
	}
	commandSuffix := args[imageIndex+1:]
	if len(commandSuffix) < 5 || commandSuffix[0] != "bash" || commandSuffix[1] != "-lc" {
		t.Fatalf("command suffix = %#v, want bash -lc setup -- command", commandSuffix)
	}
	if commandSuffix[3] != "--" || commandSuffix[4] != "bash" {
		t.Fatalf("command suffix = %#v, want default command after --", commandSuffix)
	}

	script := commandSuffix[2]
	for _, want := range []string{
		"GIT_CONFIG_COUNT",
		`url.https://${GITLAB_HOST}/.insteadOf`,
		`git@${GITLAB_HOST}:`,
		"GIT_ASKPASS",
		"GIT_TERMINAL_PROMPT",
		`exec "$@"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script = %q, want %q", script, want)
		}
	}
	for _, banned := range []string{
		"git remote set-url",
		`oauth2:${GITLAB_TOKEN}@`,
		"glpat-secret",
	} {
		if strings.Contains(script, banned) {
			t.Fatalf("script = %q, must not contain %q", script, banned)
		}
	}
}

func TestDockerInvocationRejectsMissingRequiredOptions(t *testing.T) {
	tests := []struct {
		name   string
		update func(*DockerOptions)
		want   string
	}{
		{
			name: "task id",
			update: func(opts *DockerOptions) {
				opts.TaskID = " "
			},
			want: "TaskID",
		},
		{
			name: "worktree",
			update: func(opts *DockerOptions) {
				opts.Worktree = ""
			},
			want: "Worktree",
		},
		{
			name: "gitlab token",
			update: func(opts *DockerOptions) {
				opts.GitLabToken = "\t"
			},
			want: "GitLabToken",
		},
		{
			name: "git dir",
			update: func(opts *DockerOptions) {
				opts.GitDir = ""
			},
			want: "GitDir",
		},
		{
			name: "gitlab host",
			update: func(opts *DockerOptions) {
				opts.GitLabHost = " "
			},
			want: "GitLabHost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := validDockerOptions()
			tt.update(&opts)

			_, err := DockerInvocationFor(opts)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want field %q", err, tt.want)
			}
		})
	}
}

func TestDockerInvocationValidatesTaskIDForContainerName(t *testing.T) {
	for _, taskID := range []string{"XL-123", "abc_123", "abc.123"} {
		t.Run("valid "+taskID, func(t *testing.T) {
			opts := validDockerOptions()
			opts.TaskID = taskID

			invocation, err := DockerInvocationFor(opts)
			if err != nil {
				t.Fatal(err)
			}
			args := invocation.Command
			if !containsSequence(args, []string{"--name", "agent-" + taskID}) {
				t.Fatalf("command = %#v, want container name for %q", args, taskID)
			}
		})
	}

	for _, taskID := range []string{"bad id", "bad/id", "bad:id", ""} {
		t.Run("invalid "+taskID, func(t *testing.T) {
			opts := validDockerOptions()
			opts.TaskID = taskID

			_, err := DockerInvocationFor(opts)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "TaskID") {
				t.Fatalf("error = %v, want TaskID validation", err)
			}
		})
	}
}

func TestDockerInvocationUsesGitLabHostWithoutRemotePath(t *testing.T) {
	invocation, err := DockerInvocationFor(validDockerOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !containsSequence(invocation.Command, []string{"-e", "GITLAB_HOST"}) {
		t.Fatalf("command = %#v, want GITLAB_HOST env flag", invocation.Command)
	}
	if !containsArg(invocation.Env, "GITLAB_HOST=gitlab.example.com") {
		t.Fatalf("env = %#v, want GITLAB_HOST sidecar env", invocation.Env)
	}
	if countContaining(invocation.Command, "GITLAB_REMOTE_PATH") != 0 {
		t.Fatalf("command = %#v, must not depend on GITLAB_REMOTE_PATH", invocation.Command)
	}
	if countContaining(invocation.Env, "GITLAB_REMOTE_PATH") != 0 {
		t.Fatalf("env = %#v, must not include GITLAB_REMOTE_PATH", invocation.Env)
	}

	script := setupScript(t, invocation.Command, "node:22-bookworm")
	for _, want := range []string{
		`if [ -n "${GITLAB_HOST:-}" ]; then`,
		"GIT_CONFIG_COUNT",
		`url.https://${GITLAB_HOST}/.insteadOf`,
		"GIT_ASKPASS",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script = %q, want %q", script, want)
		}
	}
	if strings.Contains(script, "GITLAB_REMOTE_PATH") {
		t.Fatalf("script = %q, must not depend on GITLAB_REMOTE_PATH", script)
	}
}

func TestDockerInvocationRejectsLeadingDashImage(t *testing.T) {
	for _, image := range []string{"--privileged", "-bad"} {
		t.Run(image, func(t *testing.T) {
			opts := validDockerOptions()
			opts.Image = image

			_, err := DockerInvocationFor(opts)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "Image") {
				t.Fatalf("error = %v, want Image validation", err)
			}
		})
	}
}

func TestDockerInvocationOrdersAllowedEnvVarsDeterministically(t *testing.T) {
	opts := validDockerOptions()
	opts.Env = map[string]string{
		"TERM":     "xterm-256color",
		"CI":       "true",
		"NO_COLOR": "1",
	}

	invocation, err := DockerInvocationFor(opts)
	if err != nil {
		t.Fatal(err)
	}

	var gotEnv []string
	for _, value := range invocation.Env {
		switch {
		case strings.HasPrefix(value, "CI="):
			gotEnv = append(gotEnv, value)
		case strings.HasPrefix(value, "NO_COLOR="):
			gotEnv = append(gotEnv, value)
		case strings.HasPrefix(value, "TERM="):
			gotEnv = append(gotEnv, value)
		}
	}

	wantEnv := []string{"CI=true", "NO_COLOR=1", "TERM=xterm-256color"}
	if !reflect.DeepEqual(gotEnv, wantEnv) {
		t.Fatalf("extra env order = %#v, want %#v", gotEnv, wantEnv)
	}

	var gotFlags []string
	for _, value := range envValues(invocation.Command) {
		switch value {
		case "CI", "NO_COLOR", "TERM":
			gotFlags = append(gotFlags, value)
		}
	}

	wantFlags := []string{"CI", "NO_COLOR", "TERM"}
	if !reflect.DeepEqual(gotFlags, wantFlags) {
		t.Fatalf("extra env flag order = %#v, want %#v", gotFlags, wantFlags)
	}
}

func TestDockerInvocationRejectsDisallowedEnvVars(t *testing.T) {
	opts := validDockerOptions()
	opts.Env = map[string]string{
		"AWS_SECRET_ACCESS_KEY": "secret",
	}

	_, err := DockerInvocationFor(opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "AWS_SECRET_ACCESS_KEY") {
		t.Fatalf("error = %v, want disallowed env key", err)
	}
}

func TestDockerInvocationRunsSetupBeforeCustomCommand(t *testing.T) {
	opts := validDockerOptions()
	opts.Image = "golang:1.22-bookworm"
	opts.Command = []string{"go", "test", "./..."}

	invocation, err := DockerInvocationFor(opts)
	if err != nil {
		t.Fatal(err)
	}
	args := invocation.Command

	imageIndex := indexOf(args, "golang:1.22-bookworm")
	if imageIndex == -1 {
		t.Fatalf("command = %#v, want custom image", args)
	}

	got := args[imageIndex+1:]
	if len(got) < 7 || got[0] != "bash" || got[1] != "-lc" {
		t.Fatalf("command suffix = %#v, want bash -lc setup -- custom command", got)
	}
	if strings.Contains(got[2], "git remote set-url") {
		t.Fatalf("setup script = %q, must not mutate git remote", got[2])
	}
	if !strings.Contains(got[2], "GIT_CONFIG_COUNT") || !strings.Contains(got[2], `exec "$@"`) {
		t.Fatalf("setup script = %q, want process git config and exec argv", got[2])
	}
	if got[3] != "--" {
		t.Fatalf("command suffix = %#v, want -- before custom command", got)
	}

	want := []string{"go", "test", "./..."}
	if !reflect.DeepEqual(got[4:], want) {
		t.Fatalf("custom command = %#v, want %#v", got[4:], want)
	}
}

func validDockerOptions() DockerOptions {
	return DockerOptions{
		TaskID:      "XL-123",
		Worktree:    "/tmp/worktree",
		GitDir:      "/tmp/repo/.git",
		GitLabToken: "glpat-secret",
		GitLabHost:  "gitlab.example.com",
	}
}

func setupScript(t *testing.T, args []string, image string) string {
	t.Helper()

	imageIndex := indexOf(args, image)
	if imageIndex == -1 {
		t.Fatalf("command = %#v, want image %q", args, image)
	}
	commandSuffix := args[imageIndex+1:]
	if len(commandSuffix) < 3 {
		t.Fatalf("command suffix = %#v, want setup script", commandSuffix)
	}
	return commandSuffix[2]
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
