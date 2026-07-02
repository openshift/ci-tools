# pod-scaler

## What
Three-mode resource optimization system: a producer queries Prometheus for historical CPU and memory usage, an admission webhook mutates pod resource requests based on 80th percentile of historical data, and a UI serves a dashboard of collected metrics. Prevents both over-provisioning (wasted cluster capacity) and under-provisioning (OOMKills and throttling).

## How it works

### Producer mode (`--mode producer`)
Queries Prometheus on every build cluster every 2 hours (or once with `--produce-once`). Three metric prefixes, each with CPU and memory:

| Prefix | Filter | Labels collected |
|---|---|---|
| `prowjobs` | `label_created_by_prow="true"` | context, org, repo, base_ref, job, type |
| `pods` | `label_created_by_ci="true", step=""` | org, repo, branch, variant, target, build, release, app |
| `steps` | `label_created_by_ci="true", step!=""` | org, repo, branch, variant, target, step |

CPU query: `rate(container_cpu_usage_seconds_total{container!="POD",container!=""}[3m])`
Memory query: `container_memory_working_set_bytes{container!="POD",container!=""}`

Data stored as Circonus log-linear histograms (no lookup table, compact) in GCS cache (or local dir). Pruning: max 25 entries per metadata key, entries older than 90 days removed.

### Admission webhook mode (`--mode consumer.admission`)
Registered on `/pods` endpoint. For each pod admission:

1. Check annotation `ci-workload-autoscaler.openshift.io/scale` (default: true)
2. Handle Build pods: backfill labels from Build object
3. Handle rehearsal pods: extract actual config from CONFIG_SPEC
4. Determine if measured: random `--percentage-measured` chance. Measured pods get label `pod-scaler.openshift.io/measured=true` and anti-affinity against unmeasured pods
5. Calculate resources via `mutatePodResources()`:
   - Query both measured and unmeasured historical data
   - Take **maximum** of both
   - 80th percentile of merged histogram
   - Apply **120% multiplier**
   - Apply **never-reduce rule**: `max(determined, configured)`
   - Cap: `--cpu-cap` (default 10 cores), `--memory-cap` (default 20Gi)
   - Remove all CPU limits (don't throttle)
   - Ensure memory limit >= 200% of request
6. High-priority scheduling: if CPU >= `--cpu-priority-scheduling` (default 8), set priority class `high-priority-nonpreempting`
7. Measured pod CPU increase: `--measured-pod-cpu-increase` (default 50%) additional CPU for measurement accuracy, capped by node allocatable

Node allocatable cache refreshes every 15 minutes, groups nodes by `ci-workload` label.

### UI mode (`--mode consumer.ui`)
Serves web dashboard with org/repo/branch/variant/target/step hierarchy for browsing resource usage data.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--mode` | — | `producer`, `consumer.admission`, or `consumer.ui` |
| `--cache-dir` | — | Local cache directory (dev) |
| `--cache-bucket` | — | GCS bucket for cache |
| `--gcs-credentials-file` | — | GCS service account credentials |
| `--produce-once` | false | Exit after single query cycle |
| `--ignore-latest` | 0 | Duration of latest data to ignore |
| `--port` | — | Webhook port (admission mode) |
| `--serving-cert-dir` | — | TLS cert directory (admission mode) |
| `--cpu-cap` | 10 | Max CPU request in cores |
| `--memory-cap` | 20Gi | Max memory request |
| `--cpu-priority-scheduling` | 8 | CPU threshold for high-priority class |
| `--percentage-measured` | 0 | Percentage of pods to measure (0-100) |
| `--measured-pod-cpu-increase` | 50 | Extra CPU % for measured pods |
| `--ui-port` | — | UI server port |

## Key files
- `cmd/pod-scaler/main.go` — mode routing, flag parsing
- `cmd/pod-scaler/producer.go` — Prometheus queries, cache updates
- `cmd/pod-scaler/admission.go` — webhook handler, resource mutation
- `cmd/pod-scaler/frontend.go` — UI serving
- `pkg/pod-scaler/types.go` — CachedQuery, histogram storage, pruning

## Deployment
Three separate Deployments on app.ci: producer, admission webhook, UI. Producer connects to all build cluster Prometheus instances.
## Producer

The producer reads Prometheus data a couple times daily and updates a static data store in GCS after digesting the metrics. The storage format records time periods for which data fetching failed, to enable eventually consistent data collection in the face of Prometheus errors or network outages.

The overall size of the raw data, however, quickly grows unmanageable. In order to operate efficiently on this dataset we store compressed histograms for each execution trace. This allows us to reduce the data footprint while continuing to allow for dataset merging and aggregation. The <a href="https://www.circonus.com/2018/11/the-problem-with-percentiles-aggregation-brings-aggravation/">Circonus log-linear histogram</a> is used as it's performant, accurate, efficient and open-source.

## Consumers

### Admission

The admission controller is what actually implements the auto-scaling process by mutating all incoming Pods to ensure their containers have appropriate resource requests and limits. In order to provide an estimate of resource usage for containers in a CI job, this server analyzes metrics from previous executions of similar containers. Aggregate statistics are used to provide resource request recommendations by digesting prior metrics. It is assumed that, for a sufficiently similar container, resource usage will not vary much across executions - we expect this to be true for e.g. all executions of unit tests for some branch on a repository. This assumption allows for samples from all executions to be treated as one dataset with a single underlying distribution, so that aggregation can be done on the larger dataset to yield higher-fidelity signal.

The controller will not reduce a resource request or limit that already exists on a container, allowing users to override historical data. As our data is updated at most a couple times daily, this component can download the data once at startup, digest it and hold onto only the bare minimum necessary to serve requests and limits, allowing the server to have a very small footprint.

### UI

The UI is a React/PatternFly based web-app that serves all the historical data in the GCS data store and the resulting suggested resource requests. The UI uses histogram heatmaps to visualize the data, presenting distributions of resource usage for all executions of the CI container that have been indexed. Each vertical slice is a histogram, so a block represents the amount of time (number of samples) that the specific execution of the CI container spent using that much of the resource. Colors represent relative density - the yellower a block, the higher the corresponding bar in the histogram would be. The left-most vertical slice is the aggregate distribution, which contains all the data presented and is used to calculate the resource request recommendation. Note that the histograms used for storing distributions use an adaptive bucket size which varies with the logarithm of the values stored. As a result, the Y axis in the heatmaps are logarithmic, not linear, or smaller buckets would be almost invisible.

## Development

The root `Makefile` contains a number of easy targets to develop the `pod-scaler`. The underlying libraries that make local execution and development possible are used for the end-to-end tests, as well.

For instance, to start a Prometheus server locally, generate fake data for it, ingest the data, and start the UI and admission controllers, run:

```shell
make local-pod-scaler
```

In order to download the production dataset (warning: this is ~300MiB) and serve the UI in a development mode using `npm` (enabling hot-reload, etc), run:

```shell
make local-pod-scaler-ui
```

Run end-to-end tests as normal:

```shell
make local-e2e TESTFLAGS='-run TestAdmission' 
```
