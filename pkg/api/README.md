# pkg/api

Core API package containing data structures and types used throughout CI-Tools.

## Overview

This package defines the fundamental data structures for CI-Tools, including:

- CI Operator configuration structures
- Dependency graph implementation
- Image promotion logic
- Common type definitions
- Step links and parameters

## Key Types

### Configuration

**`ReleaseBuildConfiguration`** (`config.go`):
The main configuration structure that defines:
- Base images to use
- Images to build
- Tests to run
- Image promotion rules
- Release configuration

### Dependency Graph

**`Node`** (`graph.go`):
Represents a step in the execution graph with:
- Step implementation
- Required inputs (`Requires`)
- Created outputs (`Creates`)

**`Graph`** (`graph.go`):
Manages the dependency graph:
- `BuildPartialGraph()` - Builds graph for specific targets
- `TopologicalSort()` - Sorts nodes by dependencies
- `ResolveMultiArch()` - Resolves multi-arch requirements

### Step Interface

**`Step`** (defined in `pkg/steps/step.go`):
Common interface for all execution steps:
```go
type Step interface {
    Inputs() []api.InputDefinition
    Validate() error
    Run(ctx context.Context) error
    Requires() []api.StepLink
    Creates() []api.StepLink
    Provides() (api.ParameterMap, error)
}
```

### Promotion

**`PromotionTarget`** (`promotion.go`):
Defines where images should be promoted:
- Target namespace
- Target name
- Additional images
- Excluded images

## Usage

### Loading Configuration

```go
import "github.com/openshift/ci-tools/pkg/api"

config, err := api.LoadConfig("path/to/config.yaml")
```

### Building Dependency Graph

```go
import "github.com/openshift/ci-tools/pkg/api"

nodes, err := api.BuildPartialGraph(steps, targets)
stepList, errs := nodes.TopologicalSort()
```

### Working with Step Links

```go
import "github.com/openshift/ci-tools/pkg/api"

// Create a step link for an image
link := api.StepLinkForImage("my-image")

// Create a step link for a release
link := api.StepLinkForRelease(api.LatestReleaseName)
```

## Related Packages

- **`pkg/config`**: Configuration loading and validation
- **`pkg/steps`**: Step implementations
- **`pkg/promotion`**: Image promotion logic
- **`pkg/defaults`**: Default value handling

## Documentation

- [Architecture Guide](../../docs/ARCHITECTURE.md) - System architecture
- [CI Operator Reference](https://steps.svc.ci.openshift.org/) - Step registry

