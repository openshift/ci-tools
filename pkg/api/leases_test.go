package api

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLeasesForTest(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		tests                  TestStepConfiguration
		targetAdditionalSuffix string
		expected               []StepLease
	}{{
		name:  "no configuration or cluster profile, no lease",
		tests: TestStepConfiguration{MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{}},
	}, {
		name: "cluster profile, lease",
		tests: TestStepConfiguration{
			MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ClusterProfileLiteral{
					Name:      "aws",
					LeaseType: "aws-quota-slice",
					Secret:    "cluster-secrets-aws",
				},
			},
		},
		expected: []StepLease{{
			ResourceType: "aws-quota-slice",
			Env:          DefaultLeaseEnv,
			Count:        1,
			ClusterProfile: &ClusterProfileLiteral{
				Name:      "aws",
				LeaseType: "aws-quota-slice",
				Secret:    "cluster-secrets-aws",
			},
		}},
	}, {
		name: "explicit configuration, lease",
		tests: TestStepConfiguration{
			MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
				Leases: []StepLease{{ResourceType: "aws-quota-slice"}},
			},
		},
		expected: []StepLease{{ResourceType: "aws-quota-slice"}},
	}, {
		name: "explicit configuration in step, lease",
		tests: TestStepConfiguration{
			MultiStageTestConfigurationLiteral: &MultiStageTestConfigurationLiteral{
				Test: []LiteralTestStep{
					{Leases: []StepLease{{ResourceType: "aws-quota-slice"}}},
				},
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
			name: "aws",
			tests: MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ClusterProfileLiteral{
					Name:            "aws-us-east-1",
					LeaseType:       "aws-quota-slice",
					IPPoolLeaseType: "aws-ip-pools",
					Secret:          "cluster-secrets-aws",
				},
			},
			metadata: Metadata{Branch: "master"},
			expected: StepLease{
				ResourceType: "aws-ip-pools",
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			},
		},
		{
			name: "other cluster profile",
			tests: MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ClusterProfileLiteral{
					Name:      "aws-2",
					LeaseType: "aws-quota-slice",
					Secret:    "cluster-secrets-aws",
				},
			},
		},
		{
			name: "aws, with 4.16 branch",
			tests: MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ClusterProfileLiteral{
					Name:            "aws-us-east-1",
					LeaseType:       "aws-quota-slice",
					IPPoolLeaseType: "aws-ip-pools",
					Secret:          "cluster-secrets-aws",
				},
			},
			metadata: Metadata{Branch: "release-4.16"},
			expected: StepLease{
				ResourceType: "aws-ip-pools",
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			},
		},
		{
			name: "aws, but older release branch",
			tests: MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ClusterProfileLiteral{
					Name:      "aws-us-east-1",
					LeaseType: "aws-quota-slice",
					Secret:    "cluster-secrets-aws",
				},
			},
			metadata: Metadata{Branch: "release-4.10"},
		},
		{
			name: "aws, with 5.0 branch (should be valid)",
			tests: MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ClusterProfileLiteral{
					Name:            "aws-us-east-1",
					LeaseType:       "aws-quota-slice",
					IPPoolLeaseType: "aws-ip-pools",
					Secret:          "cluster-secrets-aws",
				},
			},
			metadata: Metadata{Branch: "release-5.0"},
			expected: StepLease{
				ResourceType: "aws-ip-pools",
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			},
		},
		{
			name: "aws, with openshift-5.0 branch (should be valid)",
			tests: MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ClusterProfileLiteral{
					Name:            "aws-us-east-1",
					LeaseType:       "aws-quota-slice",
					IPPoolLeaseType: "aws-ip-pools",
					Secret:          "cluster-secrets-aws",
				},
			},
			metadata: Metadata{Branch: "openshift-5.0"},
			expected: StepLease{
				ResourceType: "aws-ip-pools",
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			},
		},
		{
			name: "aws, with 5.1 branch (should be valid)",
			tests: MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ClusterProfileLiteral{
					Name:            "aws-us-east-1",
					LeaseType:       "aws-quota-slice",
					IPPoolLeaseType: "aws-ip-pools",
					Secret:          "cluster-secrets-aws",
				},
			},
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
			ret := IPPoolLeaseForTest(tc.tests.ClusterProfileLiteral.IPPoolLeaseType, tc.metadata.Branch)
			if diff := cmp.Diff(tc.expected, ret); diff != "" {
				t.Errorf("incorrect lease returned, diff: %s", diff)
			}
		})
	}
}
