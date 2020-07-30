package api

import (
	"testing"
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
			expected: "rpms.svc.ci.openshift.org",
		},
		{
			service:  ServiceRegistry,
			expected: "registry.svc.ci.openshift.org",
		},
		{
			service:  ServiceProw,
			expected: "prow.ci.openshift.org",
		},
		{
			service:  ServiceConfig,
			expected: "config.ci.openshift.org",
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
