//go:build e2e
// +build e2e

package dockerfile_inputs

import (
	"testing"

	"github.com/openshift/ci-tools/test/e2e/framework"
)

func TestDockerfileInputs(t *testing.T) {
	const fakeJobSpec = `{"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"quay-proxy.ci.openshift.org/openshift/ci:ci_clonerefs_latest","initupload":"quay-proxy.ci.openshift.org/openshift/ci:ci_initupload_latest","entrypoint":"quay-proxy.ci.openshift.org/openshift/ci:ci_entrypoint_latest","sidecar":"quay-proxy.ci.openshift.org/openshift/ci:ci_sidecar_latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
	var testCases = []struct {
		name       string
		args       []string
		configPath string
		jobSpec    string
		success    bool
		output     []string
	}{
		{
			name:       "dockerfile inputs detection",
			args:       []string{"--target=[images]"},
			configPath: "testdata/config.yaml",
			jobSpec:    fakeJobSpec,
			success:    true,
			output: []string{
				"Detected base image, will tag into pipeline:ocp_4.19_base",
				"Detected base image, will tag into pipeline:ocp_builder_rhel-9-golang-1.22-openshift-4.19",
				"Detected base image, will tag into pipeline:ocp_4.18_base",
				"Detected base image, will tag into pipeline:ocp_4.17_base",
				"Detected base image, will tag into pipeline:ocp_builder_rhel-9-golang-1.23-openshift-4.20",
				"Skipping Dockerfile inputs detection: manual replacement exists",
				"Detected base image, will tag into pipeline:ocp_4.16_base",
				"Detected base image, will tag into pipeline:ocp_ubi-minimal_8",
				"Detected base image matches existing base image",
			},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(framework.LocalPullSecretFlag(t), framework.RemotePullSecretFlag(t))
			cmd.AddArgs(append(testCase.args, "--config="+testCase.configPath)...)
			cmd.AddEnv("JOB_SPEC=" + testCase.jobSpec)
			output, err := cmd.Run()
			if testCase.success != (err == nil) {
				t.Fatalf("%s: didn't expect an error from ci-operator: %v; output:\n%v", testCase.name, err, string(output))
			}
			if len(testCase.output) > 0 {
				cmd.VerboseOutputContains(t, testCase.name, testCase.output...)
			}
		})
	}
}
