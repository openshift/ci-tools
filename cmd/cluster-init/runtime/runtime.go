package runtime

import (
	"context"
	"os"
	"os/exec"
)

type Options struct {
	ClusterInstall string
	InstallBase    string
}

func BuildCmd(ctx context.Context, program string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func RunCmd(cmd *exec.Cmd) error {
	return cmd.Run()
}

func IsIntegrationTest() bool {
	_, ok := os.LookupEnv("CITOOLS_CLUSTERINIT_INTEGRATIONTEST")
	return ok
}
