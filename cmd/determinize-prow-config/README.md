# determinize-prow-config

Normalizes and formats Prow configuration files for consistency.

## Overview

Determinize-prow-config ensures that Prow configuration files are consistently formatted and ordered. It normalizes YAML structure and ensures deterministic output.

## Usage

```bash
determinize-prow-config \
  --prow-config=/path/to/config.yaml \
  --confirm
```

## Options

- `--prow-config`: Path to Prow configuration file
- `--confirm`: Actually write changes (required for modifications)

## How It Works

1. Loads Prow configuration file
2. Normalizes YAML structure and ordering
3. Sorts fields consistently
4. Writes normalized config back (if --confirm is used)

## Related

- Prow - Kubernetes CI/CD system

