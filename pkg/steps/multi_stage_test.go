package steps

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/fake"
	clientgoTesting "k8s.io/client-go/testing"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowdapi "k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestRequires(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config api.ReleaseBuildConfiguration
		steps  []api.LiteralTestStep
		req    []api.StepLink
	}{{
		name:  "step needs release images, should have ReleaseImagesLink",
		steps: []api.LiteralTestStep{{From: "from-release"}},
		req:   []api.StepLink{api.ReleaseImagesLink()},
	}, {
		name: "step needs images, should have ImagesReadyLink",
		config: api.ReleaseBuildConfiguration{
			Images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: "from-images"},
			},
		},
		steps: []api.LiteralTestStep{{From: "from-images"}},
		req:   []api.StepLink{api.ImagesReadyLink()},
	}, {
		name:  "step needs pipeline image, should have InternalImageLink",
		steps: []api.LiteralTestStep{{From: "src"}},
		req: []api.StepLink{
			api.InternalImageLink(
				api.PipelineImageStreamTagReferenceSource),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			step := multiStageTestStep{config: &tc.config, test: tc.steps}
			ret := step.Requires()
			if len(ret) == len(tc.req) {
				matches := true
				for i := range ret {
					if !ret[i].Matches(tc.req[i]) {
						matches = false
						break
					}
				}
				if matches {
					return
				}
			}
			t.Errorf("incorrect requirements: %s", diff.ObjectReflectDiff(ret, tc.req))
		})
	}
}

