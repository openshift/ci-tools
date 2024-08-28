package sanitizeprowjob

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
)

func TestUpdateConfig(t *testing.T) {
	testCases := []struct {
		name        string
		clusterName string
		input       dispatcher.Config
		expected    dispatcher.Config
	}{
		{
			name:        "update config",
			clusterName: "newcluster",
			input: dispatcher.Config{
				Groups: dispatcher.JobGroups{
					api.ClusterAPPCI: dispatcher.Group{
						Jobs: []string{
							"pull-ci-openshift-release-master-build01-dry",
							"branch-ci-openshift-release-master-build01-apply",
							"periodic-openshift-release-master-build01-apply"}}},
			},
			expected: dispatcher.Config{
				Groups: dispatcher.JobGroups{
					api.ClusterAPPCI: dispatcher.Group{
						Jobs: []string{
							"branch-ci-openshift-release-master-build01-apply",
							"branch-ci-openshift-release-master-newcluster-apply",
							"periodic-openshift-release-master-build01-apply",
							"periodic-openshift-release-master-newcluster-apply",
							"pull-ci-openshift-release-master-build01-dry",
							"pull-ci-openshift-release-master-newcluster-dry"}}},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			updateSanitizeProwJobsConfig(&tc.input, tc.clusterName)
			if diff := cmp.Diff(tc.expected, tc.input); diff != "" {
				t.Fatalf("expected jobs were different than results: %s", diff)
			}
		})
	}
}
