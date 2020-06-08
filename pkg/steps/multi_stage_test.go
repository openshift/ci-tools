package steps

import (
	"context"
	"github.com/google/go-cmp/cmp"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
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
		steps  api.MultiStageTestConfigurationLiteral
		req    []api.StepLink
	}{{
		name: "step has a cluster profile and requires a release image, should not have StableImagesLink",
		steps: api.MultiStageTestConfigurationLiteral{
			ClusterProfile: api.ClusterProfileAWS,
			Test:           []api.LiteralTestStep{{From: "from-release"}},
		},
		req: []api.StepLink{},
	}, {
		name: "step needs release images, should have StableImagesLink",
		steps: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{{From: "from-release"}},
		},
		req: []api.StepLink{api.StableImagesLink(api.LatestStableName)},
	}, {
		name: "step needs images, should have InternalImageLink",
		config: api.ReleaseBuildConfiguration{
			Images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: "from-images"},
			},
		},
		steps: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{{From: "from-images"}},
		},
		req: []api.StepLink{api.InternalImageLink("from-images")},
	}, {
		name: "step needs pipeline image, should have InternalImageLink",
		steps: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{{From: "src"}},
		},
		req: []api.StepLink{
			api.InternalImageLink(
				api.PipelineImageStreamTagReferenceSource),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			step := MultiStageTestStep(api.TestStepConfiguration{
				MultiStageTestConfigurationLiteral: &tc.steps,
			}, &tc.config, api.NewDeferredParameters(), nil, nil, nil, nil, "", nil, nil)
			ret := step.Requires()
			if len(ret) == len(tc.req) {
				matches := true
				for i := range ret {
					if !ret[i].SatisfiedBy(tc.req[i]) {
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
					As: "step0", From: "src", Commands: "command0",
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
		"created-by-ci":                    "true",
		"ci.openshift.io/refs.branch":      "base ref",
		"ci.openshift.io/refs.org":         "org",
		"ci.openshift.io/refs.repo":        "repo",
		"ci.openshift.io/multi-stage-test": "test",
	}
	coreEnv := []coreapi.EnvVar{
		{Name: "BUILD_ID", Value: "build id"},
		{Name: "CI", Value: "true"},
		{Name: "JOB_NAME", Value: "job"},
		{Name: "JOB_SPEC", Value: `{"type":"postsubmit","job":"job","buildid":"build id","prowjobid":"prow job id","refs":{"org":"org","repo":"repo","base_ref":"base ref","base_sha":"base sha"}}`},
		{Name: "JOB_TYPE", Value: "postsubmit"},
		{Name: "OPENSHIFT_CI", Value: "true"},
		{Name: "PROW_JOB_ID", Value: "prow job id"},
		{Name: "PULL_BASE_REF", Value: "base ref"},
		{Name: "PULL_BASE_SHA", Value: "base sha"},
		{Name: "PULL_REFS", Value: "base ref:base sha"},
		{Name: "REPO_NAME", Value: "repo"},
		{Name: "REPO_OWNER", Value: "org"},
	}
	customEnv := []coreapi.EnvVar{
		{Name: "NAMESPACE", Value: "namespace"},
		{Name: "JOB_NAME_SAFE", Value: "test"},
		{Name: "JOB_NAME_HASH", Value: "5e8c9"},
		{Name: "RELEASE_IMAGE_INITIAL", Value: "release:initial"},
		{Name: "RELEASE_IMAGE_LATEST", Value: "release:latest"},
		{Name: "LEASED_RESOURCE", Value: "uuid"},
		{Name: "CLUSTER_TYPE", Value: "aws"},
		{Name: "CLUSTER_PROFILE_DIR", Value: "/var/run/secrets/ci.openshift.io/cluster-profile"},
		{Name: "KUBECONFIG", Value: "/var/run/secrets/ci.openshift.io/multi-stage/kubeconfig"},
		{Name: "SHARED_DIR", Value: "/var/run/secrets/ci.openshift.io/multi-stage"},
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
	}
	jobSpec.SetNamespace("namespace")
	step := newMultiStageTestStep(config.Tests[0], &config, nil, nil, nil, nil, nil, "artifact_dir", &jobSpec, nil)
	env := []coreapi.EnvVar{
		{Name: "RELEASE_IMAGE_INITIAL", Value: "release:initial"},
		{Name: "RELEASE_IMAGE_LATEST", Value: "release:latest"},
		{Name: "LEASED_RESOURCE", Value: "uuid"},
	}
	ret, err := step.generatePods(config.Tests[0].MultiStageTestConfigurationLiteral.Test, env)
	if err != nil {
		t.Fatal(err)
	}
	expected := []coreapi.Pod{{
		ObjectMeta: meta.ObjectMeta{
			Name:   "test-step0",
			Labels: labels,
			Annotations: map[string]string{
				"ci.openshift.io/job-spec":                     "",
				"ci-operator.openshift.io/container-sub-tests": "test",
				"ci-operator.openshift.io/save-container-logs": "true",
			},
		},
		Spec: coreapi.PodSpec{
			RestartPolicy:      "Never",
			ServiceAccountName: "test",
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
				Name:                     "test",
				Image:                    "pipeline:src",
				Command:                  []string{"/tmp/secret-wrapper/secret-wrapper"},
				Args:                     []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\ncommand0"},
				Env:                      append(coreEnv, customEnv...),
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
				"ci-operator.openshift.io/container-sub-tests": "test",
				"ci-operator.openshift.io/save-container-logs": "true",
			},
		},
		Spec: coreapi.PodSpec{
			RestartPolicy:      "Never",
			ServiceAccountName: "test",
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
				Name:                     "test",
				Image:                    "stable:image1",
				Command:                  []string{"/tmp/secret-wrapper/secret-wrapper"},
				Args:                     []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\ncommand1"},
				Env:                      append(append(coreEnv, coreapi.EnvVar{Name: "ARTIFACT_DIR", Value: "/artifact/dir"}), customEnv...),
				Resources:                coreapi.ResourceRequirements{},
				TerminationMessagePolicy: "FallbackToLogsOnError",
				VolumeMounts: []coreapi.VolumeMount{{
					Name:      "artifacts",
					MountPath: "/artifact/dir",
				}, {
					Name:      "secret-wrapper",
					MountPath: "/tmp/secret-wrapper",
				}, {
					Name:      "cluster-profile",
					MountPath: "/var/run/secrets/ci.openshift.io/cluster-profile",
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
				Name: "artifacts",
				VolumeSource: coreapi.VolumeSource{
					EmptyDir: &coreapi.EmptyDirVolumeSource{},
				},
			}, {
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
	}}
	if len(expected) != len(ret) {
		t.Fatalf("did not generate %d pods, but %d: %s", len(expected), len(ret), diff.ObjectReflectDiff(expected, ret))
	}
	for i := range expected {
		if !equality.Semantic.DeepEqual(expected[i], ret[i]) {
			t.Errorf("did not generate expected pod: %s", diff.ObjectReflectDiff(expected[i], ret[i]))
		}
	}
}

func TestGeneratePodsEnvironment(t *testing.T) {
	value := "test"
	defValue := "default"
	for _, tc := range []struct {
		name     string
		env      api.TestEnvironment
		test     api.LiteralTestStep
		expected *string
	}{{
		name: "test environment is propagated to the step",
		env:  api.TestEnvironment{"TEST": "test"},
		test: api.LiteralTestStep{
			Environment: []api.StepParameter{{Name: "TEST"}},
		},
		expected: &value,
	}, {
		name: "test environment is not propagated to the step",
		env:  api.TestEnvironment{"TEST": "test"},
		test: api.LiteralTestStep{
			Environment: []api.StepParameter{{Name: "NOT_TEST"}},
		},
	}, {
		name: "default value is overwritten",
		env:  api.TestEnvironment{"TEST": "test"},
		test: api.LiteralTestStep{
			Environment: []api.StepParameter{{
				Name:    "TEST",
				Default: "default",
			}},
		},
		expected: &value,
	}, {
		name: "default value is applied",
		test: api.LiteralTestStep{
			Environment: []api.StepParameter{{
				Name:    "TEST",
				Default: "default",
			}},
		},
		expected: &defValue,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			jobSpec := api.JobSpec{
				JobSpec: prowdapi.JobSpec{
					Job:       "job",
					BuildID:   "build_id",
					ProwJobID: "prow_job_id",
					Type:      prowapi.PeriodicJob,
				},
			}
			jobSpec.SetNamespace("ns")
			test := []api.LiteralTestStep{tc.test}
			step := MultiStageTestStep(api.TestStepConfiguration{
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Test:        test,
					Environment: tc.env,
				},
			}, &api.ReleaseBuildConfiguration{}, nil, nil, nil, nil, nil, "", &jobSpec, nil)
			pods, err := step.(*multiStageTestStep).generatePods(test, nil)
			if err != nil {
				t.Fatal(err)
			}
			var env *string
			for i, v := range pods[0].Spec.Containers[0].Env {
				if v.Name == "TEST" {
					env = &pods[0].Spec.Containers[0].Env[i].Value
				}
			}
			if !reflect.DeepEqual(env, tc.expected) {
				t.Errorf("incorrect environment:\n%s", diff.ObjectReflectDiff(env, tc.expected))
			}
		})
	}
}

type fakePodExecutor struct {
	failures sets.String
	pods     []*coreapi.Pod
}

func (e *fakePodExecutor) AddReactors(cs *fake.Clientset) {
	cs.PrependReactor("create", "pods", func(action clientgoTesting.Action) (bool, runtime.Object, error) {
		pod := action.(clientgoTesting.CreateAction).GetObject().(*coreapi.Pod)
		pod.Status.Phase = coreapi.PodPending
		e.pods = append(e.pods, pod)
		return false, nil, nil
	})
	cs.PrependReactor("list", "pods", func(action clientgoTesting.Action) (bool, runtime.Object, error) {
		fieldRestrictions := action.(clientgoTesting.ListAction).GetListRestrictions().Fields
		for _, pod := range e.pods {
			if fieldRestrictions.Matches(fields.Set{"metadata.name": pod.Name}) {
				return true, &coreapi.PodList{Items: []coreapi.Pod{*pod.DeepCopy()}}, nil
			}
		}
		return false, nil, nil
	})
	cs.PrependWatchReactor("pods", func(clientgoTesting.Action) (bool, watch.Interface, error) {
		if e.pods == nil {
			return false, nil, nil
		}
		pod := e.pods[len(e.pods)-1].DeepCopy()
		fail := e.failures.Has(pod.Name)
		if fail {
			pod.Status.Phase = coreapi.PodFailed
		} else {
			pod.Status.Phase = coreapi.PodSucceeded
		}
		for _, container := range pod.Spec.Containers {
			terminated := &coreapi.ContainerStateTerminated{}
			if fail {
				terminated.ExitCode = 1
			}
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, coreapi.ContainerStatus{
				Name:  container.Name,
				State: coreapi.ContainerState{Terminated: terminated}})
		}
		ret := watch.NewFakeWithChanSize(1, true)
		ret.Modify(pod)
		return true, ret, nil
	})
}

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures sets.String
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
		failures: sets.NewString("test-pre0"),
		expected: []string{
			"test-pre0",
			"test-post0", "test-post1",
		},
	}, {
		name:     "failure in a test step, post should run",
		failures: sets.NewString("test-test0"),
		expected: []string{
			"test-pre0", "test-pre1",
			"test-test0",
			"test-post0", "test-post1",
		},
	}, {
		name:     "failure in a post step, other post steps should still run",
		failures: sets.NewString("test-post0"),
		expected: []string{
			"test-pre0", "test-pre1",
			"test-test0", "test-test1",
			"test-post0", "test-post1",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			fakecs := fake.NewSimpleClientset()
			executor := fakePodExecutor{failures: tc.failures}
			executor.AddReactors(fakecs)
			name := "test"
			client := fakecs.CoreV1()
			jobSpec := api.JobSpec{
				JobSpec: prowdapi.JobSpec{
					Job:       "job",
					BuildID:   "build_id",
					ProwJobID: "prow_job_id",
					Type:      prowapi.PeriodicJob,
				},
			}
			jobSpec.SetNamespace("ns")
			step := MultiStageTestStep(api.TestStepConfiguration{
				As: name,
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:  []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test: []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post: []api.LiteralTestStep{{As: "post0"}, {As: "post1"}},
				},
			}, &api.ReleaseBuildConfiguration{}, nil, &fakePodClient{NewPodClient(client, nil, nil)}, client, client, fakecs.RbacV1(), "", &jobSpec, nil)
			if err := step.Run(context.Background(), false); tc.failures == nil && err != nil {
				t.Error(err)
				return
			}
			secrets, err := client.Secrets(jobSpec.Namespace()).List(meta.ListOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if l := secrets.Items; len(l) != 1 || l[0].ObjectMeta.Name != name {
				t.Errorf("unexpected secrets: %#v", l)
			}
			var names []string
			for _, pods := range executor.pods {
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
	fakecs := fake.NewSimpleClientset()
	executor := fakePodExecutor{}
	executor.AddReactors(fakecs)
	client := fakecs.CoreV1()
	jobSpec := api.JobSpec{
		JobSpec: prowdapi.JobSpec{
			Job:       "job",
			BuildID:   "build_id",
			ProwJobID: "prow_job_id",
			Type:      prowapi.PeriodicJob,
		},
	}
	jobSpec.SetNamespace(ns)
	testName := "test"
	step := MultiStageTestStep(api.TestStepConfiguration{
		As: testName,
		MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{
				{As: "test0", ArtifactDir: "/path/to/artifacts"},
				{As: "test1", ArtifactDir: "/path/to/artifacts"},
			},
		},
	}, &api.ReleaseBuildConfiguration{}, nil, &fakePodClient{NewPodClient(client, nil, nil)}, client, client, fakecs.RbacV1(), tmp, &jobSpec, nil)
	if err := step.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for _, x := range []string{"test0", "test1"} {
		if _, err := os.Stat(filepath.Join(tmp, testName, x)); err != nil {
			t.Fatalf("error verifying output directory %q exists: %v", x, err)
		}
	}
}

func TestJUnit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures sets.String
		expected []string
	}{{
		name: "no step fails",
		expected: []string{
			"Run multi-stage test test - test-pre0 container test",
			"Run multi-stage test test - test-pre1 container test",
			"Run multi-stage test test - test-test0 container test",
			"Run multi-stage test test - test-test1 container test",
			"Run multi-stage test test - test-post0 container test",
			"Run multi-stage test test - test-post1 container test",
		},
	}, {
		name:     "failure in a pre step",
		failures: sets.NewString("test-pre0"),
		expected: []string{
			"Run multi-stage test test - test-pre0 container test",
			"Run multi-stage test test - test-post0 container test",
			"Run multi-stage test test - test-post1 container test",
		},
	}, {
		name:     "failure in a test step",
		failures: sets.NewString("test-test0"),
		expected: []string{
			"Run multi-stage test test - test-pre0 container test",
			"Run multi-stage test test - test-pre1 container test",
			"Run multi-stage test test - test-test0 container test",
			"Run multi-stage test test - test-post0 container test",
			"Run multi-stage test test - test-post1 container test",
		},
	}, {
		name:     "failure in a post step",
		failures: sets.NewString("test-post1"),
		expected: []string{
			"Run multi-stage test test - test-pre0 container test",
			"Run multi-stage test test - test-pre1 container test",
			"Run multi-stage test test - test-test0 container test",
			"Run multi-stage test test - test-test1 container test",
			"Run multi-stage test test - test-post0 container test",
			"Run multi-stage test test - test-post1 container test",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			fakecs := fake.NewSimpleClientset()
			executor := fakePodExecutor{failures: tc.failures}
			executor.AddReactors(fakecs)
			client := fakecs.CoreV1()
			jobSpec := api.JobSpec{
				JobSpec: prowdapi.JobSpec{
					Job:       "job",
					BuildID:   "build_id",
					ProwJobID: "prow_job_id",
					Type:      prowapi.PeriodicJob,
				},
			}
			step := MultiStageTestStep(api.TestStepConfiguration{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:  []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test: []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post: []api.LiteralTestStep{{As: "post0"}, {As: "post1"}},
				},
			}, &api.ReleaseBuildConfiguration{}, nil, &fakePodClient{NewPodClient(client, nil, nil)}, client, client, fakecs.RbacV1(), "/dev/null", &jobSpec, nil)
			if err := step.Run(context.Background(), false); tc.failures == nil && err != nil {
				t.Error(err)
				return
			}
			var names []string
			for _, t := range step.(subtestReporter).SubTests() {
				names = append(names, t.Name)
			}
			if !reflect.DeepEqual(names, tc.expected) {
				t.Error(diff.ObjectReflectDiff(names, tc.expected))
			}
		})
	}
}

