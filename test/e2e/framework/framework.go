// +build e2e

package framework

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"k8s.io/apimachinery/pkg/util/wait"
)

// CompareWithFixtureDir will compare all files in a directory with a corresponding test fixture directory.
func CompareWithFixtureDir(t *T, golden, output string) {
	if walkErr := filepath.Walk(golden, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(golden, path)
		if err != nil {
			// this should not happen
			t.Errorf("bug: could not compute relative path in fixture dir: %v", err)
		}
		CompareWithFixture(t, path, filepath.Join(output, relPath))
		return nil
	}); walkErr != nil {
		t.Errorf("failed to walk fixture tree for comparison: %v", walkErr)
	}
}

// CompareWithFixture will compare output files with a test fixture and allows to automatically update them
// by setting the UPDATE env var. The output and golden paths are relative to the test's directory.
func CompareWithFixture(t *T, golden, output string) {
	actual, err := ioutil.ReadFile(output)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}
	if os.Getenv("UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
			t.Fatalf("failed to create fixture directory: %v", err)
		}
		if err := ioutil.WriteFile(golden, actual, 0644); err != nil {
			t.Fatalf("failed to write updated fixture: %v", err)
		}
	}
	expected, err := ioutil.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(expected)),
		B:        difflib.SplitLines(string(actual)),
		FromFile: golden,
		ToFile:   output,
		Context:  3,
	}
	diffStr, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		t.Fatal(err)
	}

	if diffStr != "" {
		t.Errorf("got diff between expected and actual result: \n%s\n\nIf this is expected, re-run the test with `UPDATE=true go test ./...` to update the fixtures.", diffStr)
	}
}

// LocalPullSecretFlag formats a flag to provide access to the local registry for
// ci-operator, failing if the required env is not present to supply it.
func LocalPullSecretFlag(t *T) string {
	value, set := os.LookupEnv("LOCAL_REGISTRY_SECRET_DIR")
	if !set {
		t.Fatal("required environment LOCAL_REGISTRY_SECRET_DIR is not set")
	}
	return flag("image-import-pull-secret", filepath.Join(value, ".dockerconfigjson"))
}

// RemotePullSecretFlag formats a flag to provide access to remote registries for
// ci-operator, failing if the required env is not present to supply it.
func RemotePullSecretFlag(t *T) string {
	value, set := os.LookupEnv("REMOTE_REGISTRY_SECRET_DIR")
	if !set {
		t.Fatal("required environment REMOTE_REGISTRY_SECRET_DIR is not set")
	}
	return flag("secret-dir", value)
}

// GCSPushCredentialsFlag formats a flag to provide access to push to GCS for
// ci-operator, failing if the required env is not present to supply it.
func GCSPushCredentialsFlag(t *T) string {
	value, set := os.LookupEnv("GCS_CREDENTIALS_FILE")
	if !set {
		t.Fatal("required environment GCS_CREDENTIALS_FILE is not set")
	}
	return flag("gcs-upload-secret", value)
}

// KubernetesClientEnv returns a list of formatted environment variables for
// use in providing to a exec.Command to allow it to talk to a k8s cluster.
func KubernetesClientEnv(t *T) []string {
	host, hostSet := os.LookupEnv("KUBERNETES_SERVICE_HOST")
	port, portSet := os.LookupEnv("KUBERNETES_SERVICE_PORT")
	kubeconfig, kubeconfigSet := os.LookupEnv("KUBECONFIG")

	if !((hostSet && portSet) || kubeconfigSet) {
		t.Fatal("either KUBERNETES_SERVICE_{HOST,PORT} or KUBECONFIG must be set for this test")
	}

	if kubeconfigSet {
		return []string{"KUBECONFIG=" + kubeconfig}
	}
	return []string{"KUBERNETES_SERVICE_HOST=" + host, "KUBERNETES_SERVICE_PORT=" + port}
}

type Accessory struct {
	command    string
	args       []string
	port       string
	healthPort string

	flags func(port string) []string
}

