# repo-brancher-controller

`repo-brancher-controller` is an event-driven replacement for the periodic
`repo-brancher`. It derives a deduplicated set of source and target branches
from ci-operator configuration, receives GitHub push webhooks, and updates
target Git refs through the GitHub API with `force: false`.

The controller performs an initial reconciliation and a configurable full
resync as protection against missed webhook deliveries. Configuration is
reloaded independently; only repositories whose desired state changed are
enqueued.

Required flags are `--config-dir`, `--forwarding-config`,
`--plugin-config-dir`, and one GitHub authentication method. Configure
authentication with `--github-token-path`, or with both `--github-app-id` and
`--github-app-private-key-path`. The token or GitHub App installation needs
repository contents write permission. The plugin config directory is checked
on every reload so a managed organization cannot silently miss push events.
Configure GitHub push webhooks using the endpoint and HMAC options exposed by
the controller; the endpoint defaults to `/hook` on port 8888 and the HMAC
secret defaults to `/etc/webhook/hmac`.

The webhook listener uses Prow's standard GitHub event server. Health and
readiness are exposed on `--health-port`, and Prometheus metrics on
`--metrics-port`. GitHub rate-limit reset headers pause all workers sharing the
client. Transient failures continue at `--retry-exhausted-delay` after the fast
retry budget is consumed.

## Forwarding configuration

The forwarding config separates default branches from release branches:

```yaml
default_branch:
  configs_promoting_to: "5.0"
  targets:
  - "5.0"
  - "5.1"
  ignore:
  - Azure/ARO-HCP

release_branches:
- source: "5.0"
  targets:
  - "4.23"
  ignore:
  - Azure/ARO-HCP
```

`default_branch` selects `main` and `master` ci-operator configurations that
promote to the configured official OCP stream. Their default branches are
forwarded to every `release-TARGET` branch. Each `release_branches` entry maps
one `release-SOURCE` or `openshift-SOURCE` branch to multiple targets while
preserving the branch prefix. Ignore entries are exact organizations or
`org/repo` names and are scoped to their containing rule.

The file is strictly validated on every reload. Invalid updates leave the last
valid desired state active.
