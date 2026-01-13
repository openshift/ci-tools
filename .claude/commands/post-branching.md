---
name: post-branching
description: Execute Post-Branching tasks for OpenShift X.Y release - bootstrap config and content for X.Y+2
usage: /post-branching
examples:
  - /post-branching
---

# Post-Branching Orchestration Instructions

You are the orchestrator for OpenShift Post-Branching tasks. When this command is invoked, you will automatically determine the current release version and guide the user through all the steps required to bootstrap configuration and content for X.Y+2.

## Your Role

- **Fetch current release**: Extract version from periodic-prow-auto-config-brancher configuration
- **Calculate versions**: Current release (from config), Future release X.Y+1 (current + 0.01), Bootstrap release X.Y+2 (current + 0.02), X.Y+3 (current + 0.03)
- **Use TodoWrite**: Track all 10 steps and mark them as you progress
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
   - Explain the impact (e.g., "Jira stanza missing target_version")
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

**Scenario 1: Stanza not found in config**
- **Cause**: awk/sed insertion logic failed, previous stanza missing
- **Action**: Check config file structure, verify insertion point
- **Recovery**: Manually add missing stanza, re-run verification

**Scenario 2: Version mismatch in updates**
- **Cause**: Wrong calculation of X.Y+1, X.Y+2, or X.Y+3
- **Action**: Verify version arithmetic
- **Recovery**: Recalculate versions, re-run skill with correct values

**Scenario 3: No config/job files created**
- **Cause**: config-brancher didn't run, tool missing, wrong path
- **Action**: Check tool exists, verify release repo path
- **Recovery**: Build tools, fix paths, re-run skill

**Scenario 4: Image stream sections incomplete**
- **Cause**: One or more variants failed to add
- **Action**: Check which variants are missing
- **Recovery**: Manually add missing variants, re-run verification

## What is Post-Branching?

**Post-Branching** happens some time after the main branching is complete. Its purpose is to bootstrap all necessary configuration and infrastructure for the X.Y+2 release.

## Environment Setup

Use these paths:
- `RELEASE_REPO`: ../release
- `CI_TOOLS_REPO`: . (current directory)

## Post-Branching Workflow Overview

This workflow consists of **10 steps** that create **5 Pull Requests**:

| Step | PR # | Configuration Update Skill | PR Commit Skill | Description |
|------|------|---------------------------|-----------------|-------------|
| 1 | PR #1 | `update-jira-validation-post-branching` | `commit-jira-validation-pr` | Configure Jira validation for X.Y+2 branches |
| 2 | PR #2 | `update-merge-blockers-job` | `commit-merge-blockers-pr` | Update merge blockers job to track X.Y+2 |
| 3 | - | `trigger-periodic-jobs` | - | Trigger merge blockers job immediately |
| 4 | PR #3 | `update-fast-forward-job` | `commit-fast-forward-pr` | Enable fast-forwarding for X.Y+2 branches |
| 5 | - | `tag-imagestreams` | `verify-imagestream-tags` | Tag OCP image streams |
| 6 | - | `tag-imagestreams` (5x) | `verify-imagestream-tags` (5x) | Tag Origin image streams (5 variants) |
| 7 | - | `tag-imagestreams` | `verify-imagestream-tags` | Tag OCP-private image streams |
| 8 | - | `mirror-imagestreams-to-quay` | - | Mirror all image streams to Quay (7 streams) |
| 9 | PR #4 | `create-config-jobs-x-y-plus-2` | `commit-ci-configs-jobs-pr` | Create CI operator configs and jobs for X.Y+2 |
| 10 | PR #5 | `update-auto-config-brancher-x-y-plus-2` | `commit-auto-config-brancher-pr` | Enable automatic config maintenance |

**The 5 Pull Requests:**
1. **Jira Validation** - Configure validation criteria for openshift-X.Y+2 and release-X.Y+2
2. **Merge Blockers** - Update periodic-openshift-release-merge-blockers job
3. **Fast-Forward** - Update periodic-openshift-release-fast-forward job
4. **CI Configs & Jobs** - Bootstrap complete CI infrastructure (most complex)
5. **Auto-Config-Brancher** - Update periodic-prow-auto-config-brancher job

## Detailed Steps

### Step 0: Determine Version and Build Tooling

