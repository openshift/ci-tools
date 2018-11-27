package steps

// This file contains helpers useful for testing ci-operator steps

import (
	"context"
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/fake"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	fakecorev1 "k8s.io/client-go/kubernetes/typed/core/v1/fake"

	"github.com/openshift/ci-operator/pkg/api"
)

// Fake Clientset, created so we can override its `Core()` method
// and return our fake CoreV1 API (=ciopTestingCore)

type ciopTestingClient struct {
	fake.Clientset
	t *testing.T
}

func (c *ciopTestingClient) Core() corev1.CoreV1Interface {
	fc := c.Clientset.Core().(*fakecorev1.FakeCoreV1)
	return &ciopTestingCore{*fc, c.t}
}

// Fake CoreV1, created so we can override its `Pods()` method
// and return our fake Pods API (=ciopTestingPods)

type ciopTestingCore struct {
	fakecorev1.FakeCoreV1
	t *testing.T
}

func (c *ciopTestingCore) Pods(ns string) corev1.PodInterface {
	pods := c.FakeCoreV1.Pods(ns).(*fakecorev1.FakePods)
	return &ciopTestingPods{*pods, c.t}
}

// Fake Pods API

type ciopTestingPods struct {
	fakecorev1.FakePods
	t *testing.T
}

// Fake Create() provided by the lib creates objects without default values, so
// they would be created without any sensible Phase, which causes problems in
// the ci-operator code. Therefore, our fake Create() always creates Pods with
// a `Pending` phase if it does not carry phase already.
func (c *ciopTestingPods) Create(pod *v1.Pod) (*v1.Pod, error) {
	if pod.Status.Phase == "" {
		pod.Status.Phase = v1.PodPending
	}
	c.t.Logf("FakePods.Create(%v)", pod)
	return c.FakePods.Create(pod)
}

type doneExpectation struct {
	value bool
	err   bool
}

type providesExpectation struct {
	params api.ParameterMap
	link   api.StepLink
}

type inputsExpectation struct {
	values api.InputDefinition
	err    bool
}

type stepExpectation struct {
	name     string
	requires []api.StepLink
	creates  []api.StepLink
	provides providesExpectation
	inputs   inputsExpectation
}

type executionExpectation struct {
	prerun   doneExpectation
	runError bool
	postrun  doneExpectation
}

func someStepLink(as string) api.StepLink {
	return api.ExternalImageLink(api.ImageStreamTagReference{
		Cluster:   "cluster.com",
		Namespace: "namespace",
		Name:      "name",
		Tag:       "tag",
		As:        as,
	})
}

func errorCheck(t *testing.T, message string, expected bool, err error) {
	if expected && err == nil {
		t.Errorf("%s: expected to return error, returned nil", message)
	}
	if !expected && err != nil {
		t.Errorf("%s: returned unexpected error: %v", message, err)
	}
}

func examineStep(t *testing.T, step api.Step, expected stepExpectation) {
	// Test the "informative" methods
	if name := step.Name(); name != expected.name {
		t.Errorf("step.Name() mismatch: expected '%s', got '%s'", expected.name, name)
	}
	if desc := step.Description(); len(desc) == 0 {
		t.Errorf("step.Description() returned an empty string")
	}
	if reqs := step.Requires(); !reflect.DeepEqual(expected.requires, reqs) {
		t.Errorf("step.Requires() returned different links:\n%s", diff.ObjectReflectDiff(expected.requires, reqs))
	}
	if creates := step.Creates(); !reflect.DeepEqual(expected.creates, creates) {
		t.Errorf("step.Creates() returned different links:\n%s", diff.ObjectReflectDiff(expected.creates, creates))
	}

	params, link := step.Provides()
	if !reflect.DeepEqual(expected.provides.params, params) {
		t.Errorf("step.Provides returned different params\n%s", diff.ObjectReflectDiff(expected.provides.params, link))
	}
	if !reflect.DeepEqual(expected.provides.link, link) {
		t.Errorf("step.Provides returned different link\n%s", diff.ObjectReflectDiff(expected.provides.link, link))
	}

	inputs, err := step.Inputs(context.Background(), false)
	if !reflect.DeepEqual(expected.inputs.values, inputs) {
		t.Errorf("step.Inputs returned different inputs\n%s", diff.ObjectReflectDiff(expected.inputs.values, inputs))
	}
	errorCheck(t, "step.Inputs", expected.inputs.err, err)
}

func executeStep(t *testing.T, step api.Step, expected executionExpectation, fakeClusterBehavior func()) {
	done, err := step.Done()
	if !reflect.DeepEqual(expected.prerun.value, done) {
		t.Errorf("step.Done() before Run() returned %t, expected %t)", done, expected.prerun.value)
	}
	errorCheck(t, "step.Done() before Run()", expected.prerun.err, err)

	if fakeClusterBehavior != nil {
		go fakeClusterBehavior()
	}

	err = step.Run(context.Background(), false)
	errorCheck(t, "step.Run()", expected.runError, err)

	done, err = step.Done()
	if !reflect.DeepEqual(expected.postrun.value, done) {
		t.Errorf("step.Done() after Run() returned %t, expected %t)", done, expected.postrun.value)
	}
	errorCheck(t, "step.Done() after Run()", expected.postrun.err, err)
}
