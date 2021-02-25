package steps

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func preparePodStep(namespace string) (*podStep, stepExpectation) {
	stepName := "StepName"
	podName := "TestName"
	var resources api.ResourceConfiguration

	config := PodStepConfiguration{
		As: podName,
		From: api.ImageStreamTagReference{
			Name: "somename",
			Tag:  "sometag",
			As:   "FromName",
		},
		Commands:           "launch-tests",
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
			DecorationConfig: &prowapi.DecorationConfig{
				Timeout:     &prowapi.Duration{Duration: time.Minute},
				GracePeriod: &prowapi.Duration{Duration: time.Second},
				UtilityImages: &prowapi.UtilityImages{
					Sidecar:    "sidecar",
					Entrypoint: "entrypoint",
				},
			},
		},
	}
	jobSpec.SetNamespace(namespace)

	client := &podClient{loggingclient.New(fakectrlruntimeclient.NewFakeClient()), nil, nil}
	ps := PodStep(stepName, config, resources, client, jobSpec)

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

	return ps.(*podStep), specification
}

func TestPodStepMethods(t *testing.T) {
	namespace := "TestNamespace"
	ps, spec := preparePodStep(namespace)
	examineStep(t, ps, spec)
}

func TestPodStepExecution(t *testing.T) {
	namespace := "TestNamespace"
	testCases := []struct {
		purpose        string
		podStatus      corev1.PodPhase
		expectRunError bool
	}{
		{
			purpose:        "Pod run by PodStep succeeds so PodStep terminates and returns no error",
			podStatus:      corev1.PodSucceeded,
			expectRunError: false,
		}, {
			purpose:        "Pod run by PodStep fails so PodStep terminates and returns an error",
			podStatus:      corev1.PodFailed,
			expectRunError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.purpose, func(t *testing.T) {
			ps, _ := preparePodStep(namespace)
			ps.client = &podClient{LoggingClient: loggingclient.New(&podStatusChangingClient{Client: fakectrlruntimeclient.NewFakeClient(), dest: tc.podStatus})}

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

			executeStep(t, ps, executionExpectation)

			pod := &corev1.Pod{}
			if err := ps.client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: ps.Name()}, pod); err != nil {
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
				expectedPodStepTemplate.config.Secrets = []*api.Secret{{Name: "some-secret"}}
			},
		},
		{
			name: "with secret name and path results in mounted secret with custom path",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.config.Secrets = []*api.Secret{
					{
						Name:      "some-secret",
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
						Name:      "some-secret",
						MountPath: "/usr/local/secrets",
					},
				}
			},
		},
		{
			name: "with artifacts, multiple secrets and path results in multiple mounts",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.config.Secrets = []*api.Secret{
					{
						Name:      "some-secret",
						MountPath: "/usr/local/secrets",
					},
					{
						Name:      "another-secret",
						MountPath: "/usr/local/secrets2",
					},
				}
			},
		},
		{
			name: "with artifacts, multiple secrets and path results with unspecified mounts",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.config.Secrets = []*api.Secret{
					{
						Name: "some-secret",
					},
					{
						Name: "another-secret",
					},
				}
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

			pod, err := podStepTemplate.generatePodForStep("", corev1.ResourceRequirements{})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}

			testhelper.CompareWithFixture(t, pod.Spec.Volumes, testhelper.WithPrefix("volumes"))
			testhelper.CompareWithFixture(t, pod.Spec.Containers[0].VolumeMounts, testhelper.WithPrefix("mounts"))
		})
	}

}

func expectedPodStepTemplate() *podStep {
	s := &podStep{
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Job:       "podStep.jobSpec.Job",
				BuildID:   "podStep.jobSpec.BuildId",
				ProwJobID: "podStep.jobSpec.ProwJobID",
				Type:      "periodic",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: time.Minute},
					GracePeriod: &prowapi.Duration{Duration: time.Second},
					UtilityImages: &prowapi.UtilityImages{
						Sidecar:    "sidecar",
						Entrypoint: "entrypoint",
					},
				},
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
	s.jobSpec.SetNamespace("some-ns")
	return s
}

var _ ctrlruntimeclient.Client = &podStatusChangingClient{}

type podStatusChangingClient struct {
	ctrlruntimeclient.Client
	dest corev1.PodPhase
}

func (ps *podStatusChangingClient) Create(ctx context.Context, o ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if pod, ok := o.(*corev1.Pod); ok {
		pod.Status.Phase = ps.dest
	}
	return ps.Client.Create(ctx, o, opts...)
}
