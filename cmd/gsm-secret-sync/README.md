# GSM Secret Sync

A tool for synchronizing Google Secret Manager (GSM) resources with OpenShift CI secret collections based on the configuration defined in [_config.yaml](https://github.com/openshift/release/blob/main/core-services/sync-rover-groups/_config.yaml).

## Overview

Secrets in Google Secret Manager are organized into **secret collections** - logical groupings that define access boundaries and management scope. GSM Secret Sync manages the lifecycle of these secret collections in Google Cloud Platform by:

- Creating and managing service accounts for secret access
- Provisioning secrets in Google Secret Manager
- Configuring IAM policies with conditional access controls
- Maintaining consistency between desired and actual state

## How It Works

The tool operates on a declarative configuration file that serves as the **source of truth** for defining groups and their associated secret collections. Based on this configuration, it automatically:

1. **Service Account Management**: Creates updater service accounts with collection-specific naming
2. **Secret Provisioning**: Creates index secrets and service account key secrets
3. **IAM Policy Sync**: Configures conditional IAM bindings for viewer and updater roles
4. **State Reconciliation**: Compares desired vs actual state and applies necessary changes

## Usage

```bash
gsm-secret-sync \
  --config /path/to/config.yaml \
  --gcp-service-account-key-file /path/to/service-account.json \
  [--dry-run] \
  [--log-level info]
```

### Required Flags

- `--config`: Path to the configuration file that serves as the source of truth for groups and secret collections
- `--gcp-service-account-key-file`: Path to GCP service account credentials (JSON format). This service account must have permissions to manage IAM service accounts and policies, create/read/update/delete secrets in Google Secret Manager, and access Google Cloud Resource Manager APIs

### Optional Flags

- `--dry-run`: Preview changes without applying them (default: false)
- `--log-level`: Logging verbosity level (default: info)

### Environment Variables

- `GCP_PROJECT_ID`: GCP project ID (e.g., "openshift-ci-secrets")
- `GCP_PROJECT_NUMBER`: GCP project number (e.g., "384486694155")

## Configuration

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

## Resource Naming

The tool follows specific naming conventions:

- **Service Accounts**: `{collection}-sa@{project}.iam.gserviceaccount.com`
- **Updater Secrets**: `{collection}__updater-service-account`
- **Index Secrets**: `{collection}____index`

## Deployment

The tool is currently deployed as a **post-submit job** `branch-ci-openshift-release-master-gsm-secrets-reconciler` in the [openshift/release](https://github.com/openshift/release) repository. This ensures that GSM resources are automatically synchronized whenever changes are merged to the configuration.
