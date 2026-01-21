---
name: branching-day
description: Execute Branching Day tasks for OpenShift release - transition from Normal Development to Feature Freeze
usage: /branching-day
examples:
  - /branching-day
---

# Branching Day Orchestration Instructions

You are the orchestrator for OpenShift Branching Day. When this command is invoked, you will automatically determine the current release version from the 4-stable stream and guide the user through all the steps required to transition from Normal Development to Feature Freeze.

## Your Role

- **Verify branching readiness**: Check that at least 3 accepted nightlies exist for the target release
- **Fetch target release**: Extract version from periodic-prow-auto-config-brancher configuration
- **Calculate versions**: Current release (from config), Future release (current + 0.01)
- **Use TodoWrite**: Track all steps and mark them as you progress
- **Use AskUserQuestion**: Confirm prerequisites and manual steps
- **Invoke skills**: Call the automation scripts when appropriate
- **Guide the user**: Provide clear instructions for manual steps
- **Handle failures**: When skills fail verification, diagnose and help recover

## Error Handling and Recovery

When a skill fails verification (exits with non-zero code):

1. **STOP the workflow immediately** - Do not proceed to the next step
2. **Analyze the verification output**:
   - Read the verification section carefully
   - Identify which specific check failed (✗ indicators)
   - Understand what was expected vs. what actually happened
3. **Provide clear diagnosis to the user**:
   - Explain what failed in plain language
   - Show the relevant verification output
   - Explain the impact (e.g., "Config files were not updated correctly")
4. **Suggest remediation**:
   - **Option A - Automated fix**: If you can identify the issue, offer to:
     - Manually inspect the files to diagnose the problem
     - Fix the issue (e.g., run sed commands, edit files)
     - Re-run the failed skill to verify the fix
   - **Option B - Manual fix**: If the issue requires user intervention:
     - Provide specific commands to investigate (e.g., `git diff`, `grep`)
     - Explain what the user should look for
     - Once fixed, offer to re-run the skill verification
   - **Option C - Abort**: If the failure is critical or unclear:
     - Suggest rolling back changes (`git reset --hard`)
     - Recommend investigating root cause before retrying
5. **Re-run verification**: After fixing, always re-run the failed skill to confirm success
6. **Update TodoWrite**: Mark the task as still in_progress until verification passes

### Common Failure Scenarios

**Scenario 1: No files were modified**
- **Cause**: Tool didn't run correctly, wrong parameters, or files already up-to-date
- **Action**: Check tool output, verify parameters, inspect git status
- **Recovery**: Fix parameters and re-run

**Scenario 2: Old values still present**
- **Cause**: sed regex didn't match, file format changed
- **Action**: Manually inspect file, adjust sed command
- **Recovery**: Run corrected sed command, re-run verification

**Scenario 3: New values not found**
- **Cause**: Insertion logic failed, wrong location targeted
- **Action**: Check file structure, verify insertion point exists
- **Recovery**: Manually add missing content, re-run verification

**Scenario 4: Partial success**
- **Cause**: Multi-step operation partially completed
- **Action**: Identify what succeeded and what failed
- **Recovery**: Complete only the failed parts, re-run full verification

## What is Branching Day?

**Branching Day** marks a major transition in the OpenShift release cycle:

- **Before Branching Day:** X.Y development happens on `master` branch with relaxed merge criteria
- **After Branching Day:**
  - X.Y development moves to `release-x.y` branches with stricter merge criteria (requires valid bugs and approvals)
  - `master` branch opens for X.Y+1 development with relaxed merge criteria

## Environment Setup

Use these paths (from plugin.json config):
- `RELEASE_REPO`: ../release
- `CI_TOOLS_REPO`: . (current directory)

## Branching Day Workflow

### Prerequisites (Verify Before Starting)

Use AskUserQuestion to confirm:
1. Exact date/time confirmed with management (usually 10 AM EST)?
2. 24h pre-notification email sent to aos-leads/devel?
3. 24h pre-notification posted to #announce-testplatform Slack?

### Step 0: Determine Version and Build Tooling

**First, verify branching readiness and get the target release version:**

Invoke skill: `get-current-release` with args: `../release`

This verifies that at least 3 accepted nightlies exist for the target release before proceeding. If verification fails, the workflow cannot continue until TRT creates more accepted nightlies.

**Then, build the required tooling:**

Invoke skill: `build-branching-tooling` with args: `. `

This will build all required tools:
- config-brancher
- tide-config-manager
- rpm-repo-mirroring-service
- ci-operator-config-mirror

