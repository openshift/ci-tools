# ci-operator-config-mirror

Mirrors ci-operator configuration files from one organization to another in the openshift/release repository.

## Overview

This tool copies ci-operator configuration files from one GitHub organization to another within the openshift/release repository. It's useful for maintaining separate configurations for different organizational structures.

## Usage

```bash
ci-operator-config-mirror \
  --config-dir=ci-operator/config \
  --to-org=target-org \
  --only-org=source-org \
  --confirm
```

## Options

- `--config-dir`: Directory containing ci-operator configs
- `--to-org`: Target organization to mirror configs to
- `--only-org`: Only mirror configs from this organization (optional)
- `--clean`: Delete existing target org directory before mirroring (default: true)
- `--confirm`: Actually perform the mirroring (required)

## How It Works

1. Scans source organization's config files
2. Copies them to the target organization directory
3. Updates promotion namespaces if needed (ocp vs ocp-private)
4. Optionally cleans existing target directory first

## Example

Mirror all configs from `openshift` org to `myorg`:

```bash
ci-operator-config-mirror \
  --config-dir=ci-operator/config \
  --to-org=myorg \
  --only-org=openshift \
  --confirm
```

