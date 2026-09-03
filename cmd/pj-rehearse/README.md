# pj-rehearse

## What
Prow webhook plugin that rehearses proposed Prow job config changes before they merge into openshift/release. When a PR modifies ci-operator configs, Prow job definitions, or step registry content, pj-rehearse detects which jobs are affected, creates temporary ProwJob copies with the proposed changes, and runs them to verify nothing breaks.

This is a safety net: it catches configuration mistakes before they land in production and start failing real CI jobs.

## User-facing commands
All triggered via PR comments on openshift/release:

| Command | What it does |
|---|---|
| `/pj-rehearse` | Rehearse up to 10 affected jobs |
| `/pj-rehearse more` | Rehearse up to 20 affected jobs |
| `/pj-rehearse max` | Rehearse up to 35 affected jobs |
| `/pj-rehearse {test-name} {other}` | Rehearse specific named jobs only |
| `/pj-rehearse auto-ack` | Rehearse and auto-add `rehearsals-ack` label if all pass |
| `/pj-rehearse list` | Re-post the list of affected jobs |
| `/pj-rehearse skip` | Opt out of rehearsals (adds `rehearsals-ack` label) |
| `/pj-rehearse ack` | Acknowledge results (adds `rehearsals-ack` label) |
| `/pj-rehearse reject` | Remove `rehearsals-ack` label |
| `/pj-rehearse abort` | Abort all active rehearsal jobs for this PR |
| `/pj-rehearse network-access-allowed` | Allow rehearsing jobs with `restrict_network_access: false` (must be org member, cannot be PR author) |

## How it works — full flow

### On PR creation
1. Detect all affected jobs by comparing base branch vs PR branch configs
2. Post a GitHub comment with a markdown table listing affected jobs
3. If more jobs than `--max-limit` (35), upload the full list to GCS and link it
4. If zero jobs affected, auto-add `rehearsals-ack` label (nothing to rehearse)

### On new push to PR
1. Abort all existing rehearsal ProwJobs for this PR
2. Remove `network-access-rehearsals-ok` label
3. Remove `rehearsals-ack` label (unless PR author is in `--sticky-label-authors`)
4. Recompute affected jobs and post updated list

### On `/pj-rehearse` command
1. Block if `needs-ok-to-test` label is present (untrusted PR)
2. Post acknowledgement comment
3. Clone the repo, checkout the PR, rebase onto base branch (up to 4 retries for transient git failures)
4. Run `DetermineAffectedJobs()` to find what changed (see below)
5. Run `SetupJobs()`: inline ci-operator config into job specs, resolve registry references, upload resolved configs to GCS, create temporary ConfigMaps for changed templates/profiles, convert periodics to presubmits
6. If more affected jobs than the limit, intelligently select a subset balancing across source types (so each category of change is represented)
7. Validate the assembled Prow config
8. Create ProwJob resources in the cluster
9. If `auto-ack` mode: wait for all jobs to complete (up to 4h), add `rehearsals-ack` on success

### How it decides which jobs to rehearse
A job is "affected" if any of these changed in the PR:
- **Prow presubmit/periodic config**: spec, agent, cluster, optional/always-run settings
- **CI-operator config**: new config files, changed tests or build settings
- **Step registry**: any step, chain, workflow, or observer that a job references (transitively)

Jobs are **excluded** if:
- `Hidden: true`
- Missing the `pj-rehearse.openshift.io/can-be-rehearsed: "true"` label
- Presubmit has empty `Branches` list
- `restrict_network_access: false` without both `network-access-rehearsals-ok` AND `approved` labels

### Smart subset selection (when over limit)
When there are more affected jobs than the limit, it doesn't just take the first N alphabetically. It:
1. Groups jobs by source type (ChangedPresubmit, ChangedPeriodic, ChangedCiopConfig, ChangedRegistryContent, etc.)
2. Allocates budget evenly: `limit / numSourceTypes` per type
3. Fills remaining slots from underutilized types
This ensures all categories of change get coverage, not just the alphabetically first ones.

## Label workflow
```
PR opened
  ├─ no affected jobs → auto-add rehearsals-ack
  └─ affected jobs found → post list, wait for user
       ├─ /pj-rehearse → run rehearsals (label not added)
       ├─ /pj-rehearse auto-ack → run + add label on all-pass
       ├─ /pj-rehearse skip → add label (opt out)
       ├─ /pj-rehearse ack → add label (manual ack)
       └─ no action → label absent, blocks merge

New push
  ├─ abort active rehearsals
  ├─ remove rehearsals-ack (unless sticky-label-authors)
  ├─ remove network-access-rehearsals-ok
  └─ recompute + repost affected list
```

The `rehearsals-ack` label is a Tide merge requirement. PRs cannot merge without it.

## What rehearsal ProwJobs look like
- Name: `rehearse-{prNumber}-{originalJobName}`
- Labels: `ci.openshift.io/rehearse: {prNumber}`, source type tracking labels
- Context: `ci/rehearse/{repo}/{branch}/{shortName}`
- `Optional: true` — rehearsal failures don't block merge via status checks (only the label workflow does)
- No `reporter_config` — rehearsals never send Slack/email notifications
- All ConfigMap volume mounts are replaced with temporary copies containing the PR's proposed changes

## Key files
- `cmd/pj-rehearse/main.go` — entry point, flag parsing
- `cmd/pj-rehearse/server.go` — webhook handlers, GitHub comment/label logic
- `pkg/rehearse/rehearse.go` — core orchestration: `DetermineAffectedJobs()`, `SetupJobs()`, `RehearseJobs()`
- `pkg/rehearse/jobs.go` — job setup, config inlining, periodic-to-presubmit conversion
- `pkg/rehearse/configmaps.go` — temporary ConfigMap management for changed profiles/templates
- `pkg/diffs/diffs.go` — diff detection between base and PR configs

## Deployment
Long-lived webhook Deployment on app.ci (RBAC/ServiceAccount). Plugin also deployed on core-ci.

Listens for GitHub webhook events: PR `opened`/`synchronize`, issue comments `created`.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--normal-limit` | 10 | Max jobs for `/pj-rehearse` |
| `--more-limit` | 20 | Max jobs for `/pj-rehearse more` |
| `--max-limit` | 35 | Max jobs for `/pj-rehearse max`, also threshold for GCS upload of full list |
| `--gcs-bucket` | `test-platform-results` | Where resolved configs and large job lists are stored |
| `--gcs-credentials-file` | `/etc/gcs/service-account.json` | GCS service account key |
| `--gcs-browser-prefix` | `https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/` | URL prefix for GCS links in PR comments |
| `--prowjob-kubeconfig` | (in-cluster) | Override kubeconfig for the cluster where ProwJobs are created |
| `--no-registry` | false | Disable step registry comparison (skip registry-sourced affected jobs) |
| `--no-templates` | false | Disable template comparison |
| `--sticky-label-authors` | (empty) | Comma-separated PR authors whose `rehearsals-ack` label survives new pushes |
| `--hmac-secret-file` | `/etc/webhook/hmac` | GitHub webhook HMAC secret for signature verification |
| `--dry-run` | true | When true: fake k8s client, prints YAML instead of creating jobs |

## Related
- `cmd/config-change-trigger` — similar concept but for postsubmit jobs, not rehearsals
- GCS path pattern: `pj-rehearse/{org}/{repo}/{prNumber}/{sha}/...`
- Prometheus metric: `pj_rehearse_handlers_in_flight` gauge