**First, get the current release version:**

Invoke skill: `get-current-release` with args: `../release`

This determines the current release version from the periodic-prow-auto-config-brancher configuration.

**Then, build the required tooling:**

Invoke skill: `build-branching-tooling` with args: `. `

This will build all required tools:
- config-brancher
- tide-config-manager
- rpm-repo-mirroring-service
- ci-operator-config-mirror


### Step 1: Configure Jira Validation for X.Y+2 Branches

**Purpose:** Set up Jira validation criteria for openshift-X.Y+2 and release-X.Y+2 branches.

**Run automation skills in sequence:**
1. Invoke skill: `create-branch-from-master` with args: `jira-validation-<x.y+2> ../release --reset`
2. Invoke skill: `update-jira-validation-post-branching` with args: `<current-release> <x.y+2> ../release`
3. **Invoke skill: `verify-jira-validation` with args: `<current-release> <x.y+2> ../release`**
4. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-jira-validation` until it passes**
5. Invoke skill: `commit-jira-validation-pr` with args: `<x.y+2> ../release`

**Expected changes in** `core-services/jira-lifecycle-plugin/config.yaml`:
```yaml
default:
    ...
    # Add the two stanzas below
    openshift-X.Y+2:
      dependent_bug_states:
      - status: MODIFIED
      - status: ON_QA
      - status: VERIFIED
      dependent_bug_target_releases:
      - X.Y+3.0
      target_release: X.Y+2.0
      validate_by_default: true
    release-X.Y+2:
      dependent_bug_states:
      - status: MODIFIED
      - status: ON_QA
      - status: VERIFIED
      dependent_bug_target_releases:
      - X.Y+3.0
      target_release: X.Y+2.0
      validate_by_default: true
```

**Guide user to push and create PR:**
```bash
cd ../release
git log --oneline origin/master..HEAD  # Review commit
git push -u origin jira-validation-X.Y+2
gh pr create --title "Post-Branching: Add Jira validation criteria for X.Y+2 branches" \
  --body "This PR adds Jira validation stanzas for: 
  - openshift-X.Y+2 
  - release-X.Y+2 
Part of the post-branching process, step 1."
```

### Step 2: Update Merge Blockers Job to Track X.Y+2

**Purpose:** Configure periodic-openshift-release-merge-blockers to start maintaining blocker issues for release-X.Y+2 branches.

**Run automation skills in sequence:**
1. Invoke skill: `create-branch-from-master` with args: `merge-blockers-<x.y+2> ../release --reset`
2. Invoke skill: `update-merge-blockers-job` with args: `<current-release> <x.y+2> ../release`
3. **Invoke skill: `verify-merge-blockers-job` with args: `<current-release> <x.y+2> ../release`**
4. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-merge-blockers-job` until it passes**
5. Invoke skill: `commit-merge-blockers-pr` with args: `<x.y+2> ../release`

**Expected change in** `ci-operator/jobs/infra-periodics.yaml`:
```yaml
name: periodic-openshift-release-merge-blockers
  spec:
    containers:
      - args:
        ...
        - --current-release=X.Y+1
        - --future-release=X.Y+2  # Changed from X.Y+1
```

**Guide user to push and create PR:**
```bash
cd ../release
git log --oneline origin/master..HEAD  # Review commit
git push -u origin merge-blockers-X.Y+2
gh pr create --title "Post-Branching: Start tracking merge blockers for X.Y+2 branches" \
  --body "This PR updates the periodic-openshift-release-merge-blockers job to track 4.23 branches.
  Changes:
  - Updated --future-release flag from X.Y+1 to X.Y+2
  Part of the post-branching process, step 2."
```


### Step 3: Trigger Merge Blockers Job

**Purpose:** After Step 2 PR merges, trigger the job to immediately establish X.Y+2 merge blockers.

**Trigger merge blocker job via gangway:**

Invoke skill: `trigger-periodic-jobs` with args: `periodic-openshift-release-merge-blockers`

The skill will trigger the job and return the execution ID immediately.

**View job status:** https://prow.ci.openshift.org/?job=periodic-openshift-release-merge-blockers


### Step 4: Update Fast-Forward Job for X.Y+2

**Purpose:** Once X.Y+2 merge blockers are in place, configure periodic-openshift-release-fast-forward to create and start fast-forwarding release-X.Y+2 branches.

