# GSM Secret Sync End-to-End Tests

This directory contains end-to-end tests for the [`gsm-secret-sync`](../../../cmd/gsm-secret-sync/) tool.

## What This Tests

The e2e test validates the complete lifecycle of the GSM secret sync tool by running it against a real GCP staging environment. It verifies that the tool correctly:

- **Creates** service accounts, secrets, and IAM bindings from configuration
- **Updates** existing resources when configuration changes
- **Deletes** obsolete resources when they're removed from configuration  
- **Maintains idempotency** (running twice produces the same result)

## Why We Need This

While unit tests validate individual components, this e2e test ensures the tool works correctly with actual GCP APIs, handles eventual consistency issues, and manages the complete resource lifecycle as intended in production.

## Test Structure

The test runs through four scenarios using different configuration files:

1. **Initial Creation** (`config-create.yaml`) - Creates resources from scratch
2. **Idempotency** - Runs the same config twice to ensure no duplicates
3. **Updates** (`config-update.yaml`) - Adds new collections and groups
4. **Cleanup** (`config-delete.yaml`) - Removes resources

Each test verifies that the actual GCP state matches the expected state derived from the configuration.

## Prerequisites

### GCP Project & Credentials

The test requires a dedicated GCP staging project with:

- **Project ID**: Set via `GCP_DEV_PROJECT_ID` environment variable
- **Project Number**: Set via `GCP_DEV_PROJECT_NUMBER` environment variable  
- **Service Account Key**: Path set via `GCP_SECRETS_DEV_CREDENTIALS_FILE`
- **GCS Lock Bucket**: Set via `GCS_E2E_LOCK_BUCKET`

The service account needs permissions for:

- Secret Manager (create/delete secrets)
- IAM (create/delete service accounts, manage policies)
- Resource Manager (get/set IAM policies)
- Cloud Storage (read/write access to the lock bucket)

### Running the Tests Locally

The tests require the `gsm-secret-sync` binary to be built and available. The test expects to find it at `/go/bin/gsm-secret-sync`.

```bash
# Build the gsm-secret-sync binary first
go build -o /go/bin/gsm-secret-sync ./cmd/gsm-secret-sync/

# Set environment variables
export GCP_DEV_PROJECT_ID="your-staging-project"
export GCP_DEV_PROJECT_NUMBER="123456789012"
export GCP_SECRETS_DEV_CREDENTIALS_FILE="/path/to/service-account-key.json"
export GCS_E2E_LOCK_BUCKET="your-lock-bucket"

# Run the tests
go test -tags gsm_e2e ./test/e2e/gsm-secret-sync/ -v
```

## Distributed Locking

To prevent conflicts when multiple e2e test instances run concurrently, the tests implement a distributed locking mechanism using Google Cloud Storage (GCS). This ensures that only one test run can modify the test GCP project at a time.

### How It Works

- **Lock Object**: `gsm-secret-sync-e2e-lock` stored in the configured GCS bucket
- **Atomic Operations**: Uses GCS conditional writes to ensure only one process can acquire the lock
- **Retry Strategy**: 5 attempts with exponential backoff starting at 1 minute
- **Total Max Wait**: ~13 minutes (1m + 1.5m + 2.25m + 3.4m + 5.1m)
- **Automatic Cleanup**: Lock is automatically released when tests complete. In case the test somehow ends without deleting the lock object, as a fail-safe, the bucket is set to delete all files after 1 day.

## Important Notes

- ⚠️ **Use only staging/test projects** - The test creates and deletes real GCP resources
- The test includes cleanup logic but failures may leave resources behind
- Tests handle GCP's eventual consistency with retry mechanisms
- Build tag `gsm_e2e` prevents these tests from running with regular unit/e2e tests

## Troubleshooting

- **Permission errors**: Ensure the service account has all required IAM roles
- **Project mismatch**: Verify `GCP_DEV_PROJECT_ID` and `GCP_DEV_PROJECT_NUMBER` match your staging project
- **Leftover resources**: The test validates a clean project state before running - manually clean up if needed
