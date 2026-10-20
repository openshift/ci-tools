# prow-job-dispatcher

As designed in [[DPTP-1152] Choose a cluster for prow jobs](https://docs.google.com/document/d/1aiuZ70jtvZiQBo2P8NgacRj0GmqUH6DRxE4KFFph1RM/edit) this tool chooses a cluster in the CI build farm for Prow jobs.

* It starts off by figuring out how many runs of each Prow jobs we had in the last seven days by querying the Prometheus instance in Prow-monitoring stack.
* It groups all jobs from a Prow job file together and will always try to put all of them on the same cluster.
* If a job has config stating it must be on a specific cluster, that will always be respected. This could lead to a job with tests on different clusters. We should not have many of those cases.
* If all e2e jobs in a group run on the same cloud provider, it will only consider clusters on that cloud provider, if any. Otherwise, all build clusters are considered.
* It will then choose the cluster with the least number of jobs, based on the Prometheus metrics and the already dispatched jobs.

The choices of cluster are stored in the following stanza of [the config file](https://github.com/openshift/release/blob/main/core-services/sanitize-prow-jobs/_config.yaml) of [`sanitize-prow-jobs`](../sanitize-prow-jobs).

```
buildFarm:
  aws: 
    build01:
      jobs:
      - job-name-1
  gcp: 
    build02:
      jobs:
      - job-name-1

```

The tool `sanitize-prow-jobs` will then use the stored information to generate the `cluster` field of the Prow jobs.

We can use [run-prow-job-dispatcher.sh](../../hack/run-prow-job-dispatcher.sh) to build and run the tool locally.

## Observation API

Enable the separate TLS listener with `--observation-api-listen-address=:8443`.
Observation is disabled when this flag is unset. The scheduling listener on port
8080 does not expose observation routes; its existing metrics and control API
behavior is unchanged.

The dashboard backend uses a pod-bound projected service account token with
audience `prowjob-dispatcher-observation`. Every observation request is checked
through Kubernetes TokenReview, including the returned audience and exact
`system:serviceaccount:ci:dispatcher-dashboard` identity. Set
`--observation-api-service-account=namespace/name` to select another identity.
The dispatcher service account needs only `create` on
`tokenreviews.authentication.k8s.io` for this validation. No observation shared
secret is created or mounted. Missing, invalid, expired, revoked and wrong-audience
credentials fail closed; other authenticated identities receive HTTP 403, and
TokenReview failures return HTTP 503 without credential details.

Mount an OpenShift service serving certificate at
`/etc/dispatcher-observation-tls/tls.crt` and `tls.key`, or configure
`--observation-api-tls-cert-file` and `--observation-api-tls-key-file`.
The pair is reloaded at each TLS handshake for certificate rotation. Clients must
validate the service DNS name and injected service CA. Expose this listener only
through an internal Service and restrict its port with NetworkPolicy to the
dashboard backend. Do not publish an observation Route. The dashboard's browser
OAuth and Test Platform administrator authorization remain at its frontend.

The TLS listener accepts only authenticated GET requests to `/observe/v1/` and
`/metrics`. The latter exposes the existing Prometheus metrics for dashboard
health checks using the same TLS origin and service account authentication.

- `/state` reports readiness, policy identity, inventory and effective job demand
  per cluster. `inventoryKnown: false` identifies targets outside the inventory;
  capacity is the configured scheduling weight, not measured utilization.
- `/jobs?limit=100` lists baseline and effective decisions, with optional `name`
  substring, exact `group`, and effective `cluster` filters. Limits are 1 to 500.
  Continue with `after=<nextAfter>&generation=<policy.generation>` and the same
  filters. HTTP 409 means the policy changed; restart pagination.
  Each page is live at its `observedAt`: expiry can change the effective cluster
  without changing generation. Restart a filtered traversal after an expiry boundary.
- `/overrides` reports durable records and audit history separately from snapshot
  inclusion (`published`) and effective assignment counts (`servingJobs`). Listing
  failures or disabled runtime controls return HTTP 503.

Responses use one serving snapshot and observation time, apply expiry fallback,
and do not schedule jobs or change policy. Jobs return HTTP 503 before readiness;
state reports `ready: false`. `generatedAt` is the last policy change, not a heartbeat.
The reconcile success timestamp and error counter are exported as
`prowjob_dispatcher_last_successful_reconcile_timestamp_seconds` and
`prowjob_dispatcher_reconcile_errors_total`. The
`prowjob_dispatcher_last_baseline_update_timestamp_seconds` metric records input
delivery to the control plane, including unchanged inputs; it does not measure
Git synchronization or demand-query freshness. These metrics start at zero.
