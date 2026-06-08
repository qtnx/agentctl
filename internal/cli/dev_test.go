package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qtnx/agentctl/internal/config"
	"github.com/qtnx/agentctl/internal/remote"
	"github.com/qtnx/agentctl/internal/runtime"
)

func TestDevLocalRunsCurrentDirectoryInDockerAndStreamsCommand(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	hostHome := createAgentAuthFixtures(t, tmp)
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.userHome = hostHome

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--", "pnpm", "dev", "--host", "0.0.0.0"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one call", fakes.dockerOptions)
	}
	opts := fakes.dockerOptions[0]
	if opts.TaskID != "XL-123" || opts.Worktree != "/repo/current" || opts.Image != "node:22-bookworm" || opts.Detached {
		t.Fatalf("docker opts = %#v, want task/current-dir/node/attached", opts)
	}
	if !reflect.DeepEqual(opts.Command, []string{"pnpm", "dev", "--host", "0.0.0.0"}) {
		t.Fatalf("docker command = %#v, want pnpm dev argv", opts.Command)
	}
	if !reflect.DeepEqual(opts.Mounts, wantAgentAuthMounts(hostHome)) {
		t.Fatalf("docker mounts = %#v, want agent auth mounts", opts.Mounts)
	}
	if len(fakes.localCommands) != 1 {
		t.Fatalf("local commands = %#v, want one streamed command", fakes.localCommands)
	}
	if !reflect.DeepEqual(fakes.localCommands[0], []string{"docker", "run", "--name", "agent-XL-123"}) {
		t.Fatalf("local command = %#v, want Docker invocation command", fakes.localCommands[0])
	}
}

func TestDevLocalParsesPortsForDockerPublish(t *testing.T) {
	tmp := t.TempDir()
	fakes := newDevFakes(testDevConfig(tmp))

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--port", "8080:3000", "--port", "5173", "XL-123", "--", "pnpm", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one call", fakes.dockerOptions)
	}
	want := []runtime.PortMapping{
		{Local: "8080", Container: "3000"},
		{Local: "5173", Container: "5173"},
	}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Ports, want) {
		t.Fatalf("ports = %#v, want %#v", fakes.dockerOptions[0].Ports, want)
	}
}

