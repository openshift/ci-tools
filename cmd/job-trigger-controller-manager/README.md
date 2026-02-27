# job-trigger-controller-manager

Kubernetes controller manager for triggering Prow jobs.

## Overview

This controller manager runs controllers that watch for events and trigger Prow jobs based on configured rules and conditions.

## Usage

```bash
job-trigger-controller-manager \
  --kubeconfig=/path/to/kubeconfig
```

## Options

- `--kubeconfig`: Path to kubeconfig file
- Additional controller-specific options

## Related

- [pkg/controller](../../pkg/controller) - Controller implementations

