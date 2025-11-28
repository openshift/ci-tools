# check-cluster-profiles-config

Validates cluster profile configurations against actual cluster resources.

## Overview

This tool validates that cluster profile configurations defined in CI operator configs match the actual cluster profiles available in the cluster. It ensures consistency between configuration and reality.

## Usage

```bash
check-cluster-profiles-config \
  --config-path=/path/to/cluster-profile-config.yaml
```

## Options

- `--config-path`: Path to the cluster profile config file

## How It Works

1. Loads cluster profile configuration
2. Connects to Kubernetes cluster
3. Validates that configured profiles exist and match cluster resources
4. Reports any mismatches or missing profiles

## Related

- [pkg/api](../../pkg/api) - Cluster profile definitions