func TestDevLocalPromptsBeforeMacOSSandboxFallback(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	hostHome := createAgentAuthFixtures(t, tmp)
	cfg.Sandbox.MacOS = config.MacOSSandboxConfig{
		Mode:       "strict",
		Network:    false,
		AllowRead:  []string{"/usr/local"},
		AllowWrite: []string{"workspace", "task_home", "state_dir", "tmp"},
		DenyRead:   []string{"/Users/qqq/.config"},
		Env: map[string]string{
			"npm_config_cache": "${TASK_HOME}/.npm",
		},
		CustomRules: config.MacOSSandboxCustomRules{
			AllowRead: []string{"/company-node"},
		},
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.userHome = hostHome
	fakes.gitMetadataPaths = []string{"/repo/.git/worktrees/current", "/repo/.git"}
	fakes.dockerAvailable = false
	fakes.goosName = "darwin"
	fakes.sandboxExecAvailable = true
	fakes.confirmSandbox = true

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"XL-123", "--", "pnpm", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.confirmSandboxFallbackPrompts) != 1 {
		t.Fatalf("prompts = %#v, want one sandbox risk prompt", fakes.confirmSandboxFallbackPrompts)
	}
	if !strings.Contains(fakes.confirmSandboxFallbackPrompts[0], "sandbox-exec") ||
		!strings.Contains(fakes.confirmSandboxFallbackPrompts[0], "weaker than Docker") {
		t.Fatalf("prompt = %q, want sandbox risk warning", fakes.confirmSandboxFallbackPrompts[0])
	}
	if len(fakes.sandboxOptions) != 1 {
		t.Fatalf("sandbox options = %#v, want one call", fakes.sandboxOptions)
	}
	opts := fakes.sandboxOptions[0]
	if opts.Worktree != "/repo/current" || opts.HomeDir != filepath.Join(cfg.StateDir, "homes", "XL-123") || !opts.ExitAfterCommand {
		t.Fatalf("sandbox opts = %#v, want current dir, task home, and exit-after-command", opts)
	}
	wantHome := filepath.Join(cfg.StateDir, "homes", "XL-123")
	if opts.Mode != "strict" || opts.Network {
		t.Fatalf("sandbox opts = %#v, want strict with network disabled", opts)
	}
	for _, want := range []string{"/usr/local", "/company-node", "/private/var/select", "/var/select", "/var/db/xcode_select_link", "/private/var/db/xcode_select_link", "/etc/codex", "/private/etc/codex", "/repo/.git/worktrees/current", "/repo/.git", filepath.Join(hostHome, ".codex"), filepath.Join(hostHome, ".claude"), filepath.Join(hostHome, ".claude.json")} {
		if !containsString(opts.AllowRead, want) {
			t.Fatalf("sandbox allow read = %#v, want %q", opts.AllowRead, want)
		}
	}
	if !reflect.DeepEqual(opts.AllowWrite, []string{"/repo/current", wantHome, cfg.StateDir, "/private/tmp", "/tmp", "/repo/.git/worktrees/current", "/repo/.git", filepath.Join(hostHome, ".codex"), filepath.Join(hostHome, ".claude"), filepath.Join(hostHome, ".claude.json")}) {
		t.Fatalf("sandbox allow write = %#v, want resolved write paths", opts.AllowWrite)
	}
	for _, name := range []string{".codex", ".claude", ".claude.json"} {
		link, err := os.Readlink(filepath.Join(wantHome, name))
		if err != nil {
			t.Fatalf("auth link %s: %v", name, err)
		}
		if want := filepath.Join(hostHome, name); link != want {
			t.Fatalf("auth link %s = %q, want %q", name, link, want)
		}
	}
	if !reflect.DeepEqual(opts.DenyRead, []string{"/Users/qqq/.config"}) {
		t.Fatalf("sandbox deny read = %#v, want configured deny paths", opts.DenyRead)
	}
	if opts.Env["npm_config_cache"] != "${TASK_HOME}/.npm" {
		t.Fatalf("sandbox env = %#v, want configured npm cache", opts.Env)
	}
	if !reflect.DeepEqual(opts.Command, []string{"pnpm", "dev"}) {
		t.Fatalf("sandbox command = %#v, want pnpm dev", opts.Command)
	}
	if len(fakes.localCommands) != 1 || !reflect.DeepEqual(fakes.localCommands[0], []string{"sandbox-exec", "-p", "profile", "--", "pnpm", "dev"}) {
		t.Fatalf("local commands = %#v, want streamed sandbox invocation", fakes.localCommands)
	}
}

