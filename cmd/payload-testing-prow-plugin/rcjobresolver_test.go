package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/config"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestResolve(t *testing.T) {
	testCases := []struct {
		name           string
		ocp            string
		releaseType    api.ReleaseStream
		jobType        config.JobType
		jobSkipEnvVars map[string]string
		expected       []config.Job
		expectedError  error
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
		{
			name:        "skipping a job",
			releaseType: api.ReleaseStreamNightly,
			jobType:     config.Blocking,
			jobSkipEnvVars: map[string]string{
				fmt.Sprintf("%s1", regexPrefix):      "nightly",
				fmt.Sprintf("%s1", expirationPrefix): time.Now().Add(time.Hour).Format(time.RFC3339),
			},
			expected: []config.Job{
				{Name: "periodic-ci-openshift-release-master-ci-4.10-e2e-azure-ovn-upgrade", AggregatedCount: 10},
				{Name: "periodic-ci-openshift-release-master-ci-4.10-e2e-azure-ovn-upgrade-2"},
			},
		},
		{
			name:        "konflux-nightly basic case",
			releaseType: api.ReleaseStreamKonfluxNightly,
			jobType:     config.Blocking,
			expected: []config.Job{
				{Name: "periodic-ci-openshift-release-master-konflux-nightly-4.10-e2e-aws-upgrade", AggregatedCount: 5},
				{Name: "periodic-ci-openshift-release-master-konflux-nightly-4.10-e2e-gcp-upgrade", AggregatedCount: 10},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			httpClient := release.NewFakeHTTPClient(func(req *http.Request) (*http.Response, error) {
				var content string
				switch tc.releaseType {
				case api.ReleaseStreamNightly:
					content = `{
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
				case api.ReleaseStreamKonfluxNightly:
					content = `{
    "aggregated-aws-sdn-upgrade-4.10-micro": {
      "disabled": false,
      "optional": false,
      "upgrade": true,
      "upgradeFrom": "",
      "upgradeFromRelease": null,
      "prowJob": {
        "name": "periodic-ci-openshift-release-master-konflux-nightly-4.10-e2e-aws-upgrade"
      },
      "aggregatedProwJob": {
        "analysisJobCount": 5
      }
    },
    "aggregated-gcp-sdn-upgrade-4.10-micro": {
      "disabled": false,
      "optional": false,
      "upgrade": true,
      "upgradeFrom": "",
      "upgradeFromRelease": null,
      "prowJob": {
        "name": "periodic-ci-openshift-release-master-konflux-nightly-4.10-e2e-gcp-upgrade"
      },
      "aggregatedProwJob": {
        "analysisJobCount": 10
      }
    }
}`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBuffer([]byte(content))),
				}, nil
			})

			for key, val := range tc.jobSkipEnvVars {
				t.Setenv(key, val)
			}

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

func TestDetermineJobSkips(t *testing.T) {
	baseTime := time.Time{}
	microshiftExpiration := baseTime.Add(time.Hour)
	rosaExpiration := baseTime.Add(2 * time.Hour)
	testCases := []struct {
		name           string
		jobSkipEnvVars map[string]string
		expected       []string
		expectedError  error
	}{
		{
			name: "nothing configured",
		},
		{
			name: "multiple skips configured",
			jobSkipEnvVars: map[string]string{
				fmt.Sprintf("%s1", regexPrefix):      "microshift",
				fmt.Sprintf("%s1", expirationPrefix): microshiftExpiration.Format(time.RFC3339),
				fmt.Sprintf("%s2", regexPrefix):      "rosa",
				fmt.Sprintf("%s2", expirationPrefix): rosaExpiration.Format(time.RFC3339),
			},
			expected: []string{
				jobSkip{regex: *regexp.MustCompile("microshift"), expiration: microshiftExpiration}.String(),
				jobSkip{regex: *regexp.MustCompile("rosa"), expiration: rosaExpiration}.String(),
			},
		},
		{
			name: "no expiration configured; assume expires 1 hour in future",
			jobSkipEnvVars: map[string]string{
				fmt.Sprintf("%s1", regexPrefix): "microshift",
			},
			expected: []string{
				jobSkip{regex: *regexp.MustCompile("microshift"), expiration: baseTime.Add(time.Hour)}.String(),
			},
		},
		{
			name: "incorrectly formatted regex",
			jobSkipEnvVars: map[string]string{
				fmt.Sprintf("%s1", regexPrefix):      "microshift**",
				fmt.Sprintf("%s1", expirationPrefix): microshiftExpiration.Format(time.RFC3339),
			},
			expectedError: fmt.Errorf("could not compile job regexp microshift**: error parsing regexp: invalid nested repetition operator: `**`"),
		},
		{
			name: "incorrectly formatted expiration",
			jobSkipEnvVars: map[string]string{
				fmt.Sprintf("%s1", regexPrefix):      "microshift",
				fmt.Sprintf("%s1", expirationPrefix): microshiftExpiration.Format(time.RFC850),
			},
			expectedError: fmt.Errorf("failed to parse expiration time \"Monday, 01-Jan-01 01:00:00 UTC\": parsing time \"Monday, 01-Jan-01 01:00:00 UTC\" as \"2006-01-02T15:04:05Z07:00\": cannot parse \"Monday, 01-Jan-01 01:00:00 UTC\" as \"2006\""),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for key, val := range tc.jobSkipEnvVars {
				t.Setenv(key, val)
			}

			result, err := determineJobSkips(baseTime)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("expectedError doesn't match err, diff:\n%s", diff)
			}

			var resultingStrings []string
			for _, js := range result {
				resultingStrings = append(resultingStrings, js.String())
			}
			slices.Sort(tc.expected)
			slices.Sort(resultingStrings)
			if diff := cmp.Diff(tc.expected, resultingStrings); diff != "" {
				t.Errorf("expected doesn't match result, diff:\n%s", diff)
			}
		})
	}
}
