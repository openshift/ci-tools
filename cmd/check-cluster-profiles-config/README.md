# check-cluster-profiles-config

## What
Validates the cluster profile configuration file used by OpenShift CI. Checks for structural correctness (no duplicate profiles or duplicate orgs within a profile) and verifies that the required Kubernetes Secret for each profile exists in the `ci` namespace. Uses the ci-operator config resolver to look up the secret name for each profile.

## How it works -- full flow

1. **Load config**: Read and unmarshal the cluster profiles YAML file from `--config-path`. The file contains a `ClusterProfilesList` -- an array of profile definitions, each with a profile name and a list of owners (org references).

2. **Validate structure** (`Validate`):
   - For each profile in the list:
     - Check that no org appears more than once within the same profile's owners list
     - Check that the profile name has not already been defined earlier in the file (no duplicate profiles)
   - Build an internal `ClusterProfilesMap` for subsequent checks.

3. **Verify secrets** (`checkCiSecrets`):
   - For each validated profile:
     - Query the ci-operator config resolver (`config.ci.openshift.org`) via `NewResolverClient().ClusterProfile()` to get the profile's details, including its secret name
     - Attempt to `GET` the corresponding Secret from the `ci` namespace on the local cluster
     - Fatal if the secret does not exist or cannot be retrieved

4. If all checks pass, log success and exit 0. If any check fails, fatal with the error.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--config-path` | `""` | Path to the cluster profile configuration YAML file |

## Key files

- `cmd/check-cluster-profiles-config/main.go` -- all logic: config loading, validation, secret verification
- `pkg/api/types.go` -- `ClusterProfilesMap`, `ClusterProfilesList` types
- `pkg/registry/server/` -- `NewResolverClient()` for querying the config resolver

## Deployment
One-shot CLI tool, typically run in CI (presubmit or postsubmit) to validate changes to the cluster profiles config. Requires in-cluster access to the `ci` namespace for secret verification and network access to the config resolver service.