func TestDevRemoteSyncsCurrentDirectoryAndRunsDockerOnDevHost(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "--port", "8080:3000", "XL-123", "--", "pnpm", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	target := remote.Target{Host: "buildbox-1.example.com", User: "deploy"}
	remoteDir := "$HOME/.agent-workspaces/dev/XL-123"
	if !reflect.DeepEqual(fakes.remoteEnsureDirCalls, []remoteEnsureDirCall{{target: target, remoteDir: remoteDir}}) {
		t.Fatalf("ensure dir calls = %#v, want target remote dir", fakes.remoteEnsureDirCalls)
	}
	if len(fakes.remoteSyncCalls) != 2 {
		t.Fatalf("sync calls = %#v, want initial sync and watch sync", fakes.remoteSyncCalls)
	}
	if fakes.remoteSyncCalls[0].opts.Target != target || fakes.remoteSyncCalls[0].opts.LocalDir != "/repo/current" || fakes.remoteSyncCalls[0].opts.RemoteDir != remoteDir {
		t.Fatalf("initial sync = %#v, want local current dir to remote dir", fakes.remoteSyncCalls[0])
	}
	if fakes.remoteSyncCalls[0].opts.ShowProgress {
		t.Fatalf("initial sync = %#v, want progress disabled by default", fakes.remoteSyncCalls[0])
	}
	if fakes.remoteSyncCalls[1].opts.ShowProgress {
		t.Fatalf("watch sync = %#v, want progress disabled", fakes.remoteSyncCalls[1])
	}
	if !reflect.DeepEqual(fakes.remoteEnsureWritableCalls, []remoteEnsureWritableCall{
		{target: target, remoteDir: remoteDir},
		{target: target, remoteDir: remoteDir},
	}) {
		t.Fatalf("ensure writable calls = %#v, want one after each sync", fakes.remoteEnsureWritableCalls)
	}
	if !reflect.DeepEqual(fakes.remoteRemoveContainerCalls, []remoteRemoveContainerCall{{target: target, taskID: "XL-123"}}) {
		t.Fatalf("remove container calls = %#v, want old container removed before start", fakes.remoteRemoveContainerCalls)
	}
	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one remote Docker invocation", fakes.dockerOptions)
	}
	opts := fakes.dockerOptions[0]
	if opts.Worktree != remoteDir || opts.Image != "node:22-bookworm" {
		t.Fatalf("docker opts = %#v, want remote workspace and node image", opts)
	}
	if !reflect.DeepEqual(opts.Ports, []runtime.PortMapping{{Local: "3000", Container: "3000"}}) {
		t.Fatalf("remote docker ports = %#v, want remote host to container publish", opts.Ports)
	}
	if len(fakes.remoteDockerStreamCalls) != 1 {
		t.Fatalf("remote docker stream calls = %#v, want one", fakes.remoteDockerStreamCalls)
	}
	stream := fakes.remoteDockerStreamCalls[0]
	if stream.opts.Target != target {
		t.Fatalf("stream target = %#v, want %#v", stream.opts.Target, target)
	}
	if !reflect.DeepEqual(stream.opts.Ports, []remote.PortForward{{Local: "8080", Remote: "3000"}}) {
		t.Fatalf("stream ports = %#v, want local to remote port forward", stream.opts.Ports)
	}
	if !stream.opts.RunAsRemoteUser {
		t.Fatalf("stream opts = %#v, want remote Docker to run as SSH user", stream.opts)
	}
	if !reflect.DeepEqual(fakes.watchRoots, []string{"/repo/current"}) {
		t.Fatalf("watch roots = %#v, want current dir watch", fakes.watchRoots)
	}
}

func TestDevRemoteDebugShowsInitialSyncProgress(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--debug", "--remote", "devbox", "XL-123", "--", "true"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.remoteSyncCalls) != 2 {
		t.Fatalf("sync calls = %#v, want initial sync and watch sync", fakes.remoteSyncCalls)
	}
	if !fakes.remoteSyncCalls[0].opts.ShowProgress {
		t.Fatalf("initial sync = %#v, want progress enabled in debug mode", fakes.remoteSyncCalls[0])
	}
	if fakes.remoteSyncCalls[1].opts.ShowProgress {
		t.Fatalf("watch sync = %#v, want progress disabled even in debug mode", fakes.remoteSyncCalls[1])
	}
}

func TestDevRemoteDebugEnvShowsInitialSyncProgress(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.env = map[string]string{"AGENTCTL_DEBUG": "1"}

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "XL-123", "--", "true"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.remoteSyncCalls) != 2 {
		t.Fatalf("sync calls = %#v, want initial sync and watch sync", fakes.remoteSyncCalls)
	}
	if !fakes.remoteSyncCalls[0].opts.ShowProgress {
		t.Fatalf("initial sync = %#v, want progress enabled with AGENTCTL_DEBUG=1", fakes.remoteSyncCalls[0])
	}
	if fakes.remoteSyncCalls[1].opts.ShowProgress {
		t.Fatalf("watch sync = %#v, want progress disabled even with AGENTCTL_DEBUG=1", fakes.remoteSyncCalls[1])
	}
}

func TestDevRemoteVitePortAddsHostFlagForForwarding(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.files = map[string]string{
		"/repo/current/package.json": `{
			"scripts": {"dev": "vite"},
			"devDependencies": {"vite": "^6.0.0"}
		}`,
	}

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "--port", "5173", "XL-123", "--", "npm", "run", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one remote Docker invocation", fakes.dockerOptions)
	}
	want := []string{"npm", "run", "dev", "--", "--host", "0.0.0.0"}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Command, want) {
		t.Fatalf("remote docker command = %#v, want %#v", fakes.dockerOptions[0].Command, want)
	}
}

