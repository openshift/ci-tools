# cluster-display

## What
HTTP API server that provides a dashboard view of CI clusters and Hive cluster pools. It queries every configured build cluster plus the Hive cluster for version, console URL, image registry host, cloud provider, product type, and HyperShift supported versions. Results are cached in memory and served as JSON, with optional JSONP callback support.

## How it works -- full flow

### Startup
1. Loads multi-cluster kubeconfigs via Prow's `KubernetesOptions`. Builds a controller-runtime client for each cluster.
2. Identifies the `hive` context (required, fatal if missing) and the `app.ci` context (falls back to in-cluster config if not explicitly present).
3. Removes `InClusterContext` and `DefaultClusterAlias` from the client map to avoid duplicates.
4. Registers Hive v1, Route v1, and Config v1 schemes.
5. Sets up kubeconfig change detection: if the kubeconfig file changes on disk, the server terminates so the Kubelet can restart it and pick up the new credentials.

### Caching
In-memory cache with two independent sections:
- **Cluster data:** refreshed every **1 hour**. On refresh, queries all clusters in parallel.
- **Cluster pool data:** refreshed every **1 minute**. On fetch error, stale cached data is served with a logged error.

### Prow disabled cluster tracking
A `prowClient` wrapper queries Prow for disabled clusters, caching the result for **3 minutes**. On error, requests are rate-limited (10/sec with burst of 5) and the client waits until the limiter allows, to avoid overwhelming Prow. The cache update happens asynchronously in a goroutine so concurrent readers are not blocked.

### Cluster detail collection
For each cluster, the server:
1. Resolves the console Route host via `api.ResolveConsoleHost`.
2. Resolves the image registry host via `api.ResolveImageRegistryHost` (skipped for hive).
3. Reads the `ClusterVersion` resource for the current version (from `status.history[0]`).
4. Reads the `Infrastructure` resource for the cloud platform type.
5. Detects the product: checks for `configure-alertmanager-operator` Service in `openshift-monitoring` -- if present, it is OSD. Otherwise, checks the version string for "okd" (OKD) or defaults to OCP.
6. For the hive cluster specifically, reads the `hypershift/supported-versions` ConfigMap and includes the HyperShift supported versions array.

### Endpoints

| Endpoint | Method | Response |
|---|---|---|
| `/api/health` | GET | `{"ok": true}` |
| `/api/v1/clusters` | GET | JSON `{"data": [...]}` with cluster info maps. Accepts `?skipHive=true`. Disabled clusters are appended with `"error": "disabled cluster in Prow"`. |
| `/api/v1/clusterpools` | GET | JSON `{"data": [...]}` with pool info maps (namespace, name, ready, size, maxSize, imageSet, labels, releaseImage, owner, standby). |

Both data endpoints support `?callback=<name>` for JSONP responses (wraps JSON in `callbackName(json);`).

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--log-level` | `info` | Log verbosity |
| `--port` | `8090` | HTTP listen port |
| `--gracePeriod` | `10s` | Graceful shutdown period |
| Prow kubernetes flags | -- | Multi-cluster kubeconfig paths (`--kubeconfig`, etc.) |

## Key files
- `cmd/cluster-display/main.go` -- all logic: server setup, caching, cluster/pool data collection, HTTP handlers, Prow disabled cluster detection

## Deployment
Runs as a sidecar container inside the `ci-docs` Deployment on app.ci (not a standalone Deployment). Served behind a Route to provide a dashboard for the Test Platform team. Requires kubeconfig access to all build clusters and the hive cluster. Port 8090.
