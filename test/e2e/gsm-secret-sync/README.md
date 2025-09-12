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

- **Project ID**: Set via `GCP_PROJECT_ID` environment variable
- **Project Number**: Set via `GCP_PROJECT_NUMBER` environment variable  
- **Service Account Key**: Path set via `GOOGLE_APPLICATION_CREDENTIALS`

The service account needs permissions for:

- Secret Manager (create/delete secrets)
- IAM (create/delete service accounts, manage policies)
- Resource Manager (get/set IAM policies)

### Running the Tests Locally

The tests require the `gsm-secret-sync` binary to be built and available. The test expects to find it at `/go/bin/gsm-secret-sync`.

```bash
# Build the gsm-secret-sync binary first
go build -o /go/bin/gsm-secret-sync ./cmd/gsm-secret-sync/

# Set environment variables
export GCP_PROJECT_ID="your-staging-project"
export GCP_PROJECT_NUMBER="123456789012"
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/service-account-key.json"

# Run the tests
go test -tags gsm_e2e ./test/e2e/gsm-secret-sync/ -v
```

## Important Notes

- ⚠️ **Use only staging/test projects** - The test creates and deletes real GCP resources
- The test includes cleanup logic but failures may leave resources behind
- Tests handle GCP's eventual consistency with retry mechanisms
- Build tag `gsm_e2e` prevents these tests from running with regular unit/e2e tests

## Troubleshooting

- **Permission errors**: Ensure the service account has all required IAM roles
- **Project mismatch**: Verify `GCP_PROJECT_ID` and `GCP_PROJECT_NUMBER` match your staging project
- **Leftover resources**: The test validates a clean project state before running - manually clean up if needed
