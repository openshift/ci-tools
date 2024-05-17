package steps

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
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

// blockingStep sleeps for 5 seconds to allow testing of the release of unused ip-pool leases
type blockingStep struct{}

func (blockingStep) Inputs() (api.InputDefinition, error) {
	return api.InputDefinition{"step", "inputs"}, nil
}
func (blockingStep) Validate() error { return nil }
func (s *blockingStep) Run(ctx context.Context) error {
	time.Sleep(time.Second * 5)
	return nil
}

func (blockingStep) Name() string        { return "blocking" }
func (blockingStep) Description() string { return "this step needs a lease" }
func (blockingStep) Requires() []api.StepLink {
	return []api.StepLink{api.ReleaseImagesLink(api.LatestReleaseName)}
}
func (blockingStep) Creates() []api.StepLink { return []api.StepLink{api.ImagesReadyLink()} }

func (blockingStep) Provides() api.ParameterMap {
	return api.ParameterMap{
		"parameter": func() (string, error) { return "map", nil },
	}
}

func (blockingStep) Objects() []ctrlruntimeclient.Object {
	return nil
}

func (blockingStep) SubTests() []*junit.TestCase {
	ret := junit.TestCase{}
	return []*junit.TestCase{&ret}
}

func TestRun(t *testing.T) {
	ciOpNamespace := "ci-op-1234"
	podClient := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
		&coreapi.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ciOpNamespace,
				Name:      "blocking", //The name of the wrapped step
			},
			Data: map[string][]byte{
				UnusedIpCount: []byte("2"),
			},
		},
		&coreapi.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ciOpNamespace,
				Name:      "needs_lease", //The name of the wrapped step
			},
		},
	).Build()

	testCases := []struct {
		name           string
		step           ipPoolStep
		injectFailures map[string]error
		expected       []string
		expectedError  error
		finalResources []string // Only used to verify the release of unused leases
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
				namespace: func() string {
					return ciOpNamespace
				},
			},
			expected: []string{
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"releaseone owner aws-ip-pool-us-east-1_0 free",
				"releaseone owner aws-ip-pool-us-east-1_1 free",
			},
		},
		{
			name: "requested to release unused",
			step: ipPoolStep{
				ipPoolLease: stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        3,
					},
				},
				wrapped: &blockingStep{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
				namespace: func() string {
					return ciOpNamespace
				},
			},
			expected: []string{
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"releaseone owner aws-ip-pool-us-east-1_0 free",
				"releaseone owner aws-ip-pool-us-east-1_1 free",
				"releaseone owner aws-ip-pool-us-east-1_2 free",
			},
			finalResources: []string{"aws-ip-pool-us-east-1_2"},
		},
		{
			name: "requested to release too many unused",
			step: ipPoolStep{
				ipPoolLease: stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        1,
					},
				},
				wrapped: &blockingStep{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
				namespace: func() string {
					return ciOpNamespace
				},
			},
			expected: []string{
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"releaseone owner aws-ip-pool-us-east-1_0 free",
			},
			finalResources: []string{"aws-ip-pool-us-east-1_0"},
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
				namespace: func() string {
					return ciOpNamespace
				},
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
				namespace: func() string {
					return ciOpNamespace
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
			tc.step.secretClient = podClient
			err := tc.step.run(context.Background(), time.Second)
			if diff := cmp.Diff(err, tc.expectedError, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("unexpected error returned, diff: %s", diff)
			}
			if diff := cmp.Diff(calls, tc.expected); diff != "" {
				t.Fatalf("unexpected calls to the lease client: %s", diff)
			}
			// We can check the step's resultant resources to see which leases have been released early.
			// This works because the final release process when the step is complete leaves the resources in the list,
			// but the unused release process prunes them from it.
			if len(tc.finalResources) > 0 {
				if diff := cmp.Diff(tc.step.ipPoolLease.resources, tc.finalResources); diff != "" {
					t.Fatalf("unexpected ipPoolLease step resources: %s", diff)
				}
			}

		})
	}
}
