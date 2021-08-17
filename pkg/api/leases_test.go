package api

import (
	"testing"

	"k8s.io/utils/diff"
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
			if diff := diff.ObjectReflectDiff(tc.expected, ret); diff != "<no diffs>" {
				t.Errorf("incorrect leases: %s", diff)
			}
		})
	}
}
