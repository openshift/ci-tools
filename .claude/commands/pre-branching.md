# OpenShift Pre-Branching Automation

You are executing the pre-branching tasks for OpenShift release {{VERSION}}.

## Task Overview

Execute the following steps in order:

### 1. Update Release Repository and Create Branch

**Use the `create-pre-branching-branch` skill:**

```bash
.claude/.claude-plugin/skills/create-pre-branching-branch.sh {{OLD_VERSION}} {{NEW_VERSION}} {{RELEASE_REPO}}
```

This creates a branch named like `pre-branching-4.21-to-4.22` for your pre-branching changes.

### 2. Download Latest Sippy Config

**Use the `download-sippy-config` skill:**

```bash
.claude/.claude-plugin/skills/download-sippy-config.sh /tmp/sippy-openshift.yaml
```

### 3. Release-Controller Configurations

**Use the `copy-release-controller-configs` skill:**

```bash
.claude/.claude-plugin/skills/copy-release-controller-configs.sh {{OLD_VERSION}} {{NEW_VERSION}} {{RELEASE_REPO}}
```

This will copy and update release-controller configurations from {{OLD_VERSION}} to {{NEW_VERSION}}.

### 4. Update Generated Release Gating Jobs

**Use the `update-release-gating-jobs` skill:**

```bash
.claude/.claude-plugin/skills/update-release-gating-jobs.sh {{OLD_VERSION}} {{RELEASE_REPO}} {{CI_TOOLS_REPO}} /tmp/sippy-openshift.yaml
```

This tool will:
- Read Sippy config to get informing and blocking jobs for {{OLD_VERSION}}
- Process related releases (e.g., "{{OLD_VERSION}}", "{{OLD_VERSION}}-okd")
- Map job names to CI operator config files via Prow job metadata
- Bump configs from {{OLD_VERSION}} to {{NEW_VERSION}}
- Update filenames, content, and zz_generated_metadata

Expected output:
- Processes ~400+ jobs from Sippy
- Maps to ~20-25 unique config files
- Creates new {{NEW_VERSION}} config files
- Updates branch references in metadata

### 5. Copy Handcrafted Release-Gating Jobs

**Use the `copy-handcrafted-release-gating-jobs` skill:**

```bash
.claude/.claude-plugin/skills/copy-handcrafted-release-gating-jobs.sh {{OLD_VERSION}} {{NEW_VERSION}} {{RELEASE_REPO}}
```

**Prerequisites:**
- Step 3 (Release-Controller Configurations) must be completed
- The `core-services/release-controller/_repos/ocp-{{NEW_VERSION}}*.repo` files must exist

This will:
- Check that required `.repo` files exist for {{NEW_VERSION}}
- Copy `ci-operator/jobs/openshift/release/openshift-release-release-{{OLD_VERSION}}-periodics.yaml` to {{NEW_VERSION}}
- Bump ALL version strings in the file (handles chains like `4.19-to-4.20-to-4.21` → `4.20-to-4.21-to-4.22`)
- Run `make release-controllers` to generate release controller configurations
- Run `make jobs` to regenerate Prow job configurations

**Note:** This step is required even though these are handcrafted jobs, as one of the generators checks for the presence of this file.

### 6. Regenerate Prow Jobs

**Use the `regenerate-prow-jobs` skill:**

```bash
.claude/.claude-plugin/skills/regenerate-prow-jobs.sh {{RELEASE_REPO}}
```

This will:
- Read all CI operator config files (including new {{NEW_VERSION}} configs)
- Generate corresponding Prow job YAML files
- Create/update periodic job definitions for {{NEW_VERSION}}
- Update job configurations in `ci-operator/jobs/`

Expected output:
- New Prow job files for {{NEW_VERSION}} release configs
- Updated job definitions matching the CI operator configs

### 7. Validate Release Controller Configurations

**Use the `validate-release-controller-config` skill:**

```bash
.claude/.claude-plugin/skills/validate-release-controller-config.sh {{RELEASE_REPO}}
```

This will validate that all jobs referenced in the release-controller configurations exist.

**If you see errors**, these are standalone Prow job definitions (analysis jobs) that need to be copied:

**Use the `bump-analysis-jobs` skill to fix validation errors:**

```bash
.claude/.claude-plugin/skills/bump-analysis-jobs.sh {{OLD_VERSION}} {{NEW_VERSION}} {{RELEASE_REPO}}
```

This will automatically:
- Extract install/upgrade/overall analysis jobs for {{OLD_VERSION}}
- Create {{NEW_VERSION}} versions of these jobs
- Append them to the appropriate Prow job files

**After bumping analysis jobs, regenerate Prow jobs:**

```bash
.claude/.claude-plugin/skills/regenerate-prow-jobs.sh {{RELEASE_REPO}}
```

**Then re-run validation to confirm all jobs exist:**

```bash
.claude/.claude-plugin/skills/validate-release-controller-config.sh {{RELEASE_REPO}}
```

### 8. Create Commits

**Use the `create-pre-branching-commits` skill:**

```bash
.claude/.claude-plugin/skills/create-pre-branching-commits.sh {{OLD_VERSION}} {{NEW_VERSION}} {{RELEASE_REPO}}
```

This will automatically create three commits:
- **Commit 1**: CI operator configs (bump release gating jobs from Sippy)
- **Commit 2**: Release-controller configurations
- **Commit 3**: Prow jobs (add manual jobs + make jobs)

**Review and push:**
```bash
cd {{RELEASE_REPO}}
git log -3
git push --set-upstream origin pre-branching-{{OLD_VERSION}}-to-{{NEW_VERSION}}
```

### 9. Summary Report

Provide a summary including:

1. **Files created:**
   - Release-controller configurations (14 files)
   - CI operator configs (~22 files)
   - Prow job files (~4 new files + 8 modified)

2. **Jobs processed:**
   - Number of Sippy jobs processed
   - Number of analysis jobs added

3. **Validation:**
   - Confirm all validations passed

4. **Next steps:**
   - Review commits and create pull request
   - Continue with additional branching tasks per BRANCHING.md

## Important Notes

- All changes are made in the release repository only
- Sippy config is downloaded fresh from GitHub
- The `create-pre-branching-commits` skill handles all git operations
- Always review commits before pushing

## Variable Substitutions

- `{{VERSION}}` - The version provided by user (e.g., "4.22")
- `{{OLD_VERSION}}` - Previous version (e.g., "4.21")
- `{{NEW_VERSION}}` - Next version (e.g., "4.22")
- `{{RELEASE_REPO}}` - Path to release repository
- `{{CI_TOOLS_REPO}}` - Path to ci-tools repository
