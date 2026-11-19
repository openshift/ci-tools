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
Aggregators pass the selected private bucket to the
`openshift-release-analysis-prpqr-aggregator` step through `GOOGLE_STORAGE_BUCKET`;
the step must support that input before enabling private aggregation.
