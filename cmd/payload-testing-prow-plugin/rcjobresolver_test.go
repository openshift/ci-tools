package main

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/config"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestResolve(t *testing.T) {
	testCases := []struct {
		name          string
		ocp           string
		releaseType   api.ReleaseStream
		jobType       config.JobType
		expected      []config.Job
		expectedError error
	}{
		{
			name:        "basic case",
			releaseType: api.ReleaseStreamNightly,
			jobType:     config.Blocking,
			expected: []config.Job{
				{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-upgrade", AggregatedCount: 5},
				{Name: "periodic-ci-openshift-release-master-ci-4.10-e2e-azure-ovn-upgrade", AggregatedCount: 10},
				{Name: "periodic-ci-openshift-release-master-ci-4.10-e2e-azure-ovn-upgrade-2"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			httpClient := release.NewFakeHTTPClient(func(req *http.Request) (*http.Response, error) {
				content := `{
    "aggregated-aws-sdn-upgrade-4.10-micro": {
      "disabled": false,
      "optional": false,
      "upgrade": true,
      "upgradeFrom": "",
      "upgradeFromRelease": null,
      "prowJob": {
        "name": "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-upgrade"
      },
      "aggregatedProwJob": {
        "analysisJobCount": 5
      }
    },
    "aggregated-azure-ovn-upgrade-4.10-micro": {
      "disabled": false,
      "optional": true,
      "upgrade": true,
      "upgradeFrom": "",
      "upgradeFromRelease": null,
      "prowJob": {
        "name": "periodic-ci-openshift-release-master-ci-4.10-e2e-azure-ovn-upgrade"
      },
      "aggregatedProwJob": {
        "analysisJobCount": 10
      }
    },
    "non-aggregated-azure-ovn-upgrade-4.10-micro": {
		"disabled": false,
		"optional": true,
		"upgrade": true,
		"upgradeFrom": "",
		"upgradeFromRelease": null,
		"prowJob": {
		  "name": "periodic-ci-openshift-release-master-ci-4.10-e2e-azure-ovn-upgrade-2"
		}
	  }
}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBuffer([]byte(content))),
				}, nil
			})
			jobResolver := newReleaseControllerJobResolver(httpClient)
			actual, actualError := jobResolver.resolve(tc.ocp, tc.releaseType, tc.jobType)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}
