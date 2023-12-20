//go:build e2e
// +build e2e

package observer

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/openshift/ci-tools/test/e2e/framework"
)

const (
	defaultJobSpec = `JOB_SPEC={"type":"presubmit","job":"pull-ci-test-test-master-success","buildid":"0","prowjobid":"uuid","refs":{"org":"test","repo":"test","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
)

var timeRegex = regexp.MustCompile(`time=".*"`)
var failureTimeRegex = regexp.MustCompile(`time&#34;:&#34;.*&#34;`)
var sourceCodeLineRegex = regexp.MustCompile(`(/\w+\.go):\d+`)

func TestObservers(t *testing.T) {
	var testCases = []struct {
		name          string
		args          []string
		env           []string
		success       bool
		output        []string
		junitOperator string
	}{
		{
			name: "running with an observer",
			args: []string{
				"--unresolved-config=simple-observer.yaml",
				"--target=with-observer",
			},
			env:     []string{defaultJobSpec},
			success: true,
			output: []string{
				`Running step with-observer-observer`,
				`Running step with-observer-create-kubeconfig`,
				`Running step with-observer-check-shared-dir`,
				`Step with-observer-observer succeeded after`,
				`Step with-observer-create-kubeconfig succeeded after`,
				`Step with-observer-check-shared-dir succeeded after`,
			},
			junitOperator: "simple-observer-junit_operator.xml",
		},
		{
			name: "running with multi observers",
			args: []string{
				"--unresolved-config=multi-observers.yaml",
				"--target=multi-observers",
			},
			env:     []string{defaultJobSpec},
			success: true,
			output: []string{
				`Running step multi-observers-observer`,
				`Running step multi-observers-failing-observer`,
				`Running step multi-observers-inject-observer`,
				`Running step multi-observers-create-kubeconfig`,
				`Running step multi-observers-check-shared-dir`,
				`Step multi-observers-observer succeeded after`,
				`Step multi-observers-failing-observer failed after`,
				`Step multi-observers-inject-observer succeeded after`,
				`Step multi-observers-create-kubeconfig succeeded after`,
				`Step multi-observers-check-shared-dir succeeded after`,
			},
			junitOperator: "multi-observers-junit_operator.xml",
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
			raw, err := os.ReadFile(outputjUnit)
			if err != nil {
				t.Fatalf("could not read jUnit artifact: %v", err)
			}
			mungedJunit := timeRegex.ReplaceAll(raw, []byte(`time="whatever"`))
			mungedJunit = failureTimeRegex.ReplaceAll(mungedJunit, []byte(`time&#34;:&#34;whatever&#34;`))
			mungedJunit = sourceCodeLineRegex.ReplaceAll(mungedJunit, []byte(`$1`))
			if err := os.WriteFile(outputjUnit, mungedJunit, 0755); err != nil {
				t.Fatalf("could not munge jUnit artifact: %v", err)
			}
			expectedJunit := path.Join("artifacts", testCase.junitOperator)
			framework.CompareWithFixture(t, expectedJunit, filepath.Join(cmd.ArtifactDir(), "junit_operator.xml"))
		}, framework.ConfigResolver(framework.ConfigResolverOptions{
			ConfigPath:   "configs",
			RegistryPath: "registry",
			FlatRegistry: true,
		}))
	}
}