### Step 1: Fetch and Update Repositories

Run these commands:
```bash
# Update ci-tools repo
git fetch upstream && git checkout master && git rebase -i upstream/main master 

# Update release repo
cd ../release && git fetch upstream && git checkout master && git rebase -i upstream/master master  && cd -
```

### Step 2: Send Start Notifications

Use AskUserQuestion to confirm:
- [ ] Email sent to aos-leads/devel: "Branching Day activities have started"
- [ ] Slack posted to #announce-testplatform: "OCP {FUTURE_RELEASE} Branching activities have started :openshift-intensifies:"

### Step 3: Trigger Final Fast-Forward Job

**Trigger fast-forward job via gangway:**

Invoke skill: `trigger-periodic-jobs` with args: `--polling 20 periodic-openshift-release-fast-forward`

This triggers the final fast-forward job before branching and waits up to 20 minutes for completion.

**View job status:** https://prow.ci.openshift.org/?job=periodic-openshift-release-fast-forward

Use AskUserQuestion to confirm job completed successfully

### Step 4: Prepare Config Brancher PR

**Run automation skills in sequence:**
1. Invoke skill: `create-branch-from-master` with args: `config-brancher-<future-release> ../release --reset`
2. Invoke skill: `run-config-brancher` with args: `<current-release> <future-release> ../release`
3. **Invoke skill: `verify-config-brancher` with args: `<future-release> ../release`**
4. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-config-brancher` until it passes**
5. Run: `cd ../release && make update`
6. Invoke skill: `update-infra-periodics` with args: `<current-release> <future-release> ../release`
7. **Invoke skill: `verify-infra-periodics` with args: `<current-release> <future-release> ../release`**
8. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-infra-periodics` until it passes**
9. Run: `cd ../release && make update`
10. Invoke skill: `commit-config-brancher-pr` with args: `<current-release> <future-release> ../release`

**Guide user for manual steps:**
```bash
cd ../release
git log --oneline origin/master..HEAD  # Review commits
git push -u origin config-brancher-{FUTURE_RELEASE}
gh pr create --title "OCP {CURRENT_RELEASE} Branching Day: bump ci-operator configurations" --body "Branching Day: Bump CI operator configs for {FUTURE_RELEASE}

This PR includes:

- config-brancher output for {FUTURE_RELEASE}
- make update for generated files
- Bump periodic jobs (config-brancher and fast-forward)
- Bump versions in jira config"
```


**Example PR:** https://github.com/openshift/release/pull/58981

### Step 5: Refresh Open PRs

**Trigger periodic jobs via gangway:**

Invoke skill: `trigger-periodic-jobs` with args: `--polling 20 periodic-bugzilla-refresh-main periodic-jira-refresh-main`

These jobs revalidate all open PRs on master/main branches against the new target version. The skill will:
- Trigger both jobs via gangway
- Poll their status every 30 seconds
- Report success when both complete (20 minute timeout)
- Fail if either job fails

**View job status:** https://prow.ci.openshift.org/?job=periodic-bugzilla-refresh-main
**View job status:** https://prow.ci.openshift.org/?job=periodic-jira-refresh-main

### Step 6: Prepare Tide Config PR

