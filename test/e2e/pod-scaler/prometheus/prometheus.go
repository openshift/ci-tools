//go:build e2e
// +build e2e

package prometheus

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

// Initialize runs Prometheus with backfilled data under the given dir.
func Initialize(t testhelper.TestingTInterface, tmpDir string, r *rand.Rand, stream bool) (string, *DataInStages) {
	prometheusDir, err := ioutil.TempDir(tmpDir, "prometheus")
	if err != nil {
		t.Fatalf("Failed to create temporary directory for Prometheus: %v", err)
	}
	if err := os.Chmod(prometheusDir, 0777); err != nil {
		t.Fatalf("Failed to open permissions in temporary directory for Prometheus: %v", err)
	}
	t.Logf("Prometheus data will be in %s", prometheusDir)

	prometheusConfig := filepath.Join(prometheusDir, "prometheus.yaml")
	if err := ioutil.WriteFile(prometheusConfig, []byte(`global:
  scrape_interval: 15s`), 0666); err != nil {
		t.Fatalf("Could not write Prometheus config file: %v", err)
	}

	prometheusHostname := "0.0.0.0"
	prometheusFlags := []string{
		"--config.file", prometheusConfig,
		"--storage.tsdb.path", prometheusDir,
		"--storage.tsdb.retention.time", "20d",
		"--storage.tsdb.allow-overlapping-blocks",
		"--log.level", "info",
	}
	prometheusCtx, prometheusCancel := context.WithCancel(interrupts.Context())
	prometheusInitOutput := &bytes.Buffer{}
	prometheusInit := exec.CommandContext(prometheusCtx, "prometheus",
		append(prometheusFlags, "--web.listen-address", prometheusHostname+":"+testhelper.GetFreePort(t))...,
	)
	prometheusInit.Stdout = prometheusInitOutput
	prometheusInit.Stderr = prometheusInitOutput
	if err := prometheusInit.Start(); err != nil {
		logrus.WithError(err).Fatal("Failed to initialize Prometheus.")
	}

	retentionPeriod := 20 * 24 * time.Hour
	info := Backfill(t, prometheusDir, retentionPeriod, r)

	// restart Prometheus to reload TSDB, by default this can take minutes without a restart
	prometheusCancel()
	if err := prometheusInit.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ProcessState.String() == "signal: killed" {
			// this was us killing the process, ignore
		} else {
			logrus.WithError(err).Fatalf("Failed to initialize Prometheus: %v: %v", err, prometheusInitOutput.String())
		}
	}

	prometheus := testhelper.NewAccessory("prometheus", prometheusFlags,
		func(port, _ string) []string {
			t.Logf("Prometheus starting on port %s", port)
			return []string{"--web.listen-address", fmt.Sprintf("%s:%s", prometheusHostname, port)}
		}, func(port, _ string) []string {
			return []string{"--prometheus-endpoint", fmt.Sprintf("%s:%s", prometheusHostname, port)}
		},
	)
	prometheus.RunFromFrameworkRunner(t, interrupts.Context(), stream)
	prometheusConnectionFlags := prometheus.ClientFlags()
	// this is a hack, but whatever
	prometheusAddr := prometheusConnectionFlags[1]
	prometheusHost := "http://" + prometheusAddr
	prometheus.Ready(t, func(o *testhelper.ReadyOptions) {
		o.ReadyURL = prometheusHost + "/-/ready"
		o.WaitFor = 5
	})
	// TODO: for some reason the above is not sufficient, leave this for now
	time.Sleep(5 * time.Second)
	t.Logf("Prometheus is running at %s", prometheusHost)
	return prometheusAddr, info
}
