# payload-testing-ui

## What
Read-only web UI that displays `PullRequestPayloadQualificationRun` (PRPQR) results. When the `payload-testing-prow-plugin` creates payload test runs, it links to this UI so users can track job progress and results. The UI lists all runs in a namespace and provides detailed views for individual runs showing source PRs, release controller configuration, and per-job status with links to ProwJob logs.

Accessible at `https://pr-payload-tests.ci.openshift.org/runs`.

## How it works -- full flow

### Runs list (`/runs/`)
1. Query the Kubernetes API for all `PullRequestPayloadQualificationRun` objects in the configured namespace.
2. Render an HTML table showing:
   - Run name (linked to detail view)
   - Source repositories (linked to GitHub)
   - Pull requests with number, title, and author (linked to GitHub)

### Run detail (`/runs/<namespace>/<name>`)
1. Fetch the specific PRPQR by namespace/name from the Kubernetes API.
2. Render a detail page showing:
   - **Sources**: for each PR under test -- repository link, PR link with title and author, head SHA, base ref and SHA
   - **Release controller configuration**: OCP version, release type, specifier, with links to the release status page and the configuration JSON in openshift/release
   - **Jobs**: each job's test name with color-coded status (green for success, red for failure, yellow for aborted). If a ProwJob URL exists, the job name links to the logs
   - **Status conditions**: timestamps, types, reasons, and messages from the PRPQR status

### Status mapping
Job names in the detail view are derived from `ReleaseJobSpec.JobName(PeriodicPrefix)`. The status is matched by comparing job names, with aggregator jobs having their `aggregator-` prefix stripped for matching.

Color classes:
- `text-success` -- SuccessState
- `text-danger` -- FailureState
- `text-warning` -- AbortedState

### Static assets
Static files (CSS, JS) are served from an embedded filesystem (`pkg/html/StaticFS`) at the `/static/` URL path.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--log-level` | `info` | Log verbosity |
| `--port` | `8080` | HTTP server port |
| `--namespace` | (required) | Kubernetes namespace where PRPQR objects live |

Standard Prow instrumentation flags (`--health-port`) are also supported.

## Key files
- `cmd/payload-testing-ui/main.go` -- entry point, flag parsing, kubeconfig loading, HTTP route setup
- `cmd/payload-testing-ui/server.go` -- server implementation: list and detail handlers, HTML templates with Go template functions for PR/repo/author/SHA links

## Deployment
Long-lived Deployment on app.ci. Runs in-cluster with a kubeconfig that has read access to PRPQR objects. Serves HTTP on port 8000 (the `--port` flag defaults to 8080, but the deployment overrides it to 8000), with health probes on port 8081.

Health check endpoint: `/readyz`

## Related
- `cmd/payload-testing-prow-plugin` -- creates PRPQR objects and links to this UI in PR comments
- `cmd/job-trigger-controller-manager` -- reconciles PRPQRs into ProwJobs (populates the status that this UI displays)

## Testing

The server only requires a read-only `kubeconfig` targeting a cluster where the
`CustomResource` objects are configured.  Only `list`, `get`, and `watch`
permissions are required (the UI is entirely read-only).  The production DPTP
deployment lives in [`app.ci`][deployment] and uses a service account with only
those permissions.

If no changes to the CRD are necessary, the easiest local setup is to create a
`kubeconfig` for that same service account targeting the same cluster:

```console
$ cat > kubeconfig.yaml <<EOF
apiVersion: v1
kind: Config
current-context: app.ci
clusters:
- cluster:
    server: https://api.ci.l2s4.p1.openshiftapps.com:6443
  name: api-ci-l2s4-p1-openshiftapps-com:6443
contexts:
- context:
    cluster: api-ci-l2s4-p1-openshiftapps-com:6443
    user: payload-testing-ui/api-ci-l2s4-p1-openshiftapps-com:6443
  name: app.ci
users:
- name: payload-testing-ui/api-ci-l2s4-p1-openshiftapps-com:6443
  user:
    token: $(oc \
        --context app.ci --as system:admin --namespace ci \
        create token --duration 720h payload-testing-ui)
EOF
```

The server uses the standard loading method (`KUBECONFIG` followed by in-cluster
credentials), so to start the server from a locally-built executable using these
credentials, do:

```console
$ KUBECONFIG=kubeconfig.yaml payload-testing-ui --port 8000
```

[deployment]: https://github.com/openshift/release/tree/main/clusters/app.ci/payload-testing-ui
