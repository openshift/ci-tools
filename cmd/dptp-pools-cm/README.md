# dptp-pools-cm

## What
Controller-manager that runs on the Hive cluster to manage Hive cluster pool infrastructure. It hosts two controllers:

1. **cluster_pools_pull_secret_provider** -- keeps pull secrets in sync across cluster pool namespaces by copying a source pull secret into every namespace that has a ClusterPool referencing it.
2. **hypershift_namespace_reconciler** -- ensures HyperShift hosted control plane namespaces are excluded from monitoring to prevent alert noise from transient control plane namespaces.

## How it works -- full flow

### cluster_pools_pull_secret_provider (default: enabled)

**Watches:** `ClusterPool` resources and `Secret` resources.

**Trigger conditions:**
- A `ClusterPool` is created or updated in any namespace except the source pull secret namespace.
- The source pull secret itself changes in the source namespace -- triggers reconciliation for all pools across all namespaces.
- A pull secret copy in a target namespace changes -- triggers reconciliation for pools in that specific namespace.

**Reconciliation:**
1. Gets the `ClusterPool` from the reconcile request.
2. If the pool is deleted, does nothing (returns nil).
3. Checks if the pool has `spec.pullSecretRef` set. Skips if nil.
4. Checks if `spec.pullSecretRef.name` matches the configured source pull secret name. Skips if it does not.
5. Reads the source pull secret from the configured source namespace.
6. Constructs a copy with the same data, labels, and annotations but targeted at the pool's namespace.
7. Adds the label `dptp.openshift.io/requester: cluster_pools_pull_secret_provider` to the copy.
8. Upserts the secret using `util.UpsertImmutableSecret` (create-or-replace for immutable secrets).

### hypershift_namespace_reconciler (default: not enabled)

**Watches:** `Namespace` resources.

**Predicate:** Only namespaces with the label `hypershift.openshift.io/hosted-control-plane` (value empty or `"true"`) are processed. Delete events are ignored.

**Reconciliation:**
1. Calls `controllerutil.EnsureNamespaceNotMonitored` on the namespace to remove any monitoring configuration, preventing alert noise from transient HyperShift control plane namespaces.

### Controller manager setup
- Uses in-cluster config (runs on the Hive cluster itself).
- Leader election is enabled in the `ci` namespace with lock ID `dptp-pools-cm<suffix>`.
- Registers the Hive v1 scheme for ClusterPool watches.
- Supports dry-run mode (enabled by default).

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--leader-election-namespace` | `ci` | Namespace for leader election |
| `--leader-election-suffix` | `""` | Suffix for leader election lock name (useful for local testing, requires `--dry-run`) |
| `--enable-controller` | `cluster_pools_pull_secret_provider` | Which controllers to enable. Repeatable. Valid values: `cluster_pools_pull_secret_provider`, `hypershift_namespace_reconciler` |
| `--poolsPullSecretProviderOptions.sourcePullSecretNamespace` | `ci-cluster-pool` | Namespace containing the source pull secret |
| `--poolsPullSecretProviderOptions.sourcePullSecretName` | `pull-secret` | Name of the source pull secret |
| `--dry-run` | `true` | Dry-run mode for the controller manager |

## Key files
- `cmd/dptp-pools-cm/main.go` -- entry point, flag parsing, controller manager setup
- `pkg/controller/cluster_pools_pull_secret_provider/cluster_pools_pull_secret_provider.go` -- pull secret sync controller: reconciler, watch handlers
- `pkg/controller/hypershift_namespace_reconciler/hypershift_namespace_reconciler.go` -- HyperShift namespace controller: reconciler, label predicate

## Deployment
Long-lived Deployment on the Hive cluster (hosted-mgmt), namespace `ci`. Uses leader election so multiple replicas can run for HA.