func TestDevRemoteViteInfersDefaultPortForForwarding(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.files = map[string]string{
		"/repo/current/package.json": `{
			"scripts": {"dev": "vite"},
			"devDependencies": {"vite": "^6.0.0"}
		}`,
	}

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "XL-123", "--", "npm", "run", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one remote Docker invocation", fakes.dockerOptions)
	}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Ports, []runtime.PortMapping{{Local: "5173", Container: "5173"}}) {
		t.Fatalf("docker ports = %#v, want auto Vite port 5173", fakes.dockerOptions[0].Ports)
	}
	wantCommand := []string{"npm", "run", "dev", "--", "--port", "5173", "--host", "0.0.0.0"}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Command, wantCommand) {
		t.Fatalf("remote docker command = %#v, want %#v", fakes.dockerOptions[0].Command, wantCommand)
	}
	if len(fakes.remoteDockerStreamCalls) != 1 {
		t.Fatalf("remote docker stream calls = %#v, want one", fakes.remoteDockerStreamCalls)
	}
	if !reflect.DeepEqual(fakes.remoteDockerStreamCalls[0].opts.Ports, []remote.PortForward{{Local: "5173", Remote: "5173"}}) {
		t.Fatalf("stream ports = %#v, want auto SSH forward 5173", fakes.remoteDockerStreamCalls[0].opts.Ports)
	}
}

func TestDevRemoteViteChoosesNextLocalPortWhenDefaultBusy(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.unavailableLocalPorts = map[string]bool{"5173": true}
	fakes.files = map[string]string{
		"/repo/current/package.json": `{
			"scripts": {"dev": "vite"},
			"devDependencies": {"vite": "^6.0.0"}
		}`,
	}

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "XL-123", "--", "npm", "run", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one remote Docker invocation", fakes.dockerOptions)
	}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Ports, []runtime.PortMapping{{Local: "5174", Container: "5174"}}) {
		t.Fatalf("docker ports = %#v, want next free Vite port 5174", fakes.dockerOptions[0].Ports)
	}
	wantCommand := []string{"npm", "run", "dev", "--", "--port", "5174", "--host", "0.0.0.0"}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Command, wantCommand) {
		t.Fatalf("remote docker command = %#v, want %#v", fakes.dockerOptions[0].Command, wantCommand)
	}
	if !reflect.DeepEqual(fakes.remoteDockerStreamCalls[0].opts.Ports, []remote.PortForward{{Local: "5174", Remote: "5174"}}) {
		t.Fatalf("stream ports = %#v, want auto SSH forward 5174", fakes.remoteDockerStreamCalls[0].opts.Ports)
	}
}

func TestDevRemoteViteChoosesNextPortWhenRemoteDefaultBusy(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.unavailableRemotePorts = map[string]bool{"5173": true}
	fakes.files = map[string]string{
		"/repo/current/package.json": `{
			"scripts": {"dev": "vite"},
			"devDependencies": {"vite": "^6.0.0"}
		}`,
	}

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "XL-123", "--", "npm", "run", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.remoteFindAvailablePortCalls) != 1 {
		t.Fatalf("remote port calls = %#v, want one probe", fakes.remoteFindAvailablePortCalls)
	}
	if fakes.remoteFindAvailablePortCalls[0].start != "5173" {
		t.Fatalf("remote port start = %q, want 5173", fakes.remoteFindAvailablePortCalls[0].start)
	}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Ports, []runtime.PortMapping{{Local: "5174", Container: "5174"}}) {
		t.Fatalf("docker ports = %#v, want next free remote Vite port 5174", fakes.dockerOptions[0].Ports)
	}
	wantCommand := []string{"npm", "run", "dev", "--", "--port", "5174", "--host", "0.0.0.0"}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Command, wantCommand) {
		t.Fatalf("remote docker command = %#v, want %#v", fakes.dockerOptions[0].Command, wantCommand)
	}
}

