# Secret collection blocking

The source of truth is `openshift/release/core-services/sync-rover-groups/_config.yaml`.
An optional top-level map blocks job consumption without changing collection
ownership, GSM contents, IAM reconciliation, or secret synchronization:

```yaml
blocked_collections:
  temporarily-unavailable:
    reason: "Credential rotation is in progress. Contact the collection owner before retrying."
  needs-investigation: {}
```

An absent, null, or empty map disables blocking. Map entries with a null value,
empty object, omitted reason, or empty reason use the default message:
`The secret collection #name is blocked. Contact <#CBN38N3MW> to get more details.`
`#name` is replaced with the collection name, without a leading `#`.
Custom reasons are printed verbatim, prefixed by the selected test and collection
names, and should describe the problem and the next action. Removing an entry
unblocks it.

ci-operator reads this file from `/etc/gsm-config/ownership.yaml` by default.
The missing default file is allowed for backward compatibility, including when
that same default path is explicitly passed to `--secret-collection-policy`.
A different path supplied to this flag must exist and parse correctly.
An empty flag disables loading. A malformed existing file fails closed.
Generated GSM job volumes project the existing `gsm-config` ConfigMap together
with the optional `ownership.yaml` key from `gsm-proxy-ownership`. Jobs generated
before this volume change need regeneration to receive policy metadata. No job
fetches configuration from GitHub.

The pure metadata preflight runs after selecting and sorting the execution
graph, before namespace initialization or running any build/test steps.
Only test targets in that graph are checked, including their pre/test/post
credentials, direct fields, collection/group discovery references, and bundles.
Bundle sources are inspected before `sync_to_cluster` conversion. Resolved bundle
components and DockerConfig collections are included. No GSM field discovery is
performed by preflight. Input resolution, read-only cluster discovery, and GSM
client initialization for CSI-enabled jobs still precede it.

Legacy Kubernetes Secret references and literal cluster profiles are checked
when their namespace/name matches a synced bundle target (`ci` for profiles).
Arbitrary Kubernetes Secrets, dynamic profile overrides from runtime parameters,
templates, and non-test infrastructure credentials cannot be attributed to GSM
collections by this metadata check. Jobs without the ConfigMap mount retain
their previous behavior; for opted-out legacy jobs, mount the same ConfigMap
and supply the policy path (bundle metadata must also be mounted to attribute
Kubernetes Secrets). The policy does not inspect secret values.

## Rollout order

1. Roll out ci-tools consumers that parse the ownership file, including
   sync-rover-groups and GSM ownership/IAM reconcilers, with the new optional
   schema before adding any nonempty `blocked_collections` map.
2. Roll out ci-operator and prowgen with this change, then distribute `_config.yaml`
   as `ownership.yaml` in the existing `gsm-proxy-ownership` ConfigMap throughout
   `build_farm_ci` using config-updater. Bootstrap/resync the expanded mapping;
   do not assume changing the mapping alone populates every new target. The
   existing `core-ci/ci` target remains included. Regenerate jobs with `make jobs` in release
   to pick up the optional projection alongside their existing GSM configuration.
3. Verify the ConfigMap key and the consumer image versions, then add an entry
   with a descriptive reason. Verify a dependent selected target is rejected
   before namespace creation, and an unrelated target still runs.
