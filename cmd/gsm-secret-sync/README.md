# gsm-secret-sync

## What
Reconciler that manages GCP-side resources for Google Secret Manager (GSM): service accounts, IAM policy bindings, and secrets. It reads a declarative configuration file, compares the desired state against the actual state in GCP, computes a diff, and applies the necessary create/delete operations. This is the infrastructure counterpart to ci-secret-generator -- it ensures the GCP project has the right service accounts and secrets in place before ci-secret-generator writes secret values.

## How it works -- full flow

1. **Load configuration.** Reads command-line flags and validates them. Loads GCP credentials from the service account key file (censored from logs). Loads GCP project configuration from environment via `gsm.GetConfigFromEnv()`.

2. **Parse desired state.** Calls `gsm.GetDesiredState(configFile, projectConfig)` which parses the config file and produces four sets:
   - Desired service accounts (one per secret collection)
   - Desired secrets (individual secret entries within collections)
   - Desired IAM bindings (granting service accounts access to secrets)
   - Desired collections (groupings of secrets)

3. **Fetch actual state.** Creates three GCP API clients and fetches current state:
   - **Resource Manager client** (`resourcemanager.NewProjectsClient`): retrieves the current project IAM policy.
   - **IAM client** (`iamadmin.NewIamClient`): lists existing "updater" service accounts (those managed by this tool) in the GCP project.
   - **Secret Manager client** (`secretmanager.NewClient`): lists all existing secrets in the GSM project.

4. **Compute diff.** Calls `gsm.ComputeDiff(...)` which compares desired vs. actual state across all four dimensions:
   - Service accounts to create (in desired but not actual)
   - Service accounts to delete (in actual but not desired)
   - Secrets to create (in desired but not actual)
   - Secrets to delete (in actual but not desired)
   - Consolidated IAM policy (if bindings differ from current policy)

5. **Log change summary.** Logs the total number of changes and details at debug/trace level:
   - Service accounts to create/delete
   - Secrets to create/delete
   - IAM policy binding updates

6. **Apply changes.** If `--dry-run` is false, calls `actions.ExecuteActions(ctx, iamClient, secretsClient, projectsClient)` which:
   - Creates new service accounts
   - Deletes obsolete service accounts
   - Creates new secrets
   - Deletes obsolete secrets
   - Updates the project IAM policy with consolidated bindings

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--config` | (required) | Path to the GSM config file defining desired service accounts, secrets, and collections |
| `--gcp-service-account-key-file` | (required) | Path to GCP service account key file (JSON format) with permissions to manage IAM, secrets, and projects |
| `--dry-run` | `false` | When true, compute and log changes without applying them |
| `--log-level` | `info` | Log verbosity level |

## Key files
- `cmd/gsm-secret-sync/main.go` -- entry point: config loading, GCP client creation, diff computation, action execution
- `pkg/gsm-secrets/` -- core library: `GetDesiredState`, `ComputeDiff`, `Actions`, `ExecuteActions`, `GetAllSecrets`, `GetUpdaterServiceAccounts`, `GetProjectIAMPolicy`

## Deployment
Runs as a postsubmit Prow job (`branch-ci-openshift-release-master-gsm-secrets-reconciler`), triggered when the GSM config in openshift/release changes. Requires a GCP service account with broad permissions: `iam.admin`, `secretmanager.admin`, and `resourcemanager.projectIamAdmin` on the target GCP project.

---

## Additional details

### Overview

Secrets in Google Secret Manager are organized into **secret collections** - logical groupings that define access boundaries and management scope. GSM Secret Sync manages the lifecycle of these secret collections in Google Cloud Platform by:

- Creating and managing service accounts for secret access
- Provisioning secrets in Google Secret Manager
- Configuring IAM policies with conditional access controls
- Maintaining consistency between desired and actual state

### Usage

```bash
gsm-secret-sync \
  --config /path/to/config.yaml \
  --gcp-service-account-key-file /path/to/service-account.json \
  [--dry-run] \
  [--log-level info]
```

#### Required Flags

- `--config`: Path to the configuration file that serves as the source of truth for groups and secret collections
- `--gcp-service-account-key-file`: Path to GCP service account credentials (JSON format). This service account must have permissions to manage IAM service accounts and policies, create/read/update/delete secrets in Google Secret Manager, and access Google Cloud Resource Manager APIs

#### Optional Flags

- `--dry-run`: Preview changes without applying them (default: false)
- `--log-level`: Logging verbosity level (default: info)

#### Environment Variables

- `GCP_PROJECT_ID`: GCP project ID (e.g., "openshift-ci-secrets")
- `GCP_PROJECT_NUMBER`: GCP project number (e.g., "384486694155")

### Configuration

The configuration file is the **source of truth** that defines groups and their secret collection access. The groups referenced in this configuration are **Rover groups** - Red Hat internal groups managed through the Rover system and synchronized by the [`sync-rover-groups`](../sync-rover-groups) tool. The actual configuration can be found at [_config.yaml](https://github.com/openshift/release/blob/main/core-services/sync-rover-groups/_config.yaml).

```yaml
groups:
  team-alpha:
    secret_collections:
      - collection-a
      - collection-b
  team-beta:
    secret_collections:
      - collection-c
```

### Resource Naming

The tool follows specific naming conventions:

- **Service Accounts**: `{collection}-updater@{project}.iam.gserviceaccount.com`
- **Updater Secrets**: `{collection}__updater-service-account`
- **Index Secrets**: `{collection}____index`
