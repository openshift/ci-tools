# bugzilla-backporter

## What
**DEPRECATED.** A web UI server for cloning (backporting) Bugzilla bugs to different target releases. Users could look up a bug by ID, see its current clones, and create new clones targeting other OCP releases. Deprecated because OpenShift moved from Bugzilla to Jira for bug tracking.

## How it works
1. Starts an HTTP server on the configured address.
2. Reads the Prow plugin config (`plugins.yaml`) to extract all known Bugzilla `targetRelease` values for the `openshift` org, sorted for the UI dropdown.
3. Connects to the Bugzilla API using a secret API key, with a caching HTTP transport layer to reduce API calls.
4. Exposes these HTTP endpoints:

| Endpoint | What it does |
|---|---|
| `/` | Landing page |
| `/clones` | Look up existing clones of a bug |
| `/clones/create` | Create a new clone targeting a different release |
| `/bug` | Return bug details as JSON |
| `/help` | Help/debug endpoint |

5. Exports Prometheus metrics under `ci-operator-bugzilla-backporter` (request duration, response size).

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--log-level` | `info` | Log verbosity |
| `--address` | `:8080` | Server listen address |
| `--gracePeriod` | `10s` | Shutdown grace period |
| `--plugin-config` | `/etc/plugins/plugins.yaml` | Path to Prow plugin config for target release discovery |
| `--bugzilla-api-key-path` | (Prow default) | Path to Bugzilla API key secret |
| `--bugzilla-endpoint` | (Prow default) | Bugzilla API URL |

## Key files
- `cmd/bugzilla-backporter/main.go` -- server setup and endpoint wiring
- `pkg/backporter/` -- handler implementations, caching transport, sorting logic

## Deployment
Appears decommissioned — no Deployment manifest exists in the release repo. A stale Ingress entry for `bugs.ci.openshift.org` remains in `clusters/app.ci/cert-manager/prow_ingress.yaml` but the actual Deployment and Service have been removed.
