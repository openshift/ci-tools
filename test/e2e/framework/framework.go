//go:build e2e_framework
// +build e2e_framework

package framework

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/openshift/ci-tools/pkg/testhelper"
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
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}
	if os.Getenv("UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
			t.Fatalf("failed to create fixture directory: %v", err)
		}
		if err := os.WriteFile(golden, actual, 0644); err != nil {
			t.Fatalf("failed to write updated fixture: %v", err)
		}
	}
	expected, err := os.ReadFile(golden)
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

var ArtifactDir = testhelper.ArtifactDir

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

// HiveKubeconfigFlag formats a flag to provide access to remote Hive for
// ci-operator, failing if the required env is not present to supply it.
func HiveKubeconfigFlag(t *T) string {
	value, set := os.LookupEnv("HIVE_KUBECONFIG")
	if !set {
		t.Fatal("required environment HIVE_KUBECONFIG is not set")
	}
	return flag("hive-kubeconfig", value)
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

// ManifestToolCredentialsFlag formats a flag to provide access to push the manifest listed image
// to the target registry for ci-operator, failing if the required env is not present to supply it.
func ManifestToolCredentialsFlag(t *T) string {
	value, set := os.LookupEnv("MANIFEST_TOOL_SECRET")
	if !set {
		t.Fatal("required environment MANIFEST_TOOL_SECRET is not set")
	}
	return flag("manifest-tool-dockercfg", value)
}

// LocalRegistryDNSFlag formats a flag for the targeted image registry DNS for ci-operator,
// failing if the required env is not present to supply it.
func LocalRegistryDNSFlag(t *T) string {
	value, set := os.LookupEnv("LOCAL_REGISTRY_DNS")
	if !set {
		t.Fatal("required environment LOCAL_REGISTRY_DNS is not set")
	}
	return flag("local-registry-dns", value)
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

// Alias this for compatibility
type Accessory = testhelper.Accessory

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
	return testhelper.NewAccessory("boskos",
		flags(map[string]string{
			"config":    opt.ConfigPath,
			"in_memory": "true",
			"log-level": "debug",
		}),
		func(port, healthPort string) []string {
			return flags(map[string]string{
				"port":        port,
				"health-port": healthPort,
			})
		},
		func(port, _ string) []string {
			return flags(map[string]string{
				"lease-server":                  "http://127.0.0.1:" + port,
				"lease-server-credentials-file": credentialsFile,
				"lease-acquire-timeout":         "2s",
			})
		},
	)
}

// ConfigResolverOptions are options for running the config server
type ConfigResolverOptions struct {
	ConfigPath   string
	RegistryPath string
	FlatRegistry bool
}

// ConfigResolver begins the configresolver server and makes sure it is ready
// to serve before returning the port it is serving on.
func ConfigResolver(opt ConfigResolverOptions) *Accessory {
	return testhelper.NewAccessory("ci-operator-configresolver",
		flags(map[string]string{
			"config":        opt.ConfigPath,
			"registry":      opt.RegistryPath,
			"flat-registry": strconv.FormatBool(opt.FlatRegistry),
			"log-level":     "debug",
			"cycle":         "2m",
		}),
		func(port, healthPort string) []string {
			return flags(map[string]string{
				"port":        port,
				"health-port": healthPort,
			})
		},
		func(port, healthPort string) []string {
			return flags(map[string]string{
				"resolver-address": "http://127.0.0.1:" + port,
			})
		},
	)
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
