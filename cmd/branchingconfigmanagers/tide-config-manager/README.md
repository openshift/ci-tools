Tool is used to change merge criteria during around branching and release events.

Mandatory parameters:

```
/tide-config-manager --lifecycle-phase branching --prow-config-dir /dir --sharded-prow-config-base-dir /dir --current-release 4.x
```

Lifecycle phase can be: `branching`, `pre-general-availability` or `general-availability`.

## General availability

Use `--lifecycle-phase general-availability --current-release 5.0` for the
post-5.0 GA state (past: 4.22, current development release: 5.1). GA can run
directly after branching or after pre-GA, and repeated runs are idempotent.

For each repository, GA advances dedicated `release-5.0` / `openshift-5.0`
queries requiring `backport-risk-assessed` or `staff-eng-approved` to the
corresponding 5.1 branches, adding `staff-eng-approved` and retaining backport
risk and all custom requirements. This follows the 4.21 GA switch (release PR
#74373); it does not restore `cherry-pick-approved`, retired in PR #66732.

The GA branches join an existing past-release query only when its other labels
and selectors match (ignoring the two lifecycle labels). The past query keeps
its policy, including the public/private distinction. If none matches, a
GA-only query keeps the original policy without `staff-eng-approved`. Existing
matching future queries are reused rather than creating weaker duplicate OR
paths. Ordinary complement queries (nonempty excluded branches, no included
branches and no author filter) for transitioned repositories exclude the GA and
all final future-query aliases, preserving existing exclusions and selectors.
This also applies when their original exclusions name only main or older releases.
Author-filtered queries remain explicit exceptions and are unchanged, including
bot-specific complements and queries without branch filters.

Org-scoped queries, main/master and noFF queries, mixed-version staff exceptions,
and already transitioned policies are not automatically shifted. Unrecognized
policies need manual review. The optional `--excluded-repos-config` YAML keys
`ExcludedAllowList` and `NoXYAllowList` contain repository names (`org/repo`);
both opt repositories out of automatic GA changes. Omit the flag when no
repository exemptions are intended.
