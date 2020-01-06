package steps

import (
	"testing"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/fake"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/decorate"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
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
		Namespace: namespace,
	}

	fakecs := ciopTestingClient{
		kubecs:  fake.NewSimpleClientset(),
		imagecs: nil,
		t:       t,
	}
	client := NewPodClient(fakecs.Core(), nil, nil)
	dryLogger := &DryLogger{}
	ps := PodStep(stepName, config, resources, client, artifactDir, jobSpec, dryLogger)

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
				"build-id":                    step.jobSpec.BuildID,
				"created-by-ci":               "true",
				"job":                         step.jobSpec.Job,
				"prow.k8s.io/id":              step.jobSpec.ProwJobID,
				"ci.openshift.io/refs.org":    step.jobSpec.JobSpec.Refs.Org,
				"ci.openshift.io/refs.repo":   step.jobSpec.JobSpec.Refs.Repo,
				"ci.openshift.io/refs.branch": step.jobSpec.JobSpec.Refs.BaseRef,
			},
			Annotations: map[string]string{
				"ci.openshift.io/job-spec":                     "",
				"ci-operator.openshift.io/container-sub-tests": step.name,
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:                     step.name,
					Image:                    "somename:sometag",
					Command:                  []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\nlaunch-tests"},
					TerminationMessagePolicy: v1.TerminationMessageFallbackToLogsOnError,
					Env: decorate.KubeEnv(map[string]string{
						"BUILD_ID":      "test-build-id",
						"CI":            "true",
						"JOB_NAME":      "very-cool-prow-job",
						"JOB_SPEC":      `{"type":"presubmit","job":"very-cool-prow-job","buildid":"test-build-id","prowjobid":"prow-job-id","refs":{"org":"org","repo":"repo","base_ref":"base-ref","base_sha":"base-sha","pulls":[{"number":123,"author":"","sha":"72532003f9e01e89f455187dd92c275204bc9781"}]}}`,
						"JOB_TYPE":      string(prowapi.PresubmitJob),
						"PROW_JOB_ID":   "prow-job-id",
						"PULL_BASE_REF": "base-ref",
						"PULL_BASE_SHA": "base-sha",
						"PULL_NUMBER":   "123",
						"PULL_PULL_SHA": "72532003f9e01e89f455187dd92c275204bc9781",
						"PULL_REFS":     step.jobSpec.JobSpec.Refs.String(),
						"REPO_NAME":     "repo",
						"REPO_OWNER":    "org",
					}),
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
						t.Error("Fake cluster: watcher event closed, exiting")
						break
					}
					if pod, ok := event.Object.(*v1.Pod); ok {
						t.Logf("Fake cluster: Received event on pod '%s': %s", pod.ObjectMeta.Name, event.Type)
						t.Logf("Fake cluster: Updating pod '%s' status to '%s' and exiting", pod.ObjectMeta.Name, tc.podStatus)
						// make a copy to avoid a race
						newPod := pod.DeepCopy()
						newPod.Status.Phase = tc.podStatus
						if _, err := client.Pods(namespace).UpdateStatus(newPod); err != nil {
							t.Errorf("Fake cluster: UpdateStatus() returned an error: %v", err)
						}
						break
					}
					t.Logf("Fake cluster: Received non-pod event: %v", event)
				}
			}

			executeStep(t, ps, executionExpectation, clusterBehavior)

			if pod, err := client.Pods(namespace).Get(ps.Name(), meta.GetOptions{}); !equality.Semantic.DeepEqual(expectedPod, pod) {
				t.Errorf("Pod is different than expected:\n%s", diff.ObjectReflectDiff(expectedPod, pod))
			} else if err != nil {
				t.Errorf("Could not Get() expected Pod, err=%v", err)
			}
		})
	}
}

func TestGetPodObjectMounts(t *testing.T) {
	oneGi := resource.MustParse("1Gi")
	testCases := []struct {
		name                 string
		podStep              func(*podStep)
		expectedVolumeConfig *v1.Pod
	}{
		{
			name:    "no secret results in no mounted secrets",
			podStep: func(expectedPodStepTemplate *podStep) {},
			expectedVolumeConfig: &v1.Pod{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							VolumeMounts: []v1.VolumeMount{},
						},
					},
					Volumes: []v1.Volume{},
				},
			},
		},
		{
			name: "with secret name results in secret mounted with default path",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.config.Secrets = []*api.Secret{{Name: testSecretName}}
			},
			expectedVolumeConfig: &v1.Pod{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      testSecretName,
									MountPath: testSecretDefaultPath,
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []v1.Volume{
						{
							Name: testSecretName,
							VolumeSource: v1.VolumeSource{
								Secret: &v1.SecretVolumeSource{
									SecretName: testSecretName,
								},
							},
						},
					},
				},
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
			expectedVolumeConfig: &v1.Pod{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      testSecretName,
									MountPath: "/usr/local/secrets",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []v1.Volume{
						{
							Name: testSecretName,
							VolumeSource: v1.VolumeSource{
								Secret: &v1.SecretVolumeSource{
									SecretName: testSecretName,
								},
							},
						},
					},
				},
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
			expectedVolumeConfig: &v1.Pod{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      "artifacts",
									MountPath: "/tmp/artifacts",
								},
								{
									Name:      testSecretName,
									MountPath: "/usr/local/secrets",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []v1.Volume{
						{
							Name: "artifacts",
							VolumeSource: v1.VolumeSource{
								EmptyDir: &v1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: testSecretName,
							VolumeSource: v1.VolumeSource{
								Secret: &v1.SecretVolumeSource{
									SecretName: testSecretName,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "with memory backed volume gets a volume",
			podStep: func(expectedPodStepTemplate *podStep) {
				expectedPodStepTemplate.config.MemoryBackedVolume = &api.MemoryBackedVolume{Size: "1Gi"}
			},
			expectedVolumeConfig: &v1.Pod{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      "memory-backed",
									MountPath: "/tmp/volume",
								},
							},
						},
					},
					Volumes: []v1.Volume{
						{
							Name: "memory-backed",
							VolumeSource: v1.VolumeSource{
								EmptyDir: &v1.EmptyDirVolumeSource{
									Medium:    v1.StorageMediumMemory,
									SizeLimit: &oneGi,
								},
							},
						},
					},
				},
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

			if !equality.Semantic.DeepEqual(pod.Spec.Volumes, tc.expectedVolumeConfig.Spec.Volumes) {
				t.Errorf("test %s failed. generated pod.Spec.Volumes was not as expected", tc.name)
				t.Error(diff.ObjectReflectDiff(pod.Spec.Volumes, tc.expectedVolumeConfig.Spec.Volumes))
			}
			if !equality.Semantic.DeepEqual(pod.Spec.Containers[0].VolumeMounts, tc.expectedVolumeConfig.Spec.Containers[0].VolumeMounts) {
				t.Errorf("test %s failed. generated pod.Spec.Container[0].VolumeMounts was not as expected", tc.name)
				t.Error(diff.ObjectReflectDiff(pod.Spec.Containers[0].VolumeMounts, tc.expectedVolumeConfig.Spec.Containers[0].VolumeMounts))
			}

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
