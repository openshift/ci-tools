# Payload testing plugin

The plugin checks whether a pull request's base branch contributes to official
release images before creating payload qualification runs. For `openshift-priv`
repositories, this eligibility check uses the public counterpart's configuration.
Counterparts are resolved from the public configuration inventory using the
private config mirror's repository naming rules. Missing or ambiguous counterparts
and disabled public promotion are rejected.

This lookup does not change the repositories, pull requests, commit SHAs, runtime
configuration, or authorization used by the run. Private runs remain excluded from
the public payload-testing UI.

Private payload jobs, including mixed-repository runs and aggregators, require
`openshift-priv` Prow decoration defaults with a distinct artifact bucket and
credential reference. Missing private storage configuration fails closed.
Private buckets must be separate from all configured public decoration scopes
across configured clusters, including public organization and repository overrides.
Credential references must be separate on each job's cluster; matching secret
names on unrelated clusters do not imply a shared identity.
Bucket isolation compares parsed storage provider and bucket identities, so
optional `gs://` schemes and different object prefixes do not isolate a bucket.
Private aggregation batches with different worker and aggregator buckets are
rejected before any jobs in that batch are created.
Private aggregation accepts bare or `gs://` GCS bucket names, passes the bare name
to its reader, and rejects prefixes in either the bucket or `path_prefix`, local-only
uploads (`local_output_dir`), and other providers that the reader does not support.
Public jobs retain their existing storage configuration behavior.
Aggregators pass the selected private bucket to the
`openshift-release-analysis-prpqr-aggregator` step through `GOOGLE_STORAGE_BUCKET`;
the step must support that input before enabling private aggregation.
