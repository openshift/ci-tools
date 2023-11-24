package hypershift_namespace_reconciler

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestReconcile(t *testing.T) {

	testCases := []struct {
		name   string
		nn     types.NamespacedName
		client ctrlruntimeclient.Client
		verify func(ctrlruntimeclient.Client) error
	}{
		{
			name: "labels are in order",
			nn:   types.NamespacedName{Name: "clusters-hypershift-ci-25524"},
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "clusters-hypershift-ci-25524",
						Labels:      map[string]string{"openshift.io/cluster-monitoring": "true", "hypershift.openshift.io/hosted-control-plane": ""},
						Annotations: map[string]string{"a": "b"},
					},
				},
			).Build(),
			verify: func(client ctrlruntimeclient.Client) error {
				actual := &corev1.Namespace{}
				if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Name: "clusters-hypershift-ci-25524"}, actual); err != nil {
					return err
				}
				expected := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "clusters-hypershift-ci-25524",
						Labels:      map[string]string{"hypershift.openshift.io/hosted-control-plane": "", "openshift.io/user-monitoring": "false"},
						Annotations: map[string]string{"a": "b"},
					},
				}
				if diff := cmp.Diff(expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			t.Parallel()
			log := logrus.NewEntry(logrus.StandardLogger())
			logrus.SetLevel(logrus.TraceLevel)
			r := &reconciler{log: log, client: tc.client}
			_, _ = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: tc.nn})
			if tc.verify != nil {
				if err := tc.verify(tc.client); err != nil {
					t.Errorf("%s: an unexpected error occurred: %v", tc.name, err)
				}
			}
		})
	}
}

func TestHypershiftNamespace(t *testing.T) {

	testCases := []struct {
		name     string
		labels   map[string]string
		expected bool
	}{
		{
			name: "empty label",
		},
		{
			name:     "hypershift ns",
			labels:   map[string]string{"hypershift.openshift.io/hosted-control-plane": "", "openshift.io/user-monitoring": "false"},
			expected: true,
		},
		{
			name:     "hypershift ns with true",
			labels:   map[string]string{"hypershift.openshift.io/hosted-control-plane": "true"},
			expected: true,
		},
		{
			name:   "non hypershift ns",
			labels: map[string]string{"openshift.io/user-monitoring": "false"},
		},
		{
			name:   "non hypershift ns with a wrong value",
			labels: map[string]string{"hypershift.openshift.io/hosted-control-plane": "some", "openshift.io/user-monitoring": "false"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := hypershiftNamespace(tc.labels)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
