package testplan

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

// planBucket holds test plan objects by GCS URL for offline tests.
type planBucket map[string]string

// read returns the contents of the requested object.
func (b planBucket) read(_ context.Context, path string) ([]byte, error) {
	data, ok := b[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(data), nil
}

// TestRead checks local and GCS routing, ordered decoding, and validation of plan entries.
func TestRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plan    string
		local   bool
		want    []api.MetadataWithTest
		wantErr string
	}{
		{
			name: "ordered GCS entries with optional variant",
			plan: "tests:\n- {organization: kubernetes, repository: kubernetes, branch: release-1.35, variant: kind, test: e2e}\n- {organization: openshift, repository: installer, branch: main, test: unit}",
			want: []api.MetadataWithTest{
				{Metadata: api.Metadata{Org: "kubernetes", Repo: "kubernetes", Branch: "release-1.35", Variant: "kind"}, Test: "e2e"},
				{Metadata: api.Metadata{Org: "openshift", Repo: "installer", Branch: "main"}, Test: "unit"},
			},
		},
		{name: "missing GCS object", wantErr: fs.ErrNotExist.Error()},
		{name: "local file", local: true, plan: "tests: [{organization: openshift, repository: installer, branch: main, test: unit}]", want: []api.MetadataWithTest{{Metadata: api.Metadata{Org: "openshift", Repo: "installer", Branch: "main"}, Test: "unit"}}},
		{name: "missing local file", local: true, wantErr: "reading local file"},
		{name: "invalid YAML", plan: "tests: [", wantErr: "decoding YAML"},
		{name: "tests is not a list", plan: "tests: {test: e2e}", wantErr: "decoding YAML"},
		{name: "empty list", plan: "tests: []", wantErr: "non-empty tests list"},
		{name: "unknown field", plan: "tests: [{org: openshift}]", wantErr: "unknown field"},
		{name: "invalid entry identified by index", plan: "tests:\n- {organization: openshift, repository: installer, branch: main, test: unit}\n- {}", wantErr: "tests[1]: organization is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "gs://plans/pr/test-plan.yaml"
			bucket := planBucket{}
			if tc.local {
				path = filepath.Join(t.TempDir(), "test-plan.yaml")
				if tc.plan != "" {
					if err := os.WriteFile(path, []byte(tc.plan), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !tc.local && tc.plan != "" {
				bucket[path] = tc.plan
			}
			reader := Reader{ReadObject: bucket.read}
			if tc.local {
				reader = Reader{}
			}
			tests, err := reader.Read(t.Context(), path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, tests); diff != "" {
				t.Errorf("unexpected tests (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSource checks required fields and prevents plan coordinates from becoming path components such as '..'.
func TestSource(t *testing.T) {
	for _, tc := range []struct {
		name, org, repo, branch, test, variant, wantErr string
	}{
		{name: "missing organization", repo: "repo", branch: "main", test: "unit", wantErr: "organization is required"},
		{name: "missing repository", org: "org", branch: "main", test: "unit", wantErr: "repository is required"},
		{name: "missing branch", org: "org", repo: "repo", test: "unit", wantErr: "branch is required"},
		{name: "missing test", org: "org", repo: "repo", branch: "main", wantErr: "test is required"},
		{name: "invalid organization", org: "org/name", repo: "repo", branch: "main", test: "unit", wantErr: `invalid organization "org/name"`},
		{name: "unsafe repository", org: "org", repo: "../other", branch: "main", test: "unit", wantErr: `invalid repository "../other"`},
		{name: "dot branch", org: "org", repo: "repo", branch: ".", test: "unit", wantErr: `invalid branch "."`},
		{name: "invalid test name", org: "org", repo: "repo", branch: "main", test: "unit/special", wantErr: `invalid test "unit/special"`},
		{name: "parent directory variant", org: "org", repo: "repo", branch: "main", test: "unit", variant: "..", wantErr: `invalid variant ".."`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := entry{Organization: tc.org, Repository: tc.repo, Branch: tc.branch, Test: tc.test, Variant: tc.variant}
			if _, err := entry.source(); err == nil || err.Error() != tc.wantErr {
				t.Fatalf("want %q, got %v", tc.wantErr, err)
			}
		})
	}
}
