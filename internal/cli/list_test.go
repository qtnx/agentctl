package cli

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/qtnx/agentctl/internal/config"
	"github.com/qtnx/agentctl/internal/state"
)

func TestListPrintsSavedTaskFields(t *testing.T) {
	cfg := &config.Config{StateDir: "/tmp/agentctl-state"}
	fakes := &listFakes{
		cfg: cfg,
		tasks: []state.Task{
			{
				TaskID:      "ALPHA-1",
				Repo:        "backend",
				Status:      "running",
				Worktree:    "/tmp/worktrees/ALPHA-1",
				TmuxSession: "agentctl-ALPHA-1",
			},
			{
				TaskID:      "BRAVO_2",
				Repo:        "frontend",
				Status:      "stopped",
				Worktree:    "/tmp/worktrees/BRAVO_2",
				TmuxSession: "agentctl-BRAVO_2",
			},
		},
	}
	var out bytes.Buffer

	cmd := newListCommandWithDeps(fakes.deps())
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--config", "/tmp/config.yaml"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{"/tmp/config.yaml"}) {
		t.Fatalf("config paths = %#v, want custom config path", fakes.loadConfigPaths)
	}
	if !reflect.DeepEqual(fakes.listStateDirs, []string{cfg.StateDir}) {
		t.Fatalf("list state dirs = %#v, want configured state dir", fakes.listStateDirs)
	}

	output := out.String()
	for _, want := range []string{
		"ALPHA-1",
		"backend",
		"running",
		"/tmp/worktrees/ALPHA-1",
		"agentctl-ALPHA-1",
		"BRAVO_2",
		"frontend",
		"stopped",
		"/tmp/worktrees/BRAVO_2",
		"agentctl-BRAVO_2",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("list output = %q, want field %q", output, want)
		}
	}
	if strings.Index(output, "ALPHA-1") > strings.Index(output, "BRAVO_2") {
		t.Fatalf("list output = %q, want store order preserved", output)
	}
}

func TestListUsesDefaultConfigPath(t *testing.T) {
	fakes := &listFakes{cfg: &config.Config{StateDir: "/tmp/agentctl-state"}}
	cmd := newListCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fakes.loadConfigPaths, []string{defaultConfigPath}) {
		t.Fatalf("config paths = %#v, want default config path", fakes.loadConfigPaths)
	}
}

func TestListReturnsStateListError(t *testing.T) {
	stateErr := errors.New("state list failed")
	fakes := &listFakes{
		cfg:     &config.Config{StateDir: "/tmp/agentctl-state"},
		listErr: stateErr,
	}
	cmd := newListCommandWithDeps(fakes.deps())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	if !errors.Is(err, stateErr) {
		t.Fatalf("error = %v, want %v", err, stateErr)
	}
}

type listFakes struct {
	cfg             *config.Config
	loadConfigPaths []string
	listStateDirs   []string
	tasks           []state.Task
	listErr         error
}

func (f *listFakes) deps() listDeps {
	return listDeps{
		loadConfig: func(path string) (*config.Config, error) {
			f.loadConfigPaths = append(f.loadConfigPaths, path)
			return f.cfg, nil
		},
		listTasks: func(stateDir string) ([]state.Task, error) {
			f.listStateDirs = append(f.listStateDirs, stateDir)
			return f.tasks, f.listErr
		},
	}
}
