// +build e2e

package simple

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/openshift/ci-tools/test/e2e/framework"
)

func TestSimpleExitCodes(t *testing.T) {
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

	for _, testCase := range testCases {
		testCase := testCase
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(append(testCase.args, "--config=config.yaml")...)
			cmd.AddEnv(`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"origin-ci-test","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`)
			output, err := cmd.Run()
			if testCase.success != (err == nil) {
				t.Fatalf("%s: didn't expect an error from ci-operator: %v; output:\n%v", testCase.name, err, string(output))
			}
			for _, line := range testCase.output {
				if !bytes.Contains(output, []byte(line)) {
					t.Errorf("%s: could not find line %q in output; output:\n%v", testCase.name, line, string(output))
				}
			}
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
		if err := ioutil.WriteFile(filepath.Join(clusterProfileDir, "data"), []byte("nothing"), 0644); err != nil {
			t.Fatalf("failed to create dummy secret data: %v", err)
		}
		cmd.AddArgs(
			"--template=template.yaml",
			"--target=template",
			"--config=template-config.yaml",
			"--secret-dir="+clusterProfileDir,
		)
		cmd.AddEnv(
			`CLUSTER_TYPE=something`,
			`TEST_COMMAND=executable`,
			`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"origin-ci-test","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`,
		)
		output, err := cmd.Run()
		if err != nil {
			t.Fatalf("ci-operator failed: %v; output:\n%v", err, string(output))
		}
		framework.CompareWithFixtureDir(t, "artifacts/template", filepath.Join(cmd.ArtifactDir(), "template"))
		outputjUnit := filepath.Join(cmd.ArtifactDir(), "junit_operator.xml")
		raw, err := ioutil.ReadFile(outputjUnit)
		if err != nil {
			t.Fatalf("could not read jUnit artifact: %v", err)
		}
		if err := ioutil.WriteFile(outputjUnit, timeRegex.ReplaceAll(raw, []byte(`time="whatever"`)), 0755); err != nil {
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
			cmd.AddEnv(`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"origin-ci-test","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`)
			cmd.AddEnv(framework.KubernetesClientEnv(t)...)
			output, err := cmd.Run()
			if err != nil {
				t.Fatalf("%s: ci-operator didn't exit as expected: %v; output:\n%v", testCase.name, err, string(output))
			}
			for _, line := range []string{`Resolved release ` + testCase.release + ` to`, `Imported release.*to tag release:` + testCase.release} {
				matcher, err := regexp.Compile(line)
				if err != nil {
					t.Errorf("%s: could not compile regex %q: %v", testCase.name, line, err)
					continue
				}
				if !matcher.Match(output) {
					t.Errorf("%s: could not find line %q in output; output:\n%v", testCase.name, line, string(output))
				}
			}
		})
	}
}

func TestLiteralDynamicRelease(t *testing.T) {
	var testCases = []struct {
		name    string
		release func(t *framework.T) string
	}{
		{
			name: "published release",
			release: func(t *framework.T) string {
				type info struct {
					Nodes []struct {
						Payload string `json:"payload"`
					} `json:"nodes"`
				}
				req, err := http.NewRequest(http.MethodGet, "https://api.openshift.com/api/upgrades_info/v1/graph?channel=stable-4.4&arch=amd64", nil)
				if err != nil {
					t.Fatalf("could not create request for Cincinnati: %v", err)
				}
				req.Header.Add("Accept", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("could not fetch release from Cincinnati: %v", err)
				}
				defer func() {
					if err := resp.Body.Close(); err != nil {
						t.Errorf("could not close response body: %v", err)
					}
				}()
				raw, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("could not read release from Cincinnati: %v", err)
				}
				var i info
				if err := json.Unmarshal(raw, &i); err != nil {
					t.Fatalf("could not parse release from Cincinnati: %v; raw:\n%v", err, string(raw))
				}
				if len(i.Nodes) < 1 {
					t.Fatalf("did not get a release from Cincinnati: raw:\n%v", string(raw))
				}
				return i.Nodes[0].Payload
			},
		},
		{
			name: "nightly release",
			release: func(t *framework.T) string {
				type info struct {
					PullSpec string `json:"pullSpec"`
				}
				req, err := http.NewRequest(http.MethodGet, "https://openshift-release.svc.ci.openshift.org/api/v1/releasestream/4.5.0-0.nightly/latest?rel=1", nil)
				if err != nil {
					t.Fatalf("could not create request for Cincinnati: %v", err)
				}
				req.Header.Add("Accept", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("could not fetch release from Cincinnati: %v", err)
				}
				defer func() {
					if err := resp.Body.Close(); err != nil {
						t.Errorf("could not close response body: %v", err)
					}
				}()
				raw, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("could not read release from Cincinnati: %v", err)
				}
				var i info
				if err := json.Unmarshal(raw, &i); err != nil {
					t.Fatalf("could not parse release from Cincinnati: %v; raw:\n%v", err, string(raw))
				}
				return i.PullSpec
			},
		},
	}
	for _, testCase := range testCases {
		framework.Run(t, testCase.name, func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(
				"--config=dynamic-releases.yaml",
				framework.LocalPullSecretFlag(t),
				framework.RemotePullSecretFlag(t),
				"--target=[release:latest]",
			)
			cmd.AddEnv(`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"origin-ci-test","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`)
			cmd.AddEnv(framework.KubernetesClientEnv(t)...)
			cmd.AddEnv(`RELEASE_IMAGE_LATEST=` + testCase.release(t))
			output, err := cmd.Run()
			if err != nil {
				t.Fatalf("explicit var: didn't expect an error from ci-operator: %v; output:\n%v", err, string(output))
			}
			for _, line := range []string{`Using explicitly provided pull-spec for release latest`, `Imported release.*to tag release:latest`} {
				matcher, err := regexp.Compile(line)
				if err != nil {
					t.Errorf("explicit var: could not compile regex %q: %v", line, err)
					continue
				}
				if !matcher.Match(output) {
					t.Errorf("explicit var: could not find line %q in output; output:\n%v", line, string(output))
				}
			}
		})
	}
}