func TestGeneratePods(t *testing.T) {
	config := api.ReleaseBuildConfiguration{
		Tests: []api.TestStepConfiguration{{
			As: "test",
			MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
				ClusterProfile: api.ClusterProfileAWS,
				Test: []api.LiteralTestStep{{
					As: "step0", From: "image0", Commands: "command0",
				}, {
					As:          "step1",
					From:        "image1",
					Commands:    "command1",
					ArtifactDir: "/artifact/dir",
				}},
			},
		}},
	}
	labels := map[string]string{
		"job":                              "job",
		"build-id":                         "build id",
		"prow.k8s.io/id":                   "prow job id",
		"created-by-ci":                    "true",
		"ci.openshift.io/refs.branch":      "base ref",
		"ci.openshift.io/refs.org":         "org",
		"ci.openshift.io/refs.repo":        "repo",
		"ci.openshift.io/multi-stage-test": "test",
	}
	env := []coreapi.EnvVar{
		{Name: "BUILD_ID", Value: "build id"},
		{Name: "JOB_NAME", Value: "job"},
		{Name: "JOB_SPEC", Value: `{"type":"postsubmit","job":"job","buildid":"build id","prowjobid":"prow job id","refs":{"org":"org","repo":"repo","base_ref":"base ref","base_sha":"base sha"}}`},
		{Name: "JOB_TYPE", Value: "postsubmit"},
		{Name: "PROW_JOB_ID", Value: "prow job id"},
		{Name: "PULL_BASE_REF", Value: "base ref"},
		{Name: "PULL_BASE_SHA", Value: "base sha"},
		{Name: "PULL_REFS", Value: "base ref:base sha"},
		{Name: "REPO_NAME", Value: "repo"},
		{Name: "REPO_OWNER", Value: "org"},
		{Name: "NAMESPACE", Value: "namespace"},
		{Name: "JOB_NAME_SAFE", Value: "test"},
		{Name: "JOB_NAME_HASH", Value: "5e8c9"},
		{Name: "CLUSTER_TYPE", Value: "aws"},
		{Name: "KUBECONFIG", Value: "/var/run/secrets/ci.openshift.io/multi-stage/kubeconfig"},
		{Name: "RELEASE_IMAGE_INITIAL", Value: "release:initial"},
		{Name: "RELEASE_IMAGE_LATEST", Value: "release:latest"},
	}
	jobSpec := api.JobSpec{
		JobSpec: prowdapi.JobSpec{
			Job:       "job",
			BuildID:   "build id",
			ProwJobID: "prow job id",
			Refs: &prowapi.Refs{
				Org:     "org",
				Repo:    "repo",
				BaseRef: "base ref",
				BaseSHA: "base sha",
			},
			Type: "postsubmit",
		},
		Namespace: "namespace",
	}
	step := newMultiStageTestStep(config.Tests[0], &config, nil, nil, nil, "artifact_dir", &jobSpec, nil)
	step.releaseInitial = "release:initial"
	step.releaseLatest = "release:latest"
	ret, err := step.generatePods(config.Tests[0].MultiStageTestConfigurationLiteral.Test)
	if err != nil {
		t.Fatal(err)
	}
	expected := []coreapi.Pod{{
		ObjectMeta: meta.ObjectMeta{
			Name:   "test-step0",
			Labels: labels,
			Annotations: map[string]string{
				"ci.openshift.io/job-spec":                     "",
				"ci-operator.openshift.io/container-sub-tests": "step0",
			},
		},
		Spec: coreapi.PodSpec{
			RestartPolicy: "Never",
			InitContainers: []coreapi.Container{{
				Name:    "cp-secret-wrapper",
				Image:   "registry.svc.ci.openshift.org/ci/secret-wrapper:latest",
				Command: []string{"cp"},
				Args: []string{
					"/bin/secret-wrapper",
					"/tmp/secret-wrapper/secret-wrapper",
				},
				VolumeMounts: []coreapi.VolumeMount{{
					Name:      "secret-wrapper",
					MountPath: "/tmp/secret-wrapper",
				}},
				TerminationMessagePolicy: coreapi.TerminationMessageFallbackToLogsOnError,
			}},
			Containers: []coreapi.Container{{
				Name:                     "step0",
				Image:                    "image0",
				Command:                  []string{"/tmp/secret-wrapper/secret-wrapper"},
				Args:                     []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\ncommand0"},
				Env:                      env,
				Resources:                coreapi.ResourceRequirements{},
				TerminationMessagePolicy: "FallbackToLogsOnError",
				VolumeMounts: []coreapi.VolumeMount{{
					Name:      "secret-wrapper",
					MountPath: "/tmp/secret-wrapper",
				}, {
					Name:      "cluster-profile",
					MountPath: "/var/run/secrets/ci.openshift.io/cluster-profile",
				}, {
					Name:      "test",
					MountPath: "/var/run/secrets/ci.openshift.io/multi-stage",
				}},
			}},
			Volumes: []coreapi.Volume{{
				Name: "secret-wrapper",
				VolumeSource: coreapi.VolumeSource{
					EmptyDir: &coreapi.EmptyDirVolumeSource{},
				},
			}, {
				Name: "cluster-profile",
				VolumeSource: coreapi.VolumeSource{
					Secret: &coreapi.SecretVolumeSource{
						SecretName: "test-cluster-profile",
					},
				},
			}, {
				Name: "test",
				VolumeSource: coreapi.VolumeSource{
					Secret: &coreapi.SecretVolumeSource{
						SecretName: "test",
					},
				},
			}},
		},
	}, {
		ObjectMeta: meta.ObjectMeta{
			Name:   "test-step1",
			Labels: labels,
			Annotations: map[string]string{
				"ci.openshift.io/job-spec":                     "",
				"ci-operator.openshift.io/container-sub-tests": "step1",
			},
		},
		Spec: coreapi.PodSpec{
			RestartPolicy: "Never",
			InitContainers: []coreapi.Container{{
				Name:    "cp-secret-wrapper",
				Image:   "registry.svc.ci.openshift.org/ci/secret-wrapper:latest",
				Command: []string{"cp"},
				Args: []string{
					"/bin/secret-wrapper",
					"/tmp/secret-wrapper/secret-wrapper",
				},
				VolumeMounts: []coreapi.VolumeMount{{
					Name:      "secret-wrapper",
					MountPath: "/tmp/secret-wrapper",
				}},
				TerminationMessagePolicy: coreapi.TerminationMessageFallbackToLogsOnError,
			}},
			Containers: []coreapi.Container{{
				Name:                     "step1",
				Image:                    "image1",
				Command:                  []string{"/tmp/secret-wrapper/secret-wrapper"},
				Args:                     []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\ncommand1"},
				Env:                      env,
				Resources:                coreapi.ResourceRequirements{},
				TerminationMessagePolicy: "FallbackToLogsOnError",
				VolumeMounts: []coreapi.VolumeMount{{
					Name:      "secret-wrapper",
					MountPath: "/tmp/secret-wrapper",
				}, {
					Name:      "cluster-profile",
					MountPath: "/var/run/secrets/ci.openshift.io/cluster-profile",
				}, {
					Name:      "artifacts",
					MountPath: "/artifact/dir",
				}, {
					Name:      "test",
					MountPath: "/var/run/secrets/ci.openshift.io/multi-stage",
				}},
			}, {
				Name:  "artifacts",
				Image: "busybox",
				Command: []string{
					"/bin/sh", "-c", `#!/bin/sh
set -euo pipefail
trap 'kill $(jobs -p); exit 0' TERM

touch /tmp/done
echo "Waiting for artifacts to be extracted"
while true; do
	if [[ ! -f /tmp/done ]]; then
		echo "Artifacts extracted, will terminate after 30s"
		sleep 30
		echo "Exiting"
		exit 0
	fi
	sleep 5 & wait
done
`},
				VolumeMounts: []coreapi.VolumeMount{{
					Name:      "artifacts",
					MountPath: "/tmp/artifacts",
				}},
			}},
			Volumes: []coreapi.Volume{{
				Name: "secret-wrapper",
				VolumeSource: coreapi.VolumeSource{
					EmptyDir: &coreapi.EmptyDirVolumeSource{},
				},
			}, {
				Name: "cluster-profile",
				VolumeSource: coreapi.VolumeSource{
					Secret: &coreapi.SecretVolumeSource{
						SecretName: "test-cluster-profile",
					},
				},
			}, {
				Name: "artifacts",
				VolumeSource: coreapi.VolumeSource{
					EmptyDir: &coreapi.EmptyDirVolumeSource{},
				},
			}, {
				Name: "test",
				VolumeSource: coreapi.VolumeSource{
					Secret: &coreapi.SecretVolumeSource{
						SecretName: "test",
					},
				},
			}},
		},
	}}
	if !reflect.DeepEqual(expected, ret) {
		t.Fatalf("did not generate expected pods: %s", diff.ObjectReflectDiff(expected, ret))
	}
}

