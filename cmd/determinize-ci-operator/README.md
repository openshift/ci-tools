# determinize-ci-operator

Normalizes and formats ci-operator configuration files for consistency.

## Overview

Determinize-ci-operator ensures that ci-operator configuration files are consistently formatted and ordered. It normalizes YAML structure, sorts fields, and ensures deterministic output.

## Usage

```bash
determinize-ci-operator \
  --config-dir=ci-operator/config \
  --confirm
```

## Options

- `--config-dir`: Directory containing ci-operator configs
- `--confirm`: Actually write changes (required for modifications)
- `--prow-jobs-dir`: Directory containing prow job configs (optional)

## How It Works

1. Loads all ci-operator configuration files
2. Normalizes YAML structure and ordering
3. Sorts fields consistently
4. Writes normalized configs back (if --confirm is used)

## Use Cases

- Ensure consistent formatting across all configs
- Prepare configs for commit
- Validate config structure

## Related

- [pkg/config](../../pkg/config) - Configuration loading and management

