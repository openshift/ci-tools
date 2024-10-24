package steps

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
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
		Metadata: api.Metadata{
			Org:     "org",
			Repo:    "repo",
			Branch:  "base-ref",
			Variant: "variant",
		},
		Target: "target",
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
				SkipCloning: utilpointer.Bool(true),
			},
		},
	}
	jobSpec.SetNamespace(namespace)

	client := kubernetes.NewPodClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().Build()), nil, nil, 0)
	ps := PodStep(stepName, config, resources, client, jobSpec, nil)

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
		clone          bool
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
		{
			purpose:        "Successful pod with cloning",
			podStatus:      corev1.PodSucceeded,
			clone:          true,
			expectRunError: false,
		},
		{
			purpose:        "Successful pod with cloning new run",
			podStatus:      corev1.PodSucceeded,
			clone:          true,
			expectRunError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.purpose, func(t *testing.T) {
			ps, _ := preparePodStep(namespace)
			ps.config.Clone = tc.clone
			ps.client = kubernetes.NewPodClient(loggingclient.New(&podStatusChangingClient{WithWatch: fakectrlruntimeclient.NewClientBuilder().Build(), dest: tc.podStatus}), nil, nil, 0)

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
		{
			name: "with cluster claim",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.clusterClaim = &api.ClusterClaim{}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			podStepTemplate := expectedPodStepTemplate()
			tc.podStep(podStepTemplate)

			pod, err := podStepTemplate.generatePodForStep("", corev1.ResourceRequirements{}, false)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}

			testhelper.CompareWithFixture(t, pod.Spec.Volumes, testhelper.WithPrefix("volumes"))
			testhelper.CompareWithFixture(t, pod.Spec.Containers[0].VolumeMounts, testhelper.WithPrefix("mounts"))
			if podStepTemplate.clusterClaim != nil {
				testhelper.CompareWithFixture(t, pod.Spec.Containers[0].Env, testhelper.WithPrefix("env"))
			}
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
	ctrlruntimeclient.WithWatch
	dest corev1.PodPhase
}

func (ps *podStatusChangingClient) Create(ctx context.Context, o ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if pod, ok := o.(*corev1.Pod); ok {
		pod.Status.Phase = ps.dest
	}
	return ps.WithWatch.Create(ctx, o, opts...)
}

func TestTestStepAndRequires(t *testing.T) {
	tests := []struct {
		name     string
		config   api.TestStepConfiguration
		expected []api.StepLink
	}{
		{
			name: "step without claim",
			config: api.TestStepConfiguration{
				As:                         "some",
				ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "cli", Clone: utilpointer.Bool(false)},
			},
			expected: []api.StepLink{api.InternalImageLink("cli")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := TestStep(tc.config, nil, nil, nil, "").Requires()
			if len(actual) == len(tc.expected) {
				matches := true
				for i := range actual {
					if !actual[i].SatisfiedBy(tc.expected[i]) {
						matches = false
						break
					}
				}
				if matches {
					return
				}
			}
			t.Errorf("incorrect requirements: %s", cmp.Diff(actual, tc.expected, api.Comparer()))
		})
	}
}
