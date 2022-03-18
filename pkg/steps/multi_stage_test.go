package steps

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowdapi "k8s.io/test-infra/prow/pod-utils/downwardapi"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

// the multiStageTestStep implements the subStepReporter interface
var _ SubStepReporter = &multiStageTestStep{}

func TestRequires(t *testing.T) {
	for _, tc := range []struct {
		name         string
		config       api.ReleaseBuildConfiguration
		steps        api.MultiStageTestConfigurationLiteral
		clusterClaim *api.ClusterClaim
		req          []api.StepLink
	}{{
		name: "step has a cluster profile and requires a release image, should not have ReleaseImagesLink",
		steps: api.MultiStageTestConfigurationLiteral{
			ClusterProfile: api.ClusterProfileAWS,
			Test:           []api.LiteralTestStep{{From: "from-release"}},
		},
		req: []api.StepLink{
			api.ReleasePayloadImageLink(api.LatestReleaseName),
			api.ImagesReadyLink(),
		},
	}, {
		name: "step needs release images, should have ReleaseImagesLink",
		steps: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{{From: "from-release"}},
		},
		req: []api.StepLink{
			api.ReleaseImagesLink(api.LatestReleaseName),
		},
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
	}, {
		name: "step needs pipeline image explicitly, should have InternalImageLink",
		steps: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{{From: "pipeline:src"}},
		},
		req: []api.StepLink{
			api.InternalImageLink(
				api.PipelineImageStreamTagReferenceSource),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			step := MultiStageTestStep(api.TestStepConfiguration{
				As:                                 "some-e2e",
				ClusterClaim:                       tc.clusterClaim,
				MultiStageTestConfigurationLiteral: &tc.steps,
			}, &tc.config, api.NewDeferredParameters(nil), nil, nil, nil)
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
			t.Errorf("incorrect requirements: %s", cmp.Diff(ret, tc.req, api.Comparer()))
		})
	}
}

