package api

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLeasesForTest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tests    MultiStageTestConfigurationLiteral
		expected []StepLease
	}{{
		name:  "no configuration or cluster profile, no lease",
		tests: MultiStageTestConfigurationLiteral{},
	}, {
		name: "cluster profile, lease",
		tests: MultiStageTestConfigurationLiteral{
			ClusterProfile: ClusterProfileAWS,
		},
		expected: []StepLease{{
			ResourceType: "aws-quota-slice",
			Env:          DefaultLeaseEnv,
			Count:        1,
		}},
	}, {
		name: "explicit configuration, lease",
		tests: MultiStageTestConfigurationLiteral{
			Leases: []StepLease{{ResourceType: "aws-quota-slice"}},
		},
		expected: []StepLease{{ResourceType: "aws-quota-slice"}},
	}, {
		name: "explicit configuration in step, lease",
		tests: MultiStageTestConfigurationLiteral{
			Test: []LiteralTestStep{
				{Leases: []StepLease{{ResourceType: "aws-quota-slice"}}},
			},
		},
		expected: []StepLease{{ResourceType: "aws-quota-slice"}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret := LeasesForTest(&tc.tests)
			if diff := cmp.Diff(tc.expected, ret); diff != "" {
				t.Errorf("incorrect leases, diff: %s", diff)
			}
		})
	}
}

func TestIPPoolLeaseForTest(t *testing.T) {
	testCases := []struct {
		name     string
		tests    MultiStageTestConfigurationLiteral
		metadata Metadata
		expected StepLease
	}{
		{
			name:     "aws",
			tests:    MultiStageTestConfigurationLiteral{ClusterProfile: ClusterProfileAWS},
			metadata: Metadata{Branch: "master"},
			expected: StepLease{
				ResourceType: "aws-ip-pools",
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			},
		},
		{
			name:  "other cluster profile",
			tests: MultiStageTestConfigurationLiteral{ClusterProfile: ClusterProfileAWS2},
		},
		{
			name:     "aws, with 4.16 branch",
			tests:    MultiStageTestConfigurationLiteral{ClusterProfile: ClusterProfileAWS},
			metadata: Metadata{Branch: "release-4.16"},
			expected: StepLease{
				ResourceType: "aws-ip-pools",
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			},
		},
		{
			name:     "aws, but older release branch",
			tests:    MultiStageTestConfigurationLiteral{ClusterProfile: ClusterProfileAWS},
			metadata: Metadata{Branch: "release-4.10"},
		},
		{
			name:     "aws, with 5.0 branch (should be valid)",
			tests:    MultiStageTestConfigurationLiteral{ClusterProfile: ClusterProfileAWS},
			metadata: Metadata{Branch: "release-5.0"},
			expected: StepLease{
				ResourceType: "aws-ip-pools",
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			},
		},
		{
			name:     "aws, with openshift-5.0 branch (should be valid)",
			tests:    MultiStageTestConfigurationLiteral{ClusterProfile: ClusterProfileAWS},
			metadata: Metadata{Branch: "openshift-5.0"},
			expected: StepLease{
				ResourceType: "aws-ip-pools",
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			},
		},
		{
			name:     "aws, with 5.1 branch (should be valid)",
			tests:    MultiStageTestConfigurationLiteral{ClusterProfile: ClusterProfileAWS},
			metadata: Metadata{Branch: "release-5.1"},
			expected: StepLease{
				ResourceType: "aws-ip-pools",
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ret := IPPoolLeaseForTest(&tc.tests, tc.metadata)
			if diff := cmp.Diff(tc.expected, ret); diff != "" {
				t.Errorf("incorrect lease returned, diff: %s", diff)
			}
		})
	}
}
