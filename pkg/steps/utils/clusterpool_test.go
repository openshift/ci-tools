package utils

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

type fakePoolClient struct {
	returns []hivev1.ClusterPool
}

func (f fakePoolClient) Get(_ context.Context, _ client.ObjectKey, _ client.Object, opts ...client.GetOption) error {
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
		expectAny   []*hivev1.ClusterPool
		expectErr   error
	}{
		{
			description: "returns error on empty pool list",
			expectErr:   errors.New("failed to find a cluster pool providing the cluster: map[architecture: cloud: owner: product: version:]"),
		},
		{
			description: "returns the cluster when there is just one",
			pools:       []hivev1.ClusterPool{{ObjectMeta: v1.ObjectMeta{Name: "i-have-six-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 6}}},
			expectAny:   []*hivev1.ClusterPool{{ObjectMeta: v1.ObjectMeta{Name: "i-have-six-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 6}}},
		},
		{
			// When pools are tied, any of them is a valid selection
			description: "select one when there are many depleted",
			pools: []hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "me-neither"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
			},
			expectAny: []*hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "me-neither"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
			},
		},
		{
			// When there are better pools, one of the better ones should be selected
			description: "select one from the better two",
			pools: []hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "me-neither"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-one"}, Status: hivev1.ClusterPoolStatus{Ready: 1}},
				{ObjectMeta: v1.ObjectMeta{Name: "me-too"}, Status: hivev1.ClusterPoolStatus{Ready: 1}},
			},
			expectAny: []*hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-one"}, Status: hivev1.ClusterPoolStatus{Ready: 1}},
				{ObjectMeta: v1.ObjectMeta{Name: "me-too"}, Status: hivev1.ClusterPoolStatus{Ready: 1}},
			},
		},
		{
			description: "select the cluster with most ready clusters",
			pools: []hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-3-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 3}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-6-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 6}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-5-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 5}},
			},
			expectAny: []*hivev1.ClusterPool{{ObjectMeta: v1.ObjectMeta{Name: "i-have-6-clusters"}, Status: hivev1.ClusterPoolStatus{Ready: 6}}},
		},
		{
			description: "select the clusters with larger size when ready are equal",
			pools: []hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-3"}, Spec: hivev1.ClusterPoolSpec{Size: 3}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-4"}, Spec: hivev1.ClusterPoolSpec{Size: 4}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
			},
			expectAny: []*hivev1.ClusterPool{{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-4"}, Spec: hivev1.ClusterPoolSpec{Size: 4}, Status: hivev1.ClusterPoolStatus{Ready: 0}}},
		},
		{
			description: "select the clusters with larger maxsize when ready and size are equal",
			pools: []hivev1.ClusterPool{
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-4max"}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(4)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-3max"}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(3)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
			},
			expectAny: []*hivev1.ClusterPool{{ObjectMeta: v1.ObjectMeta{Name: "i-have-no-clusters-of-4max"}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(4)}, Status: hivev1.ClusterPoolStatus{Ready: 0}}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			got, err := ClusterPoolFromClaim(context.TODO(), &api.ClusterClaim{}, fakePoolClient{returns: tc.pools})
			if tc.expectErr != nil {
				if diff := cmp.Diff(tc.expectErr, err, testhelper.EquateErrorMessage); diff != "" {
					t.Errorf("error differs from expectAny:\n%s", diff)
					return
				} else {
					return // Expected
				}
			}

			// Check if the result matches any of the expectAny results
			if len(tc.expectAny) == 0 {
				t.Errorf("Test case must specify at least one expectAny result")
				return
			}

			found := false
			for _, expectedResult := range tc.expectAny {
				if cmp.Equal(expectedResult, got) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Selected pool %q is not one of the valid results: %v",
					got.Name, func() []string {
						var names []string
						for _, er := range tc.expectAny {
							names = append(names, er.Name)
						}
						return names
					}())
			}
		})
	}
}

func init() {
	if err := hivev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register hivev1 scheme: %v", err))
	}
}

func TestClusterPoolFromClaimWithLabels(t *testing.T) {
	testCases := []struct {
		description string
		pools       []ctrlruntimeclient.Object
		labels      map[string]string
		expected    *hivev1.ClusterPool
		expectErr   error
	}{
		{
			description: "select the clusters to satisfy labels",
			labels:      map[string]string{"a": "b"},
			pools: []ctrlruntimeclient.Object{
				&hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "pool",
					Labels: map[string]string{
						"architecture": "amd64",
						"cloud":        "aws",
						"owner":        "o",
						"product":      "ocp",
						"version":      "v",
					},
				}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(4)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				&hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "pool with label", Labels: map[string]string{"a": "b",
					"architecture": "amd64",
					"cloud":        "aws",
					"owner":        "o",
					"product":      "ocp",
					"version":      "v",
				}}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(3)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
			},
			expected: &hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "pool with label", Labels: map[string]string{"a": "b",
				"architecture": "amd64",
				"cloud":        "aws",
				"owner":        "o",
				"product":      "ocp",
				"version":      "v",
			}}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(3)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
		},
		{
			description: "select the clusters without labels",
			pools: []ctrlruntimeclient.Object{
				&hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "pool",
					Labels: map[string]string{
						"architecture": "amd64",
						"cloud":        "aws",
						"owner":        "o",
						"product":      "ocp",
						"version":      "v",
					},
				}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(4)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
				&hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "pool with label", Labels: map[string]string{"a": "b",
					"architecture": "amd64",
					"cloud":        "aws",
					"owner":        "o",
					"product":      "ocp",
					"version":      "v",
				}}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(3)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
			},
			expected: &hivev1.ClusterPool{ObjectMeta: v1.ObjectMeta{Name: "pool", Labels: map[string]string{
				"architecture": "amd64",
				"cloud":        "aws",
				"owner":        "o",
				"product":      "ocp",
				"version":      "v",
			}}, Spec: hivev1.ClusterPoolSpec{Size: 3, MaxSize: pointer.Int32(4)}, Status: hivev1.ClusterPoolStatus{Ready: 0}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			got, err := ClusterPoolFromClaim(context.TODO(), &api.ClusterClaim{Labels: tc.labels,
				Architecture: api.ReleaseArchitectureAMD64,
				Cloud:        api.CloudAWS,
				Owner:        "o",
				Product:      api.ReleaseProductOCP,
				Version:      "v",
			},
				fakectrlruntimeclient.NewClientBuilder().WithObjects(tc.pools...).Build())
			if diff := cmp.Diff(tc.expectErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%s", diff)
				return
			}
			if diff := cmp.Diff(tc.expected, got, testhelper.RuntimeObjectIgnoreRvTypeMeta); err == nil && diff != "" {
				t.Errorf("Selected pool differs from expected:\n%s", diff)
			}
		})
	}
}
