//go:build e2e
// +build e2e

package run

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"sigs.k8s.io/prow/pkg/interrupts"

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
	podScaler.Env = append(os.Environ(), "POD_SCALER_MIN_SAMPLES=1") // Set env var for e2e tests
	out, err := podScaler.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to run pod-scaler: %v: %s", err, string(out))
	}
	t.Logf(string(out))
	t.Logf("Ran pod-scaler in %s", time.Since(start))
}
