package steps

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/boskos/common"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

type stepNeedsLease struct {
	fail, ran bool
}

func (stepNeedsLease) Inputs() (api.InputDefinition, error) {
	return api.InputDefinition{"step", "inputs"}, nil
}
func (stepNeedsLease) Validate() error { return nil }
func (s *stepNeedsLease) Run(ctx context.Context) error {
	s.ran = true
	if s.fail {
		return errors.New("injected failure")
	}
	return nil
}

func (stepNeedsLease) Name() string        { return "needs_lease" }
func (stepNeedsLease) Description() string { return "this step needs a lease" }
func (stepNeedsLease) Requires() []api.StepLink {
	return []api.StepLink{api.ReleaseImagesLink(api.LatestReleaseName)}
}
func (stepNeedsLease) Creates() []api.StepLink { return []api.StepLink{api.ImagesReadyLink()} }

func (stepNeedsLease) Provides() api.ParameterMap {
	return api.ParameterMap{
		"parameter": func() (string, error) { return "map", nil },
	}
}

func (stepNeedsLease) Objects() []ctrlruntimeclient.Object {
	return nil
}

func (stepNeedsLease) SubTests() []*junit.TestCase {
	ret := junit.TestCase{}
	return []*junit.TestCase{&ret}
}

func emptyNamespace() string { return "" }

func TestLeaseStepForward(t *testing.T) {
	leases := []api.StepLease{{
		Env:          api.DefaultLeaseEnv,
		ResourceType: "lease_name",
	}}
	step := stepNeedsLease{}
	withLease := LeaseStep(nil, leases, &step, emptyNamespace, nil, nil, nil)
	t.Run("Inputs", func(t *testing.T) {
		s, err := step.Inputs()
		if err != nil {
			t.Fatal(err)
		}
		l, err := withLease.Inputs()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(l, s) {
			t.Errorf("not properly forwarded: %s", diff.ObjectDiff(l, s))
		}
	})
	t.Run("Name", func(t *testing.T) {
		if s, l := step.Name(), withLease.Name(); l != s {
			t.Errorf("not properly forwarded: %s", diff.ObjectDiff(l, s))
		}
	})
	t.Run("Description", func(t *testing.T) {
		if s, l := step.Description(), withLease.Description(); l != s {
			t.Errorf("not properly forwarded: %s", diff.ObjectDiff(l, s))
		}
	})
	t.Run("Requires", func(t *testing.T) {
		if s, l := step.Requires(), withLease.Requires(); !reflect.DeepEqual(l, s) {
			t.Errorf("not properly forwarded: %s", diff.ObjectDiff(l, s))
		}
	})
	t.Run("Creates", func(t *testing.T) {
		if s, l := step.Creates(), withLease.Creates(); !reflect.DeepEqual(l, s) {
			t.Errorf("not properly forwarded: %s", diff.ObjectDiff(l, s))
		}
	})
	t.Run("Provides includes parameters from wrapped step", func(t *testing.T) {
		sParam := step.Provides()
		sRet, err := sParam["parameter"]()
		if err != nil {
			t.Fatal(err)
		}
		lParam := withLease.Provides()
		lRet, err := lParam["parameter"]()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(lRet, sRet) {
			t.Errorf("not properly forwarded (param): %s", diff.ObjectDiff(lParam, sParam))
		}
	})
	t.Run("SubTests", func(T *testing.T) {
		s, l := step.SubTests(), withLease.(SubtestReporter).SubTests()
		if !reflect.DeepEqual(l, s) {
			t.Errorf("not properly forwarded: %s", diff.ObjectDiff(l, s))
		}
	})
}

func TestProvidesStripsSuffix(t *testing.T) {
	leases := []api.StepLease{{Env: api.DefaultLeaseEnv, ResourceType: "rtype"}}
	withLease := LeaseStep(nil, leases, &stepNeedsLease{}, emptyNamespace, nil, nil, nil)
	withLease.(*leaseStep).leases[0].resources = []string{"whatever--01"}
	expected := "whatever"
	actual, err := withLease.Provides()[api.DefaultLeaseEnv]()
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Errorf("got %q for %s, expected %q", actual, api.DefaultLeaseEnv, expected)
	}
}