func TestDevRemoteRemovesOldContainerBeforeAutoPortProbe(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.files = map[string]string{
		"/repo/current/package.json": `{
			"scripts": {"dev": "vite"},
			"devDependencies": {"vite": "^6.0.0"}
		}`,
	}

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "XL-123", "--", "npm", "run", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	removeIndex := indexOfString(fakes.events, "remove-container")
	probeIndex := indexOfString(fakes.events, "find-port")
	if removeIndex == -1 || probeIndex == -1 {
		t.Fatalf("events = %#v, want remove-container and find-port", fakes.events)
	}
	if removeIndex > probeIndex {
		t.Fatalf("events = %#v, want old container removed before remote port probe", fakes.events)
	}
}

func TestDevRemoteExplicitPortFailsBeforeDockerWhenRemotePortBusy(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.unavailableRemotePorts = map[string]bool{"3000": true}

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "--port", "3000", "XL-123", "--", "npm", "run", "dev"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected remote port availability error")
	}
	if !strings.Contains(err.Error(), `remote port 3000 is already in use`) {
		t.Fatalf("error = %v, want remote port busy message", err)
	}
	if len(fakes.remoteFindAvailablePortCalls) != 1 {
		t.Fatalf("remote port calls = %#v, want one probe", fakes.remoteFindAvailablePortCalls)
	}
	if len(fakes.remoteSyncCalls) != 0 || len(fakes.dockerOptions) != 0 || len(fakes.remoteDockerStreamCalls) != 0 {
		t.Fatalf("sync/docker calls = %#v/%#v/%#v, want fail before sync and docker", fakes.remoteSyncCalls, fakes.dockerOptions, fakes.remoteDockerStreamCalls)
	}
}

func TestDevRemoteViteInfersCommandPortForForwarding(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.files = map[string]string{
		"/repo/current/package.json": `{
			"scripts": {"dev": "vite"},
			"devDependencies": {"vite": "^6.0.0"}
		}`,
	}

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "XL-123", "--", "npm", "run", "dev", "--", "--port", "5180"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one remote Docker invocation", fakes.dockerOptions)
	}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Ports, []runtime.PortMapping{{Local: "5180", Container: "5180"}}) {
		t.Fatalf("docker ports = %#v, want inferred command port 5180", fakes.dockerOptions[0].Ports)
	}
	wantCommand := []string{"npm", "run", "dev", "--", "--port", "5180", "--host", "0.0.0.0"}
	if !reflect.DeepEqual(fakes.dockerOptions[0].Command, wantCommand) {
		t.Fatalf("remote docker command = %#v, want %#v", fakes.dockerOptions[0].Command, wantCommand)
	}
}

func TestDevRemoteInlineTargetPromptsAndSavesConfig(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.confirmSaveRemote = true

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "qtnx@codemc", "--config", filepath.Join(tmp, "config.yaml"), "XL-123", "--", "npm", "run", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	target := remote.Target{Host: "codemc", User: "qtnx"}
	if len(fakes.confirmSaveRemotePrompts) != 1 || !strings.Contains(fakes.confirmSaveRemotePrompts[0], `save it as "codemc"`) {
		t.Fatalf("prompts = %#v, want save config prompt", fakes.confirmSaveRemotePrompts)
	}
	if len(fakes.saveConfigCalls) != 1 {
		t.Fatalf("save config calls = %#v, want one", fakes.saveConfigCalls)
	}
	if fakes.saveConfigCalls[0].path != filepath.Join(tmp, "config.yaml") {
		t.Fatalf("save path = %q, want config path", fakes.saveConfigCalls[0].path)
	}
	if got := fakes.saveConfigCalls[0].cfg.Remotes["codemc"]; got.Host != "codemc" || got.User != "qtnx" {
		t.Fatalf("saved remote = %#v, want qtnx@codemc", got)
	}
	if len(fakes.remoteEnsureDirCalls) != 1 || fakes.remoteEnsureDirCalls[0].target != target {
		t.Fatalf("ensure dir calls = %#v, want inline remote target", fakes.remoteEnsureDirCalls)
	}
}

