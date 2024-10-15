package types

import (
	"context"
	"os/exec"
)

type Architecture string

const (
	ArchAMD64   Architecture = "amd64"
	ArchARM64   Architecture = "arm64"
	ArchAARCH64 Architecture = "aarch64"
)

type Step interface {
	Run(ctx context.Context) error
	Name() string
}

type CmdBuilder func(ctx context.Context, program string, args ...string) *exec.Cmd
type CmdRunner func(cmd *exec.Cmd) error