func TestRun(t *testing.T) {
	step := multiStageTestStep{
		name:   "test",
		config: &api.ReleaseBuildConfiguration{},
		jobSpec: &api.JobSpec{
			JobSpec: prowdapi.JobSpec{
				Job:       "job",
				BuildID:   "build_id",
				ProwJobID: "prow_job_id",
				Type:      prowapi.PeriodicJob,
			},
		},
		pre:  []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
		test: []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
		post: []api.LiteralTestStep{{As: "post0"}, {As: "post1"}},
	}
	for _, tc := range []struct {
		name     string
		failures []string
		expected []string
	}{{
		name: "no step fails, no error",
		expected: []string{
			"test-pre0", "test-pre1",
			"test-test0", "test-test1",
			"test-post0", "test-post1",
		},
	}, {
		name:     "failure in a pre step, test should not run, post should",
		failures: []string{"test-pre0"},
		expected: []string{
			"test-pre0",
			"test-post0", "test-post1",
		},
	}, {
		name:     "failure in a test step, post should run",
		failures: []string{"test-test0"},
		expected: []string{
			"test-pre0", "test-pre1",
			"test-test0",
			"test-post0", "test-post1",
		},
	}, {
		name:     "failure in a post step, other post steps should still run",
		failures: []string{"test-post0"},
		expected: []string{
			"test-pre0", "test-pre1",
			"test-test0", "test-test1",
			"test-post0", "test-post1",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			fakecs := fake.NewSimpleClientset()
			var pods []*coreapi.Pod
			fakecs.PrependReactor("create", "pods", func(action clientgoTesting.Action) (bool, runtime.Object, error) {
				pod := action.(clientgoTesting.CreateAction).GetObject().(*coreapi.Pod)
				for _, failure := range tc.failures {
					if pod.Name == failure {
						pod.Status.Phase = coreapi.PodFailed
					}
				}
				pods = append(pods, pod)
				return false, nil, nil
			})
			fakecs.PrependReactor("list", "pods", func(action clientgoTesting.Action) (bool, runtime.Object, error) {
				fieldRestrictions := action.(clientgoTesting.ListAction).GetListRestrictions().Fields
				for _, pods := range pods {
					if fieldRestrictions.Matches(fields.Set{"metadata.name": pods.Name}) {
						return true, &coreapi.PodList{Items: []coreapi.Pod{*pods}}, nil
					}
				}
				return false, nil, nil
			})
			step.podClient = NewPodClient(fakecs.CoreV1(), nil, nil)
			step.secretClient = fakecs.CoreV1()
			if err := step.Run(context.Background(), false); tc.failures == nil && err != nil {
				t.Error(err)
				return
			}
			secrets, err := step.secretClient.Secrets(step.jobSpec.Namespace).List(meta.ListOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if l := secrets.Items; len(l) != 1 || l[0].ObjectMeta.Name != step.name {
				t.Errorf("unexpected secrets: %#v", l)
			}
			var names []string
			for _, pods := range pods {
				names = append(names, pods.ObjectMeta.Name)
			}
			if !reflect.DeepEqual(names, tc.expected) {
				t.Errorf("did not execute correct pods: %s", diff.ObjectReflectDiff(names, tc.expected))
			}
		})
	}
}

func TestArtifacts(t *testing.T) {
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	ns := "namespace"
	step := multiStageTestStep{
		name:        "test",
		artifactDir: tmp,
		config:      &api.ReleaseBuildConfiguration{},
		jobSpec: &api.JobSpec{
			Namespace: ns,
			JobSpec: prowdapi.JobSpec{
				Job:       "job",
				BuildID:   "build_id",
				ProwJobID: "prow_job_id",
				Type:      prowapi.PeriodicJob,
			},
		},
		test: []api.LiteralTestStep{{
			As:          "test0",
			ArtifactDir: "/path/to/artifacts",
		}},
	}
	var pods []*coreapi.Pod
	fakecs := fake.NewSimpleClientset()
	fakecs.PrependReactor("create", "pods", func(action clientgoTesting.Action) (bool, runtime.Object, error) {
		pod := action.(clientgoTesting.CreateAction).GetObject().(*coreapi.Pod)
		pod.Status.Phase = coreapi.PodSucceeded
		pods = append(pods, pod)
		return false, nil, nil
	})
	fakecs.PrependReactor("list", "pods", func(action clientgoTesting.Action) (bool, runtime.Object, error) {
		fieldRestrictions := action.(clientgoTesting.ListAction).GetListRestrictions().Fields
		for _, pods := range pods {
			if fieldRestrictions.Matches(fields.Set{"metadata.name": pods.Name}) {
				return true, &coreapi.PodList{Items: []coreapi.Pod{*pods}}, nil
			}
		}
		return false, nil, nil
	})
	client := fakecs.CoreV1()
	podClient := fakePodClient{PodsGetter: client}
	step.podClient = &podClient
	step.secretClient = client
	if err := step.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "test0")); err != nil {
		t.Fatalf("error verifying output directory exists: %v", err)
	}
}
