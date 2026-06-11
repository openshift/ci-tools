package steps

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func ipPoolLeaseAdapter(lease stepLease) func(api.ClusterProfile, string) stepLease {
	return func(api.ClusterProfile, string) stepLease { return lease }
}

func atomicBool(v bool) *atomic.Bool {
	b := &atomic.Bool{}
	b.Store(v)
	return b
}

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
				stepRun: atomicBool(true),
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
				stepRun: atomicBool(true),
			},
			expected: map[string]string{
				"parameter":               "map",
				api.DefaultIPPoolLeaseEnv: "0",
			},
		},
		{
			name: "step did not run, ip pool env var is empty",
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
				stepRun: atomicBool(false),
			},
			expected: map[string]string{
				"parameter":               "map",
				api.DefaultIPPoolLeaseEnv: "",
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
		"parameter": func() (any, error) { return "map", nil },
	}
}

func (blockingStep) Objects() []ctrlruntimeclient.Object {
	return nil
}

func (blockingStep) SubTests() []*junit.TestCase {
	ret := junit.TestCase{}
	return []*junit.TestCase{&ret}
}

func (blockingStep) SubSteps() []api.CIOperatorStepDetailInfo {
	return []api.CIOperatorStepDetailInfo{{StepName: "inner-step"}}
}

func TestRun(t *testing.T) {
	ciOpNamespace := "ci-op-1234"

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
				ipPoolLeaseFunc: ipPoolLeaseAdapter(stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        2,
					},
				}),
				wrapped: &stepNeedsLease{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
				namespace: func() string {
					return ciOpNamespace
				},
				stepRun: &atomic.Bool{},
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
				ipPoolLeaseFunc: ipPoolLeaseAdapter(stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        3,
					},
				}),
				wrapped: &blockingStep{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
				namespace: func() string {
					return ciOpNamespace
				},
				stepRun: &atomic.Bool{},
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
				ipPoolLeaseFunc: ipPoolLeaseAdapter(stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        1,
					},
				}),
				wrapped: &blockingStep{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
				namespace: func() string {
					return ciOpNamespace
				},
				stepRun: &atomic.Bool{},
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
				ipPoolLeaseFunc: ipPoolLeaseAdapter(stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        1,
					},
				}),
				wrapped: &stepNeedsLease{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
				namespace: func() string {
					return ciOpNamespace
				},
				stepRun: &atomic.Bool{},
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
				ipPoolLeaseFunc: ipPoolLeaseAdapter(stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        2,
					},
				}),
				wrapped: &stepNeedsLease{},
				params:  fakeStepParams{},
				stepRun: &atomic.Bool{},
			},
			expectedError: errors.New("failed to determine region to acquire lease for aws-ip-pool"),
		},
		{
			name: "unknown lease client error",
			step: ipPoolStep{
				ipPoolLeaseFunc: ipPoolLeaseAdapter(stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        1,
					},
				}),
				wrapped: &stepNeedsLease{},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
				stepRun: &atomic.Bool{},
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
				ipPoolLeaseFunc: ipPoolLeaseAdapter(stepLease{
					StepLease: api.StepLease{
						ResourceType: "aws-ip-pool",
						Env:          api.DefaultIPPoolLeaseEnv,
						Count:        1,
					},
				}),
				namespace: func() string {
					return ciOpNamespace
				},
				wrapped: &stepNeedsLease{fail: true},
				params:  fakeStepParams{api.DefaultLeaseEnv: "us-east-1"},
				stepRun: &atomic.Bool{},
			},
			expected: []string{
				"acquire owner aws-ip-pool-us-east-1 free leased",
				"releaseone owner aws-ip-pool-us-east-1_0 free",
			},
			expectedError: errors.New("injected failure"),
		},
		{
			name: "ip pool lease not available, run wrapped step",
			step: ipPoolStep{
				ipPoolLeaseFunc: ipPoolLeaseAdapter(stepLease{
					StepLease: api.StepLease{},
				}),
				namespace: func() string {
					return ciOpNamespace
				},
				wrapped: &stepNeedsLease{},
				stepRun: &atomic.Bool{},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			client := lease.NewFakeClient("owner", "url", 0, tc.injectFailures, &calls, nil)
			tc.step.client = &client
			tc.step.secretClient = newFakeSecretClient([]coreapi.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: ciOpNamespace,
						Name:      "blocking", //The name of the wrapped step
					},
					Data: map[string][]byte{
						UnusedIpCount: []byte("2"),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: ciOpNamespace,
						Name:      "needs_lease", //The name of the wrapped step
					},
				},
			})
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

func TestIPPoolStepForward(t *testing.T) {
	step := stepNeedsLease{}
	cpAWS := api.ClusterProfileDetails{
		Name:        api.ClusterProfileAWS,
		ClusterType: "aws",
		LeaseType:   "aws-quota-slice",
		Secret:      "cluster-secrets-aws",
	}
	withIPPool := IPPoolStep(nil, nil, &step, nil, emptyNamespace, nil, cpAWS, "main")
	t.Run("SubTests", func(t *testing.T) {
		s, l := step.SubTests(), withIPPool.(SubtestReporter).SubTests()
		if diff := cmp.Diff(s, l); diff != "" {
			t.Errorf("not properly forwarded: %s", diff)
		}
	})
	t.Run("SubSteps", func(t *testing.T) {
		s, l := step.SubSteps(), withIPPool.(SubStepReporter).SubSteps()
		if diff := cmp.Diff(s, l); diff != "" {
			t.Errorf("not properly forwarded: %s", diff)
		}
	})
}

type fakeSecretClient struct {
	secrets []coreapi.Secret
}

func newFakeSecretClient(secrets []coreapi.Secret) *fakeSecretClient {
	return &fakeSecretClient{secrets: secrets}
}

func (c *fakeSecretClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	o := obj.(*coreapi.Secret)
	for i := range c.secrets {
		secret := c.secrets[i]
		if secret.GetNamespace() == key.Namespace && secret.GetName() == key.Name {
			o.Namespace = secret.Namespace
			o.Name = key.Name
			o.Data = secret.Data
		}
	}
	return nil
}