func TestDevRemoteWithoutTaskIDUsesStableWorkspaceTaskIDAndPreservesCommandAtDash(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	fakes := newDevFakes(cfg)
	fakes.cwd = "/Users/qqq/code/test-repo"
	fakes.confirmSaveRemote = false

	installCmd := newDevCommandWithDeps(fakes.deps())
	installCmd.SetOut(io.Discard)
	installCmd.SetErr(io.Discard)
	installCmd.SetArgs([]string{"--remote", "qtnx@codemc", "--", "npm", "install"})

	if err := installCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	runCmd := newDevCommandWithDeps(fakes.deps())
	runCmd.SetOut(io.Discard)
	runCmd.SetErr(io.Discard)
	runCmd.SetArgs([]string{"--remote", "qtnx@codemc", "--", "npm", "run", "dev"})

	if err := runCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.dockerOptions) != 2 {
		t.Fatalf("docker options = %#v, want install and run calls", fakes.dockerOptions)
	}
	installOpts := fakes.dockerOptions[0]
	runOpts := fakes.dockerOptions[1]
	if installOpts.TaskID != runOpts.TaskID {
		t.Fatalf("task ids = %q and %q, want same stable workspace task id", installOpts.TaskID, runOpts.TaskID)
	}
	if !strings.HasPrefix(runOpts.TaskID, "dev-test-repo-") {
		t.Fatalf("task id = %q, want dev-test-repo-*", runOpts.TaskID)
	}
	if !reflect.DeepEqual(installOpts.Command, []string{"npm", "install"}) {
		t.Fatalf("install command = %#v, want npm install", installOpts.Command)
	}
	if !reflect.DeepEqual(runOpts.Command, []string{"npm", "run", "dev"}) {
		t.Fatalf("remote docker command = %#v, want npm run dev", runOpts.Command)
	}
	if installOpts.Worktree != runOpts.Worktree || runOpts.Worktree != "$HOME/.agent-workspaces/dev/"+runOpts.TaskID {
		t.Fatalf("remote worktrees = %q and %q, want same stable task remote dir", installOpts.Worktree, runOpts.Worktree)
	}
}

func TestDevRemoteWithoutTaskIDWritesGeneratedTaskIDToAgentctlFile(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "--", "npm", "install"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one remote Docker invocation", fakes.dockerOptions)
	}
	taskID := fakes.dockerOptions[0].TaskID
	if !strings.HasPrefix(taskID, "dev-current-") {
		t.Fatalf("task id = %q, want dev-current-*", taskID)
	}
	wantPath := "/repo/current/.agentctl"
	if len(fakes.writeFileCalls) != 1 || fakes.writeFileCalls[0].path != wantPath {
		t.Fatalf("write file calls = %#v, want .agentctl write", fakes.writeFileCalls)
	}
	if !strings.Contains(string(fakes.writeFileCalls[0].data), `"dev_task_id": "`+taskID+`"`) {
		t.Fatalf(".agentctl data = %q, want persisted task id %q", string(fakes.writeFileCalls[0].data), taskID)
	}
	if fakes.writeFileCalls[0].perm != 0o644 {
		t.Fatalf(".agentctl perm = %#o, want 0644", fakes.writeFileCalls[0].perm)
	}
}

func TestDevRemoteWithoutTaskIDReadsPersistedTaskIDFromAgentctlFile(t *testing.T) {
	tmp := t.TempDir()
	cfg := testDevConfig(tmp)
	cfg.Remotes["devbox"] = config.Remote{
		Host: "buildbox-1.example.com",
		User: "deploy",
	}
	fakes := newDevFakes(cfg)
	fakes.cwd = "/repo/current"
	fakes.files = map[string]string{
		"/repo/current/.agentctl": `{"dev_task_id":"dev-saved-task"}`,
	}

	cmd := newDevCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote", "devbox", "--", "npm", "run", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if len(fakes.dockerOptions) != 1 {
		t.Fatalf("docker options = %#v, want one remote Docker invocation", fakes.dockerOptions)
	}
	if fakes.dockerOptions[0].TaskID != "dev-saved-task" {
		t.Fatalf("task id = %q, want persisted task id", fakes.dockerOptions[0].TaskID)
	}
	if len(fakes.writeFileCalls) != 0 {
		t.Fatalf("write file calls = %#v, want no rewrite when persisted id exists", fakes.writeFileCalls)
	}
}

