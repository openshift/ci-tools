package prowjobrunner

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

// failedWriter rejects manifest writes without external I/O.
type failedWriter struct{}

// Write returns the same error for every write attempt.
func (failedWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

// TestValidate checks required inputs and Kubernetes flag validation before job configuration.
func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opts     Options
		kubeArgs []string
		wantErr  string
	}{
		{name: "dry run", opts: Options{ReleaseRepo: "release", TestPlan: "plan.yaml", DryRun: true}},
		{name: "live run", opts: Options{ReleaseRepo: "release", TestPlan: "gs://plans/plan.yaml"}},
		{name: "namespace override", opts: Options{ReleaseRepo: "release", TestPlan: "plan.yaml", ProwJobNamespace: "ci-staging"}},
		{name: "missing release checkout", wantErr: "--release-repo must be a non-empty path"},
		{name: "missing test plan", opts: Options{ReleaseRepo: "release"}, wantErr: "--test-plan is required"},
		{name: "mutually exclusive PR inputs", opts: Options{ReleaseRepo: "release", TestPlan: "plan.yaml", PullRequest: "org/repo#1", PullRequestData: "pr.json"}, wantErr: "--pull-request and --pull-request-data are mutually exclusive"},
		{name: "invalid namespace", opts: Options{ReleaseRepo: "release", TestPlan: "plan.yaml", ProwJobNamespace: "CI/Staging"}, wantErr: `invalid --pj-namespace "CI/Staging"`},
		{name: "invalid Kubernetes flags", opts: Options{ReleaseRepo: "release", TestPlan: "plan.yaml"}, kubeArgs: []string{"--kubeconfig-suffix=.config"}, wantErr: "validating Kubernetes options: --kubeconfig-dir must be set if --kubeconfig-suffix is set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			fs := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			opts.Kubernetes.AddFlags(fs)
			if err := fs.Parse(tc.kubeArgs); err != nil {
				t.Fatal(err)
			}
			err := opts.validate()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestWriteManifests checks ordered YAML documents and propagation of output errors.
func TestWriteManifests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		jobs    []string
		fail    bool
		wantErr string
	}{
		{name: "empty job list"},
		{name: "one job", jobs: []string{"first"}},
		{name: "ordered jobs", jobs: []string{"first", "second"}},
		{name: "failed output", jobs: []string{"first", "second"}, fail: true, wantErr: "writing first: io: read/write on closed pipe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var jobs []*prowapi.ProwJob
			for _, name := range tc.jobs {
				jobs = append(jobs, &prowapi.ProwJob{Spec: prowapi.ProwJobSpec{Job: name}})
			}
			var buf bytes.Buffer
			var out io.Writer = &buf
			if tc.fail {
				out = failedWriter{}
			}
			err := writeManifests(out, jobs)
			if tc.wantErr != "" {
				if !errors.Is(err, io.ErrClosedPipe) || err.Error() != tc.wantErr {
					t.Fatalf("want error %q wrapping a closed pipe, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []*prowapi.ProwJob
			decoder := k8syaml.NewYAMLOrJSONDecoder(&buf, 4096)
			for {
				var job prowapi.ProwJob
				err := decoder.Decode(&job)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, &job)
			}
			if diff := cmp.Diff(jobs, got); diff != "" {
				t.Errorf("unexpected manifests (-want +got):\n%s", diff)
			}
		})
	}
}