func TestError(t *testing.T) {
	leases := []api.StepLease{
		{ResourceType: "rtype0", Count: 1},
		{ResourceType: "rtype1", Count: 1},
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name            string
		runFails        bool
		failures        map[string]error
		expectedReasons []string
		expected        []string
	}{{
		name: "first acquire fails",
		failures: map[string]error{
			"acquireWaitWithPriority owner rtype0 free leased random": errors.New("injected failure"),
		},
		expectedReasons: []string{"utilizing_lease:acquiring_lease"},
		expected:        []string{"acquireWaitWithPriority owner rtype0 free leased random"},
	}, {
		name: "second acquire fails",
		failures: map[string]error{
			"acquireWaitWithPriority owner rtype1 free leased random": errors.New("injected failure"),
		},
		expectedReasons: []string{"utilizing_lease:acquiring_lease"},
		expected: []string{
			"acquireWaitWithPriority owner rtype0 free leased random",
			"acquireWaitWithPriority owner rtype1 free leased random",
			"releaseone owner rtype0_0 free",
		},
	}, {
		name: "first release fails",
		failures: map[string]error{
			"releaseone owner rtype0_0 free": errors.New("injected failure"),
		},
		expectedReasons: []string{"utilizing_lease:releasing_lease"},
		expected: []string{
			"acquireWaitWithPriority owner rtype0 free leased random",
			"acquireWaitWithPriority owner rtype1 free leased random",
			"releaseone owner rtype0_0 free",
			"releaseone owner rtype1_1 free",
		},
	}, {
		name: "second release fails",
		failures: map[string]error{
			"releaseone owner rtype1_1 free": errors.New("injected failure"),
		},
		expectedReasons: []string{"utilizing_lease:releasing_lease"},
		expected: []string{
			"acquireWaitWithPriority owner rtype0 free leased random",
			"acquireWaitWithPriority owner rtype1 free leased random",
			"releaseone owner rtype0_0 free",
			"releaseone owner rtype1_1 free",
		},
	}, {
		name:            "run fails",
		runFails:        true,
		expectedReasons: []string{"utilizing_lease:executing_test"},
		expected: []string{
			"acquireWaitWithPriority owner rtype0 free leased random",
			"acquireWaitWithPriority owner rtype1 free leased random",
			"releaseone owner rtype0_0 free",
			"releaseone owner rtype1_1 free",
		},
	}, {
		name:     "run and release fail",
		runFails: true,
		failures: map[string]error{
			"releaseone owner rtype1_1 free": errors.New("injected failure"),
		},
		expectedReasons: []string{
			"utilizing_lease:executing_test",
			"utilizing_lease:releasing_lease",
		},
		expected: []string{
			"acquireWaitWithPriority owner rtype0 free leased random",
			"acquireWaitWithPriority owner rtype1 free leased random",
			"releaseone owner rtype0_0 free",
			"releaseone owner rtype1_1 free",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			client := lease.NewFakeClient("owner", "url", 0, tc.failures, &calls, nil)
			s := stepNeedsLease{fail: tc.runFails}
			err := LeaseStep(&client, leases, &s, func() string { return "" }, nil, nil, nil).Run(ctx)
			if err == nil {
				t.Fatalf("unexpected success, calls: %#v", calls)
			}
			testhelper.Diff(t, "reasons", results.Reasons(err), tc.expectedReasons)
			if !reflect.DeepEqual(calls, tc.expected) {
				t.Fatalf("wrong calls to the lease client: %s", diff.ObjectDiff(calls, tc.expected))
			}
		})
	}
}

