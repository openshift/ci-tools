// +build e2e

package secret_generator

import (
	"bytes"
	"context"
	"os/exec"
	"testing"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGeneratorBootstrap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := "http://" + testhelper.Vault(ctx, testhelper.NewT(ctx, t))
	cmd := func(cmd string, args ...string) *exec.Cmd {
		ret := exec.Command(cmd, append(
			args,
			"--vault-addr", addr,
			"--vault-token-file", "/dev/stdin",
			"--vault-prefix", "secret")...)
		ret.Stdin = bytes.NewBufferString(testhelper.VaultTestingRootToken)
		return ret
	}
	generator := cmd(
		"ci-secret-generator",
		"--validate=false",
		"--dry-run=false",
		"--config", "generator.yaml")
	bootstrap := cmd(
		"ci-secret-bootstrap",
		"--dry-run",
		"--config", "bootstrap.yaml")
	bootstrap.Env = []string{"KUBECONFIG=kubeconfig"}
	if out, err := generator.CombinedOutput(); err != nil {
		t.Fatalf("generator failed: %v, output:\n%s", err, out)
	}
	if out, err := bootstrap.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap failed: %v, output:\n%s", err, out)
	}
}
