package promotionnamespacereconciler

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcile(t *testing.T) {
	const nsName = "ns"
	testCases := []struct {
		name   string
		client ctrlruntimeclient.Client
	}{
		{
			name:   "Namespace already exists, nothing to do",
			client: fakectrlruntimeclient.NewFakeClient(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}),
		},
		{
			name:   "Namespace is created",
			client: fakectrlruntimeclient.NewFakeClient(),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &reconciler{client: tc.client, log: logrus.NewEntry(logrus.StandardLogger())}
			_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: nsName}})
			if err != nil {
				t.Fatalf("Reconciliation failed: %v", err)
			}
			var namespaces corev1.NamespaceList
			if err := r.client.List(context.Background(), &namespaces); err != nil {
				t.Fatalf("failed to list namespaces: %v", err)
			}
			if len(namespaces.Items) != 1 || namespaces.Items[0].Name != nsName {
				t.Errorf("exepected to get exactly one namespace named %s, got %+v", nsName, namespaces.Items)
			}
		})
	}
}
