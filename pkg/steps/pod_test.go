package steps

import (
	"context"
	"fmt"
	"testing"

	apiimagev1 "github.com/openshift/api/image/v1"
	fakeimageclientset "github.com/openshift/client-go/image/clientset/versioned/fake"
	"k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func preparePodStep(t *testing.T, namespace string) (*podStep, stepExpectation, PodClient) {
	stepName := "StepName"
	podName := "TestName"
	var artifactDir string
	var resources api.ResourceConfiguration

	config := PodStepConfiguration{
		As: podName,
		From: api.ImageStreamTagReference{
			Name: "somename",
			Tag:  "sometag",
			As:   "FromName",
		},
		Commands:           "launch-tests",
		ArtifactDir:        artifactDir,
		ServiceAccountName: "",
	}

	buildID := "test-build-id"
	jobName := "very-cool-prow-job"
	pjID := "prow-job-id"
	jobSpec := &api.JobSpec{
		JobSpec: downwardapi.JobSpec{
			Job:       jobName,
			BuildID:   buildID,
			ProwJobID: pjID,
			Type:      prowapi.PresubmitJob,
			Refs: &prowapi.Refs{
				Org:     "org",
				Repo:    "repo",
				Pulls:   []prowapi.Pull{{Number: 123, SHA: "72532003f9e01e89f455187dd92c275204bc9781"}},
				BaseRef: "base-ref",
				BaseSHA: "base-sha",
			},
		},
	}
	jobSpec.SetNamespace(namespace)

	fakecs := ciopTestingClient{
		kubecs: fake.NewSimpleClientset(),
		imagecs: fakeimageclientset.NewSimpleClientset(&apiimagev1.ImageStream{
			ObjectMeta: meta.ObjectMeta{
				Namespace: namespace,
				Name:      "somename",
			},
			Status: apiimagev1.ImageStreamStatus{
				PublicDockerImageRepository: fmt.Sprintf("some-reg/%s/somename", namespace),
				Tags: []apiimagev1.NamedTagEventList{
					{
						Tag: "sometag",
						Items: []apiimagev1.TagEvent{
							{
								Image: "sha256:47e2f82dbede8ff990e6e240f82d78830e7558f7b30df7bd8c0693992018b1e3",
							},
						},
					},
				},
			},
		}),
		t: t,
	}
	podClient := NewPodClient(fakecs.Core(), nil, nil)
	ps := PodStep(stepName, config, resources, podClient, fakecs.ImageV1(), artifactDir, jobSpec)

	specification := stepExpectation{
		name:     podName,
		requires: []api.StepLink{api.ImagesReadyLink()},
		creates:  []api.StepLink{},
		provides: providesExpectation{
			params: nil,
		},
		inputs: inputsExpectation{
			values: nil,
			err:    false,
		},
	}

	return ps.(*podStep), specification, podClient
}

func TestPodStepMethods(t *testing.T) {
	namespace := "TestNamespace"
	ps, spec, _ := preparePodStep(t, namespace)
	examineStep(t, ps, spec)
}

func TestPodStepExecution(t *testing.T) {
	namespace := "TestNamespace"
	testCases := []struct {
		purpose        string
		podStatus      v1.PodPhase
		expectRunError bool
	}{
		{
			purpose:        "Pod run by PodStep succeeds so PodStep terminates and returns no error",
			podStatus:      v1.PodSucceeded,
			expectRunError: false,
		}, {
			purpose:        "Pod run by PodStep fails so PodStep terminates and returns an error",
			podStatus:      v1.PodFailed,
			expectRunError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.purpose, func(t *testing.T) {
			ps, _, client := preparePodStep(t, namespace)

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

			watcher, err := client.Pods(namespace).Watch(context.TODO(), meta.ListOptions{})
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
						t.Error("Fake cluster: watcher event closed, exiting")
						break
					}
					if pod, ok := event.Object.(*v1.Pod); ok {
						t.Logf("Fake cluster: Received event on pod '%s': %s", pod.ObjectMeta.Name, event.Type)
						t.Logf("Fake cluster: Updating pod '%s' status to '%s' and exiting", pod.ObjectMeta.Name, tc.podStatus)
						// make a copy to avoid a race
						newPod := pod.DeepCopy()
						newPod.Status.Phase = tc.podStatus
						if _, err := client.Pods(namespace).UpdateStatus(context.TODO(), newPod, meta.UpdateOptions{}); err != nil {
							t.Errorf("Fake cluster: UpdateStatus() returned an error: %v", err)
						}
						break
					}
					t.Logf("Fake cluster: Received non-pod event: %v", event)
				}
			}

			executeStep(t, ps, executionExpectation, clusterBehavior)

			pod, err := client.Pods(namespace).Get(context.TODO(), ps.Name(), meta.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get pod: %v", err)
			}
			testhelper.CompareWithFixture(t, pod)
		})
	}
}

func TestGetPodObjectMounts(t *testing.T) {
	testCases := []struct {
		name    string
		podStep func(*podStep)
	}{
		{
			name:    "no secret results in no mounted secrets",
			podStep: func(expectedPodStepTemplate *podStep) {},
		},
		{
			name: "with secret name results in secret mounted with default path",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.config.Secrets = []*api.Secret{{Name: testSecretName}}
			},
		},
		{
			name: "with secret name and path results in mounted secret with custom path",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.config.Secrets = []*api.Secret{
					{
						Name:      testSecretName,
						MountPath: "/usr/local/secrets",
					},
				}
			},
		},
		{
			name: "with artifacts, secret name and path results in multiple mounts",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.config.Secrets = []*api.Secret{
					{
						Name:      testSecretName,
						MountPath: "/usr/local/secrets",
					},
				}
				expectedPodStepTemplate.artifactDir = "/tmp/artifacts"
				expectedPodStepTemplate.config.ArtifactDir = "/tmp/artifacts"
			},
		},
		{
			name: "with memory backed volume gets a volume",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.config.MemoryBackedVolume = &api.MemoryBackedVolume{Size: "1Gi"}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			podStepTemplate := expectedPodStepTemplate()
			tc.podStep(podStepTemplate)

			pod, err := podStepTemplate.generatePodForStep("", v1.ResourceRequirements{})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}

			testhelper.CompareWithFixture(t, pod.Spec.Volumes, testhelper.WithPrefix("volumes"))
			testhelper.CompareWithFixture(t, pod.Spec.Containers[0].VolumeMounts, testhelper.WithPrefix("mounts"))
		})
	}

}

func expectedPodStepTemplate() *podStep {
	return &podStep{
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Job:       "podStep.jobSpec.Job",
				BuildID:   "podStep.jobSpec.BuildId",
				ProwJobID: "podStep.jobSpec.ProwJobID",
				Type:      "periodic",
			},
		},
		name: "podStep.name",
		config: PodStepConfiguration{
			ServiceAccountName: "podStep.config.PodStepConfiguration.ServiceAccountName",
			Commands:           "podStep.config.Command",
			As:                 "podStep.config.As",
			From: api.ImageStreamTagReference{
				Name: "podStep.config.From.Name",
				Tag:  "podStep.config.From.Tag",
			},
		},
	}
}