func testDevConfig(tmp string) *config.Config {
	return &config.Config{
		BaseDir:  filepath.Join(tmp, "workspaces"),
		StateDir: filepath.Join(tmp, "state"),
		GitLab: config.GitLabConfig{
			Host: "gitlab.com",
		},
		Templates: config.TemplatesConfig{
			Default: "node",
		},
		Repos:   map[string]config.Repo{},
		Remotes: map[string]config.Remote{},
	}
}

type devFakes struct {
	cfg                           *config.Config
	cwd                           string
	userHome                      string
	env                           map[string]string
	files                         map[string]string
	writeFileCalls                []writeFileCall
	unavailableLocalPorts         map[string]bool
	unavailableRemotePorts        map[string]bool
	dockerAvailable               bool
	goosName                      string
	sandboxExecAvailable          bool
	confirmSandbox                bool
	confirmSandboxFallbackPrompts []string
	dockerOptions                 []runtime.DockerOptions
	sandboxOptions                []runtime.MacOSSandboxOptions
	gitMetadataPaths              []string
	confirmSaveRemote             bool
	confirmSaveRemotePrompts      []string
	saveConfigCalls               []saveConfigCall
	localCommands                 [][]string
	remoteEnsureDirCalls          []remoteEnsureDirCall
	remoteSyncCalls               []remoteSyncCall
	remoteDockerStreamCalls       []remoteDockerStreamCall
	remoteEnsureWritableCalls     []remoteEnsureWritableCall
	remoteRemoveContainerCalls    []remoteRemoveContainerCall
	remoteFindAvailablePortCalls  []remoteFindAvailablePortCall
	watchRoots                    []string
	events                        []string
}

func newDevFakes(cfg *config.Config) *devFakes {
	return &devFakes{
		cfg:             cfg,
		cwd:             "/repo",
		dockerAvailable: true,
		goosName:        "linux",
	}
}

