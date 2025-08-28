package multi_stage

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowdapi "sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	testhelper_kube "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
)

func TestRun(t *testing.T) {
	yes := true
	for _, tc := range []struct {
		name      string
		failures  sets.Set[string]
		observers []api.Observer
		expected  []string
	}{
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
		{
			name:      "observer fails, no error",
			observers: []api.Observer{{Name: "obsrv0"}},
			failures:  sets.New[string]("test-obsrv0"),
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
			observerPodNames := sets.New[string]()
			for _, observerPod := range tc.observers {
				observerPodNames.Insert(fmt.Sprintf("%s-%s", name, observerPod.Name))
			}

			crclient := &testhelper_kube.FakePodExecutor{
				LoggingClient: loggingclient.New(
					fakectrlruntimeclient.NewClientBuilder().
						WithIndex(&v1.Pod{}, "metadata.name", fakePodNameIndexer).
						WithObjects(sa).
						Build(), nil),
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
			}, &api.ReleaseBuildConfiguration{}, nil, client, &jobSpec, nil, "node-name", "", func(cf context.CancelFunc) {}, false)

			// An Observer pod failure doesn't make the test fail
			failures := tc.failures.Delete(observerPodNames.UnsortedList()...)
			hasFailures := failures != nil && failures.Len() > 0

			if err := step.Run(context.Background()); (err != nil) != hasFailures {
				t.Errorf("expected error: %t, got error: %v", hasFailures, err)
			}
			secrets := &v1.SecretList{}
			if err := crclient.List(context.TODO(), secrets, ctrlruntimeclient.InNamespace(jobSpec.Namespace())); err != nil {
				t.Fatal(err)
			}
			if l := secrets.Items; len(l) != 1 || l[0].ObjectMeta.Name != name {
				t.Errorf("unexpected secrets: %#v", l)
			}
			var names []string

			// An observer pod can be executed at any time, therefore making unstable the output
			// of the pods the client has created. Do not take into account them.
			observerPodsToRemove := observerPodNames.Clone()

			for _, pod := range crclient.CreatedPods {
				if pod.Namespace != jobSpec.Namespace() {
					t.Errorf("pod %s didn't have namespace %s set, had %q instead", pod.Name, jobSpec.Namespace(), pod.Namespace)
				}
				if !observerPodsToRemove.Has(pod.Name) {
					names = append(names, pod.Name)
				} else {
					observerPodsToRemove.Delete(pod.Name)
				}
			}

			if observerPodsToRemove.Len() > 0 {
				t.Errorf("did not find the following pods to remove: %s", observerPodsToRemove.UnsortedList())
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
				LoggingClient: loggingclient.New(
					fakectrlruntimeclient.NewClientBuilder().
						WithIndex(&v1.Pod{}, "metadata.name", fakePodNameIndexer).
						WithObjects(sa).
						Build(), nil),
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
			}, &api.ReleaseBuildConfiguration{}, nil, client, &jobSpec, nil, "node-name", "", nil, false)
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
