package prpqr_reconciler

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestRefsWithTestSource(t *testing.T) {
	t.Parallel()
	source := api.Metadata{Org: "openshift", Repo: "cluster-api-actuator-pkg", Branch: "release-5.0", Variant: "periodics"}
	pr := prowv1.Refs{Org: "openshift", Repo: "machine-api-operator", BaseRef: "main", BaseSHA: "base", WorkDir: true,
		Pulls: []prowv1.Pull{{Number: 1581, SHA: "first"}, {Number: 1582, SHA: "second"}}}
	for _, testCase := range []struct {
		name      string
		refs      []prowv1.Refs
		wantError bool
		added     bool
	}{
		{name: "borrowed non PR repository", refs: []prowv1.Refs{pr}, added: true},
		{name: "no PRs", added: true},
		{name: "source already a PR", refs: []prowv1.Refs{{Org: source.Org, Repo: source.Repo, BaseRef: source.Branch, BaseSHA: "base", Pulls: []prowv1.Pull{{Number: 1, SHA: "head"}}}}},
		{name: "conflicting branch", refs: []prowv1.Refs{{Org: source.Org, Repo: source.Repo, BaseRef: "main"}}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := refsWithTestSource(testCase.refs, source, "github.com/openshift/cluster-api-actuator-pkg")
			if (err != nil) != testCase.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil {
				return
			}
			expected := append([]prowv1.Refs(nil), testCase.refs...)
			if testCase.added {
				expected = append(expected, prowv1.Refs{Org: source.Org, Repo: source.Repo, BaseRef: source.Branch, PathAlias: "github.com/openshift/cluster-api-actuator-pkg"})
			}
			if diff := cmp.Diff(expected, actual); diff != "" {
				t.Errorf("refs changed: %s", diff)
			}
		})
	}
}