func TestAcquireLeases(t *testing.T) {
	ns := "ci-op-xxx"
	nsFunc := func() string { return ns }
	newClusterProfileGetter := func(nameToClusterProfile map[string]*api.ClusterProfileDetails) func(name string) (*api.ClusterProfileDetails, error) {
		return func(name string) (*api.ClusterProfileDetails, error) {
			cp, ok := nameToClusterProfile[name]
			if !ok {
				return nil, fmt.Errorf("cluster profile %s not found", name)
			}
			return cp, nil
		}
	}

	for _, tc := range []struct {
		name                    string
		leaseClientFailingCalls map[string]error
		leases                  []api.StepLease
		resources               map[string]*common.Resource
		objects                 []ctrlruntimeclient.Object
		clusterProfiles         map[string]*api.ClusterProfileDetails
		wantProvides            map[string]string
		wantSecrets             corev1.SecretList
		wantCalls               []string
	}{
		{
			name: "Acquire two lease of different types",
			leases: []api.StepLease{{
				ResourceType: "res-type-0",
				Env:          "lease-0",
				Count:        1,
			}, {
				ResourceType: "res-type-1",
				Env:          "lease-1",
				Count:        1,
			}},
			resources: map[string]*common.Resource{
				"acquireWaitWithPriority_res-type-0_free_leased_random": {
					Name: "res-type-0--slice-0",
				},
				"acquireWaitWithPriority_res-type-1_free_leased_random": {
					Name: "res-type-1--slice-0",
				},
			},
			wantProvides: map[string]string{
				api.ClusterProfileSetEnv: "",
				api.ClusterProfileParam:  "",
				"lease-0":                "res-type-0",
				"lease-1":                "res-type-1",
				"parameter":              "map",
			},
			wantCalls: []string{
				"acquireWaitWithPriority owner res-type-0 free leased random",
				"acquireWaitWithPriority owner res-type-1 free leased random",
				"releaseone owner res-type-0--slice-0 free",
				"releaseone owner res-type-1--slice-0 free",
			},
			wantSecrets: corev1.SecretList{Items: []corev1.Secret{}},
		},
		{
			name: "Cluster profile lease",
			leases: []api.StepLease{{
				ResourceType:         "aws",
				Env:                  api.DefaultLeaseEnv,
				Count:                1,
				ClusterProfile:       "aws",
				ClusterProfileTarget: "e2e-aws-ovn",
			}},
			resources: map[string]*common.Resource{
				"acquireWaitWithPriority_aws_free_leased_random": {
					Name: "us-east-1--aws-quota-slice-0",
				},
			},
			objects: []ctrlruntimeclient.Object{&corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ci",
					Name:      "cluster-secrets-aws",
				},
				Data: map[string][]byte{
					"k1": []byte("v1"),
					"k2": []byte("v2"),
				},
			}},
			clusterProfiles: map[string]*api.ClusterProfileDetails{
				"aws": {
					Secret:    "cluster-secrets-aws",
					LeaseType: "aws-quota-slice",
				},
			},
			wantProvides: map[string]string{
				"parameter":              "map",
				api.ClusterProfileSetEnv: "",
				api.ClusterProfileParam:  "aws",
				api.DefaultLeaseEnv:      "us-east-1",
			},
			wantSecrets: corev1.SecretList{
				Items: []corev1.Secret{
					{
						ObjectMeta: v1.ObjectMeta{
							Namespace:       "ci",
							Name:            "cluster-secrets-aws",
							ResourceVersion: "999",
						},
						Data: map[string][]byte{
							"k1": []byte("v1"),
							"k2": []byte("v2"),
						},
					},
					{
						ObjectMeta: v1.ObjectMeta{
							Namespace:       ns,
							Name:            "e2e-aws-ovn-cluster-profile",
							ResourceVersion: "1",
						},
						Data: map[string][]byte{
							"k1": []byte("v1"),
							"k2": []byte("v2"),
						},
						Immutable: ptr.To(true),
					},
				},
			},
			wantCalls: []string{
				"acquireWaitWithPriority owner aws free leased random",
				"releaseone owner us-east-1--aws-quota-slice-0 free",
			},
		},
		{
			name: "Cluster profile and regular lease",
			leases: []api.StepLease{{
				ResourceType:         "aws",
				Env:                  api.DefaultLeaseEnv,
				Count:                1,
				ClusterProfile:       "aws",
				ClusterProfileTarget: "e2e-aws-ovn",
			}, {
				ResourceType: "foobar",
				Env:          "FOOBAR_RESOURCE",
				Count:        1,
			}},
			resources: map[string]*common.Resource{
				"acquireWaitWithPriority_aws_free_leased_random": {
					Name: "us-east-1--aws-quota-slice-0",
				},
				"acquireWaitWithPriority_foobar_free_leased_random": {
					Name: "foobar-res-0",
				},
			},
			objects: []ctrlruntimeclient.Object{&corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ci",
					Name:      "cluster-secrets-aws",
				},
				Data: map[string][]byte{
					"k1": []byte("v1"),
					"k2": []byte("v2"),
				},
			}},
			clusterProfiles: map[string]*api.ClusterProfileDetails{
				"aws": {
					Secret:    "cluster-secrets-aws",
					LeaseType: "aws-quota-slice",
				},
			},
			wantProvides: map[string]string{
				"parameter":              "map",
				api.ClusterProfileSetEnv: "",
				api.ClusterProfileParam:  "aws",
				api.DefaultLeaseEnv:      "us-east-1",
				"FOOBAR_RESOURCE":        "foobar-res-0",
			},
			wantSecrets: corev1.SecretList{
				Items: []corev1.Secret{
					{
						ObjectMeta: v1.ObjectMeta{
							Namespace:       "ci",
							Name:            "cluster-secrets-aws",
							ResourceVersion: "999",
						},
						Data: map[string][]byte{
							"k1": []byte("v1"),
							"k2": []byte("v2"),
						},
					},
					{
						ObjectMeta: v1.ObjectMeta{
							Namespace:       ns,
							Name:            "e2e-aws-ovn-cluster-profile",
							ResourceVersion: "1",
						},
						Data: map[string][]byte{
							"k1": []byte("v1"),
							"k2": []byte("v2"),
						},
						Immutable: ptr.To(true),
					},
				},
			},
			wantCalls: []string{
				"acquireWaitWithPriority owner aws free leased random",
				"acquireWaitWithPriority owner foobar free leased random",
				"releaseone owner us-east-1--aws-quota-slice-0 free",
				"releaseone owner foobar-res-0 free",
			},
		},
		{
			name: "Nested cluster profile",
			leases: []api.StepLease{{
				ResourceType:         "openshift-org-aws",
				Env:                  api.DefaultLeaseEnv,
				Count:                1,
				ClusterProfile:       "aws-set",
				ClusterProfileTarget: "e2e-aws-ovn",
			}},
			resources: map[string]*common.Resource{
				"acquireWaitWithPriority_openshift-org-aws_free_leased_random": {
					Name: "aws--us-east-1--quota-slice-0",
				},
			},
			objects: []ctrlruntimeclient.Object{&corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ci",
					Name:      "cluster-secrets-aws",
				},
				Data: map[string][]byte{
					"k1": []byte("v1"),
					"k2": []byte("v2"),
				},
			}},
			clusterProfiles: map[string]*api.ClusterProfileDetails{
				"aws-set": {
					LeaseType: "openshift-org-aws",
				},
				"aws": {
					Secret:    "cluster-secrets-aws",
					LeaseType: "aws-quota-slice",
				},
			},
			wantProvides: map[string]string{
				"parameter":              "map",
				api.ClusterProfileSetEnv: "aws-set",
				api.ClusterProfileParam:  "aws",
				api.DefaultLeaseEnv:      "us-east-1",
			},
			wantSecrets: corev1.SecretList{
				Items: []corev1.Secret{
					{
						ObjectMeta: v1.ObjectMeta{
							Namespace:       "ci",
							Name:            "cluster-secrets-aws",
							ResourceVersion: "999",
						},
						Data: map[string][]byte{
							"k1": []byte("v1"),
							"k2": []byte("v2"),
						},
					},
					{
						ObjectMeta: v1.ObjectMeta{
							Namespace:       ns,
							Name:            "e2e-aws-ovn-cluster-profile",
							ResourceVersion: "1",
						},
						Data: map[string][]byte{
							"k1": []byte("v1"),
							"k2": []byte("v2"),
						},
						Immutable: ptr.To(true),
					},
				},
			},
			wantCalls: []string{
				"acquireWaitWithPriority owner openshift-org-aws free leased random",
				"releaseone owner aws--us-east-1--quota-slice-0 free",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotCalls := make([]string, 0)
			leaseClient := lease.NewFakeClient("owner", "", 1, tc.leaseClientFailingCalls, &gotCalls, tc.resources)

			kubeClient := fakectrlruntimeclient.NewClientBuilder().WithObjects(tc.objects...).Build()
			clusterProfileGetter := newClusterProfileGetter(tc.clusterProfiles)
			step := LeaseStep(&leaseClient, tc.leases, &stepNeedsLease{}, nsFunc, nil, kubeClient, clusterProfileGetter)

			if err := step.Run(context.TODO()); err != nil {
				t.Errorf("unexpected run error: %s", err)
			}

			gotProvides := make(map[string]string)
			provides := step.Provides()
			for k, f := range provides {
				v, err := f()
				if err != nil {
					t.Errorf("failed to resolve provides param %s: %s", k, err)
				}
				gotProvides[k] = v
			}

			if diff := cmp.Diff(tc.wantProvides, gotProvides); diff != "" {
				t.Errorf("unexpected provides map: %s", diff)
			}

			gotSecrets := corev1.SecretList{}
			if err := kubeClient.List(context.TODO(), &gotSecrets, &ctrlruntimeclient.ListOptions{}); err != nil {
				t.Errorf("failed list secrets: %s", err)
			}

			if diff := cmp.Diff(tc.wantSecrets, gotSecrets); diff != "" {
				t.Errorf("unexpected secrets: %s", diff)
			}

			if diff := cmp.Diff(tc.wantCalls, gotCalls); diff != "" {
				t.Errorf("unexpected lease client calls:\n%s", diff)
			}
		})
	}
}

