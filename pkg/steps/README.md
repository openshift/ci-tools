# pkg/steps

Step execution framework for CI-Tools. Provides the building blocks for CI job execution.

## Overview

The steps package implements the execution framework for CI-Tools. Steps are composable building blocks that:

- Declare their inputs and outputs
- Execute specific actions (build images, run tests, etc.)
- Can provide parameters to dependent steps
- Are executed in dependency order based on a graph

## Step Interface

All steps implement the `Step` interface:

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

## Step Types

### Build Steps

- **`src`**: Source code checkout
- **`bin`**: Binary build
- **`rpms`**: RPM build
- **`images`**: Container image builds

### Test Steps

- **`test`**: Generic test execution
- **`e2e`**: End-to-end tests
- **`integration`**: Integration tests

### Utility Steps

- **`template`**: Template execution
- **`promote`**: Image promotion
- **`release`**: Release tagging

## Step Registry

The step registry (`pkg/steps/registry/`) contains reusable step definitions that can be referenced in CI configurations.

## Execution

Steps are executed via the `Run()` function which:

1. Builds a dependency graph from steps
2. Topologically sorts steps by dependencies
3. Executes steps in order (parallel where possible)
4. Collects artifacts from completed steps
5. Handles errors and retries

## Usage

### Creating a Custom Step

```go
type MyStep struct {
    // step fields
}

func (s *MyStep) Requires() []api.StepLink {
    return []api.StepLink{api.StepLinkForImage("base-image")}
}

func (s *MyStep) Creates() []api.StepLink {
    return []api.StepLink{api.StepLinkForImage("my-image")}
}

func (s *MyStep) Run(ctx context.Context) error {
    // step implementation
    return nil
}
```

## Related Packages

- **`pkg/api`**: Core API types and graph structures
- **`pkg/config`**: Configuration loading
- **`pkg/promotion`**: Image promotion logic

## Documentation

- [Architecture Guide](../../docs/ARCHITECTURE.md) - System architecture
- [CI Operator Reference](https://steps.svc.ci.openshift.org/) - Step registry documentation

