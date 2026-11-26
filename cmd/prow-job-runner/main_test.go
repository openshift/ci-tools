package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/kube"
)

// TestRun checks the rendered jobs, including source config selection and target refs.
func TestRun(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	const prFile = "--pull-request-data=testdata/pr.json"
	const prefix = "pull-ci-openshift-release-main-"
	const e2e = "e2e-aws-ovn-techpreview"
	const e2eJob = prefix + "dynamic-tests-5.1-" + e2e
	const unitContext = "ci/prow/dynamic-second-stage/unit"
	const e2eContext = "ci/prow/dynamic-second-stage/dynamic-tests-5.1-" + e2e
	const planFlag = "--test-plan=testdata/test-plan.yaml"
	const jobSpec = `{"type":"presubmit","refs":{"org":"openshift","repo":"release","base_ref":"main","base_sha":"base-sha","pulls":[{"number":123,"sha":"head-sha","author":"contributor"}]}}`
	type renderedJob struct {
		Name    string
		Args    []string
		Context string
	}
	for _, tc := range []struct {
		name      string
		args      []string
		plan      string
		jobSpec   string
		repo      string
		branch    string
		pathAlias string
		namespace string
		jobs      []renderedJob
		wantErr   string
	}{
		{name: "external tests with and without variants in the requested namespace", args: []string{"--dry-run", prFile, planFlag, "--no-in-cluster-config", "--projected-token-file=unused", "--pj-namespace=ci-staging"}, repo: "installer", branch: "release-5.1", namespace: "ci-staging",
			jobs: []renderedJob{
				{e2eJob, []string{"--variant", "dynamic-tests-5.1", "--target=" + e2e, "--enable-secrets-store-csi-driver=true", "--with-test-from=openshift/release@main__dynamic-tests-5.1:" + e2e}, e2eContext},
				{prefix + "unit", []string{"--target=unit", "--with-test-from=openshift/release@main:unit"}, unitContext},
			}},
		{name: "Prow environment and same source config", args: []string{"--dry-run", planFlag}, jobSpec: jobSpec, repo: "release", branch: "main", pathAlias: "example.org/source",
			jobs: []renderedJob{{e2eJob, []string{"--variant", "dynamic-tests-5.1", "--target=" + e2e, "--enable-secrets-store-csi-driver=true"}, e2eContext}, {prefix + "unit", []string{"--target=unit"}, unitContext}}},
		{name: "same test from two source branches on the same PR", args: []string{"--dry-run", "--test-plan=testdata/two-branches.yaml"}, jobSpec: jobSpec, repo: "release", branch: "main",
			jobs: []renderedJob{
				{"pull-ci-openshift-release-release-5.1-unit", []string{"--target=unit", "--with-test-from=openshift/release@release-5.1:unit"}, unitContext},
				{prefix + "unit", []string{"--target=unit"}, unitContext},
			}},
		{name: "feature branch inherits source config", args: []string{"--dry-run"}, plan: "tests: [{organization: openshift, repository: release, branch: main, variant: dynamic-tests-5.1, test: " + e2e + "}]", jobSpec: strings.Replace(jobSpec, `"main"`, `"main-feature"`, 1), repo: "release", branch: "main-feature", pathAlias: "example.org/source",
			jobs: []renderedJob{{e2eJob, []string{"--variant", "dynamic-tests-5.1", "--target=" + e2e, "--enable-secrets-store-csi-driver=true"}, e2eContext}}},
		{name: "feature branch excluded from native job", args: []string{"--dry-run"}, plan: "tests: [{organization: openshift, repository: release, branch: main, variant: dynamic-tests-5.1, test: " + e2e + "}]", jobSpec: strings.Replace(jobSpec, `"main"`, `"main-excluded"`, 1), repo: "release", branch: "main-excluded",
			jobs: []renderedJob{{e2eJob, []string{"--variant", "dynamic-tests-5.1", "--target=" + e2e, "--enable-secrets-store-csi-driver=true", "--with-test-from=openshift/release@main__dynamic-tests-5.1:" + e2e}, e2eContext}}},
		{name: "live execution requires cluster credentials", args: []string{prFile, planFlag}, wantErr: "loading Prow cluster credentials"},
		{name: "live execution loads explicit kubeconfig", args: []string{prFile, planFlag, "--kubeconfig=testdata/pr.json"}, wantErr: `--kubeconfig="testdata/pr.json"`},
		{name: "suffix requires directory", args: []string{prFile, planFlag, "--kubeconfig-suffix=.config"}, wantErr: "--kubeconfig-dir must be set"},
		{name: "invalid namespace", args: []string{prFile, planFlag, "--pj-namespace=CI/Staging"}, wantErr: `invalid --pj-namespace "CI/Staging"`},
		{name: "mutually exclusive sources", args: []string{prFile, planFlag, "--pull-request=org/repo#1"}, wantErr: "mutually exclusive"},
		{name: "missing PR source", args: []string{planFlag}, wantErr: "provide --pull-request or --pull-request-data"},
		{name: "invalid PR", args: []string{planFlag, "--pull-request=org/repo#bad"}, wantErr: "invalid --pull-request"},
		{name: "invalid PR data", args: []string{planFlag, "--pull-request-data=testdata/core-services/prow/02_config/_config.yaml"}, wantErr: "decoding GitHub pull request"},
		{name: "no plan supplied", args: []string{prFile}, wantErr: "--test-plan is required"},
		{name: "invalid plan rejected before PR lookup", args: []string{"--pull-request-data=missing.json"}, plan: "tests: [{organization: openshift, repository: release, test: unit}]", wantErr: "tests[0]: branch is required"},
		{name: "missing release config", args: []string{"--dry-run", prFile, planFlag, "--release-repo=testdata/missing"}, wantErr: "loading Prow config"},
		{name: "unknown second test does not print first", args: []string{"--dry-run", prFile}, plan: "tests:\n- {organization: openshift, repository: release, branch: main, test: unit}\n- {organization: openshift, repository: release, branch: main, test: missing}", wantErr: "no matching presubmit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JOB_SPEC", tc.jobSpec)
			t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing-kubeconfig"))
			fs := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			args := append([]string{"--release-repo=testdata"}, tc.args...)
			if tc.plan != "" {
				path := filepath.Join(t.TempDir(), "plan.yaml")
				if err := os.WriteFile(path, []byte(tc.plan), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--test-plan", path)
			}
			var out bytes.Buffer
			err := run(t.Context(), fs, args, &out)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || out.Len() != 0 {
					t.Fatalf("want error containing %q and empty stdout; got %v, %q", tc.wantErr, err, &out)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoder := k8syaml.NewYAMLOrJSONDecoder(&out, 4096)
			wantNamespace := "ci"
			if tc.namespace != "" {
				wantNamespace = tc.namespace
			}
			var jobs []renderedJob
			for {
				var job prowapi.ProwJob
				err := decoder.Decode(&job)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if job.Status.State != prowapi.SchedulingState || job.Namespace != wantNamespace {
					t.Errorf("want scheduling state in namespace %s, got %s in %s", wantNamespace, job.Status.State, job.Namespace)
				}
				if !job.Spec.Report || job.Annotations[kube.ContextAnnotation] != job.Spec.Context {
					t.Errorf("%s: report=%t, context=%q, annotated context=%q", job.Spec.Job, job.Spec.Report, job.Spec.Context, job.Annotations[kube.ContextAnnotation])
				}
				refs := job.Spec.Refs
				if job.Kind != "ProwJob" || job.Spec.Type != prowapi.PresubmitJob || refs == nil || refs.Org != "openshift" || refs.Repo != tc.repo ||
					refs.BaseRef != tc.branch || refs.BaseSHA != "base-sha" || len(refs.Pulls) != 1 || refs.Pulls[0].Number != 123 || refs.Pulls[0].SHA != "head-sha" || refs.Pulls[0].Author != "contributor" {
					t.Fatalf("unexpected job type or refs: %s %s %+v", job.Kind, job.Spec.Type, refs)
				}
				if job.Spec.Job == e2eJob {
					if refs.PathAlias != tc.pathAlias || job.Spec.Cluster != "build02" || job.Labels["ci-operator.openshift.io/variant"] != "dynamic-tests-5.1" {
						t.Fatalf("lost source job settings: %+v", job)
					}
					wantEnv := []corev1.EnvVar{{Name: "FROM_PROW_PRESET", Value: "applied"}}
					if diff := cmp.Diff(wantEnv, job.Spec.PodSpec.Containers[0].Env); diff != "" {
						t.Errorf("unexpected environment from Prow presets (-want +got):\n%s", diff)
					}
				}
				jobs = append(jobs, renderedJob{job.Spec.Job, job.Spec.PodSpec.Containers[0].Args, job.Spec.Context})
			}
			if diff := cmp.Diff(tc.jobs, jobs); diff != "" {
				t.Errorf("unexpected jobs (-want +got):\n%s", diff)
			}
		})
	}
}
