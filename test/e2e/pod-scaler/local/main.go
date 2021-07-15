// +build e2e

// This tool can run the pod-scaler locally, setting up dependencies in an ephemeral manner
// and mimicking what the end-to-end tests do in order to facilitate high-fidelity local dev.
package main

import (
	"io/ioutil"
	"math/rand"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/test/e2e/pod-scaler/kubernetes"
	"github.com/openshift/ci-tools/test/e2e/pod-scaler/prometheus"
	"github.com/openshift/ci-tools/test/e2e/pod-scaler/run"
)

func main() {
	logger := logrus.WithField("component", "local-pod-scaler")
	tmpDir, err := ioutil.TempDir("", "podscaler")
	if err != nil {
		logger.WithError(err).Fatal("Failed to create temporary directory.")
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			logger.WithError(err).Error("Failed to clean up temporary directory.")
		}
	}()
	t := &fakeT{
		tmpDir: tmpDir,
		logger: logger,
	}
	prometheusAddr, _ := prometheus.Initialize(t, tmpDir, rand.New(rand.NewSource(time.Now().UnixNano())))
	kubeconfigFile := kubernetes.Fake(t, tmpDir, kubernetes.Prometheus(prometheusAddr))

	dataDir, err := ioutil.TempDir(tmpDir, "data")
	if err != nil {
		logger.WithError(err).Fatal("Failed to create temporary directory for data.")
	}
	run.Producer(t, dataDir, kubeconfigFile, 0*time.Second)
	interrupts.WaitForGracefulShutdown()
}

type fakeT struct {
	tmpDir string
	logger *logrus.Entry
}

var _ testhelper.TestingTInterface = &fakeT{}

func (t *fakeT) Cleanup(f func())                          { logrus.RegisterExitHandler(f) }
func (t *fakeT) Deadline() (deadline time.Time, ok bool)   { return time.Time{}, false }
func (t *fakeT) Error(args ...interface{})                 { t.logger.Error(args...) }
func (t *fakeT) Errorf(format string, args ...interface{}) { t.logger.Errorf(format, args...) }
func (t *fakeT) Fail()                                     { t.logger.Fatal("Fail called!") }
func (t *fakeT) FailNow()                                  { t.logger.Fatal("FailNow called!") }
func (t *fakeT) Failed() bool                              { return false }
func (t *fakeT) Fatal(args ...interface{})                 { t.logger.Fatal(args...) }
func (t *fakeT) Fatalf(format string, args ...interface{}) { t.logger.Fatalf(format, args...) }
func (t *fakeT) Helper()                                   {}
func (t *fakeT) Log(args ...interface{})                   { t.logger.Info(args...) }
func (t *fakeT) Logf(format string, args ...interface{})   { t.logger.Infof(format, args...) }
func (t *fakeT) Name() string                              { return "pod-scaler/local" }
func (t *fakeT) Parallel()                                 {}
func (t *fakeT) Skip(args ...interface{})                  { t.logger.Info(args...) }
func (t *fakeT) SkipNow()                                  {}
func (t *fakeT) Skipf(format string, args ...interface{})  { t.logger.Infof(format, args...) }
func (t *fakeT) Skipped() bool                             { return false }
func (t *fakeT) TempDir() string {
	dir, err := ioutil.TempDir(t.tmpDir, "")
	if err != nil {
		t.logger.WithError(err).Fatal("Could not create temporary directory.")
	}
	return dir
}
