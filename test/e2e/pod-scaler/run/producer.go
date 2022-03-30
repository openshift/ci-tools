//go:build e2e
// +build e2e

package run

import (
	"fmt"
	"os/exec"
	"time"

	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func Producer(t testhelper.TestingTInterface, dataDir, kubeconfigFile string, ignoreUntil time.Duration) {
	podScalerFlags := []string{
		"--loglevel=info",
		"--log-style=text",
		"--cache-dir", dataDir,
		"--kubeconfig", kubeconfigFile,
		"--mode=producer",
		"--produce-once=true",
		"--metrics-port=9091",
		fmt.Sprintf("--ignore-latest=%s", ignoreUntil.String()),
	}
	start := time.Now()
	t.Logf("Running pod-scaler %v", podScalerFlags)
	podScaler := exec.CommandContext(interrupts.Context(), "pod-scaler", podScalerFlags...)
	out, err := podScaler.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to run pod-scaler: %v: %s", err, string(out))
	}
	t.Logf(string(out))
	t.Logf("Ran pod-scaler in %s", time.Since(start))
}
