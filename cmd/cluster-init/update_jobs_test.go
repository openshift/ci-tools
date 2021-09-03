package main

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestPeriodicExistsFor(t *testing.T) {
	testCases := []struct {
		name string
		options
		expected bool
	}{
		{
			name: "exists",
			options: options{
				clusterName: "existingCluster",
				releaseRepo: "testdata",
			},
			expected: true,
		},
		{
			name: "does not exist",
			options: options{
				clusterName: "newCluster",
				releaseRepo: "testdata",
			},
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exists, err := periodicExistsFor(tc.options)
			if err != nil {
				t.Fatalf("unexpected error occurred while running periodicExistsFor: %v", err)
			}
			if tc.expected != exists {
				t.Fatalf("result: %v does not match expected: %v", exists, tc.expected)
			}
		})
	}
}

func TestFindPeriodic(t *testing.T) {
	testCases := []struct {
		name             string
		ip               InfraPeriodics
		periodicName     string
		expectedPeriodic *prowconfig.Periodic
		expectedError    error
	}{
		{
			name: "exists",
			ip: InfraPeriodics{
				Periodics: []prowconfig.Periodic{
					{
						JobBase: prowconfig.JobBase{Name: "per-0"},
					},
					{
						JobBase: prowconfig.JobBase{Name: "per-a"},
					},
					{
						JobBase: prowconfig.JobBase{Name: "per-b"},
					},
				},
			},
			periodicName: "per-a",
			expectedPeriodic: &prowconfig.Periodic{
				JobBase: prowconfig.JobBase{Name: "per-a"},
			},
		},
		{
			name: "does not exist",
			ip: InfraPeriodics{
				Periodics: []prowconfig.Periodic{
					{
						JobBase: prowconfig.JobBase{Name: "per-0"},
					},
					{
						JobBase: prowconfig.JobBase{Name: "per-a"},
					},
					{
						JobBase: prowconfig.JobBase{Name: "per-b"},
					},
				},
			},
			periodicName:  "per-c",
			expectedError: errors.New("couldn't find periodic with name: per-c"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			periodic, err := findPeriodic(&tc.ip, tc.periodicName)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("expectedError doesn't match err, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedPeriodic, periodic, cmp.AllowUnexported(prowconfig.Periodic{})); diff != "" {
				t.Fatalf("expectedPeriodic doesn't match periodic, diff: %s", diff)
			}
		})
	}
}