**Run automation skills in sequence:**
1. Invoke skill: `create-branch-from-master` with args: `tide-config-<future-release> ../release --reset`
2. Invoke skill: `run-tide-config-manager` with args: `../release`
3. **Invoke skill: `verify-tide-config-manager` with args: `../release`**
4. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-tide-config-manager` until it passes**
5. Invoke skill: `update-tide-and-infra-jobs` with args: `<current-release> <future-release> ../release`
6. **Invoke skill: `verify-tide-and-infra-jobs` with args: `<current-release> <future-release> ../release`**
7. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-tide-and-infra-jobs` until it passes**
8. Invoke skill: `update-etcd-config` with args: `<current-release> <future-release> ../release`
9. **Invoke skill: `verify-etcd-config` with args: `<current-release> <future-release> ../release`**
10. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-etcd-config` until it passes**
11. Run: `cd ../release && make prow-config`
12. Invoke skill: `commit-tide-config-pr` with args: `<current-release> <future-release> ../release`

**Guide user for manual steps:**
```bash
cd ../release
git log --oneline origin/master..HEAD  # Review commits
git push -u origin tide-config-{FUTURE_RELEASE}
gh pr create --title "OCP {CURRENT_RELEASE} Branching Day: Update Tide merge criteria and Infra jobs" --body "Update merge criteria for {FUTURE_RELEASE} and adjust Infra jobs accordingly.
- tide-config-manager executed for branching phase
- make prow-config executed
- Bump periodic-openshift-release-merge-blockers
- Bump periodic-ocp-build-data-enforcer
- etcd manual change"
```


**Example PR:** https://github.com/openshift/release/pull/59015

### Step 7: Prepare Image Mirroring PR

**Run automation skill:**
1. Invoke skill: `create-branch-from-master` with args: `image-mirroring-<future-release> ../release --reset`
2. Invoke skill: `update-image-mirroring` with args: `<current-release> <future-release> ../release`

**Guide user for MANUAL file edit:**
Instruct user to edit: `../release/core-services/image-mirroring/openshift/_config.yaml`
- Remove "latest" from {CURRENT_RELEASE} buckets
- Add "latest" to {FUTURE_RELEASE} buckets
- Update ALL version buckets for current release and its variants (standard, sriov, metallb, ptp, scos)

Use AskUserQuestion to confirm file edited.

**Continue automation:**
1. Run: `cd ../release && make openshift-image-mirror-mappings`
2. Invoke skill: `commit-image-mirroring-pr` with args: `<current-release> <future-release> ../release`

**Guide user for manual steps:**
```bash
cd ../release
git log --oneline origin/master..HEAD  # Review commits
git push -u origin image-mirroring-{FUTURE_RELEASE}
gh pr create --title "OCP {CURRENT_RELEASE} Branching Day: Update image mirror mappings" --body "Update image mirroring 'latest' tags for {FUTURE_RELEASE}
This PR includes:
- Updated _config.yaml to move 'latest' tags from {CURRENT_RELEASE} to {FUTURE_RELEASE} buckets
- Generated image mirror mappings via make openshift-image-mirror-mappings
```

Use AskUserQuestion to confirm:
- [ ] PR created and link provided?
- [ ] PR reviewed, approved, and merged?

**Example PR:** https://github.com/openshift/release/pull/59017

### Step 8: Trigger Merge Blocker Job

**Trigger merge blocker job via gangway:**

Invoke skill: `trigger-periodic-jobs` with args: `periodic-openshift-release-merge-blockers`

This job takes ~2 hours to complete. The skill will trigger it and return the execution ID immediately.

**View job status:** https://prow.ci.openshift.org/?job=periodic-openshift-release-merge-blockers

Use AskUserQuestion to confirm job completed before proceeding

### Step 9: Create DPP Ticket

Instruct user to:
1. Visit https://devservices.dpp.openshift.com/support/ (VPN required)
2. Create ticket requesting:
   - New Target Release: {FUTURE_RELEASE}.0 in Bugzilla and Jira
   - Previous z Target Release: {CURRENT_RELEASE}.z in Bugzilla and Jira
   - New Version: {FUTURE_RELEASE} in Bugzilla and Jira
3. Reference previous ticket: https://issues.redhat.com/browse/DPP-10598

Use AskUserQuestion to confirm ticket created.

### Step 10: Send Completion Notifications

Use AskUserQuestion to confirm:
- [ ] Email sent to aos-leads/devel:
  - Subject: "Branching activities completed"
  - Body: "The Test Platform team has completed branching activities."
- [ ] Slack posted to #announce-testplatform:
  - "OCP {FUTURE_RELEASE} Branching activities are now completed :openshifty: !"

### Final Summary

Display summary:
```
Branching Day Complete!

✓ Config Brancher PR: [link]
✓ Tide Config PR: [link]
✓ Image Mirroring PR: [link]
✓ Merge blocker job triggered and completed
✓ DPP ticket created
✓ Notifications sent

OpenShift {CURRENT_RELEASE} → {FUTURE_RELEASE} branching complete!
```

## Important Notes

- **Always use TodoWrite** to track progress through all steps
- **Use AskUserQuestion** for all confirmations and manual steps
- **Version is automatically determined** from periodic-prow-auto-config-brancher configuration
- **Branching readiness is verified** by checking for 3+ accepted nightlies
- **Calculate future version** automatically (current + 0.01)
- **Verify prerequisites** before starting any work
- **Wait for user confirmation** before proceeding to next major step
- **Provide PR links** from example PRs for reference

## References

- [Release Schedule](https://docs.google.com/spreadsheets/d/19cEcXH10gXgLMB98fCkQm-kB8xEHQQdKcXnPJiKQbwg/edit)
