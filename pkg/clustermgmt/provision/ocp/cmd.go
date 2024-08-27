package ocp

import (
	"context"
	"os/exec"
)

type CmdBuilder func(ctx context.Context, program string, args ...string) *exec.Cmd
type CmdRunner func(cmd *exec.Cmd) error
