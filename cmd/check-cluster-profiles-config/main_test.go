package main

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestValidate(t *testing.T) {
	var testCases = []struct {
		name     string
		expected error
		profiles api.ClusterProfilesList
	}{
		{
			name: "Empty config file",
		},
		{
			name: "Valid config file",
			profiles: api.ClusterProfilesList{
				api.ClusterProfileDetails{
					Profile: "aws",
					Owners:  []api.ClusterProfileOwners{{Org: "aws", Repos: []string{"repo1"}}},
				},
				api.ClusterProfileDetails{
					Profile: "gcp",
					Owners:  []api.ClusterProfileOwners{{Org: "gcp-org"}},
				},
				api.ClusterProfileDetails{Profile: "aws2"},
			},
		},
		{
			name: "Duplicated profile in config file",
			profiles: api.ClusterProfilesList{
				api.ClusterProfileDetails{
					Profile: "aws",
					Owners:  []api.ClusterProfileOwners{{Org: "aws", Repos: []string{"repo1"}}},
				},
				api.ClusterProfileDetails{
					Profile: "gcp",
					Owners:  []api.ClusterProfileOwners{{Org: "gcp-org"}},
				},
				api.ClusterProfileDetails{Profile: "aws"},
				api.ClusterProfileDetails{Profile: "gcp2"},
			},
			expected: fmt.Errorf("cluster profile 'aws' already exists in the configuration file"),
		},
	}

	validator := newValidator(fakectrlruntimeclient.NewFakeClient())

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validator.Validate(tc.profiles)
			if diff := cmp.Diff(tc.expected, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%v", diff)
			}
		})
	}
}
