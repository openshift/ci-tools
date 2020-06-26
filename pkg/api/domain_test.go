package api

import (
	"testing"
)

func TestDomainForService(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		service  Service
		expected string
	}{
		{
			name:     "boskos",
			service:  ServiceBoskos,
			expected: "boskos-ci.apps.ci.l2s4.p1.openshiftapps.com",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if result := DomainForService(tc.service); result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}
