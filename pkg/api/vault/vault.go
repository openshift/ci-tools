package vault

const (
	SecretSyncTargetNamepaceKey = "secretsync/target-namespace"
	SecretSyncTargetNameKey     = "secretsync/target-name"

	// VaultSourceKey is the key in the resulting kubernetes secret
	// that holds the vault path from which the user secret sync
	// synced.
	VaultSourceKey = "secretsync-vault-source-path"
)