**Run automation skills in sequence:**
1. Invoke skill: `create-branch-from-master` with args: `fast-forward-<x.y+2> ../release --reset`
2. Invoke skill: `update-fast-forward-job` with args: `<current-release> <x.y+2> ../release`
3. **Invoke skill: `verify-fast-forward-job` with args: `<current-release> <x.y+2> ../release`**
4. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-fast-forward-job` until it passes**
5. Invoke skill: `commit-fast-forward-pr` with args: `<x.y+2> ../release`

**Expected change in** `ci-operator/jobs/infra-periodics.yaml`:
```yaml
name: periodic-openshift-release-fast-forward
spec:
  containers:
    - args:
      ...
      - --current-release=X.Y+1
      - --future-release=X.Y+2  # Changed from X.Y+1
```

**Guide user to push and create PR:**
```bash
cd ../release
git log --oneline origin/master..HEAD  # Review commit
git push -u origin fast-forward-X.Y+2
gh pr create --title "Post-Branching: Start fast-forwarding X.Y+2 branches from master" \
  --body "This PR updates the periodic-openshift-release-fast-forward job to track X.Y+2 branches.
Changes:
- Updated --future-release flag from X.Y+1 to X.Y+2
Part of the post-branching process, step 4."
```

### Step 5: Tag OCP Streams

**Purpose:** Seed ocp namespace image streams for X.Y+2.

**IMPORTANT:** This step requires cluster access (app.ci context)!

Invoke skill: `tag-imagestreams` with args: `<x.y+1> <x.y+2> ocp`

Invoke skill: `verify-imagestream-tags` with args: `<x.y+1> <x.y+2> ocp`

**If verification fails: Re-run `tag-imagestreams`, then re-run `verify-imagestream-tags` until it passes**


### Step 6: Tag Origin Streams (Multiple Variants)

**Purpose:** Seed origin namespace image streams for X.Y+2, including all variants.

**Tag each variant:**

Invoke skill: `tag-imagestreams` with args: `<x.y+1> <x.y+2> origin` (standard)
Invoke skill: `tag-imagestreams` with args: `<x.y+1> <x.y+2> origin sriov-`
Invoke skill: `tag-imagestreams` with args: `<x.y+1> <x.y+2> origin ptp-`
Invoke skill: `tag-imagestreams` with args: `<x.y+1> <x.y+2> origin metallb-`
Invoke skill: `tag-imagestreams` with args: `<x.y+1> <x.y+2> origin scos-`

**Verify each variant:**

Invoke skill: `verify-imagestream-tags` with args: `<x.y+1> <x.y+2> origin` (standard)
Invoke skill: `verify-imagestream-tags` with args: `<x.y+1> <x.y+2> origin sriov-`
Invoke skill: `verify-imagestream-tags` with args: `<x.y+1> <x.y+2> origin ptp-`
Invoke skill: `verify-imagestream-tags` with args: `<x.y+1> <x.y+2> origin metallb-`
Invoke skill: `verify-imagestream-tags` with args: `<x.y+1> <x.y+2> origin scos-`

**If any verification fails: Re-run `tag-imagestreams` for that variant, then re-run `verify-imagestream-tags` until it passes**


### Step 7: Tag OCP-Private Streams

**Purpose:** Seed ocp-private namespace image streams for X.Y+2.

Invoke skill: `tag-imagestreams` with args: `<x.y+1> <x.y+2> ocp-private '' -priv`

Invoke skill: `verify-imagestream-tags` with args: `<x.y+1> <x.y+2> ocp-private '' -priv`

**If verification fails: Re-run `tag-imagestreams`, then re-run `verify-imagestream-tags` until it passes**

### Step 8: Mirror Created Image Streams to Quay

**Purpose:** Mirror all newly created X.Y+2 image streams to Quay registry.

Invoke skill: `mirror-imagestreams-to-quay` with args: `<x.y+2> ../release`

This mirrors all 7 image streams (with automatic retry on failure, up to 3 attempts per stream):
- ocp/X.Y+2
- origin/X.Y+2 (+ 4 variants: sriov, scos, ptp, metallb)
- ocp-private/X.Y+2-priv


### Step 9: Create CI Operator Configs and Jobs for X.Y+2

**Purpose:** Generate CI operator configuration files and Prow jobs for X.Y+2 branches.

**IMPORTANT:** This is a complex step. Use previous PR as reference (example: 4.11 PR).

**Run automation skills in sequence:**
1. Invoke skill: `create-branch-from-master` with args: `ci-configs-<x.y+2> ../release --reset`
2. Invoke skill: `create-config-jobs-x-y-plus-2` with args: `<x.y+1> <x.y+2> ../release`
3. **Invoke skill: `verify-config-jobs-x-y-plus-2` with args: `<x.y+1> <x.y+2> ../release`**
4. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-config-jobs-x-y-plus-2` until it passes**