// Run begins the accessory process. This call is not blocking.
func (a *Accessory) Run(t *T, parentCtx context.Context) {
	a.port, a.healthPort = getFreePort(t), getFreePort(t)
	ctx, cancel := context.WithCancel(parentCtx)
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		t.Logf("cleanup: killing `%s` process", a.command)
		cancel()
		<-cleanupCtx.Done()
	})
	cmd := exec.CommandContext(ctx, a.command, append(a.args, flags(map[string]string{
		"port":        a.port,
		"health-port": a.healthPort,
	})...)...)
	t.Logf("running: %v", cmd.Args)
	tmpDir := t.TempDir()
	logFile, err := os.Create(filepath.Join(tmpDir, fmt.Sprintf("%s.log", a.command)))
	if err != nil {
		t.Fatalf("could not create log file: %v", err)
	}
	log := bytes.Buffer{}
	tee := io.TeeReader(&log, logFile)
	cmd.Stdout = &log
	cmd.Stderr = &log
	go func() {
		defer func() { cleanupCancel() }()
		err := cmd.Run()
		data, readErr := ioutil.ReadAll(tee)
		if readErr != nil {
			t.Logf("could not read `%s` log: %v", a.command, readErr)
		}
		t.Logf("`%s` logs:\n%v", a.command, string(data))
		if err != nil && ctx.Err() == nil {
			// we care about errors in the process that did not result from the
			// context expiring and us killing it
			t.Fatalf("`%s` failed: %v logs:\n%v", a.command, err, string(data))
		}
	}()
}

// Ready returns when the accessory process is ready to serve data.
func (a *Accessory) Ready(t *T) {
	if waitErr := wait.PollImmediate(1*time.Second, 30*time.Second, func() (done bool, err error) {
		resp, getErr := http.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz/ready", a.healthPort))
		defer func() {
			if resp == nil || resp.Body == nil {
				return
			}
			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Logf("could not close response body: %v", closeErr)
			}
		}()
		if resp != nil {
			t.Logf("`%s` readiness probe: %v", a.command, resp.StatusCode)
		}
		if getErr != nil {
			t.Logf("`%s` readiness probe error: %v:", a.command, getErr)
		}
		return (resp != nil && resp.StatusCode == http.StatusOK) && getErr == nil, nil
	}); waitErr != nil {
		t.Fatalf("could not wait for `%s` to be healthy: %v", a.command, waitErr)
	}
}

// Flags exposes the port on which we are serving content and
// any other flags that are needed for the ci-operator to consume
// this accessory.
func (a *Accessory) Flags() []string {
	return a.flags(a.port)
}

// BoskosOptions are options for running the boskos server
type BoskosOptions struct {
	ConfigPath string
}

// Boskos begins the boskos server and makes sure it is ready to serve
// before returning the port it is serving on.
func Boskos(opt BoskosOptions) *Accessory {
	credentialsFile := os.Getenv("BOSKOS_CREDENTIALS_FILE")
	if credentialsFile == "" {
		credentialsFile = "/dev/null"
	}
	return &Accessory{
		command: "boskos",
		args: flags(map[string]string{
			"config":    opt.ConfigPath,
			"in_memory": "true",
			"log-level": "debug",
		}),
		flags: func(port string) []string {
			return flags(map[string]string{
				"lease-server":                  "http://127.0.0.1:" + port,
				"lease-server-credentials-file": credentialsFile,
				"lease-acquire-timeout":         "2s",
			})
		},
	}
}

// ConfigResolverOptions are options for running the config server
type ConfigResolverOptions struct {
	ConfigPath     string
	RegistryPath   string
	ProwConfigPath string
	FlatRegistry   bool
}

// ConfigResolver begins the configresolver server and makes sure it is ready
// to serve before returning the port it is serving on.
func ConfigResolver(opt ConfigResolverOptions) *Accessory {
	return &Accessory{
		command: "ci-operator-configresolver",
		args: flags(map[string]string{
			"config":        opt.ConfigPath,
			"registry":      opt.RegistryPath,
			"prow-config":   opt.ProwConfigPath,
			"flat-registry": strconv.FormatBool(opt.FlatRegistry),
			"log-level":     "debug",
			"cycle":         "2m",
		}),
		flags: func(port string) []string {
			return []string{flag("resolver-address", "http://127.0.0.1:"+port)}
		},
	}
}

func flags(data map[string]string) []string {
	var out []string
	for key, value := range data {
		out = append(out, flag(key, value))
	}
	return out
}

func flag(flag, value string) string {
	return fmt.Sprintf("--%s=%s", flag, value)
}

var ports = sync.Map{}

// getFreePort asks the kernel for a free open port that is ready to use.
func getFreePort(t *T) string {
	for {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			t.Fatalf("could not resolve free port: %v", err)
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			t.Fatalf("could not listen on free port: %v", err)
		}
		defer func() {
			if err := l.Close(); err != nil {
				t.Errorf("could not close listener: %v", err)
			}
		}()
		port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
		if _, previouslyAllocated := ports.LoadOrStore(port, nil); !previouslyAllocated {
			// we've never seen this before, we can use it
			return port
		}
	}
}
