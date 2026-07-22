# dptp-controller-manager

## What
Multi-controller Kubernetes manager handling image promotion, test image distribution across build clusters, service account secret lifecycle, test image import cleanup, and ephemeral cluster provisioning. Connects to multiple clusters simultaneously.

## How it works

### Multi-cluster architecture
- **Main manager**: Created for app.ci cluster with leader election
- **Build cluster managers**: One per build cluster kubeconfig, no leader election, metrics disabled
- **Registry manager**: Special manager for registry cluster (default: app.ci) with 24-hour cache sync period
- All build cluster managers are added as Runnables to the main manager

### Controllers

#### promotionreconciler
Watches ImageStreamTags on the registry cluster. When a promotion target IST exists but its commit is stale (doesn't match current branch HEAD on GitHub), enqueues a ProwJob to re-promote.

- Indexes CI operator configs by promotion target IST
- Subscribes to config changes — when new promotion configs appear, checks if IST exists and creates ProwJob if missing
- Only reconciles tags younger than `--promotionReconcilerOptions.since` (default: 15 days)
- MaxConcurrentReconciles: 100
- Ignore patterns via `--promotionReconcilerOptions.ignore-image-stream` (regex)

#### testimagesdistributor
Distributes ImageStreamTags from the registry cluster to all build clusters. When ci-operator configs reference test input images, this controller ensures those images are available on the cluster where the job runs.

- Creates ImageStreamImport requests on target clusters to sync images
- Creates namespaces and RBAC roles on target clusters as needed
- Blocks imports from forbidden registries (`--testImagesDistributorOptions.forbidden-registry`)
- Can ignore specific clusters (`--testImagesDistributorOptions.ignore-cluster-name`)
- MaxConcurrentReconciles: 1 (conflicts on ImageStream level)

#### serviceaccount_secret_refresher
Manages service account token and pull secret lifecycle across all clusters.

- Watches ServiceAccounts and Secrets
- Checks expiration via TTL annotation (`serviaccount-secret-rotator.openshift.io/delete-after`)
- Default TTL: 30 days
- If `--serviceAccountRefresherOptions.remove-old-secrets`: deletes secrets older than 60 days
- Runs per-cluster (controller added for each build cluster)
- MaxConcurrentReconciles: 20

#### testimagestreamimportcleaner
Cleans up stale TestImageStreamTagImport objects older than 7 days. If younger, requeues for cleanup at expiry time.

- Runs per-cluster
- MaxConcurrentReconciles: 10

#### ephemeral_cluster_provisioner
Provisions ephemeral clusters for testing via ProwJobs.

- Watches EphemeralCluster resources in `ephemeral-cluster` namespace
- Creates ProwJobs from PR metadata stored in annotations
- Generates ci-operator config with cluster claim and CLI image
- Manages finalizer: `ephemeralcluster.ci.openshift.io/dependent-prowjob`
- Polling interval configurable (default: 1m)
- MaxConcurrentReconciles: 1

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--enable-controller` | `promotionreconciler` | Controllers to enable (repeatable) |
| `--ci-operator-config-path` | — | Path to CI operator config |
| `--step-config-path` | — | Path to step registry config |
| `--registry-cluster-name` | `app.ci` | Cluster with CI central registry |
| `--leader-election-namespace` | `ci` | Namespace for leader election |
| `--dry-run` | true | Dry-run mode |
| `--release-repo-git-sync-path` | — | Path to release repo (alternative to config paths) |

## Key files
- `cmd/dptp-controller-manager/main.go` — controller registration, multi-cluster setup
- `pkg/controller/promotionreconciler/reconciler.go` — promotion logic
- `pkg/controller/test-images-distributor/test_images_distributor.go` — image distribution
- `pkg/controller/serviceaccount_secret_refresher/` — SA secret lifecycle
- `pkg/controller/ephemeralcluster/reconciler.go` — ephemeral cluster provisioning

## Deployment
Long-lived Deployment on app.ci, namespace ci. Leader election enabled. RBAC distributed across build clusters.