func TestOptionalOperators(t *testing.T) {
	testCases := []struct {
		name       string
		indexName  string
		bundleName string
	}{{
		name:       "unnamed bundle",
		indexName:  "ci-index",
		bundleName: "ci-bundle0",
	}, {
		name:       "named bunlde",
		indexName:  "ci-index-named-bundle",
		bundleName: "named-bundle",
	}}
	for _, testCase := range testCases {
		framework.Run(t, fmt.Sprintf("optional operators %s", testCase.name), func(t *framework.T, cmd *framework.CiOperatorCommand) {
			cmd.AddArgs(
				"--config=optional-operators.yaml",
				framework.LocalPullSecretFlag(t),
				framework.RemotePullSecretFlag(t),
				"--target=[images]",
				fmt.Sprintf("--target=%s", testCase.indexName),
			)
			cmd.AddEnv(`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"886f493b3b7db24450e80d41a6d4c801b3b49881","pulls":[]},"decoration_config":{"timeout":"4h0m0s","grace_period":"30m0s","utility_images":{"clonerefs":"registry.ci.openshift.org/ci/clonerefs:latest","initupload":"registry.ci.openshift.org/ci/initupload:latest","entrypoint":"registry.ci.openshift.org/ci/entrypoint:latest","sidecar":"registry.ci.openshift.org/ci/sidecar:latest"},"resources":{"clonerefs":{"limits":{"memory":"3Gi"},"requests":{"cpu":"100m","memory":"500Mi"}},"initupload":{"limits":{"memory":"200Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"place_entrypoint":{"limits":{"memory":"100Mi"},"requests":{"cpu":"100m","memory":"25Mi"}},"sidecar":{"limits":{"memory":"2Gi"},"requests":{"cpu":"100m","memory":"250Mi"}}},"gcs_configuration":{"bucket":"origin-ci-test","path_strategy":"single","default_org":"openshift","default_repo":"origin","mediaTypes":{"log":"text/plain"}},"gcs_credentials_secret":"gce-sa-credentials-gcs-publisher"}}`)
			cmd.AddEnv(framework.KubernetesClientEnv(t)...)
			output, err := cmd.Run()
			if err != nil {
				t.Fatalf("explicit var: didn't expect an error from ci-operator: %v; output:\n%v", err, string(output))
			}
			for _, line := range []string{
				"Build src-bundle succeeded after",
				fmt.Sprintf("Build %s succeeded after", testCase.bundleName),
				fmt.Sprintf("Build %s-gen succeeded after", testCase.indexName),
				fmt.Sprintf("Build %s succeeded after", testCase.indexName)} {
				if !bytes.Contains(output, []byte(line)) {
					t.Errorf("optional operators: could not find line %q in output; output:\n%v", line, string(output))
				}
			}
		})
	}
}
