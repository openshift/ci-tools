package steps

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/lease"
)

type stepNeedsLease struct {
	fail, ran bool
}

func (stepNeedsLease) Inputs(dry bool) (api.InputDefinition, error) {
	return api.InputDefinition{"step", "inputs"}, nil
}
func (s *stepNeedsLease) Run(ctx context.Context, dry bool) error {
	s.ran = true
	if s.fail {
		return errors.New("injected failure")
	}
	return nil
}

func (stepNeedsLease) Name() string        { return "needs_lease" }
func (stepNeedsLease) Description() string { return "this step needs a lease" }
func (stepNeedsLease) Requires() []api.StepLink {
	return []api.StepLink{api.StableImagesLink(api.LatestStableName)}
}
func (stepNeedsLease) Creates() []api.StepLink { return []api.StepLink{api.ImagesReadyLink()} }

func (stepNeedsLease) Provides() (api.ParameterMap, api.StepLink) {
	return api.ParameterMap{
		"parameter": func() (string, error) { return "map", nil },
	}, api.ExternalImageLink(api.ImageStreamTagReference{Name: "test"})
}

func (stepNeedsLease) SubTests() []*junit.TestCase {
	ret := junit.TestCase{}
	return []*junit.TestCase{&ret}
}

func TestLeaseStepForward(t *testing.T) {
	name := "lease_name"
	step := stepNeedsLease{}
	withLease := LeaseStep(nil, name, &step, func() string { return "" }, nil)
	t.Run("Inputs", func(t *testing.T) {
		s, err := step.Inputs(false)
		if err != nil {
			t.Fatal(err)
		}
		l, err := withLease.Inputs(false)
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
	t.Run("Provides", func(t *testing.T) {
		sParam, sLinks := step.Provides()
		sRet, err := sParam["parameter"]()
		if err != nil {
			t.Fatal(err)
		}
		lParam, lLinks := withLease.Provides()
		lRet, err := lParam["parameter"]()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(lRet, sRet) {
			t.Errorf("not properly forwarded (param): %s", diff.ObjectDiff(lParam, sParam))
		}
		if !reflect.DeepEqual(lLinks, sLinks) {
			t.Errorf("not properly forwarded (links): %s", diff.ObjectDiff(lLinks, sLinks))
		}
	})
	t.Run("SubTests", func(T *testing.T) {
		s, l := step.SubTests(), withLease.(subtestReporter).SubTests()
		if !reflect.DeepEqual(l, s) {
			t.Errorf("not properly forwarded: %s", diff.ObjectDiff(l, s))
		}
	})
}

func TestError(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		runFails bool
		failures sets.String
		expected []string
	}{{
		name:     "acquire fails",
		failures: sets.NewString("acquire owner rtype free leased random"),
		expected: []string{"acquire owner rtype free leased random"},
	}, {
		name:     "release fails",
		failures: sets.NewString("releaseone owner rtype0 free"),
		expected: []string{
			"acquire owner rtype free leased random",
			"releaseone owner rtype0 free",
		},
	}, {
		name:     "run fails",
		runFails: true,
		expected: []string{
			"acquire owner rtype free leased random",
			"releaseone owner rtype0 free",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			client := lease.NewFakeClient("owner", "url", 0, tc.failures, &calls)
			s := stepNeedsLease{fail: tc.runFails}
			if LeaseStep(&client, "rtype", &s, func() string { return "" }, nil).Run(ctx, false) == nil {
				t.Fatalf("unexpected success, calls: %#v", calls)
			}
			if !reflect.DeepEqual(calls, tc.expected) {
				t.Fatalf("wrong calls to the lease client: %s", diff.ObjectDiff(calls, tc.expected))
			}
		})
	}
}

func TestAcquireRelease(t *testing.T) {
	var calls []string
	client := lease.NewFakeClient("owner", "url", 0, nil, &calls)
	step := stepNeedsLease{}
	withLease := LeaseStep(&client, "rtype", &step, func() string { return "" }, nil)
	if err := withLease.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !step.ran {
		t.Fatal("step was not executed")
	}
	expected := []string{
		"acquire owner rtype free leased random",
		"releaseone owner rtype0 free",
	}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("wrong calls to the lease client: %s", diff.ObjectDiff(calls, expected))
	}
}
