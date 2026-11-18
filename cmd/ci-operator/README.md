# Secret collection blocking

The source of truth is
`openshift/release/core-services/ci-secret-bootstrap/gsm-config.yaml`.
Its optional top-level map blocks job consumption without changing collection
ownership, GSM contents, IAM reconciliation, or secret synchronization:

```yaml
blocked_collections:
  temporarily-unavailable:
    reason: "Credential rotation is in progress. Contact the collection owner before retrying."
  needs-investigation: {}
```

An absent, null, or empty map disables blocking. Entries with a null value,
empty object, omitted reason, or empty/null reason use the default message:
`The secret collection #name is blocked. Contact <#CBN38N3MW> to get more details.`
`#name` is replaced with the collection name, without a leading `#`.
Custom reasons are printed verbatim, prefixed by the selected test and collection
names, and should describe the problem and the next action. Removing an entry
unblocks it.

ci-operator reads this metadata and bundle definitions together through
`api.GSMConfig`, using the existing `--gsm-config=/etc/gsm-config/gsm-config.yaml`
and `gsm-config` ConfigMap mount. A supplied path is loaded even when CSI is
disabled; missing, unreadable, or malformed supplied configuration fails before
execution rather than silently bypassing blocking. Legacy jobs with CSI disabled
and no `--gsm-config` remain unchanged and do not load a policy. CSI-enabled jobs
still require the existing GSM and project configurations. There is no separate
policy flag, ownership-file generation, additional ConfigMap, or projection.

The metadata-only preflight runs after selecting and sorting the execution
graph, before namespace initialization or running any build/test steps.
Only test targets in that graph are checked, including their pre/test/post
credentials, direct fields, collection/group discovery references, and bundles.
Bundle sources are inspected before `sync_to_cluster` conversion. Resolved bundle
components and DockerConfig collections are included. No GSM field discovery is
performed by preflight. Input resolution, read-only cluster discovery, and GSM
client initialization for CSI-enabled jobs still precede it.

Legacy Kubernetes Secret references and literal cluster profiles are checked
when their namespace/name matches a synced bundle target (`ci` for profiles).
Matching is conservative across clusters: if bundles share a name and target
namespace, any matching blocked collection rejects the reference. This can
reject a job whose executing cluster would use an allowed collection. Preflight
does not infer the executing cluster, and a blocked candidate is never ignored
because another matching candidate is allowed. Different namespaces remain
separate.
Arbitrary Kubernetes Secrets, dynamic profile overrides from runtime parameters,
templates, and non-test infrastructure credentials cannot be attributed to GSM
collections by this metadata check. The policy does not inspect secret values.

## Rollout

Deliver the shared `api.GSMConfig` schema separately first, without ci-operator
enforcement or blocking entries. After it merges, roll out the schema to
ci-operator, ci-secret-bootstrap, and ci-secret-generator. Check startup and
configuration reload errors, bundle resolution, and normal secret reconciliation
before proceeding. Observation and approval are explicit rollout gates, not a
fixed waiting period or an automatic merge.

Deliver ci-operator enforcement only after the schema rollout is accepted. Use
the existing GSM configuration distribution; no ownership ConfigMap rollout or
job-volume regeneration is needed. Before activating entries, verify a dependent
selected target is rejected before namespace creation and an unrelated target
still runs. Account for conservative cross-cluster rejection when choosing
entries.
