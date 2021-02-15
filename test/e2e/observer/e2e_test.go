//go:build e2e
// +build e2e

package multi_stage

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/openshift/ci-tools/test/e2e/framework"
)

const (
	defaultJobSpec = `JOB_SPEC={"type":"presubmit","job":"pull-ci-test-test-master-success","buildid":"0","prowjobid":"uuid","refs":{"org":"test","repo":"test","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"origin-ci-test","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
)

var timeRegex = regexp.MustCompile(`time=".*"`)

func TestObservers(t *testing.T) {
	clusterProfileDir := filepath.Join(t.TempDir(), "with-observer-cluster-profile")
	if err := os.MkdirAll(clusterProfileDir, 0755); err != nil {
		t.Fatalf("failed to create dummy secret dir: %v", err)
	}
	if err := ioutil.WriteFile(filepath.Join(clusterProfileDir, "data"), []byte("nothing"), 0644); err != nil {
		t.Fatalf("failed to create dummy secret data: %v", err)
	}
	var testCases = []struct {
		name    string
		args    []string
		env     []string
		success bool
		output  []string
	}{
		{
			name:    "running with an observer",
			args:    []string{"--unresolved-config=config.yaml", "--target=with-observer", "--secret-dir=" + clusterProfileDir},
			env:     []string{defaultJobSpec},
			success: true,
			output: []string{
				`Executing pod "with-observer-observer" running image "pipeline:os"`,
				`Container test in pod with-observer-observer completed successfully`,
				`Pod with-observer-observer succeeded after`,
			},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(testCase.args...)
			cmd.AddEnv(testCase.env...)
			output, err := cmd.Run()
			if testCase.success != (err == nil) {
				t.Fatalf("%s: didn't expect an error from ci-operator: %v; output:\n%v", testCase.name, err, string(output))
			}
			for _, line := range testCase.output {
				if !bytes.Contains(output, []byte(line)) {
					t.Errorf("%s: could not find line %q in output; output:\n%v", testCase.name, line, string(output))
				}
			}
			outputjUnit := filepath.Join(cmd.ArtifactDir(), "junit_operator.xml")
			raw, err := ioutil.ReadFile(outputjUnit)
			if err != nil {
				t.Fatalf("could not read jUnit artifact: %v", err)
			}
			if err := ioutil.WriteFile(outputjUnit, timeRegex.ReplaceAll(raw, []byte(`time="whatever"`)), 0755); err != nil {
				t.Fatalf("could not munge jUnit artifact: %v", err)
			}
			framework.CompareWithFixture(t, "artifacts/junit_operator.xml", filepath.Join(cmd.ArtifactDir(), "junit_operator.xml"))
		}, framework.ConfigResolver(framework.ConfigResolverOptions{
			ConfigPath:   "configs",
			RegistryPath: "registry",
			FlatRegistry: true,
		}), framework.Boskos(framework.BoskosOptions{ConfigPath: "boskos.yaml"}))
	}
}
