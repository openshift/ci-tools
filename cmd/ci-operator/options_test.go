package main

import (
	"reflect"
	"testing"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"k8s.io/utils/diff"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestGetResolverInfo(t *testing.T) {
	testCases := []struct {
		name     string
		opt      *options
		jobSpec  *api.JobSpec
		expected *api.Metadata
	}{{
		name: "Only JobSpec Refs",
		opt:  &options{},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &api.Metadata{
			Org:    "testOrganization",
			Repo:   "testRepo",
			Branch: "testBranch",
		},
	}, {
		name: "JobSpec Refs w/ variant set via flag",
		opt:  &options{variant: "v2"},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &api.Metadata{
			Org:     "testOrganization",
			Repo:    "testRepo",
			Branch:  "testBranch",
			Variant: "v2",
		},
	}, {
		name: "Ref with ExtraRefs",
		opt:  &options{},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
				ExtraRefs: []prowapi.Refs{{
					Org:     "anotherOrganization",
					Repo:    "anotherRepo",
					BaseRef: "anotherBranch",
				}},
			},
		},
		expected: &api.Metadata{
			Org:    "testOrganization,anotherOrganization",
			Repo:   "testRepo,anotherRepo",
			Branch: "testBranch,anotherBranch",
		},
	}, {
		name: "Incomplete refs not used",
		opt:  &options{},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					BaseRef: "testBranch",
				},
				ExtraRefs: []prowapi.Refs{{
					Org:     "anotherOrganization",
					Repo:    "anotherRepo",
					BaseRef: "anotherBranch",
				}},
			},
		},
		expected: &api.Metadata{
			Org:    "anotherOrganization",
			Repo:   "anotherRepo",
			Branch: "anotherBranch",
		},
	}, {
		name: "Refs with single field overridden by options",
		opt: &options{
			repo:    "anotherRepo",
			variant: "v2",
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &api.Metadata{
			Org:     "testOrganization",
			Repo:    "anotherRepo",
			Branch:  "testBranch",
			Variant: "v2",
		},
	}, {
		name: "Only options",
		opt: &options{
			org:     "testOrganization",
			repo:    "testRepo",
			branch:  "testBranch",
			variant: "v2",
		},
		jobSpec: &api.JobSpec{},
		expected: &api.Metadata{
			Org:     "testOrganization",
			Repo:    "testRepo",
			Branch:  "testBranch",
			Variant: "v2",
		},
	}, {
		name: "All fields overridden by options",
		opt: &options{
			org:     "anotherOrganization",
			repo:    "anotherRepo",
			branch:  "anotherBranch",
			variant: "v2",
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &api.Metadata{
			Org:     "anotherOrganization",
			Repo:    "anotherRepo",
			Branch:  "anotherBranch",
			Variant: "v2",
		},
	}, {
		name: "Refs and ExtraRefs with variant (MultiPRPresubmit case)",
		opt: &options{
			org:     "",
			repo:    "",
			branch:  "",
			variant: "v2",
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
				ExtraRefs: []prowapi.Refs{{
					Org:     "extraOrganization",
					Repo:    "extraRepo",
					BaseRef: "extraBranch",
				}},
			},
		},
		expected: &api.Metadata{
			Org:     "testOrganization,extraOrganization",
			Repo:    "testRepo,extraRepo",
			Branch:  "testBranch,extraBranch",
			Variant: "v2,",
		},
	}, {
		name: "Refs and 2 ExtraRefs with variant",
		opt: &options{
			org:     "",
			repo:    "",
			branch:  "",
			variant: "v2",
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
				ExtraRefs: []prowapi.Refs{{
					Org:     "extraOrganization",
					Repo:    "extraRepo",
					BaseRef: "extraBranch",
				}, {
					Org:     "moreOrganization",
					Repo:    "moreRepo",
					BaseRef: "moreBranch",
				}},
			},
		},
		expected: &api.Metadata{
			Org:     "testOrganization,extraOrganization,moreOrganization",
			Repo:    "testRepo,extraRepo,moreRepo",
			Branch:  "testBranch,extraBranch,moreBranch",
			Variant: "v2,,",
		},
	}}
	for _, testCase := range testCases {
		actual := testCase.opt.getResolverInfo(testCase.jobSpec)
		if !reflect.DeepEqual(actual, testCase.expected) {
			t.Errorf("%s: Actual does not match expected:\n%s", testCase.name, diff.ObjectReflectDiff(testCase.expected, actual))
		}
	}
}

func TestGetClusterProfileNamesFromTargets(t *testing.T) {
	profile1 := &api.ClusterProfileLiteral{Name: "profile1"}
	profile2 := &api.ClusterProfileLiteral{Name: "profile2"}

	testCases := []struct {
		name                 string
		options              *options
		expectedProfileNames []string
	}{
		{
			name: "single target, single profile",
			options: &options{
				targets: stringSlice{values: []string{"target-test-1"}},
				configSpec: &api.ReleaseBuildConfiguration{
					Tests: []api.TestStepConfiguration{
						{
							As: "target-test-1",
							MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
								ClusterProfileLiteral: profile1,
							},
						},
					},
				},
			},
			expectedProfileNames: []string{"profile1"},
		},
		{
			name: "multiple targets, multiple profiles",
			options: &options{
				targets: stringSlice{values: []string{"target-test-1", "target-test-2"}},
				configSpec: &api.ReleaseBuildConfiguration{
					Tests: []api.TestStepConfiguration{
						{
							As: "target-test-1",
							MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
								ClusterProfileLiteral: profile1,
							},
						},
						{
							As: "target-test-2",
							MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
								ClusterProfileLiteral: profile2,
							},
						},
					},
				},
			},
			expectedProfileNames: []string{"profile1", "profile2"},
		},
		{
			name: "target without cluster profile",
			options: &options{
				targets: stringSlice{values: []string{"target-test-1"}},
				configSpec: &api.ReleaseBuildConfiguration{
					Tests: []api.TestStepConfiguration{
						{
							As: "target-test-1",
						},
						{
							As:                                 "target-test-2",
							MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{},
						},
					},
				},
			},
			expectedProfileNames: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.options.getClusterProfileNamesFromTargets()
			reflect.DeepEqual(tc.expectedProfileNames, tc.options.clusterProfiles)
		})
	}
}
