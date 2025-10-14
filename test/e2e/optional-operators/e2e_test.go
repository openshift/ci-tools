//go:build optional_operators
// +build optional_operators

package optional_operators

import (
	"fmt"
	"testing"

	"github.com/openshift/ci-tools/test/e2e/framework"
)

func TestOptionalOperators(t *testing.T) {
	var testCases = []struct {
		name       string
		indexName  string
		bundleName string
		target     string
	}{{
		name:       "unnamed bundle",
		indexName:  "ci-index",
		bundleName: "ci-bundle1",
		target:     "verify-db-unnamed",
	}, {
		name:       "named bundle",
		indexName:  "ci-index-named-bundle",
		bundleName: "named-bundle",
		target:     "verify-db-named",
	}, {
		name:       "ci-index-named-skip-index-bundle",
		bundleName: "named-skip-index-bundle",
		target:     "named-skip-index-bundle",
	}}
	for _, testCase := range testCases {
		testCase := testCase
		framework.Run(t, fmt.Sprintf("optional operators %s", testCase.name), func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(
				"--config=optional-operators.yaml",
				framework.LocalPullSecretFlag(t),
				framework.RemotePullSecretFlag(t),
				fmt.Sprintf("--target=%s", testCase.target),
			)
			cmd.AddEnv(`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"b439e7e55dcb924e8f372ae02566b5f7f003615d","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"quay-proxy.ci.openshift.org/openshift/ci:ci_clonerefs_latest","initupload":"quay-proxy.ci.openshift.org/openshift/ci:ci_initupload_latest","entrypoint":"quay-proxy.ci.openshift.org/openshift/ci:ci_entrypoint_latest","sidecar":"quay-proxy.ci.openshift.org/openshift/ci:ci_sidecar_latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`)
			cmd.AddEnv(framework.KubernetesClientEnv(t)...)
			output, err := cmd.Run()
			if err != nil {
				t.Fatalf("explicit var: didn't expect an error from ci-operator: %v; output:\n%v", err, string(output))
			}
			cmd.VerboseOutputContains(t, testCase.name, "Build src-bundle-amd64 succeeded after",
				fmt.Sprintf("Build %s-amd64 succeeded after", testCase.bundleName))

			fragments := []string{
				fmt.Sprintf("Build %s-gen-amd64 succeeded after", testCase.indexName),
				fmt.Sprintf("Build %s-amd64 succeeded after", testCase.indexName),
			}
			if testCase.indexName != "" {
				cmd.VerboseOutputContains(t, testCase.name, fragments...)
			} else {
				cmd.VerboseOutputDoesNotContain(t, testCase.name, fragments...)
			}

		})
	}
}
