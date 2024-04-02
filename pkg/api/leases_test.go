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
		expected StepLease
	}{
		{
			name:  "aws",
			tests: MultiStageTestConfigurationLiteral{ClusterProfile: ClusterProfileAWS},
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
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ret := IPPoolLeaseForTest(&tc.tests)
			if diff := cmp.Diff(tc.expected, ret); diff != "" {
				t.Errorf("incorrect lease returned, diff: %s", diff)
			}
		})
	}
}
