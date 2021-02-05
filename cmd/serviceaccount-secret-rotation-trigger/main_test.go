package main

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestClean(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                           string
		clients                        map[string]ctrlruntimeclient.Client
		namespaces                     []string
		expectedUpdatedSecrets         map[ctrlruntimeclient.ObjectKey]struct{}
		expectedUpdatedServiceAccounts map[ctrlruntimeclient.ObjectKey]struct{}
	}{
		{
			name: "Non SA-Secrets are ignored",
			clients: createObjectInMultipleFakeClusters(
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      "some-secret",
					Namespace: "default",
				}},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      "some-secret",
					Namespace: "non-default",
				}},
			),
			namespaces: []string{"default", "non-default"},
		},
		{
			name: "Other namespaces get ignored",
			clients: createObjectInMultipleFakeClusters(
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:        "some-secret",
					Namespace:   "default",
					Annotations: map[string]string{corev1.ServiceAccountUIDKey: "some-val"},
				}},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:        "some-secret",
					Namespace:   "non-default",
					Annotations: map[string]string{corev1.ServiceAccountUIDKey: "some-val"},
				}},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "some-sa",
						Namespace: "default",
					},
					Secrets:          []corev1.ObjectReference{{}},
					ImagePullSecrets: []corev1.LocalObjectReference{{}},
				},
			),
		},
		{
			name: "Secrets with existing ttl annotation get ignored",
			clients: createObjectInMultipleFakeClusters(
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      "some-secret",
					Namespace: "default",
					Annotations: map[string]string{
						corev1.ServiceAccountUIDKey:                             "some-val",
						"serviaccount-secret-rotator.openshift.io/delete-after": "not a time",
					},
				}},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      "some-secret",
					Namespace: "non-default",
					Annotations: map[string]string{
						corev1.ServiceAccountUIDKey:                             "some-val",
						"serviaccount-secret-rotator.openshift.io/delete-after": "not a time",
					},
				}},
			),
			namespaces: []string{"default", "non-default"},
		},
		{
			name: "Secrets get annotated",
			clients: createObjectInMultipleFakeClusters(
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:        "some-secret",
					Namespace:   "default",
					Annotations: map[string]string{corev1.ServiceAccountUIDKey: "some-val"},
				}},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:        "some-secret",
					Namespace:   "non-default",
					Annotations: map[string]string{corev1.ServiceAccountUIDKey: "some-val"},
				}},
			),
			namespaces: []string{"default", "non-default"},
			expectedUpdatedSecrets: map[ctrlruntimeclient.ObjectKey]struct{}{
				{Namespace: "default", Name: "some-secret"}:     {},
				{Namespace: "non-default", Name: "some-secret"}: {},
			},
		},
		{
			name: "SA gets updated",
			clients: createObjectInMultipleFakeClusters(
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "some-sa",
						Namespace: "default",
					},
					Secrets:          []corev1.ObjectReference{{}},
					ImagePullSecrets: []corev1.LocalObjectReference{{}},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "some-sa",
						Namespace: "non-default",
					},
					Secrets:          []corev1.ObjectReference{{}},
					ImagePullSecrets: []corev1.LocalObjectReference{{}},
				},
			),
			namespaces: []string{"default", "non-default"},
			expectedUpdatedServiceAccounts: map[ctrlruntimeclient.ObjectKey]struct{}{
				{Namespace: "default", Name: "some-secret"}:     {},
				{Namespace: "non-default", Name: "some-secret"}: {},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if err := clean(ctx, tc.clients, tc.namespaces); err != nil {
				t.Fatalf("clean failed: %v", err)
			}

			for cluster, client := range tc.clients {
				var allSecrets corev1.SecretList
				if err := client.List(ctx, &allSecrets); err != nil {
					t.Errorf("failed to list secrets in cluster %s: %v", cluster, err)
					continue
				}
				for _, item := range allSecrets.Items {
					// We abuse "is not parseable" which could be both empty or pre-existing value as check for "Did our code change this"
					_, expectParseableTimestamp := tc.expectedUpdatedSecrets[ctrlruntimeclient.ObjectKeyFromObject(&item)]
					parsed, err := time.Parse(time.RFC3339, item.Annotations["serviaccount-secret-rotator.openshift.io/delete-after"])
					if (err == nil) != expectParseableTimestamp {
						t.Errorf("cluster %s secret %s, expectParseableTimestamp: %t but got error %v", cluster, ctrlruntimeclient.ObjectKeyFromObject(&item), expectParseableTimestamp, err)
						continue
					}
					if expectParseableTimestamp {
						if expected, actual := time.Now().Hour(), parsed.Hour(); expected != actual {
							t.Errorf("expected value of serviaccount-secret-rotator.openshift.io/delete-after annotation to be %d, was %d", expected, actual)
						}
					}

					var allServiceAccounts corev1.ServiceAccountList
					if err := client.List(ctx, &allServiceAccounts); err != nil {
						t.Errorf("failed to list serviceaccounts in cluster %s: %v", cluster, err)
						continue
					}
					for _, item := range allServiceAccounts.Items {
						_, expectRemovedSecrets := tc.expectedUpdatedServiceAccounts[ctrlruntimeclient.ObjectKeyFromObject(&item)]
						hasRemovedSecrets := len(item.Secrets)+len(item.ImagePullSecrets) == 0
						if expectRemovedSecrets != hasRemovedSecrets {
							t.Errorf("Servicaccount %s: expected removed secrets %t but had %d token secrets and %d pull secrets", ctrlruntimeclient.ObjectKeyFromObject(&item), expectRemovedSecrets, len(item.Secrets), len(item.ImagePullSecrets))
						}

					}
				}
			}
		})
	}
}

// Always use multiple clients to implicitly verify threadsafety
func createObjectInMultipleFakeClusters(obj ...ctrlruntimeclient.Object) map[string]ctrlruntimeclient.Client {
	return map[string]ctrlruntimeclient.Client{
		"a": fakectrlruntimeclient.NewClientBuilder().WithObjects(obj...).Build(),
		"b": fakectrlruntimeclient.NewClientBuilder().WithObjects(obj...).Build(),
	}
}

//serviaccount-secret-rotator.openshift.io/delete-after