func TestGeneratePods(t *testing.T) {
	yes := true
	config := api.ReleaseBuildConfiguration{
		Tests: []api.TestStepConfiguration{{
			As: "test",
			MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
				ClusterProfile: api.ClusterProfileAWS,
				Test: []api.LiteralTestStep{{
					As: "step0", From: "src", Commands: "command0",
				}, {
					As:       "step1",
					From:     "image1",
					Commands: "command1",
				}, {
					As: "step2", From: "stable-initial:installer", Commands: "command2", RunAsScript: &yes,
				}, {
					As: "step3", From: "src", Commands: "command3", DNSConfig: &api.StepDNSConfig{
						Nameservers: []string{"nameserver1", "nameserver2"},
						Searches:    []string{"my.dns.search1", "my.dns.search2"},
					},
				}},
			}},
		},
	}

	jobSpec := api.JobSpec{
		Metadata: api.Metadata{
			Org:     "org",
			Repo:    "repo",
			Branch:  "base ref",
			Variant: "variant",
		},
		Target: "target",
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
	jobSpec.SetNamespace("namespace")
	step := newMultiStageTestStep(config.Tests[0], &config, nil, nil, &jobSpec, nil)
	step.test[0].Resources = api.ResourceRequirements{
		Requests: api.ResourceList{api.ShmResource: "2G"},
		Limits:   api.ResourceList{api.ShmResource: "2G"}}
	env := []coreapi.EnvVar{
		{Name: "RELEASE_IMAGE_INITIAL", Value: "release:initial"},
		{Name: "RELEASE_IMAGE_LATEST", Value: "release:latest"},
		{Name: "LEASED_RESOURCE", Value: "uuid"},
	}
	secretVolumes := []coreapi.Volume{{
		Name:         "secret",
		VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "k8-secret"}},
	}}
	secretVolumeMounts := []coreapi.VolumeMount{{
		Name:      "secret",
		MountPath: "/secret",
	}}
	ret, _, err := step.generatePods(config.Tests[0].MultiStageTestConfigurationLiteral.Test, env, false, secretVolumes, secretVolumeMounts)
	if err != nil {
		t.Fatal(err)
	}
	testhelper.CompareWithFixture(t, ret)
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
				Default: &defValue,
			}},
		},
		expected: &value,
	}, {
		name: "default value is applied",
		test: api.LiteralTestStep{
			Environment: []api.StepParameter{{
				Name:    "TEST",
				Default: &defValue,
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
			jobSpec.SetNamespace("ns")
			test := []api.LiteralTestStep{tc.test}
			step := MultiStageTestStep(api.TestStepConfiguration{
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Test:        test,
					Environment: tc.env,
				},
			}, &api.ReleaseBuildConfiguration{}, nil, nil, &jobSpec, nil)
			pods, _, err := step.(*multiStageTestStep).generatePods(test, nil, false, nil, nil)
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

func TestGeneratePodBestEffort(t *testing.T) {
	yes := true
	no := false
	config := api.ReleaseBuildConfiguration{
		Tests: []api.TestStepConfiguration{{
			As: "test",
			MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
				AllowBestEffortPostSteps: &yes,
				Test: []api.LiteralTestStep{{
					As:       "step0",
					From:     "src",
					Commands: "command0",
				}},
				Post: []api.LiteralTestStep{{
					As:         "step1",
					From:       "src",
					Commands:   "command1",
					BestEffort: &yes,
				}, {
					As:         "step2",
					From:       "src",
					Commands:   "command2",
					BestEffort: &no,
				}},
			},
		}},
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
	jobSpec.SetNamespace("namespace")
	step := newMultiStageTestStep(config.Tests[0], &config, nil, nil, &jobSpec, nil)
	_, isBestEffort, err := step.generatePods(config.Tests[0].MultiStageTestConfigurationLiteral.Post, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for pod, bestEffort := range map[string]bool{
		"test-step0": false,
		"test-step1": true,
		"test-step2": false,
	} {
		if actual, expected := isBestEffort(pod), bestEffort; actual != expected {
			t.Errorf("didn't check best-effort status of Pod %s correctly, expected %v", pod, bestEffort)
		}
	}
}

type fakePodExecutor struct {
	loggingclient.LoggingClient
	failures    sets.String
	createdPods []*coreapi.Pod
}

func (f *fakePodExecutor) Create(ctx context.Context, o ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if pod, ok := o.(*coreapi.Pod); ok {
		if pod.Namespace == "" {
			return errors.New("pod had no namespace set")
		}
		f.createdPods = append(f.createdPods, pod.DeepCopy())
		pod.Status.Phase = coreapi.PodPending
	}
	return f.LoggingClient.Create(ctx, o, opts...)
}

func (f *fakePodExecutor) Get(ctx context.Context, n ctrlruntimeclient.ObjectKey, o ctrlruntimeclient.Object) error {
	if err := f.LoggingClient.Get(ctx, n, o); err != nil {
		return err
	}
	if pod, ok := o.(*coreapi.Pod); ok {
		fail := f.failures.Has(n.Name)
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
	}

	return nil
}

func TestRun(t *testing.T) {
	yes := true
	for _, tc := range []struct {
		name     string
		failures sets.String
		expected []string
	}{{
		name: "no step fails, no error",
		expected: []string{
			"test-pre0", "test-pre1",
			"test-test0", "test-test1",
			"test-post0",
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
			"test-post0",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			sa := &coreapi.ServiceAccount{
				ObjectMeta:       metav1.ObjectMeta{Name: "test", Namespace: "ns", Labels: map[string]string{"ci.openshift.io/multi-stage-test": "test"}},
				ImagePullSecrets: []v1.LocalObjectReference{{Name: "ci-operator-dockercfg-12345"}},
			}
			name := "test"
			crclient := &fakePodExecutor{LoggingClient: loggingclient.New(fakectrlruntimeclient.NewFakeClient(sa.DeepCopyObject())), failures: tc.failures}
			jobSpec := api.JobSpec{
				JobSpec: prowdapi.JobSpec{
					Job:       "job",
					BuildID:   "build_id",
					ProwJobID: "prow_job_id",
					Type:      prowapi.PeriodicJob,
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
			jobSpec.SetNamespace("ns")
			step := MultiStageTestStep(api.TestStepConfiguration{
				As: name,
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:                []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test:               []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post:               []api.LiteralTestStep{{As: "post0"}, {As: "post1", OptionalOnSuccess: &yes}},
					AllowSkipOnSuccess: &yes,
				},
			}, &api.ReleaseBuildConfiguration{}, nil, &fakePodClient{fakePodExecutor: crclient}, &jobSpec, nil)
			if err := step.Run(context.Background()); (err != nil) != (tc.failures != nil) {
				t.Errorf("expected error: %t, got error: %v", (tc.failures != nil), err)
			}
			secrets := &coreapi.SecretList{}
			if err := crclient.List(context.TODO(), secrets, ctrlruntimeclient.InNamespace(jobSpec.Namespace())); err != nil {
				t.Fatal(err)
			}
			if l := secrets.Items; len(l) != 1 || l[0].ObjectMeta.Name != name {
				t.Errorf("unexpected secrets: %#v", l)
			}
			var names []string
			for _, pod := range crclient.createdPods {
				if pod.Namespace != jobSpec.Namespace() {
					t.Errorf("pod %s didn't have namespace %s set, had %q instead", pod.Name, jobSpec.Namespace(), pod.Namespace)
				}
				names = append(names, pod.Name)
			}
			if diff := cmp.Diff(names, tc.expected); diff != "" {
				t.Errorf("did not execute correct pods: %s, actual: %v, expected: %v", diff, names, tc.expected)
			}
		})
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
			"Run multi-stage test pre phase",
			"Run multi-stage test test - test-test0 container test",
			"Run multi-stage test test - test-test1 container test",
			"Run multi-stage test test phase",
			"Run multi-stage test test - test-post0 container test",
			"Run multi-stage test test - test-post1 container test",
			"Run multi-stage test post phase",
		},
	}, {
		name:     "failure in a pre step",
		failures: sets.NewString("test-pre0"),
		expected: []string{
			"Run multi-stage test test - test-pre0 container test",
			"Run multi-stage test pre phase",
			"Run multi-stage test test - test-post0 container test",
			"Run multi-stage test test - test-post1 container test",
			"Run multi-stage test post phase",
		},
	}, {
		name:     "failure in a test step",
		failures: sets.NewString("test-test0"),
		expected: []string{
			"Run multi-stage test test - test-pre0 container test",
			"Run multi-stage test test - test-pre1 container test",
			"Run multi-stage test pre phase",
			"Run multi-stage test test - test-test0 container test",
			"Run multi-stage test test phase",
			"Run multi-stage test test - test-post0 container test",
			"Run multi-stage test test - test-post1 container test",
			"Run multi-stage test post phase",
		},
	}, {
		name:     "failure in a post step",
		failures: sets.NewString("test-post1"),
		expected: []string{
			"Run multi-stage test test - test-pre0 container test",
			"Run multi-stage test test - test-pre1 container test",
			"Run multi-stage test pre phase",
			"Run multi-stage test test - test-test0 container test",
			"Run multi-stage test test - test-test1 container test",
			"Run multi-stage test test phase",
			"Run multi-stage test test - test-post0 container test",
			"Run multi-stage test test - test-post1 container test",
			"Run multi-stage test post phase",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			sa := &coreapi.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-namespace", Labels: map[string]string{"ci.openshift.io/multi-stage-test": "test"}}}

			client := &fakePodExecutor{LoggingClient: loggingclient.New(fakectrlruntimeclient.NewFakeClient(sa.DeepCopyObject())), failures: tc.failures}
			jobSpec := api.JobSpec{
				JobSpec: prowdapi.JobSpec{
					Job:       "job",
					BuildID:   "build_id",
					ProwJobID: "prow_job_id",
					Type:      prowapi.PeriodicJob,
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
			jobSpec.SetNamespace("test-namespace")
			step := MultiStageTestStep(api.TestStepConfiguration{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:  []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test: []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post: []api.LiteralTestStep{{As: "post0"}, {As: "post1"}},
				},
			}, &api.ReleaseBuildConfiguration{}, nil, &fakePodClient{fakePodExecutor: client}, &jobSpec, nil)
			if err := step.Run(context.Background()); tc.failures == nil && err != nil {
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
		{
			name: "dots in volume name are replaced",
			credentials: []api.CredentialReference{
				{Namespace: "ns", Name: "hive-hive-credentials", MountPath: "/tmp"},
			},
			pod: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{}}},
				Volumes:    []coreapi.Volume{},
			}},
			expected: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{
					{Name: "ns-hive-hive-credentials", MountPath: "/tmp"},
				}}},
				Volumes: []coreapi.Volume{
					{Name: "ns-hive-hive-credentials", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "ns-hive-hive-credentials"}}},
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

func TestSecretsForCensoring(t *testing.T) {
	// this ends up returning based on alphanumeric sort of names, so name things accordingly
	client := loggingclient.New(
		fakectrlruntimeclient.NewFakeClient(
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "target-namespace",
					Name:      "1first",
				},
			},
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "target-namespace",
					Name:      "2second",
				},
			},
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "target-namespace",
					Name:      "3third",
				},
			},
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "target-namespace",
					Name:      "4skipped",
					Labels:    map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			},
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "target-namespace",
					Name:        "5sa-secret",
					Annotations: map[string]string{"kubernetes.io/service-account.name": "foo"},
				},
			},
		),
	)

	volumes, mounts, err := secretsForCensoring(client, "target-namespace", context.Background())
	if err != nil {
		t.Fatalf("got error when listing secrets: %v", err)
	}
	expectedVolumes := []coreapi.Volume{
		{
			Name: "censor-0",
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{
					SecretName: "1first",
				},
			},
		},
		{
			Name: "censor-1",
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{
					SecretName: "2second",
				},
			},
		},
		{
			Name: "censor-2",
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{
					SecretName: "3third",
				},
			},
		},
	}
	if diff := cmp.Diff(volumes, expectedVolumes); diff != "" {
		t.Errorf("got incorrect volumes: %v", diff)
	}

	expectedMounts := []coreapi.VolumeMount{
		{
			Name:      "censor-0",
			MountPath: path.Join("/secrets", "1first"),
		},
		{
			Name:      "censor-1",
			MountPath: path.Join("/secrets", "2second"),
		},
		{
			Name:      "censor-2",
			MountPath: path.Join("/secrets", "3third"),
		},
	}
	if diff := cmp.Diff(mounts, expectedMounts); diff != "" {
		t.Errorf("got incorrect mounts: %v", diff)
	}
}

