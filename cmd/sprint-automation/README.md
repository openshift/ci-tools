# sprint-automation

## What
Daily automated digest tool for the DPTP (Developer Productivity Test Platform) team. It queries PagerDuty for on-call assignments, posts team digests to Slack, manages Slack user group membership for rotating roles, handles Jira intake ticket assignment, and optionally notifies about build cluster upgrades. Designed to run as a daily CronJob.

## How it works -- full flow

### On every run (daily)

#### 1. Resolve on-call users from PagerDuty
Queries PagerDuty for three rotating roles using schedule names:
- `@dptp-triage Primary` (schedule query: "DPTP Primary On-Call")
- `@dptp-helpdesk` (schedule query: "DPTP Help Desk")
- `@dptp-intake` (schedule query: "DPTP Intake")

For each schedule, it queries who is on-call between 8:00-21:00 UTC. If multiple users are returned (indicating an override), it resolves the override user. Each PagerDuty user is then mapped to their Slack ID via email lookup.

#### 2. Post team digest to Slack
Posts a message to the `team-dp-testplatform` private channel containing:
- Today's rotating positions (triage, helpdesk, intake) with Slack user mentions
- Links to role manuals and team definition docs
- Cards awaiting acceptance: Jira issues in the DPTP project with status "Review", grouped by assignee

#### 3. Ensure Slack user group membership
Updates the `@dptp-triage` and `@dptp-helpdesk` Slack user groups to contain exactly the on-call user for that role.

#### 4. Assign and send intake digest
- Queries Jira for unassigned DPTP issues created in the last 30 days with status "To Do" (excluding sub-tasks and issues labeled `ready` or `no-intake`)
- Auto-assigns all matching issues to the current intake person (looked up via their email in Jira)
- Sends a DM to the intake person listing the issues they need to review

### On Monday runs only (`--week-start`)

#### 5. Send next week's role digest
Queries PagerDuty for on-call assignments one week from now. DMs each user about their upcoming roles so they can prepare.

#### 6. Notify triage of handover
DMs the current triage engineer a link to the Triage Handover Document with a reminder to review ongoing incidents.

### Build cluster upgrade notification (`--enable-build02-upgrade-notification`)
Compares the OCP version of build01 and build02 clusters:
- If build01's version has been stable (Z-stream: 1 day soak; Y-stream: 7 day soak) and is newer than build02
- Posts a notification to `alerts-testplatform-build-farms` channel with the `oc adm upgrade` command

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--log-level` | `info` | Log output level |
| `--slack-token-path` | (required) | Path to the Slack bot token file |
| `--week-start` | `false` | Enable Monday-only activities (next week roles, triage handover) |
| `--enable-build02-upgrade-notification` | `false` | Check if build02 needs upgrading to match build01 |
| `--jira-*` | (various) | Jira connection options |
| `--pagerduty-*` | (various) | PagerDuty API options |
| `--kubeconfig` | (various) | Kubeconfig for build01 and build02 clusters |

## Key files
- `cmd/sprint-automation/main.go` -- entry point, PagerDuty queries, Slack digest posting, user group management, Jira intake, cluster version comparison
- `cmd/sprint-automation/jira_search_v3.go` -- paginated Jira JQL search using v3 REST API

## Deployment
Runs as a periodic CronJob on app.ci. Two instances: one daily (all activities), one on Mondays only (with `--week-start`).

Requires kubeconfigs for build01 and build02 clusters (for upgrade notification), PagerDuty API credentials, Jira credentials, and a Slack bot token.
## Local testing
You can test out `sprint-automation` utilizing the `dptp-robot-testing` and the `hack/local-sprint-automation.sh` script:
- Make sure to join the `dptp-robot-testing` slack space.
- Run the `hack/local-sprint-automation.sh` script like so: `RELEASE_REPO_DIR=<path to release repo dir> bash hack/local-sprint-automation.sh`
- Now you can go into `dptp-robot-testing` and watch the output of your local `sprint-automation` run.
