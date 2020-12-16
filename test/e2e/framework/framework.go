// +build e2e

package framework

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"k8s.io/apimachinery/pkg/util/wait"
)

func init() {
	rand.Seed(time.Now().Unix())
}

// CompareWithFixtureDir will compare all files in a directory with a corresponding test fixture directory.
func CompareWithFixtureDir(t *testing.T, golden, output string) {
	t.Helper()
	if walkErr := filepath.Walk(golden, func(path string, info os.FileInfo, err error) error {
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
func CompareWithFixture(t *testing.T, golden, output string) {
	t.Helper()
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

// CiOperatorCommand returns the basic ci-operator command and artifact dir. Add args and env as necessary.
func CiOperatorCommand(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		c, cancel := context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		ctx = c
		t.Cleanup(cancel) // this does not really matter but govet is upset
	}
	var artifactDir string
	if dir, set := os.LookupEnv("ARTIFACT_DIR"); set {
		artifactDir = filepath.Join(dir, strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(t.Name()))
		if err := os.MkdirAll(artifactDir, 0755); err != nil {
			t.Fatalf("could not create artifact dir for ci-operator: %v", err)
		}
	} else {
		artifactDir = t.TempDir()
	}
	t.Cleanup(func() {
		if walkErr := filepath.Walk(artifactDir, func(path string, info os.FileInfo, err error) error {
			if info.IsDir() {
				return nil
			}
			// if we do not mangle these file-names, the jUnit spyglass lens
			// will show (somtimes expected) failures in these files from delegated
			// ci-operator runs in the overview, which is confusing
			if strings.HasPrefix(info.Name(), "junit") {
				if err := os.Rename(path, strings.ReplaceAll(path, "/junit", "/_junit")); err != nil {
					t.Logf("failed to mangle jUnit filename for %s: %v", path, err)
				}
			}
			return nil
		}); walkErr != nil {
			t.Errorf("failed to walk fixture tree for comparison: %v", walkErr)
		}
	})
	cmd := exec.CommandContext(ctx, "ci-operator",
		"--input-hash="+strconv.Itoa(rand.Int()), // we need unique namespaces
		"--artifact-dir="+artifactDir,
	)
	cmd.Env = append(cmd.Env, KubernetesClientEnv(t)...)
	return cmd, artifactDir
}

func Execute(t *testing.T, cmd *exec.Cmd, testDone <-chan struct{}, cleanupDone chan<- struct{}) ([]byte, error) {
	t.Helper()
	t.Logf("running: %v", cmd.Args)
	var b bytes.Buffer
	cmd.Stdout = &b
	cmd.Stderr = &b
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start ci-operator command: %v", err)
	}
	if deadline, ok := t.Deadline(); ok {
		go func() {
			defer func() {
				cleanupDone <- struct{}{}
			}()
			select {
			case <-testDone:
				// nothing to do
				return
			case <-time.After(time.Until(deadline.Add(-1 * time.Minute))):
				// the command context will send a SIGKILL, but we want an earlier SIGINT to allow
				// cleanup and artifact retrieval for sensible test output
				if err := cmd.Process.Signal(os.Interrupt); err != nil && !strings.Contains(err.Error(), "os: process already finished") { // why don't they export this ...
					t.Errorf("could not interrupt ci-operator: %v", err)
				}
			}
		}()
	} else {
		// we're not doing cleanup, so signal we're done anyway
		cleanupDone <- struct{}{}
	}
	// TODO(skuznets): stream this output?
	err := cmd.Wait()
	output := b.Bytes()
	t.Logf("ci-operator output:\n%v", string(output))
	return output, err
}

// LocalPullSecretFlag formats a flag to provide access to the local registry for
// ci-operator, failing if the required env is not present to supply it. This
// secret is stored in the
func LocalPullSecretFlag(t *testing.T) string {
	t.Helper()
	value, set := os.LookupEnv("LOCAL_REGISTRY_SECRET_DIR")
	if !set {
		t.Fatal("required environment LOCAL_REGISTRY_SECRET_DIR is not set")
	}
	return flag("image-import-pull-secret", filepath.Join(value, ".dockerconfigjson"))
}

// RemotePullSecretFlag formats a flag to provide access to remote registries for
// ci-operator, failing if the required env is not present to supply it.
func RemotePullSecretFlag(t *testing.T) string {
	t.Helper()
	value, set := os.LookupEnv("REMOTE_REGISTRY_SECRET_DIR")
	if !set {
		t.Fatal("required environment REMOTE_REGISTRY_SECRET_DIR is not set")
	}
	return flag("secret-dir", value)
}

// KubernetesClientEnv returns a list of formatted environment variables for
// use in providing to a exec.Command to allow it to talk to a k8s cluster.
func KubernetesClientEnv(t *testing.T) []string {
	t.Helper()
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

// BoskosOptions are options for running the boskos server
type BoskosOptions struct {
	ConfigPath string
}

// RunBoskos begins the boskos server and makes sure it is ready to serve
// before returning the port it is serving on.
func RunBoskos(t *testing.T, opt BoskosOptions) string {
	t.Helper()
	return runAccessory(t, "boskos", flags(map[string]string{
		"config":    opt.ConfigPath,
		"in_memory": "true",
		"log-level": "debug",
	})...)
}

// ConfigResolverOptions are options for running the config server
type ConfigResolverOptions struct {
	ConfigPath     string
	RegistryPath   string
	ProwConfigPath string
	FlatRegistry   bool
}

// RunConfigResolver begins the configresolver server and makes sure it is ready
// to serve before returning the port it is serving on.
func RunConfigResolver(t *testing.T, opt ConfigResolverOptions) string {
	t.Helper()
	return runAccessory(t, "ci-operator-configresolver", flags(map[string]string{
		"config":        opt.ConfigPath,
		"registry":      opt.RegistryPath,
		"prow-config":   opt.ProwConfigPath,
		"flat-registry": strconv.FormatBool(opt.FlatRegistry),
		"log-level":     "debug",
		"cycle":         "2m",
	})...)
}

func runAccessory(t *testing.T, command string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath(command); err != nil {
		t.Fatalf("no `%s` binary was found: %v", command, err)
	}

	tmpDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	main, health := getFreePort(t), getFreePort(t)
	cmd := exec.CommandContext(ctx, command, append(args, flags(map[string]string{
		"port":        main,
		"health-port": health,
	})...)...)
	log, err := os.Create(filepath.Join(tmpDir, fmt.Sprintf("%s.log", command)))
	if err != nil {
		t.Fatalf("could not create log file: %v", err)
	}
	cmd.Stdout = log
	cmd.Stderr = log
	go func() {
		if err := cmd.Run(); err != nil && ctx.Err() != context.Canceled {
			data, readErr := ioutil.ReadAll(log)
			if readErr != nil {
				t.Logf("could not read `%s` log: %v", command, readErr)
			}
			t.Errorf("`%s` failed: %v\nlogs:%v", command, err, string(data))
		}
	}()
	t.Cleanup(func() {
		cancel()
	})
	if err := wait.PollImmediate(1*time.Second, 1*time.Minute, func() (done bool, err error) {
		resp, err := http.Get(fmt.Sprintf("127.0.0.1:%s/healthz/ready", health))
		defer func() {
			if err := resp.Body.Close(); err != nil {
				t.Errorf("could not close response body: %v", err)
			}
		}()
		return (resp != nil && resp.StatusCode == http.StatusOK) && err != nil, nil
	}); err != nil {
		t.Fatalf("could not wait for `%s` to be healthy: %v", command, err)
	}
	return main
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

// getFreePort asks the kernel for a free open port that is ready to use.
func getFreePort(t *testing.T) string {
	t.Helper()
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
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}
