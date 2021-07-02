package utils

import (
	"context"
	"fmt"
	"math"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func ClusterPoolFromClaim(ctx context.Context, claim *api.ClusterClaim, hiveClient ctrlruntimeclient.Reader) (*hivev1.ClusterPool, error) {
	clusterPools := &hivev1.ClusterPoolList{}
	listOption := ctrlruntimeclient.MatchingLabels{
		"product":      string(claim.Product),
		"version":      claim.Version,
		"architecture": string(claim.Architecture),
		"cloud":        string(claim.Cloud),
		"owner":        claim.Owner,
	}
	if err := hiveClient.List(ctx, clusterPools, listOption); err != nil {
		return nil, fmt.Errorf("failed to list cluster pools with list option %v: %w", listOption, err)
	}

	pools := clusterPools.Items
	if len(pools) == 0 {
		return nil, fmt.Errorf("failed to find a cluster pool providing the cluster: %v", listOption)
	}

	better := func(one, two *hivev1.ClusterPool) *hivev1.ClusterPool {
		oneMaxSize := math.MaxInt32
		twoMaxSize := math.MaxInt32
		if one.Spec.MaxSize != nil {
			oneMaxSize = int(*one.Spec.MaxSize)
		}
		if two.Spec.MaxSize != nil {
			twoMaxSize = int(*two.Spec.MaxSize)
		}
		switch {
		case one.Status.Ready > two.Status.Ready:
			return one
		case one.Status.Ready < two.Status.Ready:
			return two
		case one.Spec.Size > two.Spec.Size:
			return one
		case one.Spec.Size < two.Spec.Size:
			return two
		case oneMaxSize > twoMaxSize:
			return one
		case oneMaxSize < twoMaxSize:
			return two
		}

		return one
	}

	best := &pools[0]
	for i := range pools[1:] {
		best = better(best, &pools[i+1])
	}
	return best, nil
}