func TestGetClusterClaimPodParams(t *testing.T) {
	var testCases = []struct {
		name               string
		secretVolumeMounts []coreapi.VolumeMount
		expectedEnv        []coreapi.EnvVar
		expectedMount      []coreapi.VolumeMount
		expectedError      error
	}{
		{
			name: "basic case",
			secretVolumeMounts: []coreapi.VolumeMount{
				{
					Name:      "censor-as-hive-admin-kubeconfig",
					MountPath: "/secrets/as-hive-admin-kubeconfig",
				},
				{
					Name:      "censor-as-hive-admin-password",
					MountPath: "/secrets/as-hive-admin-password",
				},
			},
			expectedEnv: []coreapi.EnvVar{
				{Name: "KUBECONFIG", Value: "/secrets/as-hive-admin-kubeconfig/kubeconfig"},
				{Name: "KUBEADMIN_PASSWORD_FILE", Value: "/secrets/as-hive-admin-password/password"},
			},
			expectedMount: []coreapi.VolumeMount{
				{Name: "censor-as-hive-admin-kubeconfig", MountPath: "/secrets/as-hive-admin-kubeconfig"},
				{Name: "censor-as-hive-admin-password", MountPath: "/secrets/as-hive-admin-password"},
			},
		},
		{
			name: "missing a secretVolumeMount",
			secretVolumeMounts: []coreapi.VolumeMount{
				{
					Name:      "censor-as-hive-admin-kubeconfig",
					MountPath: "/secrets/as-hive-admin-kubeconfig",
				},
			},
			expectedError: utilerrors.NewAggregate([]error{fmt.Errorf("failed to find foundMountPath /secrets/as-hive-admin-password to create secret as-hive-admin-password")}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualEnv, actualMount, actualError := getClusterClaimPodParams(tc.secretVolumeMounts, "as")
			if diff := cmp.Diff(tc.expectedEnv, actualEnv); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedMount, actualMount); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

type fakeStepParams map[string]string

func (f fakeStepParams) Has(key string) bool {
	_, ok := f[key]
	return ok
}

func (f fakeStepParams) HasInput(_ string) bool {
	panic("This should not be used")
}

func (f fakeStepParams) Get(key string) (string, error) {
	return f[key], nil
}

func TestEnvironment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		params    api.Parameters
		leases    []api.StepLease
		expected  []coreapi.EnvVar
		expectErr bool
	}{
		{
			name:     "leases are exposed in environment",
			params:   fakeStepParams{"LEASE_ONE": "ONE", "LEASE_TWO": "TWO"},
			leases:   []api.StepLease{{Env: "LEASE_ONE"}, {Env: "LEASE_TWO"}},
			expected: []coreapi.EnvVar{{Name: "LEASE_ONE", Value: "ONE"}, {Name: "LEASE_TWO", Value: "TWO"}},
		},
		{
			name: "arbitrary variables are not exposed in environment",
			params: fakeStepParams{
				"OO_IMSMART":     "nope",
				"IM_A_POWERUSER": "nope you are not",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &multiStageTestStep{
				params: tc.params,
				leases: tc.leases,
			}
			got, err := s.environment(context.TODO())
			if (err != nil) != tc.expectErr {
				t.Errorf("environment() error = %v, wantErr %v", err, tc.expectErr)
				return
			}
			sort.Slice(tc.expected, func(i, j int) bool {
				return tc.expected[i].Name < tc.expected[j].Name
			})
			sort.Slice(got, func(i, j int) bool {
				return got[i].Name < got[j].Name
			})
			if diff := cmp.Diff(tc.expected, got); diff != "" {
				t.Errorf("%s: result differs from expected:\n %s", tc.name, diff)
			}
		})
	}
}
