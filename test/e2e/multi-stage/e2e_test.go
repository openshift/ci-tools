// +build e2e

package multi_stage

import (
	"bytes"
	"io/ioutil"
	"testing"

	"github.com/openshift/ci-tools/test/e2e/framework"
)

const (
	defaultJobSpec = `JOB_SPEC={"type":"presubmit","job":"pull-ci-test-test-master-success","buildid":"0","prowjobid":"uuid","refs":{"org":"test","repo":"test","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"origin-ci-test","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
	depsJobSpec    = `JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"origin-ci-test","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
)

func TestMultiStage(t *testing.T) {
	rawConfig, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	var testCases = []struct {
		name    string
		args    []string
		env     []string
		success bool
		output  []string
	}{
		{
			name:    "fetching full config from resolver",
			args:    []string{"--target=success"},
			env:     []string{defaultJobSpec},
			success: true,
			output:  []string{"Container test in pod success completed successfully"},
		},
		{
			name:    "without references",
			args:    []string{"--unresolved-config=config.yaml", "--target=without-references"},
			env:     []string{defaultJobSpec},
			success: true,
			output:  []string{"Container test in pod without-references-produce-content completed successfully", "Container test in pod without-references-consume-and-validate completed successfully", "Container test in pod without-references-step-with-imagestreamtag completed successfully"},
		},
		{
			name:    "with references",
			args:    []string{"--unresolved-config=config.yaml", "--target=with-references"},
			env:     []string{defaultJobSpec},
			success: true,
			output:  []string{"Container test in pod with-references-step completed successfully"},
		},
		{
			name:    "with references via env",
			args:    []string{"--target=with-references"},
			env:     []string{defaultJobSpec, "UNRESOLVED_CONFIG=" + string(rawConfig)},
			success: true,
			output:  []string{"Container test in pod with-references-step completed successfully"},
		},
		{
			name:    "skipping on success",
			args:    []string{"--unresolved-config=config.yaml", "--target=skip-on-success"},
			env:     []string{defaultJobSpec},
			success: true,
			output:  []string{`Skipping optional step skip-on-success-skip-on-success-post-step`},
		},
		{
			name:    "step with timeout",
			args:    []string{"--unresolved-config=config.yaml", "--target=timeout"},
			env:     []string{defaultJobSpec},
			success: false,
			output: []string{
				`Process did not finish before 2m0s timeout`,
				`Process gracefully exited before 10s grace period`,
				`Container test in pod timeout-timeout failed`,
			},
		},
		{
			name:    "step with dependencies",
			args:    []string{"--unresolved-config=dependencies.yaml", "--target=with-dependencies"},
			env:     []string{depsJobSpec},
			success: true,
			output:  []string{`Pod with-dependencies-depend-on-stuff succeeded`},
		},
		{
			name:    "step with cli",
			args:    []string{"--unresolved-config=dependencies.yaml", "--target=with-cli"},
			env:     []string{depsJobSpec},
			success: true,
			output:  []string{`Container inject-cli in pod with-cli-use-cli completed successfully`},
		},
		{
			name:    "step with best effort post-step",
			args:    []string{"--unresolved-config=best-effort.yaml", "--target=best-effort-success"},
			env:     []string{defaultJobSpec},
			success: true,
			output:  []string{`Pod best-effort-success-failure is running in best-effort mode`},
		},
		{
			name:    "step without best effort post-step failure",
			args:    []string{"--unresolved-config=best-effort.yaml", "--target=best-effort-failure"},
			env:     []string{defaultJobSpec},
			success: false,
			output:  []string{`could not run steps: step best-effort-failure failed`},
		},
		{
			name:    "step name explicitly uses an imagestream name",
			args:    []string{"--unresolved-config=names.yaml", "--target=os"},
			env:     []string{defaultJobSpec},
			success: true,
			output:  []string{`Pod os-test succeeded after`},
		},
		{
			name:    "step with run_as_script in alpine image",
			args:    []string{"--unresolved-config=config.yaml", "--target=run-as-script"},
			env:     []string{defaultJobSpec},
			success: true,
			output:  []string{`run-as-script-success succeeded`},
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
		}, framework.ConfigResolver(framework.ConfigResolverOptions{
			ConfigPath:     "configs",
			RegistryPath:   "registry",
			ProwConfigPath: "./../../integration/ci-operator-configresolver/config.yaml",
			FlatRegistry:   true,
		}))
	}
}
