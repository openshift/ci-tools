// +build e2e

package simple

import (
	"bytes"
	"encoding/json"
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

	for _, tc := range testCases {
		testCase := tc
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			testDone := make(chan struct{})
			cleanupDone := make(chan struct{})
			defer func() {
				close(testDone)
				<-cleanupDone
			}()
			cmd, _ := framework.CiOperatorCommand(t)
			cmd.Args = append(cmd.Args, append(testCase.args, "--config=config.yaml")...)
			cmd.Env = append(cmd.Env, `JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}`)
			output, err := framework.Execute(t, cmd, testDone, cleanupDone)
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
	t.Parallel()
	clusterProfileDir := filepath.Join(t.TempDir(), "cluster-profile")
	if err := os.MkdirAll(clusterProfileDir, 0755); err != nil {
		t.Fatalf("failed to create dummy secret dir: %v", err)
	}
	if err := ioutil.WriteFile(filepath.Join(clusterProfileDir, "data"), []byte("nothing"), 0644); err != nil {
		t.Fatalf("failed to create dummy secret data: %v", err)
	}
	testDone, cleanupDone := make(chan struct{}), make(chan struct{})
	defer func() {
		close(testDone)
		<-cleanupDone
	}()
	cmd, artifactDir := framework.CiOperatorCommand(t)
	cmd.Args = append(cmd.Args,
		"--template=template.yaml",
		"--target=template",
		"--config=template-config.yaml",
		"--secret-dir="+clusterProfileDir,
	)
	cmd.Env = append(cmd.Env,
		`CLUSTER_TYPE=something`,
		`TEST_COMMAND=executable`,
		`JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}`,
	)
	output, err := framework.Execute(t, cmd, testDone, cleanupDone)
	if err != nil {
		t.Fatalf("ci-operator failed: %v; output:\n%v", err, string(output))
	}
	framework.CompareWithFixtureDir(t, "artifacts/template", filepath.Join(artifactDir, "template"))
	outputjUnit := filepath.Join(artifactDir, "junit_operator.xml")
	raw, err := ioutil.ReadFile(outputjUnit)
	if err != nil {
		t.Fatalf("could not read jUnit artifact: %v", err)
	}
	if err := ioutil.WriteFile(outputjUnit, timeRegex.ReplaceAll(raw, []byte(`time="whatever"`)), 0755); err != nil {
		t.Fatalf("could not munge jUnit artifact: %v", err)
	}
	framework.CompareWithFixture(t, "artifacts/junit_operator.xml", filepath.Join(artifactDir, "junit_operator.xml"))
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

	for _, tc := range testCases {
		testCase := tc
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			testDone := make(chan struct{})
			cleanupDone := make(chan struct{})
			defer func() {
				close(testDone)
				<-cleanupDone
			}()
			cmd, _ := framework.CiOperatorCommand(t)
			cmd.Args = append(cmd.Args,
				"--config=dynamic-releases.yaml",
				framework.LocalPullSecretFlag(t),
				"--target=[release:"+testCase.release+"]",
			)
			cmd.Env = append(cmd.Env, `JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}`)
			cmd.Env = append(cmd.Env, framework.KubernetesClientEnv(t)...)
			output, err := framework.Execute(t, cmd, testDone, cleanupDone)
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
	t.Parallel()
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
	testDone, cleanupDone := make(chan struct{}), make(chan struct{})
	defer func() {
		close(testDone)
		<-cleanupDone
	}()
	cmd, _ := framework.CiOperatorCommand(t)
	cmd.Args = append(cmd.Args,
		"--config=dynamic-releases.yaml",
		framework.LocalPullSecretFlag(t),
		framework.RemotePullSecretFlag(t),
		"--target=[release:latest]",
	)
	cmd.Env = append(cmd.Env, `JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}`)
	cmd.Env = append(cmd.Env, framework.KubernetesClientEnv(t)...)
	cmd.Env = append(cmd.Env, `RELEASE_IMAGE_LATEST=`+i.Nodes[0].Payload)
	output, err := framework.Execute(t, cmd, testDone, cleanupDone)
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
}

func TestOptionalOperators(t *testing.T) {
	t.Parallel()
	testDone, cleanupDone := make(chan struct{}), make(chan struct{})
	defer func() {
		close(testDone)
		<-cleanupDone
	}()
	cmd, _ := framework.CiOperatorCommand(t)
	cmd.Args = append(cmd.Args,
		"--config=optional-operators.yaml",
		framework.LocalPullSecretFlag(t),
		"--target=[images]",
		"--target=ci-index",
	)
	cmd.Env = append(cmd.Env, `JOB_SPEC={"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"886f493b3b7db24450e80d41a6d4c801b3b49881","pulls":[]}}`)
	cmd.Env = append(cmd.Env, framework.KubernetesClientEnv(t)...)
	output, err := framework.Execute(t, cmd, testDone, cleanupDone)
	if err != nil {
		t.Fatalf("explicit var: didn't expect an error from ci-operator: %v; output:\n%v", err, string(output))
	}
	for _, line := range []string{"Build src-bundle succeeded after", "Build ci-bundle0 succeeded after", "Build ci-index-gen succeeded after", "Build ci-index succeeded after"} {
		if !bytes.Contains(output, []byte(line)) {
			t.Errorf("optional operators: could not find line %q in output; output:\n%v", line, string(output))
		}
	}
}
