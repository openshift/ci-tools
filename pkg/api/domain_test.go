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
	}

	for _, tc := range testCases {
		t.Run(string(tc.service), func(t *testing.T) {
			if result := DomainForService(tc.service); result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestPublicDomainForImage(t *testing.T) {
	testCases := []struct {
		name               string
		clusterName        string
		potentiallyPrivate string
		expected           string
		expectedError      error
	}{
		{
			name:               "app.ci with svc dns",
			clusterName:        "app.ci",
			potentiallyPrivate: "image-registry.openshift-image-registry.svc:5000/ci/applyconfig@sha256:bf08a76268b29f056cfab7a105c8473b359d1154fbbe3091fe6052ad6d0427cd",
			expected:           "registry.ci.openshift.org/ci/applyconfig@sha256:bf08a76268b29f056cfab7a105c8473b359d1154fbbe3091fe6052ad6d0427cd",
		},

		{
			name:               "app.ci with public domain",
			clusterName:        "app.ci",
			potentiallyPrivate: "gcr.io/k8s-prow/tide@sha256:5245b7747c44d560aab27bc07dbaaf50bbb55f71d0973f85b09c79b8d8b93c97",
			expected:           "gcr.io/k8s-prow/tide@sha256:5245b7747c44d560aab27bc07dbaaf50bbb55f71d0973f85b09c79b8d8b93c97",
		},
		{
			name:               "unknown context",
			clusterName:        "unknown",
			potentiallyPrivate: "gcr.io/k8s-prow/tide@sha256:5245b7747c44d560aab27bc07dbaaf50bbb55f71d0973f85b09c79b8d8b93c97",
			expected:           "",
			expectedError:      fmt.Errorf("failed to get the domain for cluster unknown"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualError := PublicDomainForImage(tc.clusterName, tc.potentiallyPrivate)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
		})
	}
}
