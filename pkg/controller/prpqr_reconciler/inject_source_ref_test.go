package prpqr_reconciler

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

// TestWithInjectedSourceRef is a regression test for /payload-job-with-prs runs that use
// --with-test-from to borrow a test whose own project image had to be carried over by
// api.ReleaseBuildConfiguration.WithPresubmitFrom (see pkg/api/config.go). That source
// repository must be checked out as an extra ref, or the carried-over image build has
// no source to build from.
func TestWithInjectedSourceRef(t *testing.T) {
	inject := &api.MetadataWithTest{
		Metadata: api.Metadata{Org: "openshift", Repo: "vcf-migration-operator", Branch: "main", Variant: "4.21"},
		Test:     "e2e-vsphere-vcf-migration",
	}
	prRefs := []prowv1.Refs{
		{Org: "openshift", Repo: "installer", BaseRef: "main"},
	}

	testCases := []struct {
		name       string
		refs       []prowv1.Refs
		ciopConfig *api.ReleaseBuildConfiguration
		expected   []prowv1.Refs
	}{
		{
			name:       "no images need the injected source: refs are untouched",
			refs:       prRefs,
			ciopConfig: &api.ReleaseBuildConfiguration{},
			expected:   prRefs,
		},
		{
			name: "an image needs the injected source: its repo is added as an extra ref",
			refs: prRefs,
			ciopConfig: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{
					Items: []api.ProjectDirectoryImageBuildStepConfiguration{
						{To: "vcf-migration-operator-openshift.vcf-migration-operator", Ref: "openshift.vcf-migration-operator"},
					},
				},
			},
			expected: append(append([]prowv1.Refs{}, prRefs...), prowv1.Refs{
				Org:     "openshift",
				Repo:    "vcf-migration-operator",
				BaseRef: "main",
			}),
		},
		{
			name: "the injected repo is already present: it is not added twice",
			refs: []prowv1.Refs{
				{Org: "openshift", Repo: "vcf-migration-operator", BaseRef: "main"},
			},
			ciopConfig: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{
					Items: []api.ProjectDirectoryImageBuildStepConfiguration{
						{To: "vcf-migration-operator-openshift.vcf-migration-operator", Ref: "openshift.vcf-migration-operator"},
					},
				},
			},
			expected: []prowv1.Refs{
				{Org: "openshift", Repo: "vcf-migration-operator", BaseRef: "main"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := withInjectedSourceRef(tc.refs, tc.ciopConfig, inject)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("refs differ from expected:\n%s", diff)
			}
		})
	}
}
