package vault

import (
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	SecretSyncTargetNamepaceKey = "secretsync/target-namespace"
	SecretSyncTargetNameKey     = "secretsync/target-name"
	SecretSyncTargetClusterKey  = "secretsync/target-clusters"

	// VaultSourceKey is the key in the resulting kubernetes secret
	// that holds the vault path from which the user secret sync
	// synced.
	VaultSourceKey = "secretsync-vault-source-path"
)

// TargetsCluster determines if the given cluster is targeted by the given user secret
func TargetsCluster(clusterName string, data map[string]string) bool {
	return data["secretsync/target-clusters"] == "" || sets.New[string](strings.Split(data["secretsync/target-clusters"], ",")...).Has(clusterName)
}
