# serviceaccount-secret-rotation-trigger

## What
One-shot tool that triggers ServiceAccount (SA) token secret rotation across multiple clusters and namespaces. It forces the Kubernetes control plane to regenerate SA token secrets by adding TTL annotations to existing secrets and clearing SA secret references, which causes the `serviceaccount_secret_refresher` controller to rotate them.

## How it works -- full flow

1. **Load kubeconfigs.** Loads multi-cluster kubeconfigs via Prow's `KubernetesOptions` (defaults to no in-cluster config). Sets QPS to 50 and burst to 500 per cluster for high-throughput operations. Constructs a controller-runtime client for each cluster. If `--dry-run` is true (default), wraps each client in a dry-run decorator.
   - Clusters that fail client construction are skipped with a warning (non-fatal).
   - If no clients are available at all, exits fatally.

2. **Process each namespace on each cluster.** Launches goroutines in parallel across all cluster/namespace combinations using `errgroup`.

3. **Add TTL annotations to SA secrets.** For each namespace:
   - Lists all secrets in the namespace.
   - Filters for secrets that have the `kubernetes.io/service-account.uid` annotation (SA token secrets) but do NOT already have the TTL annotation (`serviaccount-secret-rotator.openshift.io/delete-after`).
   - For each matching secret, patches it to add a TTL annotation set to `now + 24 hours` (RFC3339 format).
   - The `serviceaccount_secret_refresher` controller watches for this annotation and handles the actual rotation.

4. **Clear SA secret references.** For each namespace:
   - Lists all ServiceAccounts.
   - For each SA, patches it to clear both `secrets` and `imagePullSecrets` lists to `nil`.
   - This removes the existing secret references; the `serviceaccount_secret_refresher` controller then handles recreating them.

Within each namespace, the two phases are sequential: TTL annotations are applied first (parallelized internally via `errgroup`), then SA secret references are cleared (also parallelized internally).

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--namespace` | (required) | Namespace to process. Repeatable (must pass at least one). |
| `--dry-run` | `true` | When true, uses dry-run client (no actual changes). |
| Prow kubernetes flags | -- | Multi-cluster kubeconfig flags. Default: no in-cluster config. |

## Key files
- `cmd/serviceaccount-secret-rotation-trigger/main.go` -- all logic: client setup, secret TTL annotation, SA reference clearing
- `pkg/controller/serviceaccount_secret_refresher/` -- the controller that watches for the TTL annotation and performs the actual secret rotation (this tool just triggers it)

## Deployment
Runs as a periodic Prow job ([recent runs](https://prow.ci.openshift.org/?job=periodic-rotate-serviceaccount-secrets)) or via manual invocation. Targets specific namespaces across all configured build clusters. Typically used when SA token secrets need to be rotated (e.g., credential compromise, certificate renewal).
