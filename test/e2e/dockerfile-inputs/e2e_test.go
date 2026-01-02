//go:build e2e
// +build e2e

package dockerfile_inputs

import (
	"testing"

	"github.com/openshift/ci-tools/test/e2e/framework"
)

func TestDockerfileInputs(t *testing.T) {
	const defaultJobSpec = `{"type":"postsubmit","job":"branch-ci-test-test-master-dockerfile-inputs","buildid":"0","prowjobid":"uuid","refs":{"org":"test","repo":"test","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"quay-proxy.ci.openshift.org/openshift/ci:ci_clonerefs_latest","initupload":"quay-proxy.ci.openshift.org/openshift/ci:ci_initupload_latest","entrypoint":"quay-proxy.ci.openshift.org/openshift/ci:ci_entrypoint_latest","sidecar":"quay-proxy.ci.openshift.org/openshift/ci:ci_sidecar_latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`

	var testCases = []struct {
		name    string
		args    []string
		success bool
		output  []string
	}{
		{
			name:    "auto-detect single registry.ci.openshift.org reference",
			args:    []string{"--target=auto-detect-single"},
			success: true,
			output: []string{
				"Dockerfile-inputs: Detected registry reference",
				"registry.ci.openshift.org/ocp/4.19:base",
			},
		},
		{
			name:    "auto-detect multiple registry references",
			args:    []string{"--target=auto-detect-multiple"},
			success: true,
			output: []string{
				"Dockerfile-inputs: Detected registry reference",
				"registry.ci.openshift.org/ocp/4.19:base",
				"registry.ci.openshift.org/ocp/builder:rhel-9-golang-1.22-openshift-4.19",
			},
		},
		{
			name:    "auto-detect with quay-proxy reference",
			args:    []string{"--target=auto-detect-quay-proxy"},
			success: true,
			output: []string{
				"Dockerfile-inputs: Detected registry reference",
				"quay-proxy.ci.openshift.org/openshift/ci:ocp_builder_rhel-9-golang-1.22-openshift-4.19",
			},
		},
		{
			name:    "skip auto-detect when manual inputs.as defined",
			args:    []string{"--target=manual-inputs"},
			success: true,
			output: []string{
				"Skipping Dockerfile inputs detection: manual inputs defined",
				"registry.ci.openshift.org/ocp/4.19:base",
			},
		},
		{
			name:    "auto-detect with COPY --from reference",
			args:    []string{"--target=auto-detect-copy-from"},
			success: true,
			output: []string{
				"Dockerfile-inputs: Detected registry reference",
				"registry.ci.openshift.org/ocp/4.19:base",
			},
		},
		{
			name:    "no auto-detect when no registry references",
			args:    []string{"--target=no-registry-refs"},
			success: true,
			output:  []string{},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(framework.LocalPullSecretFlag(t), framework.RemotePullSecretFlag(t))
			cmd.AddArgs(append(testCase.args, "--config=config.yaml")...)
			cmd.AddEnv("JOB_SPEC=" + defaultJobSpec)
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