This will:
1. Run config-brancher to create X.Y+2 config files
2. Run ci-operator-config-mirror for openshift-priv org
3. Generate vanilla jobs
4. Copy job customization from master/main to release-X.Y+2
5. Update the rpm dependency
6. Create commits and branch for the PR

**Guide user to push and create PR:**
```bash
cd ../release
git log --oneline origin/master..HEAD  # Review commits
git push -u origin ci-configs-X.Y+2
gh pr create --title "Post-Branching: Generate ci-operator configs and jobs" \
  --body "Generate X.Y+2 configuration files with config-brancher
- Create X.Y+2 configs for openshift-priv org
- Generate vanilla jobs for X.Y+2
- Add rpm repository files for X.Y+2
- Carry job customization over from master/main jobs
- Part of the post-branching process, step 9.
```

### Step 10: Update Auto-Config-Brancher to Maintain X.Y+2

**Purpose:** After Step 9 PR merges, configure periodic-prow-auto-config-brancher to automatically maintain X.Y+2 CI configuration files.

**Run automation skills in sequence:**
1. Invoke skill: `create-branch-from-master` with args: `auto-config-brancher-<x.y+2> ../release --reset`
2. Invoke skill: `update-auto-config-brancher-x-y-plus-2` with args: `<x.y+1> <x.y+2> ../release`
3. **Invoke skill: `verify-auto-config-brancher` with args: `<x.y+1> <x.y+2> ../release`**
4. **If verification fails: STOP, diagnose the issue, fix it (manually or with edits), then re-run `verify-auto-config-brancher` until it passes**
5. Invoke skill: `commit-auto-config-brancher-pr` with args: `<x.y+2> ../release`

**Expected change in** `ci-operator/jobs/infra-periodics.yaml`:
```yaml
name: periodic-prow-auto-config-brancher
spec:
  containers:
    - args:
      ...
      - --current-release=X.Y+1
      - --future-release=X.Y+2
```

**Guide user to push and create PR:**
```bash
cd ../release
git log --oneline origin/master..HEAD  # Review commit
git push -u origin auto-config-brancher-X.Y+2
gh pr create --title "Post-Branching: Updating periodic-prow-auto-config-brancher" \
  --body "This updates the periodic-prow-auto-config-brancher job to automatically maintain CI configuration files for the X.Y+2 release.
Part of the post-branching process, step 10."
```

### Final Summary

Display summary:
```
Post-Branching Day Complete! (10 Steps)

✓ Step 1:  Jira validation configured for X.Y+2
✓ Step 2:  Merge blockers job updated for X.Y+2
✓ Step 3:  Merge blockers job triggered
✓ Step 4:  Fast-forward job configured for X.Y+2
✓ Step 5:  OCP image streams tagged
✓ Step 6:  Origin image streams tagged (5 variants)
✓ Step 7:  OCP-private image streams tagged
✓ Step 8:  All image streams mirrored to Quay (7 streams)
✓ Step 9:  CI operator configs and jobs created for X.Y+2
✓ Step 10: Auto-config-brancher updated

All PRs:
1. Jira validation: [link]
2. Merge blockers: [link]
3. Fast-forward: [link]
4. CI configs and jobs: [link]
5. Auto-config-brancher: [link]

OpenShift X.Y+2 infrastructure bootstrapped successfully!
```

## References

- [OpenShift CI Documentation](https://docs.ci.openshift.org/)
- [Release Schedule Spreadsheet](https://docs.google.com/spreadsheets/d/19cEcXH10gXgLMB98fCkQm-kB8xEHQQdKcXnPJiKQbwg/edit)