func (f *devFakes) deps() devDeps {
	return devDeps{
		loadConfig: func(_ string) (*config.Config, error) {
			return f.cfg, nil
		},
		saveConfig: func(path string, cfg *config.Config) error {
			f.saveConfigCalls = append(f.saveConfigCalls, saveConfigCall{
				path: path,
				cfg:  cloneConfig(cfg),
			})
			return nil
		},
		confirmSaveRemote: func(prompt string) (bool, error) {
			f.confirmSaveRemotePrompts = append(f.confirmSaveRemotePrompts, prompt)
			return f.confirmSaveRemote, nil
		},
		getenv: func(key string) string {
			return f.env[key]
		},
		workingDir: func() (string, error) {
			return f.cwd, nil
		},
		localPortAvailable: func(port string) bool {
			return !f.unavailableLocalPorts[port]
		},
		readFile: func(path string) ([]byte, error) {
			if value, ok := f.files[path]; ok {
				return []byte(value), nil
			}
			return nil, os.ErrNotExist
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			f.writeFileCalls = append(f.writeFileCalls, writeFileCall{
				path: path,
				data: append([]byte(nil), data...),
				perm: perm,
			})
			if f.files == nil {
				f.files = map[string]string{}
			}
			f.files[path] = string(data)
			return nil
		},
		userHome: func() (string, error) {
			if f.userHome == "" {
				return os.UserHomeDir()
			}
			return f.userHome, nil
		},
		gitMetadataPaths: func(_ context.Context, _ string) []string {
			return append([]string(nil), f.gitMetadataPaths...)
		},
		dockerInvocationFor: func(opts runtime.DockerOptions) (runtime.DockerInvocation, error) {
			f.events = append(f.events, "docker-invocation")
			f.dockerOptions = append(f.dockerOptions, opts)
			return runtime.DockerInvocation{
				Command: []string{"docker", "run", "--name", "agent-" + opts.TaskID},
			}, nil
		},
		dockerAvailable: func() bool {
			return f.dockerAvailable
		},
		dockerReady: func(context.Context) error {
			return nil
		},
		goos: func() string {
			return f.goosName
		},
		sandboxExecAvailable: func() bool {
			return f.sandboxExecAvailable
		},
		confirmSandboxFallback: func(prompt string) (bool, error) {
			f.confirmSandboxFallbackPrompts = append(f.confirmSandboxFallbackPrompts, prompt)
			return f.confirmSandbox, nil
		},
		macOSSandboxInvocationFor: func(opts runtime.MacOSSandboxOptions) (runtime.DockerInvocation, error) {
			f.sandboxOptions = append(f.sandboxOptions, opts)
			return runtime.DockerInvocation{
				Command: []string{"sandbox-exec", "-p", "profile", "--", opts.Command[0], opts.Command[1]},
			}, nil
		},
		runCommand: func(_ context.Context, command []string) error {
			f.localCommands = append(f.localCommands, append([]string(nil), command...))
			return nil
		},
		remoteEnsureDir: func(_ context.Context, target remote.Target, remoteDir string) error {
			f.remoteEnsureDirCalls = append(f.remoteEnsureDirCalls, remoteEnsureDirCall{
				target:    target,
				remoteDir: remoteDir,
			})
			return nil
		},
		remoteSync: func(_ context.Context, opts remote.SyncOptions) error {
			f.events = append(f.events, "sync")
			f.remoteSyncCalls = append(f.remoteSyncCalls, remoteSyncCall{opts: opts})
			return nil
		},
		remoteEnsureWritable: func(_ context.Context, target remote.Target, remoteDir string) error {
			f.remoteEnsureWritableCalls = append(f.remoteEnsureWritableCalls, remoteEnsureWritableCall{
				target:    target,
				remoteDir: remoteDir,
			})
			return nil
		},
		remoteRemoveContainer: func(_ context.Context, target remote.Target, taskID string) error {
			f.events = append(f.events, "remove-container")
			f.remoteRemoveContainerCalls = append(f.remoteRemoveContainerCalls, remoteRemoveContainerCall{
				target: target,
				taskID: taskID,
			})
			return nil
		},
		remoteFindAvailablePort: func(_ context.Context, target remote.Target, start string) (string, error) {
			f.events = append(f.events, "find-port")
			f.remoteFindAvailablePortCalls = append(f.remoteFindAvailablePortCalls, remoteFindAvailablePortCall{
				target: target,
				start:  start,
			})
			port := start
			for f.unavailableRemotePorts[port] {
				next, ok := nextDevPort(port)
				if !ok {
					return start, nil
				}
				port = next
			}
			return port, nil
		},
		remoteRunDockerStream: func(_ context.Context, opts remote.DockerStreamOptions) error {
			f.events = append(f.events, "run-docker")
			f.remoteDockerStreamCalls = append(f.remoteDockerStreamCalls, remoteDockerStreamCall{opts: opts})
			return nil
		},
		watch: func(ctx context.Context, root string, onChange func(context.Context) error) error {
			f.watchRoots = append(f.watchRoots, root)
			if err := onChange(ctx); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		},
	}
}

type remoteEnsureDirCall struct {
	target    remote.Target
	remoteDir string
}

type remoteEnsureWritableCall struct {
	target    remote.Target
	remoteDir string
}

type remoteRemoveContainerCall struct {
	target remote.Target
	taskID string
}

type remoteFindAvailablePortCall struct {
	target remote.Target
	start  string
}

type writeFileCall struct {
	path string
	data []byte
	perm os.FileMode
}

type saveConfigCall struct {
	path string
	cfg  *config.Config
}

func cloneConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	clone.Repos = map[string]config.Repo{}
	for key, value := range cfg.Repos {
		clone.Repos[key] = value
	}
	clone.Remotes = map[string]config.Remote{}
	for key, value := range cfg.Remotes {
		clone.Remotes[key] = value
	}
	return &clone
}

type remoteSyncCall struct {
	opts remote.SyncOptions
}

type remoteDockerStreamCall struct {
	opts remote.DockerStreamOptions
}

func indexOfString(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}
