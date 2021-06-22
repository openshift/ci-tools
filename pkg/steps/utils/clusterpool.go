package utils

import (
	"context"
	"fmt"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func ClusterPoolFromClaim(ctx context.Context, claim *api.ClusterClaim, hiveClient ctrlruntimeclient.WithWatch) (*hivev1.ClusterPool, error) {
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

	l := len(clusterPools.Items)
	if l == 0 {
		return nil, fmt.Errorf("failed to find a cluster pool providing the cluster: %v", listOption)
	} else if l > 1 {
		return nil, fmt.Errorf("found %d cluster pools that could provide the cluster (%v), but there should be only one", len(clusterPools.Items), listOption)
	}

	return &clusterPools.Items[0], nil
}
