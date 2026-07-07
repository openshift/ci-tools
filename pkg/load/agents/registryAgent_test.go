package agents

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGetClusterProfile(t *testing.T) {
	agent := &registryAgent{
		clusterProfiles: api.ClusterProfiles{
			Items: []api.ClusterProfile{{
				Name: "aws",
				Owners: []api.ClusterProfileOwners{{
					Org:   "openshift",
					Repos: []string{"release"},
				}},
				ClusterType: "aws",
				LeaseType:   "aws-quota-slice",
				Secret:      "cluster-secrets-aws",
			}},
		},
	}

	testCases := []struct {
		name          string
		profileName   string
		expected      api.ClusterProfile
		expectedError error
	}{
		{
			name:        "profile found",
			profileName: "aws",
			expected: api.ClusterProfile{
				Name: "aws",
				Owners: []api.ClusterProfileOwners{{
					Org:   "openshift",
					Repos: []string{"release"},
				}},
				ClusterType: "aws",
				LeaseType:   "aws-quota-slice",
				Secret:      "cluster-secrets-aws",
			},
		},
		{
			name:          "profile not found",
			profileName:   "aws-2",
			expectedError: fmt.Errorf("cluster profile aws-2 not found"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := agent.ResolveClusterProfile(tc.profileName)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Errorf("result differs from expected: %v", diff)
			}
		})
	}
}
