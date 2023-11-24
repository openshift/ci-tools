package multi_stage

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowdapi "k8s.io/test-infra/prow/pod-utils/downwardapi"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	testhelper_kube "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
)

func TestRun(t *testing.T) {
	yes := true
	for _, tc := range []struct {
		name      string
		observers []api.Observer
		failures  sets.Set[string]
		// Remove these names from the expected ones. So far the sole use case is for the observers
		// as they run asynchrounously so the ordering is not stable
		removeNames sets.Set[string]
		expected    []string
		podPayload  map[string]testhelper_kube.PodPayload
	}{
		{
			name:        "observer fails, no error",
			observers:   []api.Observer{{Name: "obsrv0"}},
			removeNames: sets.New[string]("test-obsrv0"),
			expected: []string{
				"test-pre0", "test-pre1",
				"test-test0", "test-test1",
				"test-post0",
			},
			podPayload: map[string]testhelper_kube.PodPayload{
				"test-pre0": func(pod *v1.Pod, env *testhelper_kube.PodRunnerEnv, dispatch func(events ...watch.Event)) {
					// Asynchronously wait for the observer which, by design, is not guaranteed to even start
					// executing
					go func() {
						// Wait for the observer to complete the execution, then go ahead and succeed
						<-env.ObserverDone

						pod.Status.Phase = v1.PodSucceeded
						terminated := v1.ContainerState{
							Terminated: &v1.ContainerStateTerminated{},
						}

						for _, container := range pod.Spec.Containers {
							pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, v1.ContainerStatus{
								Name:  container.Name,
								State: terminated,
							})
						}

						dispatch(watch.Event{Type: watch.Modified, Object: pod})
					}()
				},
				"test-obsrv0": func(pod *v1.Pod, env *testhelper_kube.PodRunnerEnv, dispatch func(events ...watch.Event)) {
					pod.Status.Phase = v1.PodFailed
					terminated := v1.ContainerState{
						Terminated: &v1.ContainerStateTerminated{ExitCode: 1},
					}

					for _, container := range pod.Spec.Containers {
						pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, v1.ContainerStatus{
							Name:  container.Name,
							State: terminated,
						})
					}

					dispatch(watch.Event{Type: watch.Modified, Object: pod})
					env.ObserverDone <- struct{}{}
				},
			},
		},
		{
			name: "no step fails, no error",
			expected: []string{
				"test-pre0", "test-pre1",
				"test-test0", "test-test1",
				"test-post0",
			},
		},
		{
			name:     "failure in a pre step, test should not run, post should",
			failures: sets.New[string]("test-pre0"),
			expected: []string{
				"test-pre0",
				"test-post0", "test-post1",
			},
		}, {
			name:     "failure in a test step, post should run",
			failures: sets.New[string]("test-test0"),
			expected: []string{
				"test-pre0", "test-pre1",
				"test-test0",
				"test-post0", "test-post1",
			},
		},
		{
			name:     "failure in a post step, other post steps should still run",
			failures: sets.New[string]("test-post0"),
			expected: []string{
				"test-pre0", "test-pre1",
				"test-test0", "test-test1",
				"test-post0",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sa := &v1.ServiceAccount{
				ObjectMeta:       metav1.ObjectMeta{Name: "test", Namespace: "ns", Labels: map[string]string{"ci.openshift.io/multi-stage-test": "test"}},
				ImagePullSecrets: []v1.LocalObjectReference{{Name: "ci-operator-dockercfg-12345"}},
			}
			name := "test"

			podRunnerEnv := testhelper_kube.NewPodRunnerEnv()
			podPayloadRunners := make(map[string]*testhelper_kube.PodPayloadRunner)
			for pod, payload := range tc.podPayload {
				podPayloadRunners[pod] = testhelper_kube.NewPodPayloadRunner(payload, *podRunnerEnv)
			}

			crclient := &testhelper_kube.FakePodExecutor{
				Lock: sync.RWMutex{},
				LoggingClient: loggingclient.New(
					fakectrlruntimeclient.NewClientBuilder().
						WithIndex(&v1.Pod{}, "metadata.name", fakePodNameIndexer).
						WithObjects(sa).
						Build()),
				Failures:          tc.failures,
				PodPayloadRunners: podPayloadRunners,
			}
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
			client := &testhelper_kube.FakePodClient{
				PendingTimeout:  30 * time.Minute,
				FakePodExecutor: crclient,
			}
			step := MultiStageTestStep(api.TestStepConfiguration{
				As: name,
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:                []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test:               []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post:               []api.LiteralTestStep{{As: "post0"}, {As: "post1", OptionalOnSuccess: &yes}},
					Observers:          tc.observers,
					AllowSkipOnSuccess: &yes,
				},
			}, &api.ReleaseBuildConfiguration{}, nil, client, &jobSpec, nil, "node-name", "")
			if err := step.Run(context.Background()); (err != nil) != (tc.failures != nil) {
				t.Errorf("expected error: %t, got error: %v", (tc.failures != nil), err)
			}
			secrets := &v1.SecretList{}
			if err := crclient.List(context.TODO(), secrets, ctrlruntimeclient.InNamespace(jobSpec.Namespace())); err != nil {
				t.Fatal(err)
			}
			if l := secrets.Items; len(l) != 1 || l[0].ObjectMeta.Name != name {
				t.Errorf("unexpected secrets: %#v", l)
			}
			var names []string
			removeNames := tc.removeNames.Clone()
			for _, pod := range crclient.CreatedPods {
				if pod.Namespace != jobSpec.Namespace() {
					t.Errorf("pod %s didn't have namespace %s set, had %q instead", pod.Name, jobSpec.Namespace(), pod.Namespace)
				}
				if !removeNames.Has(pod.Name) {
					names = append(names, pod.Name)
				} else {
					removeNames.Delete(pod.Name)
				}
			}

			if removeNames.Len() > 0 {
				t.Errorf("did not find the following pods to remove: %s", removeNames.UnsortedList())
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
		failures sets.Set[string]
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
		failures: sets.New[string]("test-pre0"),
		expected: []string{
			"Run multi-stage test test - test-pre0 container test",
			"Run multi-stage test pre phase",
			"Run multi-stage test test - test-post0 container test",
			"Run multi-stage test test - test-post1 container test",
			"Run multi-stage test post phase",
		},
	}, {
		name:     "failure in a test step",
		failures: sets.New[string]("test-test0"),
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
		failures: sets.New[string]("test-post1"),
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
			sa := &v1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-namespace", Labels: map[string]string{"ci.openshift.io/multi-stage-test": "test"}}}

			crclient := &testhelper_kube.FakePodExecutor{
				Lock: sync.RWMutex{},
				LoggingClient: loggingclient.New(
					fakectrlruntimeclient.NewClientBuilder().
						WithIndex(&v1.Pod{}, "metadata.name", fakePodNameIndexer).
						WithObjects(sa).
						Build()),
				Failures: tc.failures,
			}
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
			client := &testhelper_kube.FakePodClient{FakePodExecutor: crclient}
			step := MultiStageTestStep(api.TestStepConfiguration{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:  []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test: []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post: []api.LiteralTestStep{{As: "post0"}, {As: "post1"}},
				},
			}, &api.ReleaseBuildConfiguration{}, nil, client, &jobSpec, nil, "node-name", "")
			if err := step.Run(context.Background()); tc.failures == nil && err != nil {
				t.Error(err)
				return
			}
			var names []string
			for _, t := range step.(steps.SubtestReporter).SubTests() {
				names = append(names, t.Name)
			}
			if !reflect.DeepEqual(names, tc.expected) {
				t.Error(diff.ObjectReflectDiff(names, tc.expected))
			}
		})
	}
}

func fakePodNameIndexer(object ctrlruntimeclient.Object) []string {
	p, ok := object.(*v1.Pod)
	if !ok {
		panic(fmt.Errorf("indexer function for type %T's metadata.name field received object of type %T", v1.Pod{}, object))
	}
	return []string{p.Name}
}