// TestLeaseProvidesCapturesBehavior is a focused unit test that verifies the closure
// capture behavior in Provides(). This test directly exercises the core bug: closures
// must return the correct resources even after s.leases is mutated/reordered.
func TestLeaseProvidesCapturesBehavior(t *testing.T) {
	// Create a lease step with multiple distinct env values and resources
	step := &stepNeedsLease{}
	withLease := LeaseStep(nil, []api.StepLease{
		{Env: "ENV_A", ResourceType: "type-z"},
		{Env: "ENV_B", ResourceType: "type-y"},
		{Env: "ENV_C", ResourceType: "type-x"},
	}, step, emptyNamespace, nil, nil, nil).(*leaseStep)

	// Populate resources for each lease
	withLease.leases[0].resources = []string{"resource-A"}
	withLease.leases[1].resources = []string{"resource-B"}
	withLease.leases[2].resources = []string{"resource-C"}

	// Call Provides() to obtain the parameter map (closures)
	provides := withLease.Provides()

	// Verify closures return correct values BEFORE mutation
	if val, _ := provides["ENV_A"](); val != "resource-A" {
		t.Errorf("Before mutation: ENV_A = %q, want %q", val, "resource-A")
	}
	if val, _ := provides["ENV_B"](); val != "resource-B" {
		t.Errorf("Before mutation: ENV_B = %q, want %q", val, "resource-B")
	}
	if val, _ := provides["ENV_C"](); val != "resource-C" {
		t.Errorf("Before mutation: ENV_C = %q, want %q", val, "resource-C")
	}

	// Mutate s.leases by sorting (simulates what acquireLeases does)
	sort.Slice(withLease.leases, func(i, j int) bool {
		return withLease.leases[i].ResourceType < withLease.leases[j].ResourceType
	})

	// After sort, the slice order is now: type-x (ENV_C), type-y (ENV_B), type-z (ENV_A)
	// Verify each closure still returns the correct resource for its original Env
	testCases := []struct {
		env      string
		expected string
	}{
		{"ENV_A", "resource-A"},
		{"ENV_B", "resource-B"},
		{"ENV_C", "resource-C"},
	}

	for _, tc := range testCases {
		val, err := provides[tc.env]()
		if err != nil {
			t.Errorf("After sort: error getting %s: %v", tc.env, err)
		}
		if val != tc.expected {
			t.Errorf("After sort: %s = %q, want %q (closure captured wrong lease after slice reorder)",
				tc.env, val, tc.expected)
		}
	}

	t.Log("✓ Closures correctly return resources for their original Env despite slice reordering")
}

