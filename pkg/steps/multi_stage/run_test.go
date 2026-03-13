package multi_stage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowdapi "sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	testhelper_kube "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
)

func TestRun(t *testing.T) {
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

	sa := &v1.ServiceAccount{
		ObjectMeta:       metav1.ObjectMeta{Name: "test", Namespace: "ns", Labels: map[string]string{"ci.openshift.io/multi-stage-test": "test"}},
		ImagePullSecrets: []v1.LocalObjectReference{{Name: "ci-operator-dockercfg-12345"}},
	}

	for _, tc := range []struct {
		name                             string
		failures                         sets.Set[string]
		testConfig                       *api.TestStepConfiguration
		observers                        []api.Observer
		objects                          []ctrlruntimeclient.Object
		leaseProxyClientConfigMapBackoff wait.Backoff
		interceptorsFunc                 func() interceptor.Funcs
		wantPodNames                     []string
		wantConfigMaps                   []corev1.ConfigMap
		wantSecrets                      []corev1.Secret
		wantErr                          error
	}{
		{
			name: "no step fails, no error",
			testConfig: &api.TestStepConfiguration{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:                []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test:               []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post:               []api.LiteralTestStep{{As: "post0"}, {As: "post1", OptionalOnSuccess: ptr.To(true)}},
					AllowSkipOnSuccess: ptr.To(true),
				},
			},
			wantPodNames: []string{
				"test-pre0", "test-pre1",
				"test-test0", "test-test1",
				"test-post0",
			},
			wantConfigMaps: []corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
				Immutable:  ptr.To(true),
				Data:       map[string]string{"post0": "", "post1": "", "pre0": "", "pre1": "", "test0": "", "test1": ""},
			}},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
		},
		{
			name:     "failure in a pre step, test should not run, post should",
			failures: sets.New("test-pre0"),
			testConfig: &api.TestStepConfiguration{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:                []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test:               []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post:               []api.LiteralTestStep{{As: "post0"}, {As: "post1", OptionalOnSuccess: ptr.To(true)}},
					AllowSkipOnSuccess: ptr.To(true),
				},
			},
			wantPodNames: []string{
				"test-pre0",
				"test-post0", "test-post1",
			},
			wantConfigMaps: []corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
				Immutable:  ptr.To(true),
				Data:       map[string]string{"post0": "", "post1": "", "pre0": "", "pre1": "", "test0": "", "test1": ""},
			}},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
			wantErr: errors.New(`"test" pre steps failed: "test" pod "test-pre0" failed: could not watch pod: the pod ns/test-pre0 failed after 0s (failed containers: sidecar, test): ContainerFailed one or more containers exited

Container test exited with code 1, reason 
Container sidecar exited with code 1, reason
Link to step on registry info site: https://steps.ci.openshift.org/reference/pre0
Link to job on registry info site: https://steps.ci.openshift.org/job?org=&repo=&branch=&test=test`),
		},
		{
			name:     "failure in a test step, post should run",
			failures: sets.New("test-test0"),
			testConfig: &api.TestStepConfiguration{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:                []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test:               []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post:               []api.LiteralTestStep{{As: "post0"}, {As: "post1", OptionalOnSuccess: ptr.To(true)}},
					AllowSkipOnSuccess: ptr.To(true),
				},
			},
			wantPodNames: []string{
				"test-pre0", "test-pre1",
				"test-test0",
				"test-post0", "test-post1",
			},
			wantConfigMaps: []corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
				Immutable:  ptr.To(true),
				Data:       map[string]string{"post0": "", "post1": "", "pre0": "", "pre1": "", "test0": "", "test1": ""},
			}},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
			wantErr: errors.New(`"test" test steps failed: "test" pod "test-test0" failed: could not watch pod: the pod ns/test-test0 failed after 0s (failed containers: sidecar, test): ContainerFailed one or more containers exited

Container test exited with code 1, reason 
Container sidecar exited with code 1, reason
Link to step on registry info site: https://steps.ci.openshift.org/reference/test0
Link to job on registry info site: https://steps.ci.openshift.org/job?org=&repo=&branch=&test=test`),
		},
		{
			name:     "failure in a post step, other post steps should still run",
			failures: sets.New("test-post0"),
			testConfig: &api.TestStepConfiguration{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:                []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test:               []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post:               []api.LiteralTestStep{{As: "post0"}, {As: "post1", OptionalOnSuccess: ptr.To(true)}},
					AllowSkipOnSuccess: ptr.To(true),
				},
			},
			wantPodNames: []string{
				"test-pre0", "test-pre1",
				"test-test0", "test-test1",
				"test-post0",
			},
			wantConfigMaps: []corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
				Immutable:  ptr.To(true),
				Data:       map[string]string{"post0": "", "post1": "", "pre0": "", "pre1": "", "test0": "", "test1": ""},
			}},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
			wantErr: errors.New(`"test" post steps failed: "test" pod "test-post0" failed: could not watch pod: the pod ns/test-post0 failed after 0s (failed containers: sidecar, test): ContainerFailed one or more containers exited

Container test exited with code 1, reason 
Container sidecar exited with code 1, reason
Link to step on registry info site: https://steps.ci.openshift.org/reference/post0
Link to job on registry info site: https://steps.ci.openshift.org/job?org=&repo=&branch=&test=test`),
		},
		{
			name:     "observer fails, no error",
			failures: sets.New("test-obsrv0"),
			testConfig: &api.TestStepConfiguration{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:                []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test:               []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post:               []api.LiteralTestStep{{As: "post0"}, {As: "post1", OptionalOnSuccess: ptr.To(true)}},
					Observers:          []api.Observer{{Name: "obsrv0"}},
					AllowSkipOnSuccess: ptr.To(true),
				},
			},
			wantPodNames: []string{
				"test-pre0", "test-pre1",
				"test-test0", "test-test1",
				"test-post0",
			},
			wantConfigMaps: []corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
				Immutable:  ptr.To(true),
				Data:       map[string]string{"post0": "", "post1": "", "pre0": "", "pre1": "", "test0": "", "test1": ""},
			}},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
		},
		{
			name: "Copy lease proxy script config map: success",
			objects: []ctrlruntimeclient.Object{&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
				Data:       map[string]string{"foo": "bar"},
			}},
			leaseProxyClientConfigMapBackoff: wait.Backoff{Steps: 3},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"foo": "bar"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lease-proxy", ResourceVersion: "1"},
					Data:       map[string]string{"foo": "bar"},
					Immutable:  ptr.To(true),
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
					Immutable:  ptr.To(true),
				},
			},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
		},
		{
			name: "Copy lease proxy script config map: update stale configmap",
			objects: []ctrlruntimeclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
					Data:       map[string]string{"super": "duper"},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lease-proxy"},
					Data:       map[string]string{"foo": "bar"},
				},
			},
			leaseProxyClientConfigMapBackoff: wait.Backoff{Steps: 3},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"super": "duper"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lease-proxy", ResourceVersion: "1"},
					Data:       map[string]string{"super": "duper"},
					Immutable:  ptr.To(true),
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
					Immutable:  ptr.To(true),
				},
			},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
		},
		{
			name: "Copy lease proxy script config map: retry on deletion failure",
			objects: []ctrlruntimeclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
					Data:       map[string]string{"super": "duper"},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lease-proxy"},
					Data:       map[string]string{"foo": "bar"},
				},
			},
			leaseProxyClientConfigMapBackoff: wait.Backoff{Steps: 3},
			interceptorsFunc: func() interceptor.Funcs {
				deleted := atomic.Bool{}
				return interceptor.Funcs{
					Delete: func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
						if _, ok := obj.(*corev1.ConfigMap); ok && deleted.CompareAndSwap(false, true) {
							return &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}}
						}
						return client.Delete(ctx, obj, opts...)
					},
				}
			},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"super": "duper"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lease-proxy", ResourceVersion: "1"},
					Data:       map[string]string{"super": "duper"},
					Immutable:  ptr.To(true),
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
					Immutable:  ptr.To(true),
				},
			},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
		},
		{
			name: "Copy lease proxy script config map: retry when it fails to create the up-to-date configmap",
			objects: []ctrlruntimeclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
					Data:       map[string]string{"super": "duper"},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lease-proxy"},
					Data:       map[string]string{"foo": "bar"},
				},
			},
			leaseProxyClientConfigMapBackoff: wait.Backoff{Steps: 3},
			interceptorsFunc: func() interceptor.Funcs {
				createCount := atomic.Int32{}
				return interceptor.Funcs{
					Create: func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
						// Fail when `create` is called for the second time. This means the stale configmap
						// has been deleted and the new one, with the up-to-date content, is about to be created.
						if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Name == api.LeaseProxyConfigMapName && createCount.Add(1) == 2 {
							return &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonAlreadyExists}}
						}
						return client.Create(ctx, obj, opts...)
					},
				}
			},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"super": "duper"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lease-proxy", ResourceVersion: "1"},
					Data:       map[string]string{"super": "duper"},
					Immutable:  ptr.To(true),
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
					Immutable:  ptr.To(true),
				},
			},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
		},
		{
			name: "Copy lease proxy script config map: give up retrying due to create failing consistently",
			objects: []ctrlruntimeclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
					Data:       map[string]string{"super": "duper"},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lease-proxy"},
					Data:       map[string]string{"foo": "bar"},
				},
			},
			leaseProxyClientConfigMapBackoff: wait.Backoff{Steps: 3},
			interceptorsFunc: func() interceptor.Funcs {
				return interceptor.Funcs{
					Create: func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
						if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Name == "lease-proxy" {
							return errors.New("permafailing")
						}
						return client.Create(ctx, obj, opts...)
					},
				}
			},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"super": "duper"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"foo": "bar"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "test-commands", Namespace: "ns", ResourceVersion: "1"},
					Immutable:  ptr.To(true),
				},
			},
			wantSecrets: []v1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test",
					Namespace:       "ns",
					ResourceVersion: "1",
					Labels:          map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			}},
			wantErr: errors.New("copy lease proxy scripts into ns ns: create configmap ns/lease-proxy: permafailing"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.testConfig == nil {
				tc.testConfig = &api.TestStepConfiguration{
					As:                                 "test",
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{}}
			}
			if tc.interceptorsFunc == nil {
				tc.interceptorsFunc = func() interceptor.Funcs { return interceptor.Funcs{} }
			}

			observerPodNames := sets.New[string]()
			for _, observerPod := range tc.testConfig.MultiStageTestConfigurationLiteral.Observers {
				observerPodNames.Insert(fmt.Sprintf("%s-%s", tc.testConfig.As, observerPod.Name))
			}

			crclient := &testhelper_kube.FakePodExecutor{
				LoggingClient: loggingclient.New(
					fakectrlruntimeclient.NewClientBuilder().
						WithIndex(&v1.Pod{}, "metadata.name", fakePodNameIndexer).
						WithObjects(sa).
						WithObjects(tc.objects...).
						WithInterceptorFuncs(tc.interceptorsFunc()).
						Build(), nil),
				Failures:     tc.failures,
				AutoSchedule: true,
			}

			client := &testhelper_kube.FakePodClient{
				PendingTimeout:  30 * time.Minute,
				FakePodExecutor: crclient,
			}
			step := MultiStageTestStep(*tc.testConfig, &api.ReleaseBuildConfiguration{}, nil, client, &jobSpec, nil, "node-name", "", func(cf context.CancelFunc) {}, false, nil, false, tc.leaseProxyClientConfigMapBackoff)

			gotErr := step.Run(context.Background())

			// Observer do not produce any error.
			wantFailures := tc.failures.Clone()
			observers := sets.New[string]()
			if len(tc.testConfig.MultiStageTestConfigurationLiteral.Observers) > 0 {
				for _, o := range tc.testConfig.MultiStageTestConfigurationLiteral.Observers {
					observers.Insert(tc.testConfig.As + "-" + o.Name)
				}
			}
			wantFailures = wantFailures.Difference(observers)
			// Do not compare errors literally when we expect a pod to fail. The error message is not stable
			// since a pod might fail after an arbitrary amount of time, therefore we get:
			// `pod ns/test-post0 failed after <n>s` and we can't predict what the value of <n>
			// might actually be.
			if wantFailures.Len() > 0 {
				if gotErr == nil {
					t.Fatal("expected pod failure errors but got nil")
				}
				msg := gotErr.Error()
				for pod := range tc.failures {
					if observers.Has(pod) {
						continue
					}
					if !strings.Contains(msg, fmt.Sprintf("pod %q failed", pod)) {
						t.Errorf("pod %s didn't fail as expected", pod)
					}
				}
			} else {
				if gotErr != nil && tc.wantErr == nil {
					t.Errorf("want err nil but got: %v", gotErr)
				}
				if gotErr == nil && tc.wantErr != nil {
					t.Errorf("want err %v but nil", tc.wantErr)
				}
				if gotErr != nil && tc.wantErr != nil {
					if diff := cmp.Diff(tc.wantErr.Error(), gotErr.Error()); diff != "" {
						t.Errorf("unexpected error: %s", diff)
					}
				}
			}

			gotSecrets := &v1.SecretList{}
			if err := crclient.List(context.TODO(), gotSecrets, ctrlruntimeclient.InNamespace(jobSpec.Namespace())); err != nil {
				t.Fatal(err)
			}
			secretSorter := func(a, b *v1.Secret) bool { return a.Namespace+a.Name <= b.Namespace+b.Name }
			if diff := cmp.Diff(tc.wantSecrets, gotSecrets.Items, cmpopts.SortSlices(secretSorter)); diff != "" {
				t.Errorf("unexpected secrets: %s", diff)
			}

			// An observer pod can be executed at any time, therefore making unstable the output
			// of the pods the client has created. Do not take into account them.
			observerPodsToRemove := observerPodNames.Clone()
			var gotPodNames []string
			for _, pod := range crclient.CreatedPods {
				if pod.Namespace != jobSpec.Namespace() {
					t.Errorf("pod %s didn't have namespace %s set, had %q instead", pod.Name, jobSpec.Namespace(), pod.Namespace)
				}
				if !observerPodsToRemove.Has(pod.Name) {
					gotPodNames = append(gotPodNames, pod.Name)
				} else {
					observerPodsToRemove.Delete(pod.Name)
				}
			}

			if observerPodsToRemove.Len() > 0 {
				t.Errorf("did not find the following pods to remove: %s", observerPodsToRemove.UnsortedList())
			}

			if diff := cmp.Diff(gotPodNames, tc.wantPodNames); diff != "" {
				t.Errorf("did not execute correct pods: %s, actual: %v, expected: %v", diff, gotPodNames, tc.wantPodNames)
			}

			gotCMs := corev1.ConfigMapList{}
			if err := client.List(context.TODO(), &gotCMs, &ctrlruntimeclient.ListOptions{}); err != nil {
				t.Fatalf("list configmaps: %s", err)
			}

			configMapSorter := func(a, b *v1.ConfigMap) bool { return a.Namespace+a.Name <= b.Namespace+b.Name }
			if diff := cmp.Diff(tc.wantConfigMaps, gotCMs.Items, cmpopts.SortSlices(configMapSorter)); diff != "" {
				t.Errorf("unexpected configmaps: %s", diff)
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
				Failures:     tc.failures,
				AutoSchedule: true,
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
			client := &testhelper_kube.FakePodClient{
				FakePodExecutor: crclient,
				PendingTimeout:  30 * time.Minute,
			}
			step := MultiStageTestStep(api.TestStepConfiguration{
				As: "test",
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Pre:  []api.LiteralTestStep{{As: "pre0"}, {As: "pre1"}},
					Test: []api.LiteralTestStep{{As: "test0"}, {As: "test1"}},
					Post: []api.LiteralTestStep{{As: "post0"}, {As: "post1"}},
				},
			}, &api.ReleaseBuildConfiguration{}, nil, client, &jobSpec, nil, "node-name", "", nil, false, nil, false, wait.Backoff{})
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

func TestRunPodDeletesPendingPodsOnError(t *testing.T) {
	sa := &v1.ServiceAccount{
		ObjectMeta:       metav1.ObjectMeta{Name: "test", Namespace: "ns", Labels: map[string]string{"ci.openshift.io/multi-stage-test": "test"}},
		ImagePullSecrets: []v1.LocalObjectReference{{Name: "ci-operator-dockercfg-12345"}},
	}
	name := "test"

	// Create a pod that will fail while still in Pending state
	pendingPodName := "test-pre0"
	crclient := &testhelper_kube.FakePodExecutor{
		LoggingClient: loggingclient.New(
			fakectrlruntimeclient.NewClientBuilder().
				WithIndex(&v1.Pod{}, "metadata.name", fakePodNameIndexer).
				WithObjects(sa).
				Build(), nil),
		Failures: sets.New[string](pendingPodName),
		Pending:  sets.New[string](pendingPodName), // Keep this pod in Pending state
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
		PendingTimeout:  100 * time.Millisecond, // Short timeout to trigger pending timeout
		FakePodExecutor: crclient,
	}

	yes := true
	step := MultiStageTestStep(api.TestStepConfiguration{
		As: name,
		MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
			Pre:                []api.LiteralTestStep{{As: "pre0"}},
			Test:               []api.LiteralTestStep{{As: "test0"}},
			Post:               []api.LiteralTestStep{{As: "post0"}},
			AllowSkipOnSuccess: &yes,
		},
	}, &api.ReleaseBuildConfiguration{}, nil, client, &jobSpec, nil, "node-name", "", func(cf context.CancelFunc) {}, false, nil, false, wait.Backoff{})

	// Use a context with timeout to ensure the test doesn't hang
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run the step - it should fail because pre0 times out while Pending
	if err := step.Run(ctx); err == nil {
		t.Error("expected step to fail, but it succeeded")
	}

	// Verify the pending pod was deleted
	if len(crclient.DeletedPods) == 0 {
		t.Error("expected pending pod to be deleted, but no pods were deleted")
	} else {
		found := false
		for _, pod := range crclient.DeletedPods {
			if pod.Name == pendingPodName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected pod %s to be deleted, but it was not in the deleted pods list", pendingPodName)
		}
	}
}
