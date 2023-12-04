package api

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestDomainForService(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		service  Service
		expected string
	}{
		{
			service:  ServiceBoskos,
			expected: "boskos-ci.apps.ci.l2s4.p1.openshiftapps.com",
		},
		{
			service:  ServiceRPMs,
			expected: "artifacts-rpms-openshift-origin-ci-rpms.apps.ci.l2s4.p1.openshiftapps.com",
		},
		{
			service:  ServiceRegistry,
			expected: "registry.ci.openshift.org",
		},
		{
			service:  ServiceProw,
			expected: "prow.ci.openshift.org",
		},
		{
			service:  ServiceConfig,
			expected: "config.ci.openshift.org",
		},
		{
			service:  ServiceGCSStorage,
			expected: "storage.googleapis.com",
		},
	}

	for _, tc := range testCases {
		t.Run(string(tc.service), func(t *testing.T) {
			if result := DomainForService(tc.service); result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestRegistryDomainForClusterName(t *testing.T) {
	testCases := []struct {
		name               string
		clusterName        string
		potentiallyPrivate string
		expected           string
		expectedError      error
	}{
		{
			name:        "app.ci",
			clusterName: "app.ci",
			expected:    "registry.ci.openshift.org",
		},
		{
			name:        "vsphere",
			clusterName: "vsphere02",
			expected:    "registry.apps.build02.vmc.ci.openshift.org",
		},
		{
			name:        "arm01",
			clusterName: "arm01",
			expected:    "registry.arm-build01.arm-build.devcluster.openshift.com",
		},
		{
			name:        "multi01",
			clusterName: "multi01",
			expected:    "registry.multi-build01.arm-build.devcluster.openshift.com",
		},
		{
			name:        "build01",
			clusterName: "build01",
			expected:    "registry.build01.ci.openshift.org",
		},
		{
			name:        "build99",
			clusterName: "build99",
			expected:    "registry.build99.ci.openshift.org",
		},
		{
			name:          "b01",
			clusterName:   "b01",
			expectedError: fmt.Errorf("failed to get the domain for cluster b01"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualError := RegistryDomainForClusterName(tc.clusterName)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
		})
	}
}
