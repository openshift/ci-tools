//go:build e2e
// +build e2e

package secret_generator

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGeneratorBootstrap(t *testing.T) {
	t.Parallel()
	vaultAddr := "http://" + testhelper.Vault(t)
	fakeGSMServerEndpoint := startGSMTestServer(t)
	binDir := buildSecretBinaries(t)
	generator := buildSecretCommand(vaultAddr, fakeGSMServerEndpoint,
		filepath.Join(binDir, "ci-secret-generator"),
		"--validate=false",
		"--dry-run=false",
		"--config", "generator.yaml",
		"--enable-gsm-sync")
	if out, err := generator.CombinedOutput(); err != nil {
		t.Fatalf("generator failed: %v, output:\n%s", err, out)
	}

	clusters := []string{"core-ci", "app.ci", "build01", "build02", "vsphere"}
	vaultFieldClusters := []string{"core-ci", "app.ci", "build01", "build02"}
	for _, tc := range []struct {
		name        string
		args        []string
		wantSecrets map[string][]string
	}{
		{
			name: "only vault",
			args: []string{"--config", "bootstrap.yaml"},
			wantSecrets: map[string][]string{
				"build-farm-credentials": vaultFieldClusters,
			},
		},
		{
			name: "only gsm",
			args: []string{"--config", "bootstrap-empty.yaml", "--enable-gsm"},
			wantSecrets: map[string][]string{
				"gsm-build-farm-credentials": clusters,
			},
		},
		{
			name: "vault and gsm",
			args: []string{"--config", "bootstrap.yaml", "--enable-gsm"},
			wantSecrets: map[string][]string{
				"build-farm-credentials":     vaultFieldClusters,
				"gsm-build-farm-credentials": clusters,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outputDir := t.TempDir()
			bootstrap := buildSecretCommand(vaultAddr, fakeGSMServerEndpoint,
				filepath.Join(binDir, "ci-secret-bootstrap"), append([]string{"--dry-run"}, tc.args...)...)
			bootstrap.Env = append(bootstrap.Env, "KUBECONFIG=kubeconfig", "TMPDIR="+outputDir)
			if out, err := bootstrap.CombinedOutput(); err != nil {
				t.Fatalf("bootstrap failed: %v, output:\n%s", err, out)
			}

			wantData := make(map[string]map[string][]byte, len(tc.wantSecrets))
			for name, fieldClusters := range tc.wantSecrets {
				fields := make(map[string][]byte, len(fieldClusters))
				for _, cluster := range fieldClusters {
					fields[fmt.Sprintf("sa.config-updater.%s.config", cluster)] = []byte{0}
				}
				wantData[name] = fields
			}
			files, err := filepath.Glob(filepath.Join(outputDir, "*.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != len(clusters) {
				t.Fatalf("expected %d cluster output files, got %v", len(clusters), files)
			}
			for _, cluster := range clusters {
				t.Run(cluster, func(t *testing.T) {
					files, err := filepath.Glob(filepath.Join(outputDir, cluster+"_*.yaml"))
					if err != nil {
						t.Fatal(err)
					}
					if len(files) != 1 {
						t.Fatalf("expected one output file, got %v", files)
					}
					data, err := os.ReadFile(files[0])
					if err != nil {
						t.Fatal(err)
					}
					documents := bytes.Split(data, []byte("---\n"))
					if len(documents) != len(tc.wantSecrets)+1 || len(bytes.TrimSpace(documents[len(documents)-1])) != 0 {
						t.Fatalf("expected exactly %d secrets, got:\n%s", len(tc.wantSecrets), data)
					}
					gotData := make(map[string]map[string][]byte, len(tc.wantSecrets))
					for _, document := range documents[:len(documents)-1] {
						var secret corev1.Secret
						if err := yaml.UnmarshalStrict(document, &secret); err != nil {
							t.Fatal(err)
						}
						if _, exists := gotData[secret.Name]; exists {
							t.Fatalf("duplicate secret %q", secret.Name)
						}
						testhelper.Diff(t, "namespace", "ci", secret.Namespace)
						gotData[secret.Name] = secret.Data
					}
					testhelper.Diff(t, "secrets by name", wantData, gotData)
				})
			}
		})
	}
}

func buildSecretBinaries(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	cmd := exec.Command("go", "build", "-tags=secret_generator_e2e", "-o", binDir+string(os.PathSeparator),
		"./cmd/ci-secret-generator", "./cmd/ci-secret-bootstrap")
	cmd.Dir = "../../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building secret-generator e2e binaries: %v, output:\n%s", err, out)
	}
	return binDir
}

func buildSecretCommand(vaultAddr, fakeGSMServerEndpoint, command string, args ...string) *exec.Cmd {
	cmd := exec.Command(command, append(args,
		"--vault-addr", vaultAddr,
		"--vault-token-file", "/dev/stdin",
		"--vault-prefix", "secret",
		"--gsm-config", "gsm-bootstrap.yaml",
		"--gsm-project-config", "gsm-project.yaml",
		"--gsm-credentials-file", "/dev/null")...)
	cmd.Stdin = bytes.NewBufferString(testhelper.VaultTestingRootToken)
	cmd.Env = append(os.Environ(), "CI_TOOLS_TEST_GSM_ENDPOINT="+fakeGSMServerEndpoint)
	return cmd
}
