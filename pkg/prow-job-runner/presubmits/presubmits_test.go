package presubmits

import (
	"strings"
	"testing"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

// TestResolve checks that only the requested source branch, test, and variant match.
func TestResolve(t *testing.T) {
	const mainJob = "pull-ci-org-source-main-fast-aws-unit"
	const releaseJob = "pull-ci-org-source-release-fast-aws-unit"
	jobs := []prowconfig.Presubmit{
		{JobBase: prowconfig.JobBase{Name: mainJob, SourcePath: "org-source-main-presubmits.yaml", Labels: map[string]string{jobconfig.ProwJobLabelVariant: "fast-aws"}}},
		{JobBase: prowconfig.JobBase{Name: mainJob, SourcePath: "org-source-main-presubmits.yaml"}},
		{JobBase: prowconfig.JobBase{Name: releaseJob, SourcePath: "org-source-release-presubmits.yaml", Labels: map[string]string{jobconfig.ProwJobLabelVariant: "fast-aws"}}},
	}
	for _, tc := range []struct {
		name, branch, variant, test, wantJob, wantErr string
	}{
		{name: "requested PR branch", branch: "main", variant: "fast-aws", test: "unit", wantJob: mainJob},
		{name: "default variant with hyphenated test name", branch: "main", test: "fast-aws-unit", wantJob: mainJob},
		{name: "requested branch differs from PR", branch: "release", variant: "fast-aws", test: "unit", wantJob: releaseJob},
		{name: "unknown branch cannot fall back to PR branch", branch: "other", variant: "fast-aws", test: "unit", wantErr: "no matching presubmit"},
		{name: "wrong variant with same generated name", branch: "main", variant: "fast", test: "aws-unit", wantErr: "no matching presubmit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := api.MetadataWithTest{Metadata: api.Metadata{Org: "org", Repo: "source", Branch: tc.branch, Variant: tc.variant}, Test: tc.test}
			job, err := resolve(jobs, source, &prowapi.Refs{Org: "org", Repo: "source", BaseRef: "main"}, false)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil || job.Spec.Job != tc.wantJob {
				t.Fatalf("want %s, got %v, %v", tc.wantJob, job, err)
			}
			if got := job.Labels[jobconfig.ProwJobLabelVariant]; got != tc.variant {
				t.Errorf("want variant %q, got %q", tc.variant, got)
			}
			if job.Status.State != prowapi.TriggeredState {
				t.Errorf("want %s with scheduling disabled, got %s", prowapi.TriggeredState, job.Status.State)
			}
		})
	}
}

// TestForTests checks the inputs that can be handled without loading a release checkout.
func TestForTests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tests   []api.MetadataWithTest
		wantErr string
	}{
		{name: "empty plan"},
		{name: "no refs for requested test", tests: []api.MetadataWithTest{{Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "main"}, Test: "unit"}}, wantErr: "pull request refs are required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs, err := ForTests(t.TempDir(), tc.tests, nil)
			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != tc.wantErr || len(jobs) != 0 {
				t.Fatalf("want no jobs and error %q, got %v, %v", tc.wantErr, jobs, err)
			}
		})
	}
}
