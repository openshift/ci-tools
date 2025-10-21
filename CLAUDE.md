# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Purpose

This repository contains tooling for OpenShift CI, providing a comprehensive suite of CLI tools and libraries for managing CI/CD infrastructure, Prow jobs, configuration generation, and automated workflows across the OpenShift ecosystem. For detailed documentation, refer to https://docs.ci.openshift.org/.

## Build, Test, and Development Commands

### Building and Installing

```bash
# Build and install all Go binaries to $GOPATH/bin
make install

# Production build with version info (includes frontend builds)
make production-install

# Build with race detector enabled
make race-install
```

### Testing

```bash
# Run all unit tests (uses gotestsum with race detector)
make test

# Run specific test packages
PACKAGES=./pkg/api/... make test

# Run specific test with filter
TESTFLAGS='--run TestProduce' make test

# Update test golden files
UPDATE=true make test
# or for unit tests specifically:
make update-unit

# Run integration tests (runs 10x in CI, 1x locally)
make integration

# Run specific integration suite
make integration SUITE=multi-stage

# Update integration test golden files
make update-integration

# Run e2e tests (requires cluster access)
make e2e

# Run specific e2e test
make e2e PACKAGES=test/e2e/pod-scaler TESTFLAGS='--run TestProduce'
```

### Code Quality and Formatting

```bash
# Format all code (Go + frontend)
make format

# Format only Go code
make gofmt

# Verify code formatting and conventions
make verify

# Run linter
make lint

# Verify generated code is up to date
make verify-gen

# Regenerate code (imports, codegen, CI operator reference)
make generate
```

### Vendor Management

```bash
# Update vendor dependencies (uses Docker container)
make update-vendor

# Validate vendor is up to date
make validate-vendor
```

### Frontend Development

The repository includes React-based frontends for `pod-scaler` and `repo-init`:

```bash
# Build frontend distributions
make cmd/pod-scaler/frontend/dist
make cmd/repo-init/frontend/dist

# Format frontend code
make frontend-format

# Run frontend checks (linting, tests)
make frontend-checks

# Run pod-scaler UI locally in dev mode
make local-pod-scaler-ui
```

### Running Tests for a Single Package/File

```bash
# Test a single package
go test -race -v ./pkg/api

# Test with a specific test function
go test -race -v ./pkg/api -run TestConfigLoading

# Test specific package containing a file
go test -race -v ./pkg/config
```

## Major Components and Architecture

### Core Architectural Patterns

1. **Configuration-Driven Design**
   - Heavily relies on declarative YAML-based configuration management
   - The `pkg/api` package defines strongly-typed Go structs for all configuration formats with extensive validation and defaulting
   - CI operator configs define test and build workflows for individual repositories
   - Configurations are managed across hundreds of OpenShift repositories with tooling for branching, promotion, and lifecycle management

2. **Modular Command-Line Tools**
   - The `cmd/` directory contains 80+ specialized tools, each purpose-built for specific CI/CD tasks
   - Key tools include:
     - `ci-operator`: Core test execution engine
     - `ci-operator-prowgen`: Generates Prow jobs from CI operator configs
     - `config-brancher`: Creates new release branch configurations
     - `prow-job-dispatcher`: Routes jobs to appropriate clusters
     - `pod-scaler`: Analyzes and optimizes resource requests based on metrics
     - `backporter`: Manages backport workflows via GitHub/Jira/Bugzilla
     - `slack-bot`: Slack integration for team workflows
     - `vault-secret-collection-manager`: Manages secrets in Vault

