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

If `--slack-token-path` is set, the controller also publishes an active failure
digest to `--ops-channel-id` every `--slack-report-period`, defaulting to two
hours. The digest reports the total number of active fast-forward failures and
shows the five most recently observed entries with compare links. A failure is
removed from the digest after the matching source-to-target branch forwarding
succeeds or the target is removed from desired state.

## Forwarding configuration

The forwarding config separates default branches from release branches:

```yaml
default_branch:
  configs_promoting_to: "5.0"
  forward:
  - family: release
    targets:
    - "5.0"
    - "5.1"
    ignore:
    - org: Azure
      repo: ARO-HCP
  - family: openshift
    targets:
    - "5.0"
    - "5.1"
    only:
    - org: openshift
      repo: kubecsr

release_branches:
- source: "5.0"
  forward:
  - family: release
    targets:
    - "4.23"
  - family: openshift
    targets:
    - "4.23"
    only:
    - org: openshift
      repo: kubecsr
    ignore:
    - org: Azure
      repo: ARO-HCP
```

`default_branch` selects `main` and `master` ci-operator configurations that
promote to the configured official OCP stream. Each `forward` entry chooses the
target branch family: `release` forwards to `release-TARGET`, and `openshift`
forwards to `openshift-TARGET`.

Each `release_branches` entry maps one configured source release. A `release`
forward block matches `release-SOURCE`; an `openshift` forward block matches
`openshift-SOURCE`. `only` and `ignore` entries are structured and scoped to
their containing forward block. They may match by `org`, `repo`, exact `source`,
exact `target`, or any combination of those selectors. Empty `only` means all
repositories are included. When `only` and `ignore` both match, `ignore` wins.

Legacy `targets` and string `ignore` fields are still accepted for backward
compatibility. In legacy mode, `default_branch.targets` means release-family
targets only, while `release_branches.targets` means both release and openshift
families, matching the original controller behavior.

The file is strictly validated on every reload. Invalid updates leave the last
valid desired state active.