func TestAddCredentials(t *testing.T) {
	var testCases = []struct {
		name        string
		credentials []api.CredentialReference
		pod         coreapi.Pod
		expected    coreapi.Pod
	}{
		{
			name:        "none to add",
			credentials: []api.CredentialReference{},
			pod:         coreapi.Pod{},
			expected:    coreapi.Pod{},
		},
		{
			name:        "one to add",
			credentials: []api.CredentialReference{{Namespace: "ns", Name: "name", MountPath: "/tmp"}},
			pod: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{}}},
				Volumes:    []coreapi.Volume{},
			}},
			expected: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{{Name: "ns-name", MountPath: "/tmp"}}}},
				Volumes:    []coreapi.Volume{{Name: "ns-name", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "ns-name"}}}},
			}},
		},
		{
			name: "many to add and disambiguate",
			credentials: []api.CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/tmp"},
				{Namespace: "other", Name: "name", MountPath: "/tamp"},
			},
			pod: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{}}},
				Volumes:    []coreapi.Volume{},
			}},
			expected: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{
					{Name: "ns-name", MountPath: "/tmp"},
					{Name: "other-name", MountPath: "/tamp"},
				}}},
				Volumes: []coreapi.Volume{
					{Name: "ns-name", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "ns-name"}}},
					{Name: "other-name", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "other-name"}}},
				},
			}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			addCredentials(testCase.credentials, &testCase.pod)
			if !equality.Semantic.DeepEqual(testCase.pod, testCase.expected) {
				t.Errorf("%s: got incorrect Pod: %s", testCase.name, cmp.Diff(testCase.pod, testCase.expected))
			}
		})
	}
}
