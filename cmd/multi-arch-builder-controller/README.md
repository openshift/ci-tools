# multi-arch-builder-controller

## What
Kubernetes controller that reconciles `MultiArchBuildConfig` custom resources into per-architecture OpenShift `Build` objects, assembles a multi-architecture container image manifest, and optionally mirrors the result to external registries. This enables building and publishing multi-arch images (e.g., amd64 + arm64 + s390x) within the CI infrastructure.

## How it works -- full flow

### Startup
1. Discovers available architectures by listing cluster nodes and collecting their `kubernetes.io/arch` labels
2. Registers the controller with watches on `MultiArchBuildConfig` resources and owned `Build` resources

### Reconciliation loop
When a `MultiArchBuildConfig` (MABC) is created or updated, the controller progresses through a state machine:

#### Phase 1: Create builds
- For each discovered architecture, create an OpenShift `Build` object
- Each Build is a copy of the MABC's `BuildSpec` with `nodeSelector` set to `kubernetes.io/arch: <arch>`
- The output image tag is suffixed with the architecture name (e.g., `myimage-amd64`, `myimage-arm64`)
- Builds are created with an owner reference back to the MABC

#### Phase 2: Wait for builds to complete
- On each reconcile, list Builds owned by the MABC
- If not all builds are finished, return and wait for the next event (Build status change triggers re-reconcile via owner reference)
- If any build fails, set MABC state to `Failure` and stop

#### Phase 3: Push manifest list
- Once all per-arch builds succeed, use `manifestpusher.PushImageWithManifest()` to create a multi-architecture manifest list
- The manifest list points to each architecture-specific image in the internal registry (`image-registry.openshift-image-registry.svc:5000`)
- The target image reference is derived from the MABC's output spec
- On failure, set MABC state to `Failure`

#### Phase 4: Mirror to external registries
- If `spec.externalRegistries` is set, use `oc image mirror` to push the multi-arch manifest to each external registry
- If no external registries are configured, skip with a success condition
- On failure, set MABC state to `Failure`

#### Completion
- Set MABC state to `Success`
- On subsequent reconciles, skip MABCs already in `Success` or `Failure` state

### Status conditions
The MABC status tracks progress through conditions:
- `CreateBuildsDone` -- all per-architecture Builds created
- `BuildsCompleted` -- all Builds finished (success or failure)
- `PushManifestDone` -- manifest list pushed to internal registry
- `ImageMirrorDone` -- manifest mirrored to external registries (or skipped)

### Concurrency
Max concurrent reconciles is 1 to avoid conflicts on shared resources.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--dry-run` | `true` | Dry-run mode: no actual resource creation |
| `--docker-cfg` | `/.docker/config.json` | Path to registry credentials for manifest push and image mirror |

## Key files
- `cmd/multi-arch-builder-controller/main.go` -- entry point, node architecture discovery, manager setup
- `pkg/controller/multiarchbuildconfig/multiarchbuildconfig.go` -- reconciler: Build creation, manifest push orchestration, image mirroring, status management
- `pkg/controller/multiarchbuildconfig/mirror.go` -- `oc image mirror` wrapper for external registry mirroring
- `pkg/api/multiarchbuildconfig/v1/` -- MABC CRD types
- `pkg/manifestpusher/` -- multi-arch manifest list assembly and push

## Deployment
Long-lived controller-runtime Deployment on a heterogeneous (multi-arch) build cluster. Requires in-cluster access with permissions to create Builds, read nodes (for architecture discovery), and access the internal image registry.

Registry credentials (`--docker-cfg`) must include push access to both the internal registry and any configured external registries.
```console
$ ./multi-arch-builder-controller --help
Usage of ./multi-arch-builder-controller:
  -dry-run
    	Whether to run the controller-manager with dry-run (default true)
```


## Requirements

- target registry credentials mounted on /.docker/config.json 
