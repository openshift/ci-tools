package steps

import (
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/openshift/ci-operator/pkg/api"
)

func preparePodStep(t *testing.T, namespace string) (*podStep, stepExpectation, PodClient) {
	stepName := "StepName"
	podName := "TestName"
	var artifactDir string
	var resources api.ResourceConfiguration

	config := PodStepConfiguration{
		As: podName,
		From: api.ImageStreamTagReference{
			Cluster: "kluster",
			Name:    "somename",
			Tag:     "sometag",
			As:      "FromName",
		},
		Commands:           "launch-tests",
		ArtifactDir:        artifactDir,
		ServiceAccountName: "",
	}

	buildID := "test-build-id"
	jobName := "very-cool-prow-job"
	pjID := "prow-job-id"
	jobSpec := &api.JobSpec{
		Job:       jobName,
		BuildId:   buildID,
		ProwJobID: pjID,
		Namespace: namespace,
	}

	fakecs := ciopTestingClient{*fake.NewSimpleClientset(), t}
	client := NewPodClient(fakecs.Core(), nil, nil)

	ps := PodStep(stepName, config, resources, client, artifactDir, jobSpec)

	specification := stepExpectation{
		name:     podName,
		requires: []api.StepLink{api.ImagesReadyLink()},
		creates:  []api.StepLink{},
		provides: providesExpectation{
			params: nil,
			link:   nil,
		},
		inputs: inputsExpectation{
			values: nil,
			err:    false,
		},
	}

	return ps.(*podStep), specification, client
}

func makeExpectedPod(step *podStep, phaseAfterRun v1.PodPhase) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      step.config.As,
			Namespace: step.jobSpec.Namespace,
			Labels: map[string]string{
				"build-id":      step.jobSpec.BuildId,
				"created-by-ci": "true",
				"job":           step.jobSpec.Job,

				"persists-between-builds": "false",
				"prow.k8s.io/id":          step.jobSpec.ProwJobID,
			},
			Annotations: map[string]string{
				"ci.openshift.io/job-spec": "",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:                     step.name,
					Image:                    "somename:sometag",
					Command:                  []string{"/bin/sh", "-c", "#!/bin/sh\nset -eu\nlaunch-tests"},
					TerminationMessagePolicy: v1.TerminationMessageFallbackToLogsOnError,
				},
			},
			RestartPolicy: v1.RestartPolicyNever,
		},
		Status: v1.PodStatus{Phase: phaseAfterRun},
	}
}

func TestPodStepMethods(t *testing.T) {
	namespace := "TestNamespace"
	ps, spec, _ := preparePodStep(t, namespace)
	examineStep(t, ps, spec)
}

func TestPodStepExecution(t *testing.T) {
	namespace := "TestNamespace"
	testCases := []struct {
		podStatus      v1.PodPhase
		expectRunError bool
	}{
		{podStatus: v1.PodSucceeded, expectRunError: false},
		{podStatus: v1.PodFailed, expectRunError: true},
	}

	for _, tc := range testCases {
		ps, _, client := preparePodStep(t, namespace)
		expectedPod := makeExpectedPod(ps, tc.podStatus)

		executionExpectation := executionExpectation{
			prerun: doneExpectation{
				value: false,
				err:   false,
			},
			runError: tc.expectRunError,
			postrun: doneExpectation{
				value: true,
				err:   false,
			},
		}

		watcher, err := client.Pods(namespace).Watch(meta.ListOptions{})
		if err != nil {
			t.Errorf("Failed to create a watcher over pods in namespace")
		}
		defer watcher.Stop()

		clusterBehavior := func() {
			// Expect a single event (a Creation) to happen
			// Immediately set the Pod status to Succeeded, because
			// that is what the step waits on
			for {
				event, ok := <-watcher.ResultChan()
				if !ok {
					break
				}
				if pod, ok := event.Object.(*v1.Pod); ok {
					pod.Status.Phase = tc.podStatus
					client.Pods(namespace).UpdateStatus(pod)
					break
				}
			}
		}

		executeStep(t, ps, executionExpectation, clusterBehavior)

		if pod, err := client.Pods(namespace).Get(ps.Name(), meta.GetOptions{}); !reflect.DeepEqual(expectedPod, pod) {
			t.Errorf("Pod is different than expected:\n%s", diff.ObjectReflectDiff(expectedPod, pod))
		} else if err != nil {
			t.Errorf("Could not Get() expected Pod, err=%v", err)
		}
	}
}
