package jobrunhistoricaldataanalyzer

import (
	"strings"
	"testing"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

func TestDetermineCurrentRelease(t *testing.T) {
	releases := []byte(`{
		"release_attrs": {
			"4.22": {"development_start": "2025-12-12T08:30:00Z", "product": "OCP"},
			"4.23": {"development_start": "2026-08-13", "product": "OCP"},
			"5.0": {"development_start": "2026-04-13", "product": "OCP"},
			"5.1": {"development_start": "2026-08-14", "product": "OCP"},
			"5.2": {"development_start": "2027-01-01", "product": "OCP"},
			"6.0": {"development_start": "2026-09-20", "product": "OKD"},
			"automation": {"development_start": "2026-09-21", "product": "OCP"}
		}
	}`)

	tests := []struct {
		name             string
		now              time.Time
		data             []byte
		expectedCurrent  string
		expectedPrevious string
		expectedError    string
	}{
		{
			name:             "latest started OCP release",
			now:              time.Date(2026, time.September, 25, 0, 0, 0, 0, time.UTC),
			data:             releases,
			expectedCurrent:  "5.1",
			expectedPrevious: "5.0",
		},
		{
			name:             "future releases are ignored",
			now:              time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC),
			data:             releases,
			expectedCurrent:  "4.23",
			expectedPrevious: "4.22",
		},
		{
			name:          "no started OCP release",
			now:           time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			data:          releases,
			expectedError: "no valid OCP development releases found",
		},
		{
			name:          "invalid development date",
			now:           time.Date(2026, time.September, 25, 0, 0, 0, 0, time.UTC),
			data:          []byte(`{"release_attrs":{"4.23":{"development_start":"not-a-date","product":"OCP"}}}`),
			expectedError: "cannot parse",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, previous, err := determineCurrentRelease(test.data, test.now)
			if test.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), test.expectedError) {
					t.Fatalf("determineCurrentRelease() error = %v, expected error containing %q", err, test.expectedError)
				}
				return
			}
			if err != nil {
				t.Fatalf("determineCurrentRelease() returned an unexpected error: %v", err)
			}
			if current != test.expectedCurrent || previous != test.expectedPrevious {
				t.Errorf("determineCurrentRelease() = (%q, %q), expected (%q, %q)", current, previous, test.expectedCurrent, test.expectedPrevious)
			}
		})
	}
}

func TestIndexHistoricalData(t *testing.T) {
	jobData := jobrunaggregatorapi.HistoricalJobData{
		Release:      "4.21",
		FromRelease:  "4.20",
		Platform:     "aws",
		Architecture: "amd64",
		Network:      "ovn",
		Topology:     "ha",
	}

	tests := []struct {
		name              string
		data              []jobrunaggregatorapi.HistoricalData
		expectedSize      int
		expectedErrorText []string
	}{
		{
			name: "unique disruption keys",
			data: []jobrunaggregatorapi.HistoricalData{
				&jobrunaggregatorapi.DisruptionHistoricalDataRow{BackendName: "kube-api-new-connections", HistoricalJobData: jobData},
				&jobrunaggregatorapi.DisruptionHistoricalDataRow{BackendName: "kube-api-reused-connections", HistoricalJobData: jobData},
			},
			expectedSize: 2,
		},
		{
			name: "duplicate disruption key",
			data: []jobrunaggregatorapi.HistoricalData{
				&jobrunaggregatorapi.DisruptionHistoricalDataRow{
					BackendName:       "kube-api-new-connections",
					HistoricalJobData: withJobRuns(jobData, 7),
					P99:               "0.94",
				},
				&jobrunaggregatorapi.DisruptionHistoricalDataRow{
					BackendName:       "kube-api-new-connections",
					HistoricalJobData: withJobRuns(jobData, 556),
					P99:               "1.0",
				},
			},
			expectedErrorText: []string{
				"test source contains duplicate historical data key",
				"kube-api-new-connections_4.20_4.21_amd64_aws_ovn_ha",
				"rows 0 and 1",
				"JobRuns=7/556",
				`P99="0.94"/"1.0"`,
			},
		},
		{
			name: "identical duplicate rows",
			data: []jobrunaggregatorapi.HistoricalData{
				&jobrunaggregatorapi.DisruptionHistoricalDataRow{
					BackendName:       "kube-api-new-connections",
					HistoricalJobData: withJobRuns(jobData, 556),
					P99:               "1.0",
				},
				&jobrunaggregatorapi.DisruptionHistoricalDataRow{
					BackendName:       "kube-api-new-connections",
					HistoricalJobData: withJobRuns(jobData, 556),
					P99:               "1.0",
				},
			},
			expectedErrorText: []string{
				"test source contains duplicate historical data key",
				"JobRuns=556/556",
				`P99="1.0"/"1.0"`,
			},
		},
		{
			name: "duplicate alert key",
			data: []jobrunaggregatorapi.HistoricalData{
				&jobrunaggregatorapi.AlertHistoricalDataRow{
					AlertName:         "KubeAPIErrorBudgetBurn",
					AlertNamespace:    "openshift-kube-apiserver",
					AlertLevel:        "warning",
					HistoricalJobData: withJobRuns(jobData, 100),
					P99:               "2.0",
				},
				&jobrunaggregatorapi.AlertHistoricalDataRow{
					AlertName:         "KubeAPIErrorBudgetBurn",
					AlertNamespace:    "openshift-kube-apiserver",
					AlertLevel:        "warning",
					HistoricalJobData: withJobRuns(jobData, 101),
					P99:               "3.0",
				},
			},
			expectedErrorText: []string{
				"test source contains duplicate historical data key",
				"KubeAPIErrorBudgetBurn_openshift-kube-apiserver_warning_4.20_4.21_amd64_aws_ovn_ha",
				"JobRuns=100/101",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			indexed, err := indexHistoricalData("test source", test.data)
			if len(test.expectedErrorText) == 0 {
				if err != nil {
					t.Fatalf("indexHistoricalData() returned an unexpected error: %v", err)
				}
				if len(indexed) != test.expectedSize {
					t.Fatalf("indexHistoricalData() returned %d rows, expected %d", len(indexed), test.expectedSize)
				}
				return
			}

			if err == nil {
				t.Fatal("indexHistoricalData() did not return an error for duplicate keys")
			}
			if indexed != nil {
				t.Fatalf("indexHistoricalData() returned a non-nil map after detecting a duplicate: %#v", indexed)
			}
			for _, expected := range test.expectedErrorText {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("indexHistoricalData() error %q does not contain %q", err, expected)
				}
			}
		})
	}
}

func withJobRuns(data jobrunaggregatorapi.HistoricalJobData, jobRuns int) jobrunaggregatorapi.HistoricalJobData {
	data.JobRuns = jobRuns
	return data
}
