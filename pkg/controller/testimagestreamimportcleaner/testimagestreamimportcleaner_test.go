package testimagestreamimportcleaner

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
)

func TestReconcile(t *testing.T) {
	t.Parallel()

	now := time.Time{}.Add(2 * sevenDays)
	testCases := []struct {
		name   string
		client ctrlruntimeclient.Client

		expectReconcileResult reconcile.Result
		expectImport          bool
	}{
		{
			name:   "Not found is swallowed",
			client: fakectrlruntimeclient.NewClientBuilder().Build(),
		},
		{
			name: "ReconcileAfter is returned for item younger seven days",
			client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(&testimagestreamtagimportv1.TestImageStreamTagImport{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "name",
					Namespace:         "namespace",
					CreationTimestamp: metav1.Time{Time: now.Add(-6 * 24 * time.Hour)},
				},
			}).Build(),
			expectReconcileResult: reconcile.Result{RequeueAfter: 24 * time.Hour},
			expectImport:          true,
		},
		{
			name: "Item older seven days gets deleted",
			client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(&testimagestreamtagimportv1.TestImageStreamTagImport{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "name",
					Namespace:         "namespace",
					CreationTimestamp: metav1.Time{},
				},
			}).Build(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &reconciler{
				client: tc.client,
				now:    func() time.Time { return now },
			}

			result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "namespace", Name: "name"}})
			if err != nil {
				t.Fatalf("reconcile failed: %v", err)
			}
			if diff := cmp.Diff(result, tc.expectReconcileResult); diff != "" {
				t.Errorf("reconcile result differs from expected: %s", diff)
			}

			var list testimagestreamtagimportv1.TestImageStreamTagImportList
			if err := r.client.List(context.Background(), &list); err != nil {
				t.Fatalf("failed to list testimagestreamtagimports: %v", err)
			}
			hasImport := len(list.Items) > 0

			if hasImport != tc.expectImport {
				t.Errorf("expected import to exist: %t, import did exist: %t", tc.expectImport, hasImport)
			}
		})
	}
}
