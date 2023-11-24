package cluster_pools_pull_secret_provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := hivev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

var (
	pool = &hivev1.ClusterPool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "pool",
		},
		Spec: hivev1.ClusterPoolSpec{
			PullSecretRef: &corev1.LocalObjectReference{
				Name: "pull-secret",
			},
		},
	}

	anotherPool = &hivev1.ClusterPool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "another-pool",
		},
		Spec: hivev1.ClusterPoolSpec{
			PullSecretRef: &corev1.LocalObjectReference{
				Name: "pull-secret",
			},
		},
	}

	anotherPoolInAnotherNS = &hivev1.ClusterPool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "another-ns",
			Name:      "another-pool",
		},
		Spec: hivev1.ClusterPoolSpec{
			PullSecretRef: &corev1.LocalObjectReference{
				Name: "pull-secret",
			},
		},
	}

	poolWithoutPullSecret = &hivev1.ClusterPool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo-ns",
			Name:      "another-pool",
		},
	}
)

func TestReconcile(t *testing.T) {

	srcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci-cluster-pool",
			Name:      "pull-secret",
			Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
		},
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("abc")},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	poolWithAnotherSecret := &hivev1.ClusterPool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "pool",
		},
		Spec: hivev1.ClusterPoolSpec{
			PullSecretRef: &corev1.LocalObjectReference{
				Name: "another-pull-secret",
			},
		},
	}

	im := true

	testCases := []struct {
		name          string
		nn            types.NamespacedName
		client        ctrlruntimeclient.Client
		expected      reconcile.Result
		expectedError error
		verify        func(ctrlruntimeclient.Client) error
	}{
		{
			name:   "the target secret is created",
			nn:     types.NamespacedName{Namespace: "ns", Name: "pool"},
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(srcSecret.DeepCopy(), pool.DeepCopy()).Build(),
			verify: func(client ctrlruntimeclient.Client) error {
				actual := &corev1.Secret{}
				if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: "ns", Name: "pull-secret"}, actual); err != nil {
					return err
				}
				expected := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "pull-secret",
						Labels:    map[string]string{api.DPTPRequesterLabel: ControllerName},
					},
					Data:      map[string][]byte{corev1.DockerConfigJsonKey: []byte("abc")},
					Type:      corev1.SecretTypeDockerConfigJson,
					Immutable: &im,
				}
				if diff := cmp.Diff(expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:   "the pool doesn't exist",
			nn:     types.NamespacedName{Namespace: "ns", Name: "pool"},
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(srcSecret.DeepCopy()).Build(),
			verify: func(client ctrlruntimeclient.Client) error {
				if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: "ns", Name: "pull-secret"}, &corev1.Secret{}); !kerrors.IsNotFound(err) {
					return fmt.Errorf("expected not found error did not occur")
				}
				return nil
			},
		},
		{
			name:          "the src secret is not there",
			nn:            types.NamespacedName{Namespace: "ns", Name: "pool"},
			client:        fakeclient.NewClientBuilder().WithRuntimeObjects(pool.DeepCopy()).Build(),
			expectedError: fmt.Errorf("failed to get the secret pull-secret in namespace ci-cluster-pool: %w", fmt.Errorf("secrets \"pull-secret\" not found")),
			verify: func(client ctrlruntimeclient.Client) error {
				if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: "ns", Name: "pull-secret"}, &corev1.Secret{}); !kerrors.IsNotFound(err) {
					return fmt.Errorf("expected not found error did not occur")
				}
				return nil
			},
		},
		{
			name:   "the pool does not use the pull secret",
			nn:     types.NamespacedName{Namespace: "ns", Name: "pool"},
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(srcSecret.DeepCopy(), poolWithAnotherSecret.DeepCopy()).Build(),
			verify: func(client ctrlruntimeclient.Client) error {
				if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: "ns", Name: "pull-secret"}, &corev1.Secret{}); !kerrors.IsNotFound(err) {
					return fmt.Errorf("expected not found error did not occur")
				}
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			// Needed so the racedetector tells us if we accidentally re-use global state, e.G. by not deepcopying
			t.Parallel()
			log := logrus.NewEntry(logrus.StandardLogger())
			logrus.SetLevel(logrus.TraceLevel)
			r := &reconciler{
				log:                       log,
				client:                    tc.client,
				sourcePullSecretNamespace: "ci-cluster-pool",
				sourcePullSecretName:      "pull-secret",
			}
			request := reconcile.Request{NamespacedName: tc.nn}
			actual, actualError := r.Reconcile(context.Background(), request)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if tc.verify != nil {
				if err := tc.verify(tc.client); err != nil {
					t.Errorf("%s: an unexpected error occurred: %v", tc.name, err)
				}
			}
		})
	}
}

func TestRequestsFactoryForSecretEvent(t *testing.T) {
	testCases := []struct {
		name      string
		namespace string
		client    ctrlruntimeclient.Client
		expected  []reconcile.Request
	}{
		{
			name:   "empty namespace",
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(pool.DeepCopy(), anotherPool.DeepCopy(), anotherPoolInAnotherNS.DeepCopy(), poolWithoutPullSecret.DeepCopy()).Build(),
			expected: []reconcile.Request{
				{NamespacedName: types.NamespacedName{
					Namespace: "another-ns",
					Name:      "another-pool",
				}},
				{NamespacedName: types.NamespacedName{
					Namespace: "foo-ns",
					Name:      "another-pool",
				}},
				{NamespacedName: types.NamespacedName{
					Namespace: "ns",
					Name:      "another-pool",
				}},
				{NamespacedName: types.NamespacedName{
					Namespace: "ns",
					Name:      "pool",
				}},
			},
		},
		{
			name:      "some namespace",
			namespace: "another-ns",
			client:    fakeclient.NewClientBuilder().WithRuntimeObjects(pool.DeepCopy(), anotherPool.DeepCopy(), anotherPoolInAnotherNS.DeepCopy()).Build(),
			expected: []reconcile.Request{
				{NamespacedName: types.NamespacedName{
					Namespace: "another-ns",
					Name:      "another-pool",
				}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logrus.SetLevel(logrus.TraceLevel)
			actual := requestsFactoryForSecretEvent(tc.namespace, tc.client)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