3. **Key Package Architecture**

   **`pkg/api/`** - Core data models and type definitions:
   - `types.go`: CI operator config structures (`ReleaseBuildConfiguration`)
   - `config.go`: Configuration loading and validation
   - `graph.go`: Multi-stage test workflow composition
   - `promotion.go`: Image promotion between registries
   - `job_spec.go`: Prow job specifications
   - `metadata.go`: Org/repo/branch identification

   **`pkg/config/`** - Configuration management:
   - Config resolution across branches and variants
   - Sharded Prow config management
   - Multi-architecture build configuration
   - Secret bootstrapping and generation

   **`pkg/controller/`** - Kubernetes controllers:
   - Test image stream tag imports
   - Pull request payload qualification
   - Automated image promotion
   - Secret management
   - Follows standard controller-runtime patterns with reconciliation loops

   **`pkg/dispatcher/`** - Job dispatching infrastructure:
   - Job routing and cluster selection
   - Execution tracking across multiple clusters

   **Step Registry** - Library of reusable test steps:
   - Steps stored as YAML/shell scripts
   - Composable into multi-stage tests
   - Referenced by name in test configurations

### Key Workflows and Patterns

#### Configuration Loading Pattern

Most tools follow this standard pattern:
1. Load metadata (org/repo/branch) from job spec or flags
2. Resolve appropriate config file path
3. Load and validate YAML config into API types
4. Apply defaults and perform semantic validation
5. Execute tool-specific logic

#### Adding a New CI Operator Config

1. Create YAML config in appropriate org/repo/branch structure
2. Run `make verify-gen` to ensure it's normalized
3. Generate Prow jobs: `ci-operator-prowgen --config-path=...`
4. Validate with `ci-operator-checkconfig`

#### Branching for a New Release

1. Run `config-brancher` to create new branch configs
2. Update promotion targets for the new release
3. Generate new Prow jobs
4. Update TestGrid dashboards

#### Adding a New Multi-Stage Test

1. Define test steps in the step registry (YAML + shell scripts)
2. Reference steps in CI operator config's `tests` section
3. Use `graph.go` logic to compose steps into workflow
4. Test locally with `ci-operator --config=... --target=...`

### Testing Strategy

1. **Unit Tests**: Comprehensive coverage with table-driven tests, using testify for assertions
2. **Integration Tests**: Golden file-based tests in `test/integration/` with UPDATE flag for regenerating expected outputs
3. **E2E Tests**: Full end-to-end tests requiring cluster access, tagged with `e2e,e2e_framework`
4. **Mocking**: Uses `go.uber.org/mock` for interface mocking (see `hack/update-mocks.sh`)

## Development Tips

### TypeScript Compilation

Some tools include TypeScript (e.g., `vault-secret-collection-manager`). The build system automatically compiles TS to JS via `hack/compile-typescript.sh` when needed.

### Docker-Based Tooling

Vendor updates run in Docker containers to ensure consistent Go versions (see `update-vendor` target which uses `registry.ci.openshift.org/openshift/release:rhel-9-release-golang-1.23-openshift-4.19`).

### Golden File Testing

When tests fail with "golden file mismatch", run with `UPDATE=true` to regenerate expected outputs, then carefully review the diff before committing.

### Working with the Release Repository

Many tools expect a sibling `release` repository (configured via `release_folder` in Makefile). This contains the actual CI configurations that these tools generate and manage.

### PR Deployment

Several tools support PR-based deployment for testing changes:
```bash
# Deploy a PR version of configresolver
make pr-deploy-configresolver PULL_REQUEST=1234

# Deploy other services
make pr-deploy-backporter PULL_REQUEST=1234
make pr-deploy-vault-secret-manager PULL_REQUEST=1234
make pr-deploy-repo-init-api PULL_REQUEST=1234
```

### Registry-Backed Resources

Many resources (secrets, configs, images) are backed by Git repositories:
- Changes are made via PR to config repos
- CI validates changes before merge
- Post-merge automation applies changes to clusters
- This creates an audit trail and enables GitOps workflows

## Important Notes

### Main Branch

The main development branch is **`master`** (not `main`). When creating pull requests, target `master`.

### Code Review Process

Follow the guidelines in CONTRIBUTING.md:
- Keep changes small and independent
- PRs require both `/lgtm` from reviewers and `/approve` from approvers
- Ensure presubmits pass before requesting review
- Author is responsible for verifying changes work in production after merge
