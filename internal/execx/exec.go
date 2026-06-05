package execx

import (
	"context"
	"os"
	"os/exec"
)

type Executor interface {
	Run(ctx context.Context, name string, args ...string) error
}

type LocalExecutor struct{}

func (LocalExecutor) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
