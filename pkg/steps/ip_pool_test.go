package steps

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestProvides(t *testing.T) {
	testCases := []struct {
		name     string
		step     ipPoolStep
		expected map[string]string
	}{
		{
			name: "leases acquired",
			step: ipPoolStep{
				ipPoolLease: stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        2,
					},
					resources: []string{"some-resource", "some-other-resource"},
				},
				wrapped: &stepNeedsLease{},
			},
			expected: map[string]string{
				"parameter":               "map",
				api.DefaultIPPoolLeaseEnv: "2",
			},
		},
		{
			name: "no leases acquired",
			step: ipPoolStep{
				ipPoolLease: stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        2,
					},
					resources: []string{},
				},
				wrapped: &stepNeedsLease{},
			},
			expected: map[string]string{
				"parameter":               "map",
				api.DefaultIPPoolLeaseEnv: "0",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.step.Provides()
			if len(result) != len(tc.expected) {
				t.Fatalf("resulting parameters (%d) are not the same length as expected (%d)", len(result), len(tc.expected))
			}
			for key, value := range tc.expected {
				match, exists := result[key]
				if !exists {
					t.Errorf("expected parameter: %s doesn't exist in result", key)
				}
				resultValue, _ := match()
				if diff := cmp.Diff(value, resultValue); diff != "" {
					t.Errorf("expected doesn't match result, diff: %s", diff)
				}
			}
		})
	}
}

type fakeStepParams map[string]string

func (f fakeStepParams) Has(key string) bool {
	_, ok := f[key]
	return ok
}

func (f fakeStepParams) HasInput(_ string) bool {
	panic("This should not be used")
}

func (f fakeStepParams) Get(key string) (string, error) {
	return f[key], nil
}

func TestRun(t *testing.T) {
	testCases := []struct {
		name           string
		step           ipPoolStep
		injectFailures map[string]error
		expected       []string
		expectedError  error
	}{
		{
			name: "leases available in region",
			step: ipPoolStep{
				ipPoolLease: stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        2,
					},
				},
				wrapped: &stepNeedsLease{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
			},
			expected: []string{
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"releaseone owner aws-ip-pool-us-east-1_0 free",
				"releaseone owner aws-ip-pool-us-east-1_1 free",
			},
		},
		{
			name: "leases unavailable in region, step should not error",
			step: ipPoolStep{
				ipPoolLease: stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        1,
					},
				},
				wrapped: &stepNeedsLease{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
			},
			injectFailures: map[string]error{
				"acquire owner aws-ip-pool-us-east-1 free leased": lease.ErrNotFound,
			},
			expected: []string{
				"acquire owner aws-ip-pool-us-east-1 free leased",
			},
		},
		{
			name: "region not provided, errors",
			step: ipPoolStep{
				ipPoolLease: stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        2,
					},
				},
				wrapped: &stepNeedsLease{},
				params:  fakeStepParams{},
			},
			expectedError: errors.New("failed to determine region to acquire lease for aws-ip-pool"),
		},
		{
			name: "unknown lease client error",
			step: ipPoolStep{
				ipPoolLease: stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        1,
					},
				},
				wrapped: &stepNeedsLease{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
			},
			injectFailures: map[string]error{
				"acquire owner aws-ip-pool-us-east-1 free leased": errors.New("some client error"),
			},
			expected: []string{
				"acquire owner aws-ip-pool-us-east-1 free leased",
			},
			expectedError: errors.New("failed to acquire lease for aws-ip-pool-us-east-1: some client error"),
		},
		{
			name: "wrapped step fails",
			step: ipPoolStep{
				ipPoolLease: stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        1,
					},
				},
				wrapped: &stepNeedsLease{fail: true},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
			},
			expected: []string{
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"releaseone owner aws-ip-pool-us-east-1_0 free",
			},
			expectedError: errors.New("injected failure"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			client := lease.NewFakeClient("owner", "url", 0, tc.injectFailures, &calls)
			tc.step.client = &client
			err := tc.step.Run(context.Background())
			if diff := cmp.Diff(err, tc.expectedError, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("unexpected error returned, diff: %s", diff)
			}
			if diff := cmp.Diff(calls, tc.expected); diff != "" {
				t.Fatalf("unexpected calls to the lease client: %s", diff)
			}
		})
	}
}
