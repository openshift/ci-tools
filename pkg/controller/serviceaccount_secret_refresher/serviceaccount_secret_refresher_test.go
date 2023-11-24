package serviceaccountsecretrefresher

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcile(t *testing.T) {

	sa := &corev1.ServiceAccount{
		ObjectMeta:       metav1.ObjectMeta{Namespace: "namespace", Name: "sa"},
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: "pull-secret"}},
		Secrets:          []corev1.ObjectReference{{Name: "pull-secret"}, {Name: "token-secret"}},
	}

	testCases := []struct {
		name                       string
		objects                    []runtime.Object
		removeOldSecrets           bool
		filter                     func(reconcile.Request) bool
		expectedRequeAfterHours    int
		expectedNumImagePullSecret uint
		expectedNumTokenSecret     uint
		expectedPullSecretName     string
		expectedTokenSecretName    string
	}{
		{
			name: "secrets are rotated and cleaned",
			objects: []runtime.Object{
				sa.DeepCopy(),
				secretForSA(sa, corev1.SecretTypeDockercfg, func(s *corev1.Secret) { s.Name = "pull-secret" }),
				secretForSA(sa, corev1.SecretTypeServiceAccountToken, func(s *corev1.Secret) { s.Name = "token-secret" }),
			},
			removeOldSecrets:           true,
			expectedNumImagePullSecret: 1,
			expectedNumTokenSecret:     0,
			expectedPullSecretName:     "new-pull-secret",
			expectedTokenSecretName:    "token-secret",
		},
		{
			name: "secrets are rotated but not old enough to be cleaned",
			objects: []runtime.Object{
				sa.DeepCopy(),
				secretForSA(sa, corev1.SecretTypeDockercfg, func(s *corev1.Secret) {
					s.Name = "pull-secret"
					s.CreationTimestamp = metav1.NewTime(time.Now().Add(-59 * 24 * time.Hour))
				}),
				secretForSA(sa, corev1.SecretTypeServiceAccountToken, func(s *corev1.Secret) {
					s.Name = "token-secret"
					s.CreationTimestamp = metav1.NewTime(time.Now().Add(-59 * 24 * time.Hour))
				}),
			},
			removeOldSecrets:           true,
			expectedNumImagePullSecret: 2,
			expectedNumTokenSecret:     1,
			expectedPullSecretName:     "new-pull-secret",
			expectedTokenSecretName:    "token-secret",
			expectedRequeAfterHours:    23,
		},
		{
			name: "secrets are rotated only",
			objects: []runtime.Object{
				sa.DeepCopy(),
				secretForSA(sa, corev1.SecretTypeDockercfg, func(s *corev1.Secret) { s.Name = "pull-secret" }),
				secretForSA(sa, corev1.SecretTypeServiceAccountToken, func(s *corev1.Secret) { s.Name = "token-secret" }),
			},
			removeOldSecrets:           false,
			expectedNumImagePullSecret: 2,
			expectedNumTokenSecret:     1,
			expectedPullSecretName:     "new-pull-secret",
			expectedTokenSecretName:    "token-secret",
		},
		{
			name: "secrets are up to date, reconcileAfter is returned",
			objects: []runtime.Object{
				sa.DeepCopy(),
				secretForSA(sa, corev1.SecretTypeDockercfg, func(s *corev1.Secret) {
					s.Name = "pull-secret"
					s.CreationTimestamp = metav1.NewTime(time.Now().Add(-29 * 24 * time.Hour))
				}),
				secretForSA(sa, corev1.SecretTypeServiceAccountToken, func(s *corev1.Secret) {
					s.Name = "token-secret"
					s.CreationTimestamp = metav1.NewTime(time.Now().Add(-28 * 24 * time.Hour))
				}),
			},
			removeOldSecrets:           true,
			expectedRequeAfterHours:    23,
			expectedNumImagePullSecret: 1,
			expectedNumTokenSecret:     1,
			expectedPullSecretName:     "pull-secret",
			expectedTokenSecretName:    "token-secret",
		},
		{
			name: "namespace is filtered out, nothing happens",
			objects: []runtime.Object{
				sa.DeepCopy(),
				secretForSA(sa, corev1.SecretTypeDockercfg, func(s *corev1.Secret) { s.Name = "pull-secret" }),
				secretForSA(sa, corev1.SecretTypeServiceAccountToken, func(s *corev1.Secret) { s.Name = "token-secret" }),
			},
			filter:                     func(r reconcile.Request) bool { return false },
			removeOldSecrets:           true,
			expectedNumImagePullSecret: 1,
			expectedNumTokenSecret:     1,
			expectedPullSecretName:     "pull-secret",
			expectedTokenSecretName:    "token-secret",
		},
		{
			name: "young secrets are rotated and deleted because of ttl annotation",
			objects: []runtime.Object{
				sa.DeepCopy(),
				secretForSA(sa, corev1.SecretTypeDockercfg, func(s *corev1.Secret) {
					s.Name = "pull-secret"
					s.CreationTimestamp = metav1.Now()
					s.Annotations = map[string]string{"serviaccount-secret-rotator.openshift.io/delete-after": time.Time{}.Format(time.RFC3339)}
				}),
				secretForSA(sa, corev1.SecretTypeServiceAccountToken, func(s *corev1.Secret) {
					s.Name = "token-secret"
					s.CreationTimestamp = metav1.Now()
					s.Annotations = map[string]string{"serviaccount-secret-rotator.openshift.io/delete-after": time.Time{}.Format(time.RFC3339)}
				}),
			},
			removeOldSecrets:           true,
			expectedNumImagePullSecret: 1,
			expectedNumTokenSecret:     0,
			expectedPullSecretName:     "new-pull-secret",
			expectedTokenSecretName:    "token-secret",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.filter == nil {
				tc.filter = func(_ reconcile.Request) bool { return true }
			}
			client := &serviceaccountSecretRecreatingClient{t: t, Client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(tc.objects...).Build()}

			r := &reconciler{
				client:           client,
				filter:           tc.filter,
				log:              logrus.WithField("test", tc.name),
				second:           10 * time.Millisecond,
				removeOldSecrets: tc.removeOldSecrets,
			}

			ctx := context.Background()

			reconcileAfter, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "namespace", Name: "sa"}})
			if err != nil {
				t.Fatalf("reconciliation failed: %v", err)
			}
			if tc.expectedRequeAfterHours != 0 {
				if actual := int(reconcileAfter.RequeueAfter.Hours()); actual != tc.expectedRequeAfterHours {
					t.Errorf("expected requeueAfter hours of %d, got %d", tc.expectedRequeAfterHours, actual)
				}
			}

			sa := &corev1.ServiceAccount{}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "namespace", Name: "sa"}, sa); err != nil {
				t.Fatalf("failed to get sa: %v", err)
			}
			if n := len(sa.ImagePullSecrets); n != 1 {
				t.Errorf("expected exactly one pull secret, had %d", n)
			} else if sa.ImagePullSecrets[0].Name != tc.expectedPullSecretName {
				t.Errorf("expected pull secret name %s, was %s", tc.expectedPullSecretName, sa.ImagePullSecrets[0].Name)
			}
			if n := len(sa.Secrets); n != 2 {
				t.Errorf("expected exactly two secrets, had %d", n)
			} else {
				var found bool
				for _, secret := range sa.Secrets {
					if secret.Name == tc.expectedTokenSecretName {
						found = true
						break
					}

				}
				if !found {
					t.Errorf("sa didn't have token secret %s", tc.expectedTokenSecretName)
				}
			}

			secretList := &corev1.SecretList{}
			if err := client.List(ctx, secretList); err != nil {
				t.Errorf("failed to list secrets: %v", err)
			}

			var imagePullSecretCount, tokenSecretCount uint
			for _, secret := range secretList.Items {
				switch secret.Type {
				case corev1.SecretTypeDockercfg:
					imagePullSecretCount++
				case corev1.SecretTypeServiceAccountToken:
					tokenSecretCount++
				default:
					t.Errorf("secret %s had unexpected type %s", secret.Name, secret.Type)
				}
			}

			if imagePullSecretCount != tc.expectedNumImagePullSecret {
				t.Errorf("expected %d imagepull secrets, got %d", tc.expectedNumImagePullSecret, imagePullSecretCount)
			}
			if tokenSecretCount != tc.expectedNumTokenSecret {
				t.Errorf("expected %d token secrets, got %d", tc.expectedNumTokenSecret, tokenSecretCount)
			}
		})

	}
}

