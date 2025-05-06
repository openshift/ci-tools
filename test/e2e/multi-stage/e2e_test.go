//go:build e2e
// +build e2e

package multi_stage

import (
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/openshift/ci-tools/test/e2e/framework"
)

const (
	defaultJobSpec  = `JOB_SPEC={"type":"presubmit","job":"pull-ci-test-test-master-success","buildid":"0","prowjobid":"uuid","refs":{"org":"test","repo":"test","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
	depsJobSpec     = `JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
	multiRefJobSpec = `JOB_SPEC={"type":"presubmit","job":"pull-ci-test-test-master-success","buildid":"0","prowjobid":"uuid","refs":{"org":"test","repo":"test","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[{"number":1234,"author":"a-developer","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]},"extra_refs":[{"org":"test","repo":"test","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[{"number":1234,"author":"a-developer","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]},{"org":"test","repo":"another","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[{"number":1298,"author":"a-developer","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]}],"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
)

func TestMultiStage(t *testing.T) {
	rawConfig, err := os.ReadFile("config.yaml")
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	var testCases = []struct {
		name     string
		args     []string
		env      []string
		success  bool
		needHive bool
		output   []string
	}{
		{
			name:    "fetching full config for simple container test from resolver",
			args:    []string{"--target=success"},
			env:     []string{defaultJobSpec},
			success: true,
			output:  []string{"Container test in pod success completed successfully"},
		},
		{
			name:    "target-additional-suffix set",
			args:    []string{"--target=success", "--target-additional-suffix=1"},
			env:     []string{defaultJobSpec},
			success: true,
			output:  []string{"Container test in pod success-1 completed successfully"},
		},
		{
			name: "multiple extra_refs supplied",
			//TODO(sgoeddel): Currently, multi-pr support only exists when also injecting a test. If/When this changes, this test can be updated
			args:    []string{"--target=success", "--with-test-from=test/another@master:success"},
			env:     []string{multiRefJobSpec},
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
			name:    "step with credentials",
			args:    []string{"--unresolved-config=config.yaml", "--target=with-credentials"},
			env:     []string{depsJobSpec},
			success: true,
			output:  []string{`Container test in pod with-credentials-consume completed successfully`},
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
		{
			name:     "non-claim test in config with a claim",
			args:     []string{"--unresolved-config=cluster-claim.yaml", "--target=e2e-no-claim"},
			env:      []string{defaultJobSpec},
			needHive: false,
			success:  true,
			output:   []string{`e2e-no-claim-step succeeded`},
		},
		{
			name:     "e2e-claim",
			args:     []string{"--unresolved-config=cluster-claim.yaml", "--target=e2e-claim"},
			env:      []string{defaultJobSpec},
			needHive: true,
			success:  true,
			output:   []string{`Imported release 4.18.`, `to tag release:latest-e2e-claim`, `e2e-claim-claim-step succeeded`},
		},
		{
			name:     "e2e-claim-as-custom",
			args:     []string{"--unresolved-config=cluster-claim.yaml", "--target=e2e-claim-as-custom"},
			env:      []string{defaultJobSpec},
			needHive: true,
			success:  true,
			output:   []string{`Imported release 4.18.`, `to tag release:custom-e2e-claim-as-custom`, `e2e-claim-as-custom-claim-step succeeded`},
		},
		{
			name:     "e2e-claim depends on release image",
			args:     []string{"--unresolved-config=cluster-claim.yaml", "--target=e2e-claim-depend-on-release-image"},
			env:      []string{defaultJobSpec},
			needHive: true,
			success:  true,
			output:   []string{`Imported release 4.18.`, `to tag release:latest-e2e-claim-depend-on-release-image`, `e2e-claim-depend-on-release-image-claim-step succeeded`},
		},
		{
			name:    "assembled releases function",
			args:    []string{"--unresolved-config=integration-releases.yaml", "--target=verify-releases"},
			env:     []string{defaultJobSpec},
			success: true,
			output: []string{
				`Imported release 4.17.`, `images to tag release:initial`,
				`Snapshot integration stream into release 4.18.`, `-latest to tag release:latest`,
				`verify-releases-initial succeeded`, `verify-releases-initial-cli succeeded`,
				`verify-releases-latest succeeded`, `verify-releases-latest-cli succeeded`,
			},
		},
		{
			name:    "assembled release includes built image",
			args:    []string{"--unresolved-config=assembled-release.yaml", "--target=verify-releases"},
			env:     []string{defaultJobSpec},
			success: true,
			output: []string{
				`Snapshot integration stream into release 4.17.`, `-latest to tag release:latest`,
				`verify-releases-latest-cli succeeded`,
			},
		},
		{
			name:    "assembled release does not include built image when not asked to",
			args:    []string{"--unresolved-config=assembled-release-no-injection.yaml", "--target=verify-releases"},
			env:     []string{defaultJobSpec},
			success: true,
			output: []string{
				`Snapshot integration stream into release 4.17.`, `-latest to tag release:latest`,
				`verify-releases-latest-cli succeeded`,
			},
		},
		{
			name:    "request for increased shm-size",
			args:    []string{"--unresolved-config=config.yaml", "--target=shm-increase"},
			env:     []string{defaultJobSpec},
			success: true,
			output: []string{
				`Adding Dshm Volume to pod: shm-increase-step-with-increased-shm`,
				`Container test in pod shm-increase-step-with-increased-shm completed successfully`,
			},
		},
		{
			name: "pending timeout",
			args: []string{
				"--pod-pending-timeout", "30s",
				"--unresolved-config", "config.yaml",
				"--target", "pending",
			},
			env:    []string{defaultJobSpec},
			output: []string{`pod pending for more than 30s:`},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(framework.LocalPullSecretFlag(t), framework.RemotePullSecretFlag(t))
			cmd.AddArgs(testCase.args...)
			if testCase.needHive {
				cmd.AddArgs(framework.HiveKubeconfigFlag(t))
				// The job name will be used as claim name and e2e tests from different PRs make make claims from the same namespace.
				// The job name should be unique.
				testCase.env = []string{strings.Replace(defaultJobSpec, "uuid", string(uuid.NewUUID()), 1)}
			}
			cmd.AddEnv(testCase.env...)
			output, err := cmd.Run()
			if testCase.success != (err == nil) {
				t.Fatalf("%s: didn't expect an error from ci-operator: %v; output:\n%v", testCase.name, err, string(output))
			}
			cmd.VerboseOutputContains(t, testCase.name, testCase.output...)
		}, framework.ConfigResolver(framework.ConfigResolverOptions{
			ConfigPath:   "configs",
			RegistryPath: "registry",
			FlatRegistry: true,
		}))
	}
}
