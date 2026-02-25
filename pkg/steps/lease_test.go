package steps

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

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
	withLease := LeaseStep(nil, leases, &step, emptyNamespace, nil)
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
	withLease := LeaseStep(nil, leases, &stepNeedsLease{}, emptyNamespace, nil)
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
			err := LeaseStep(&client, leases, &s, func() string { return "" }, nil).Run(ctx)
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

func TestAcquireRelease(t *testing.T) {
	var calls []string
	client := lease.NewFakeClient("owner", "url", 0, nil, &calls, nil)
	leases := []api.StepLease{
		{ResourceType: "rtype1", Count: 1},
		{ResourceType: "rtype0", Count: 2},
	}
	step := stepNeedsLease{}
	withLease := LeaseStep(&client, leases, &step, func() string { return "" }, nil)
	if err := withLease.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !step.ran {
		t.Fatal("step was not executed")
	}
	expected := []string{
		"acquireWaitWithPriority owner rtype0 free leased random",
		"acquireWaitWithPriority owner rtype0 free leased random",
		"acquireWaitWithPriority owner rtype1 free leased random",
		"releaseone owner rtype1_2 free",
		"releaseone owner rtype0_0 free",
		"releaseone owner rtype0_1 free",
	}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("wrong calls to the lease client: %s", diff.ObjectDiff(calls, expected))
	}
}
