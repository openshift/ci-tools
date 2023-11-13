package steps

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestDetermineRefsWorkdir(t *testing.T) {
	testCases := []struct {
		testName  string
		refs      *prowapi.Refs
		extraRefs []prowapi.Refs
		ref       string
		expected  *prowapi.Refs
	}{
		{
			testName:  "no workdir, nil Refs/ExtraRefs, expect nil",
			refs:      nil,
			extraRefs: nil,
			expected:  nil,
		},
		{
			testName: "no workdir, expect Refs",
			refs: &prowapi.Refs{
				Org:     "org",
				Repo:    "repo",
				BaseRef: "branch",
			},
			extraRefs: []prowapi.Refs{
				{
					Org:     "testOrg1",
					Repo:    "testRepo1",
					BaseRef: "master",
				},
				{

					Org:     "testOrg2",
					Repo:    "testRepo2",
					BaseRef: "master",
				},
				{

					Org:     "testOrg3",
					Repo:    "testRepo3",
					BaseRef: "master",
				},
			},
			expected: &prowapi.Refs{
				Org:     "org",
				Repo:    "repo",
				BaseRef: "branch",
			},
		},
		{
			testName: "for specific ref",
			extraRefs: []prowapi.Refs{
				{
					Org:     "testOrg1",
					Repo:    "testRepo1",
					BaseRef: "master",
				},
				{

					Org:     "testOrg2",
					Repo:    "testRepo2",
					BaseRef: "master",
				},
				{

					Org:     "testOrg3",
					Repo:    "testRepo3",
					BaseRef: "master",
				},
			},
			ref: "testOrg1.testRepo1",
			expected: &prowapi.Refs{
				Org:     "testOrg1",
				Repo:    "testRepo1",
				BaseRef: "master",
			},
		},
		{
			testName: "no workdir, nil Refs, expect extraRefs[0]",
			refs:     nil,
			extraRefs: []prowapi.Refs{
				{
					Org:     "testOrg1",
					Repo:    "testRepo1",
					BaseRef: "master",
				},
				{

					Org:     "testOrg2",
					Repo:    "testRepo2",
					BaseRef: "master",
				},
				{

					Org:     "testOrg3",
					Repo:    "testRepo3",
					BaseRef: "master",
				},
			},
			expected: &prowapi.Refs{
				Org:     "testOrg1",
				Repo:    "testRepo1",
				BaseRef: "master",
			},
		},
		{
			testName: "workdir, expect extraRefs with workdir",
			refs: &prowapi.Refs{
				Org:     "org",
				Repo:    "repo",
				BaseRef: "branch",
			},
			extraRefs: []prowapi.Refs{
				{
					Org:     "testOrg1",
					Repo:    "testRepo1",
					BaseRef: "master",
				},
				{

					Org:     "testOrg2",
					Repo:    "testRepo2",
					BaseRef: "master",
				},
				{
					Org:     "org-with-workdir",
					Repo:    "repo-with-workdir",
					BaseRef: "branch",
					WorkDir: true,
				},
				{

					Org:     "testOrg3",
					Repo:    "testRepo3",
					BaseRef: "master",
				},
			},
			expected: &prowapi.Refs{
				Org:     "org-with-workdir",
				Repo:    "repo-with-workdir",
				BaseRef: "branch",
				WorkDir: true,
			},
		},
		{
			testName: "workdir and different ref, expect defined ref",
			refs: &prowapi.Refs{
				Org:     "org",
				Repo:    "repo",
				BaseRef: "branch",
			},
			extraRefs: []prowapi.Refs{
				{
					Org:     "testOrg1",
					Repo:    "testRepo1",
					BaseRef: "master",
				},
				{
					Org:     "testOrg2",
					Repo:    "testRepo2",
					BaseRef: "master",
				},
				{
					Org:     "org-with-workdir",
					Repo:    "repo-with-workdir",
					BaseRef: "branch",
					WorkDir: true,
				},
				{
					Org:     "testOrg3",
					Repo:    "testRepo3",
					BaseRef: "master",
				},
			},
			ref: "testOrg3.testRepo3",
			expected: &prowapi.Refs{
				Org:     "testOrg3",
				Repo:    "testRepo3",
				BaseRef: "master",
			},
		},
		{
			testName: "workdir, nil refs, expect extraRefs with workdir",
			refs:     nil,
			extraRefs: []prowapi.Refs{
				{
					Org:     "testOrg1",
					Repo:    "testRepo1",
					BaseRef: "master",
				},
				{

					Org:     "testOrg2",
					Repo:    "testRepo2",
					BaseRef: "master",
				},
				{
					Org:     "org-with-workdir",
					Repo:    "repo-with-workdir",
					BaseRef: "branch",
					WorkDir: true,
				},
				{

					Org:     "testOrg3",
					Repo:    "testRepo3",
					BaseRef: "master",
				},
			},
			expected: &prowapi.Refs{
				Org:     "org-with-workdir",
				Repo:    "repo-with-workdir",
				BaseRef: "branch",
				WorkDir: true,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			step := gitSourceStep{config: api.ProjectDirectoryImageBuildInputs{Ref: testCase.ref}}
			ref := step.determineRefsWorkdir(testCase.refs, testCase.extraRefs)
			if !equality.Semantic.DeepEqual(ref, testCase.expected) {
				t.Errorf("Refs are different than expected: %v", diff.ObjectReflectDiff(ref, testCase.expected))
			}
		})
	}
}