// TestLeaseSortDoesNotAffectEnvMapping verifies that sorting leases by ResourceType
// doesn't cause environment variables to be mapped to the wrong lease resources.
// This is an end-to-end regression test for the bug where closures captured pointers
// to slice elements that were later reordered by sort.Slice() in acquireLeases().
func TestLeaseSortDoesNotAffectEnvMapping(t *testing.T) {
	// Simulate the scenario from the bug report: two leases where alphabetical
	// sorting by ResourceType would reverse their order.
	leases := []api.StepLease{
		{Env: "LEASED_RESOURCE", ResourceType: "vsphere-dis-2-quota-slice", Count: 1},
		{Env: "VSPHERE_BASTION_LEASED_RESOURCE", ResourceType: "vsphere-connected-2-quota-slice", Count: 1},
	}

	// Create a lease client that returns predictable resources
	// The resources map key format is: acquireWaitWithPriority_{resourceType}_free_leased_random
	resources := map[string]*common.Resource{
		"acquireWaitWithPriority_vsphere-dis-2-quota-slice_free_leased_random": {
			Name: "bcr01a.dal12.990",
		},
		"acquireWaitWithPriority_vsphere-connected-2-quota-slice_free_leased_random": {
			Name: "bcr01a.dal12.1148",
		},
	}
	var calls []string
	client := lease.NewFakeClient("owner", "url", 1, nil, &calls, resources)

	// Create the lease step
	step := &stepNeedsLease{}
	withLease := LeaseStep(&client, leases, step, emptyNamespace, nil, nil, nil).(*leaseStep)

	// Get the Provides() map BEFORE running (before sort happens)
	provides := withLease.Provides()

	// Run the step - this triggers acquireLeases() which sorts the slice
	ctx := context.Background()
	if err := withLease.Run(ctx); err != nil {
		t.Fatalf("unexpected error running lease step: %v", err)
	}

	// Now evaluate the closures - they should return the correct resources
	// despite the slice having been sorted
	leased, err := provides["LEASED_RESOURCE"]()
	if err != nil {
		t.Fatalf("error getting LEASED_RESOURCE: %v", err)
	}

	bastion, err := provides["VSPHERE_BASTION_LEASED_RESOURCE"]()
	if err != nil {
		t.Fatalf("error getting VSPHERE_BASTION_LEASED_RESOURCE: %v", err)
	}

	// Verify the env vars map to the correct resources
	expectedLeased := "bcr01a.dal12.990"
	expectedBastion := "bcr01a.dal12.1148"

	if leased != expectedLeased {
		t.Errorf("LEASED_RESOURCE = %q, expected %q (got vsphere-connected resource instead of vsphere-dis)",
			leased, expectedLeased)
	}

	if bastion != expectedBastion {
		t.Errorf("VSPHERE_BASTION_LEASED_RESOURCE = %q, expected %q (got vsphere-dis resource instead of vsphere-connected)",
			bastion, expectedBastion)
	}

	t.Logf("✓ LEASED_RESOURCE correctly maps to %s (vsphere-dis)", leased)
	t.Logf("✓ VSPHERE_BASTION_LEASED_RESOURCE correctly maps to %s (vsphere-connected)", bastion)
}