type serviceaccountSecretRecreatingClient struct {
	t *testing.T
	ctrlruntimeclient.Client
}

func (c *serviceaccountSecretRecreatingClient) Update(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.UpdateOption) error {
	if sa, ok := obj.(*corev1.ServiceAccount); ok {
		oldSA := sa.DeepCopy()
		sa := sa.DeepCopy()

		updateDone := make(chan struct{})
		defer func() { close(updateDone) }()

		go func() {
			// If there are missing secrets, create them after a 100 ms delay
			if len(sa.ImagePullSecrets) == 1 && len(sa.Secrets) == 2 {
				return
			}

			// Make sure we patch this after the update call went through to avoid test flakes
			<-updateDone

			if len(sa.ImagePullSecrets) == 0 {
				newSecret := secretForSA(sa.DeepCopy(), corev1.SecretTypeDockercfg, func(s *corev1.Secret) {
					s.Name = "new-pull-secret"
					s.CreationTimestamp = metav1.Now()
				})
				if err := c.Create(ctx, newSecret); err != nil {
					panic(fmt.Sprintf("failed to create new imagepullsecret: %v", err))
				}
				sa.ImagePullSecrets = []corev1.LocalObjectReference{{Name: newSecret.Name}}
				sa.Secrets = append(sa.Secrets, corev1.ObjectReference{Name: newSecret.Name})
			}

			if len(sa.Secrets) != 2 {
				newSecet := secretForSA(sa, corev1.SecretTypeServiceAccountToken, func(s *corev1.Secret) {
					s.Name = "new-token-secret"
					s.CreationTimestamp = metav1.Now()
				})
				newSecet.CreationTimestamp = metav1.Now()
				if err := c.Create(ctx, newSecet); err != nil {
					panic(fmt.Sprintf("failed to create new token secret: %v", err))
				}
				sa.Secrets = append(sa.Secrets, corev1.ObjectReference{Name: newSecet.Name})
			}

			if err := c.Client.Patch(ctx, sa, ctrlruntimeclient.MergeFrom(oldSA)); err != nil {
				panic(fmt.Sprintf("failed to patch serviceaccount after creating new pull secret: %v", err))
			}
		}()
	}

	return c.Client.Update(ctx, obj, opts...)
}

func secretForSA(sa *corev1.ServiceAccount, tp corev1.SecretType, mod ...func(*corev1.Secret)) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: sa.Namespace,
			Name:      strconv.Itoa(time.Now().Nanosecond()),
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: sa.Name,
				corev1.ServiceAccountUIDKey:  string(sa.UID),
			},
		},
		Type: tp,
	}
	for _, m := range mod {
		m(s)
	}
	return s
}
