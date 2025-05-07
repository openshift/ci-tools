package onboard

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
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
							"pull-ci-openshift-release-main-build01-dry",
							"branch-ci-openshift-release-main-build01-apply",
							"periodic-openshift-release-main-build01-apply"}}},
			},
			expected: dispatcher.Config{
				Groups: dispatcher.JobGroups{
					api.ClusterAPPCI: dispatcher.Group{
						Jobs: []string{
							"branch-ci-openshift-release-main-build01-apply",
							"branch-ci-openshift-release-main-newcluster-apply",
							"periodic-openshift-release-main-build01-apply",
							"periodic-openshift-release-main-newcluster-apply",
							"pull-ci-openshift-release-main-build01-dry",
							"pull-ci-openshift-release-main-newcluster-dry"}}},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSanitizeProwjobStep(logrus.NewEntry(logrus.StandardLogger()), &clusterinstall.ClusterInstall{ClusterName: tc.clusterName})
			s.updateSanitizeProwJobsConfig(&tc.input)
			if diff := cmp.Diff(tc.expected, tc.input); diff != "" {
				t.Fatalf("expected jobs were different than results: %s", diff)
			}
		})
	}
}
