package utils

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

type fakePoolClient struct {
	returns []hivev1.ClusterPool
}

func (f fakePoolClient) Get(_ context.Context, _ client.ObjectKey, _ client.Object) error {
	panic("implement me")
}

func (f fakePoolClient) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	l := list.(*hivev1.ClusterPoolList)
	l.Items = f.returns
	return nil
}

func TestClusterPoolFromClaim(t *testing.T) {
	testCases := []struct {
		description string
		pools       []hivev1.ClusterPool
		expected    *hivev1.ClusterPool
		expectErr   error
	}{
		{
			description: "returns error on empty pool list",
			expectErr:   errors.New("failed to find a cluster pool providing the cluster: map[architecture: cloud: owner: product: version:]"),
		},
		{
			description: "returns the cluster when there is just one",
			pools:       []hivev1.ClusterPool{{ObjectMeta: v1.ObjectMeta{Name: "i-have-six-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 6}}},
			expected:    &hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "i-have-six-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 6}},
		},
		{
			description: "select the first when there are many depleted",
			pools: []hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "me-neither"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
			},
			expected: &hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
		},
		{
			description: "select the cluster with most ready clusters",
			pools: []hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-3-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 3}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-6-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 6}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-5-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 5}},
			},
			expected: &hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "i-have-6-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 6}},
		},
		{
			description: "select the clusters with larger size when ready are equal",
			pools: []hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-3"}, Spec: hivev1.ClusterPoolSpec{Size: 3}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-4"}, Spec: hivev1.ClusterPoolSpec{Size: 4}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
			},
			expected: &hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-4"}, Spec: hivev1.ClusterPoolSpec{Size: 4}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
		},
		{
			description: "select the clusters with larger maxsize when ready and size are equal",
			pools: []hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-4max"}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(4)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-3max"}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(3)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
			},
			expected: &hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-4max"}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(4)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			got, err := ClusterPoolFromClaim(context.TODO(), &api.ClusterClaim{}, fakePoolClient{returns: tc.pools})
			if diff := cmp.Diff(tc.expectErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%s", diff)
				return
			}
			if diff := cmp.Diff(tc.expected, got); err == nil && diff != "" {
				t.Errorf("Selected pool differs from expected:\n%s", diff)
			}
		})
	}
}
