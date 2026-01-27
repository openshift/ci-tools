# pkg/controller

Kubernetes controller implementations for CI-Tools.

## Overview

This package contains Kubernetes controllers that manage CI resources. Controllers follow the standard Kubernetes controller pattern:

- Watch for changes in resources
- Reconcile desired state
- Handle errors and retries gracefully
- Update resource status

## Controllers

### Promotion Reconciler

**`pkg/controller/promotionreconciler/`**:
Manages image promotion based on policies:
- Watches ImageStreams for successful builds
- Promotes images to release streams
- Handles promotion policies and exclusions

### Test Images Distributor

**`pkg/controller/test-images-distributor/`**:
Distributes test images to clusters:
- Watches for new test images
- Mirrors images to target clusters
- Manages image distribution policies

### Service Account Secret Refresher

**`pkg/controller/serviceaccount_secret_refresher/`**:
Refreshes service account secrets:
- Monitors service account secrets
- Rotates secrets periodically
- Updates references in pods

### Ephemeral Cluster Controller

**`pkg/controller/ephemeralcluster/`**:
Manages ephemeral test clusters:
- Provisions clusters on demand
- Monitors cluster lifecycle
- Cleans up expired clusters

### Test ImageStream Import Cleaner

**`pkg/controller/testimagestreamimportcleaner/`**:
Cleans up test ImageStream imports:
- Removes stale imports
- Manages import lifecycle

## Controller Manager

All controllers run in `dptp-controller-manager` (`cmd/dptp-controller-manager/`):

```bash
dptp-controller-manager \
  --kubeconfig=~/.kube/config \
  --controllers=promotionreconciler,testimagesdistributor
```

## Controller Pattern

Controllers implement the reconciler interface:

```go
type Reconciler interface {
    Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error)
}
```

## Utilities

**`pkg/controller/util/`**:
Common utilities for controllers:
- Reconciler helpers
- Error handling
- Retry logic

## Related Packages

- **`pkg/api`**: Core API types
- **`pkg/kubernetes`**: Kubernetes client utilities

## Documentation

- [Architecture Guide](../../docs/ARCHITECTURE.md) - System architecture
- [Controller Manager README](../../cmd/dptp-controller-manager/README.md) - Controller manager documentation

