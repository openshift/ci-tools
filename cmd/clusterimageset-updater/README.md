# clusterimageset-updater

Updates ClusterImageSet resources based on cluster pool specifications.

## Overview

This tool generates ClusterImageSet YAML files from cluster pool specifications. It reads cluster pool YAML files and creates corresponding ClusterImageSet resources for use in cluster provisioning.

## Usage

```bash
clusterimageset-updater \
  --pools=/path/to/cluster-pools \
  --imagesets=/path/to/output
```

## Options

- `--pools`: Path to directory containing cluster pool specs (*_clusterpool.yaml files)
- `--imagesets`: Path to directory for output ClusterImageSet files (*_clusterimageset.yaml files)

## How It Works

1. Scans input directory for cluster pool YAML files
2. Extracts version information from pool labels
3. Generates ClusterImageSet resources with appropriate version streams
4. Writes output files to the specified directory

## Related

- [pkg/release](../../pkg/release) - Release and version management

