//go:build e2e
// +build e2e

// This tool can run the pod-scaler locally, setting up dependencies in an ephemeral manner
// and mimicking what the end-to-end tests do in order to facilitate high-fidelity local dev.
package main

import (
	"flag"
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

type options struct {
	cacheDir   string
	serveDevUI bool
}

func bindOptions(fs *flag.FlagSet) *options {
	o := options{}
	fs.StringVar(&o.cacheDir, "cache-dir", "", "Local directory holding cache data.")
	fs.BoolVar(&o.serveDevUI, "serve-dev-ui", false, "Run the development UI server.")
	return &o
}

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opts := bindOptions(flagSet)
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("failed to parse flags")
	}
	logger := logrus.WithField("component", "local-pod-scaler")
	tmpDir, err := os.MkdirTemp("", "podscaler")
	if err != nil {
		logger.WithError(err).Fatal("Failed to create temporary directory.")
	}
	logger.Infof("Working directory set as %s", tmpDir)
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			logger.WithError(err).Error("Failed to clean up temporary directory.")
		}
	}()
	t := &fakeT{
		tmpDir: tmpDir,
		logger: logger,
	}
	cacheDir := opts.cacheDir
	if opts.cacheDir == "" {
		prometheusAddr, _ := prometheus.Initialize(t.withName("pod-scaler/local/prometheus"), tmpDir, rand.New(rand.NewSource(time.Now().UnixNano())), true)
		kubeconfigFile := kubernetes.Fake(t.withName("pod-scaler/local/kubernetes"), tmpDir, kubernetes.Prometheus(prometheusAddr))

		dataDir, err := os.MkdirTemp(tmpDir, "data")
		if err != nil {
			logger.WithError(err).Fatal("Failed to create temporary directory for data.")
		}
		logger.Infof("Data will be saved to %s", dataDir)
		run.Producer(t.withName("pod-scaler/local/producer"), dataDir, kubeconfigFile, 0*time.Second)
		run.Admission(t.withName("pod-scaler/local/consumer.admission"), dataDir, kubeconfigFile, interrupts.Context(), true)
		cacheDir = dataDir
	}
	uiHost := run.UI(t.withName("pod-scaler/local/consumer.ui"), cacheDir, interrupts.Context(), true)
	if opts.serveDevUI {
		devUi := testhelper.NewAccessory("npm", []string{"--prefix", "cmd/pod-scaler/frontend", "run", "start:dev"}, func(port, healthPort string) []string {
			return []string{}
		}, func(port, healthPort string) []string {
			return []string{}
		}, "API_ENDPOINT="+uiHost, "PATH="+os.Getenv("PATH"), "ASSET_PATH=/")
		devUi.RunFromFrameworkRunner(t, interrupts.Context(), true)
	}
	interrupts.WaitForGracefulShutdown()
}

type fakeT struct {
	tmpDir string
	name   string
	logger *logrus.Entry
}

func (t *fakeT) withName(name string) *fakeT {
	c := *t
	c.name = name
	return &c
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
func (t *fakeT) Name() string                              { return t.name }
func (t *fakeT) Parallel()                                 {}
func (t *fakeT) Skip(args ...interface{})                  { t.logger.Info(args...) }
func (t *fakeT) SkipNow()                                  {}
func (t *fakeT) Skipf(format string, args ...interface{})  { t.logger.Infof(format, args...) }
func (t *fakeT) Skipped() bool                             { return false }
func (t *fakeT) TempDir() string {
	dir, err := os.MkdirTemp(t.tmpDir, "")
	if err != nil {
		t.logger.WithError(err).Fatal("Could not create temporary directory.")
	}
	return dir
}
