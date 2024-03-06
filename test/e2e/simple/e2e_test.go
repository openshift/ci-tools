//go:build e2e
// +build e2e

package simple

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/hashicorp/go-retryablehttp"

	"github.com/openshift/ci-tools/test/e2e/framework"
)

func TestSimpleExitCodes(t *testing.T) {

	const defaultJobSpec = `{"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
	const jobSpecWithSkipClone = `{"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"skip_cloning":true,"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`
	var testCases = []struct {
		name    string
		args    []string
		success bool
		jobSpec string
		output  []string
	}{
		{
			name:    "success on one successful target",
			args:    []string{"--target=success"},
			success: true,
			output:  []string{"Container test in pod success completed successfully"},
		},
		{
			name:    "failure on one successful and one failed target",
			args:    []string{"--target=success", "--target=failure"},
			success: false,
			output:  []string{"Container test in pod success completed successfully", "Container test in pod failure failed, exit code 1, reason Error"},
		},
		{
			name:    "failure on one failed target",
			args:    []string{"--target=failure"},
			success: false,
			output:  []string{"Container test in pod failure failed, exit code 1, reason Error"},
		},
		{
			name:    "implicit cloning",
			args:    []string{"--target=container-test-from-base-image-implicitly-clones"},
			success: true,
		},
		{
			name:    "implicit cloning can be disabled",
			args:    []string{"--target=container-test-from-base-image-without-cloning-doesnt-clone"},
			success: true,
		},
		{
			name:    "implicit cloning on a job spec that skips cloning",
			args:    []string{"--target=container-test-from-base-image-implicitly-clones"},
			jobSpec: jobSpecWithSkipClone,
			success: true,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(framework.LocalPullSecretFlag(t), framework.RemotePullSecretFlag(t))
			cmd.AddArgs(append(testCase.args, "--config=config.yaml")...)
			if testCase.jobSpec == "" {
				testCase.jobSpec = defaultJobSpec
			}
			cmd.AddEnv("JOB_SPEC=" + testCase.jobSpec)
			output, err := cmd.Run()
			if testCase.success != (err == nil) {
				t.Fatalf("%s: didn't expect an error from ci-operator: %v; output:\n%v", testCase.name, err, string(output))
			}
			cmd.VerboseOutputContains(t, testCase.name, testCase.output...)
		})
	}
}

func TestCompressed(t *testing.T) {
	var testCases = []struct {
		name    string
		args    []string
		success bool
		output  []string
	}{
		{
			name:    "success on one successful target",
			args:    []string{"--target=success"},
			success: true,
			output:  []string{"Container test in pod success completed successfully"},
		},
		{
			name:    "failure on one successful and one failed target",
			args:    []string{"--target=success", "--target=failure"},
			success: false,
			output:  []string{"Container test in pod success completed successfully", "Container test in pod failure failed, exit code 1, reason Error"},
		},
		{
			name:    "failure on one failed target",
			args:    []string{"--target=failure"},
			success: false,
			output:  []string{"Container test in pod failure failed, exit code 1, reason Error"},
		},
	}

	configFile, err := os.ReadFile("compressedConfig.txt")
	if err != nil {
		t.Fatalf("Failed to read compressed config file: %v", err)
	}

	for _, testCase := range testCases {
		testCase := testCase
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(framework.LocalPullSecretFlag(t), framework.RemotePullSecretFlag(t))
			cmd.AddArgs(testCase.args...)
			cmd.AddEnv(fmt.Sprintf("CONFIG_SPEC=%s", string(configFile)))
			cmd.AddEnv(`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e-compressed","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`)
			output, err := cmd.Run()
			if testCase.success != (err == nil) {
				t.Fatalf("%s: didn't expect an error from ci-operator: %v; output:\n%v", testCase.name, err, string(output))
			}
			cmd.VerboseOutputContains(t, testCase.name, testCase.output...)
		})
	}
}

var timeRegex = regexp.MustCompile(`time=".*"`)

func TestTemplate(t *testing.T) {
	framework.Run(t, "template", func(t *framework.T, cmd *framework.CiOperatorCommand) {
		clusterProfileDir := filepath.Join(t.TempDir(), "cluster-profile")
		if err := os.MkdirAll(clusterProfileDir, 0755); err != nil {
			t.Fatalf("failed to create dummy secret dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(clusterProfileDir, "data"), []byte("nothing"), 0644); err != nil {
			t.Fatalf("failed to create dummy secret data: %v", err)
		}
		cmd.AddArgs(framework.LocalPullSecretFlag(t), framework.RemotePullSecretFlag(t))
		cmd.AddArgs(
			"--template=template.yaml",
			"--target=template",
			"--config=template-config.yaml",
			"--secret-dir="+clusterProfileDir,
		)
		cmd.AddEnv(
			`CLUSTER_TYPE=something`,
			`TEST_COMMAND=executable`,
			`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`,
		)
		output, err := cmd.Run()
		if err != nil {
			t.Fatalf("ci-operator failed: %v; output:\n%v", err, string(output))
		}
		framework.CompareWithFixtureDir(t, "artifacts/template", filepath.Join(cmd.ArtifactDir(), "template"))
		outputjUnit := filepath.Join(cmd.ArtifactDir(), "junit_operator.xml")
		raw, err := os.ReadFile(outputjUnit)
		if err != nil {
			t.Fatalf("could not read jUnit artifact: %v", err)
		}
		if err := os.WriteFile(outputjUnit, timeRegex.ReplaceAll(raw, []byte(`time="whatever"`)), 0755); err != nil {
			t.Fatalf("could not munge jUnit artifact: %v", err)
		}
		framework.CompareWithFixture(t, "artifacts/junit_operator.xml", filepath.Join(cmd.ArtifactDir(), "junit_operator.xml"))
	})
}

func TestDynamicReleases(t *testing.T) {
	var testCases = []struct {
		name    string
		release string
	}{
		{
			name:    "success on okd release",
			release: "initial",
		},
		{
			name:    "success on stable release",
			release: "latest",
		},
		{
			name:    "success on nightly release",
			release: "custom",
		},
		{
			name:    "success on prerelease release",
			release: "pre",
		},
		{
			name:    "successfully imports release for different arch",
			release: "mainframe",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(
				"--config=dynamic-releases.yaml",
				framework.LocalPullSecretFlag(t),
				framework.RemotePullSecretFlag(t),
				"--target=[release:"+testCase.release+"]",
			)
			cmd.AddEnv(`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`)
			cmd.AddEnv(framework.KubernetesClientEnv(t)...)
			output, err := cmd.Run()
			if err != nil {
				t.Fatalf("%s: ci-operator didn't exit as expected: %v; output:\n%v", testCase.name, err, string(output))
			}
			cmd.VerboseOutputContains(t, testCase.name, `Resolved release `+testCase.release+` to`, `to tag release:`+testCase.release)
		})
	}
}

func resolveOfficialSpec(url string) func(*framework.T) string {
	return func(t *framework.T) string {
		type info struct {
			Nodes []struct {
				Payload string `json:"payload"`
			} `json:"nodes"`
		}
		var i info
		raw := do(url, t)
		if err := json.Unmarshal(raw, &i); err != nil {
			t.Fatalf("could not parse release from Cincinnati: %v; raw:\n%v", err, string(raw))
		}
		if len(i.Nodes) < 1 {
			t.Fatalf("did not get a release from Cincinnati: raw:\n%v", string(raw))
		}
		return i.Nodes[0].Payload
	}
}

func resolveSpec(url string) func(*framework.T) string {
	return func(t *framework.T) string {
		type info struct {
			PullSpec string `json:"pullSpec"`
		}
		var i info
		raw := do(url, t)
		if err := json.Unmarshal(raw, &i); err != nil {
			t.Fatalf("could not parse release from Cincinnati: %v; raw:\n%v", err, string(raw))
		}
		return i.PullSpec
	}
}

func do(url string, t *framework.T) []byte {
	client := retryablehttp.NewClient()
	req, err := retryablehttp.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("could not create request for Cincinnati: %v", err)
	}
	req.Header.Add("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("could not fetch release from Cincinnati: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("could not close response body: %v", err)
		}
	}()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("could not read release from Cincinnati: %v", err)
	}
	return raw
}

func TestLiteralDynamicRelease(t *testing.T) {
	var testCases = []struct {
		name    string
		release func(t *framework.T) string
		target  string
	}{
		{
			name:    "published release",
			release: resolveOfficialSpec("https://api.openshift.com/api/upgrades_info/v1/graph?channel=stable-4.4&arch=amd64"),
			target:  "latest",
		},
		{
			name:    "nightly release",
			release: resolveSpec("https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/4.10.0-0.nightly/latest?rel=1"),
			target:  "latest",
		},
		{
			name:    "non-x86 release",
			release: resolveSpec("https://s390x.ocp.releases.ci.openshift.org/api/v1/releasestream/4.9.0-0.nightly-s390x/latest?rel=1"),
			target:  "mainframe",
		},
		{
			name:    "built release",
			release: resolveSpec("https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/4.10.0-0.nightly/latest?rel=1"),
			target:  "assembled",
		},
	}
	for _, testCase := range testCases {
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("test panicked, stacktrace:\n%s", string(debug.Stack()))
				}
			}()
			cmd.AddArgs(
				"--config=dynamic-releases.yaml",
				framework.LocalPullSecretFlag(t),
				framework.RemotePullSecretFlag(t),
				fmt.Sprintf("--target=[release:%s]", testCase.target),
			)
			cmd.AddEnv(`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"test-platform-results","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`)
			cmd.AddEnv(framework.KubernetesClientEnv(t)...)
			cmd.AddEnv(fmt.Sprintf("RELEASE_IMAGE_%s=%s", strings.ToUpper(testCase.target), testCase.release(t)))
			output, err := cmd.Run()
			if err != nil {
				t.Fatalf("explicit var: didn't expect an error from ci-operator: %v; output:\n%v", err, string(output))
			}
			cmd.VerboseOutputContains(t, testCase.name, `Using explicitly provided pull-spec for release `+testCase.target, `to tag release:`+testCase.target)
		})
	}
}
