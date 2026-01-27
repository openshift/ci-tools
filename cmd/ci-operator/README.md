# ci-operator

The core orchestration engine for OpenShift CI. Reads declarative YAML configurations and executes multi-stage image builds and tests on OpenShift clusters.

## Overview

`ci-operator` is the main tool that orchestrates CI jobs for OpenShift projects. It:

- Reads YAML configuration files that define builds, tests, and image promotion
- Builds a dependency graph to determine execution order
- Executes steps in parallel where possible
- Manages Kubernetes resources (Builds, Pods, ImageStreams)
- Collects artifacts and promotes images after successful builds

## Usage

### Basic Usage

```bash
ci-operator \
  --config=path/to/config.yaml \
  --git-ref=org/repo@branch \
  --target=test-name
```

### With JOB_SPEC (typical in Prow)

```bash
export JOB_SPEC='{
  "type":"presubmit",
  "job":"pull-ci-openshift-kubernetes-main-unit",
  "refs":{
    "org":"openshift",
    "repo":"kubernetes",
    "base_ref":"main",
    "base_sha":"abc123def456",
    "pulls":[{"number":12345,"author":"user","sha":"def789"}]
  }
}'
ci-operator --config=config.yaml
```

### Common Options

- `--config`: CI operator config file (required)
- `--git-ref`: Git reference in format `org/repo@branch`
- `--target`: Specific target to run (image name or test name)
- `--namespace`: Kubernetes namespace (auto-generated if not provided)
- `--promote`: Promote images after successful build
- `--artifact-dir`: Directory for artifact collection
- `--print-graph`: Print dependency graph and exit
- `--dry-run`: Show what would be done without executing

## How It Works

1. **Configuration Loading**: Loads and validates YAML configuration
2. **Graph Building**: Builds dependency graph from config to determine execution order
3. **Input Resolution**: Resolves base images and other inputs
4. **Namespace Creation**: Creates a namespace for the build
5. **Step Execution**: Executes steps in dependency order (builds images, runs tests)
6. **Artifact Collection**: Collects artifacts from completed steps
7. **Image Promotion**: Promotes images to release streams (if `--promote` is used)
8. **Cleanup**: Cleans up namespace (if configured)

## Configuration

CI Operator configs are YAML files with this structure:

```yaml
base_images:
  base:
    namespace: openshift
    name: base
    tag: "4.15"

build_root:
  image_stream_tag:
    namespace: openshift
    name: release
    tag: golang-1.21

images:
- dockerfile_path: Dockerfile
  to: my-image

tests:
- as: unit
  commands: make test
  container:
    from: my-image
```

See [pkg/api/config.go](../../pkg/api/config.go) for the full configuration structure.

## Key Components

- **Configuration Loading**: `pkg/config/load.go`
- **Graph Building**: `pkg/api/graph.go`
- **Step Execution**: `pkg/steps/`
- **Image Promotion**: `pkg/promotion/`

## Examples

### Running a Test

```bash
ci-operator \
  --config=config.yaml \
  --git-ref=myorg/myrepo@main \
  --target=unit
```

### Building and Promoting Images

```bash
ci-operator \
  --config=config.yaml \
  --git-ref=openshift/origin@main \
  --target=images \
  --promote
```

### Debugging

```bash
# Print dependency graph
ci-operator --config=config.yaml --print-graph

# Run with verbose logging
ci-operator --config=config.yaml --log-level=debug
```

## Related Documentation

- [Architecture Guide](../../docs/ARCHITECTURE.md) - System architecture and design
- [CI Operator Reference](https://steps.svc.ci.openshift.org/) - Step registry documentation
- [OpenShift CI Documentation](https://docs.ci.openshift.org/) - Official CI documentation

