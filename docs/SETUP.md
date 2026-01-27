# Setup Guide (Beginner Friendly)

This guide will help you set up a development environment for working with CI-Tools.

## Prerequisites

### Required Software

1. **Go** (version 1.24.0 or later)
   ```bash
   # Check your Go version
   go version
   
   # If not installed, download from https://golang.org/dl/
   ```

2. **Git**
   ```bash
   git --version
   # Install via your package manager if needed
   ```

3. **Make**
   ```bash
   make --version
   # Usually pre-installed on Linux/macOS
   ```

4. **Docker** (optional, for building container images)
   ```bash
   docker --version
   ```

### Optional but Recommended

- **kubectl** - For interacting with Kubernetes clusters
- **oc** (OpenShift CLI) - For interacting with OpenShift clusters
- **jq** - For JSON processing in scripts
- **yq** - For YAML processing

## Installation Steps

### 1. Clone the Repository

```bash
# Clone the repository
git clone https://github.com/openshift/ci-tools.git
cd ci-tools

# If you plan to contribute, fork first and clone your fork
```

### 2. Set Up Go Environment

```bash
# Set Go environment variables (if not already set)
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin

# Verify Go is working
go env
```

### 3. Install Dependencies

```bash
# Download and vendor dependencies
make update-vendor

# Or use go mod directly
go mod download
go mod vendor
```

### 4. Build the Tools

```bash
# Build all tools
make build

# Or install to $GOPATH/bin
make install

# Build specific tool
go build ./cmd/ci-operator
```

### 5. Verify Installation

```bash
# Check that tools are installed
which ci-operator
ci-operator --help

# List all available tools
ls $GOPATH/bin/ | grep -E "(ci-|config-|prow-)"
```

## Environment Requirements

### Development Environment

- **Operating System**: Linux or macOS (Windows with WSL2)
- **Memory**: Minimum 8GB RAM (16GB recommended)
- **Disk Space**: At least 10GB free space
- **Network**: Internet connection for downloading dependencies

### For Testing with OpenShift

- Access to an OpenShift cluster (or use CodeReady Containers for local development)
- Valid kubeconfig file
- Appropriate cluster permissions

### For Integration Testing

- Access to `app.ci` OpenShift cluster (for OpenShift team members)
- GitHub token with appropriate permissions
- Access to Vault (for secret management tools)

## How to Run the Project

### Running Individual Tools

Most tools are command-line applications. Here are examples:

#### CI Operator

```bash
# Basic usage
ci-operator \
  --config=path/to/config.yaml \
  --git-ref=org/repo@ref \
  --target=test-name

# With JOB_SPEC environment variable
export JOB_SPEC='{"type":"presubmit","job":"test-job",...}'
ci-operator --config=config.yaml
```

#### Configuration Tools

```bash
# Generate Prow jobs from ci-operator configs
ci-operator-prowgen --from-dir=configs --to-dir=jobs

# Branch configurations
config-brancher --current-release=4.15 --future-release=4.16

# Determinize (normalize) configs
determinize-ci-operator --config-dir=configs --confirm
```

#### Controller Manager

```bash
# Run the controller manager locally
dptp-controller-manager \
  --kubeconfig=~/.kube/config \
  --controllers=promotionreconciler,testimagesdistributor
```

### Running Tests

```bash
# Run all unit tests
make test

# Run specific package tests
go test ./pkg/api/...

# Run with verbose output
go test -v ./pkg/api/...

# Run integration tests
make integration

# Run specific integration suite
make integration SUITE=multi-stage

# Run end-to-end tests (requires cluster access)
make e2e
```

### Running Locally with Docker

Some tools can be run in containers:

```bash
# Build container image
docker build -t ci-tools:latest .

# Run tool in container
docker run --rm ci-tools:latest ci-operator --help
```

## How to Test It

### Unit Testing

```bash
# Run all unit tests
make test

# Run tests for specific package
go test ./pkg/config/...

# Run tests with coverage
go test -cover ./pkg/...

# Generate coverage report
go test -coverprofile=coverage.out ./pkg/...
go tool cover -html=coverage.out
```

### Integration Testing

```bash
# Run integration tests
make integration

# Update golden files (if test outputs changed)
make update-integration

# Run specific suite
make integration SUITE=config-brancher
```

### Manual Testing

1. **Test CI Operator Locally:**
   ```bash
   # Create a test config
   cat > test-config.yaml <<EOF
   base_images:
     base:
       namespace: openshift
       name: base
       tag: "4.15"
   tests:
   - as: unit
     commands: echo "test"
   EOF
   
   # Run with dry-run (if supported)
   ci-operator --config=test-config.yaml --dry-run
   ```

2. **Test Configuration Tools:**
   ```bash
   # Test config-brancher with sample configs
   config-brancher \
     --current-release=4.15 \
     --future-release=4.16 \
     --config-dir=test/integration/config-brancher/input
   ```

## Common Errors and Solutions

### Error: "go: cannot find module"

**Solution:**
```bash
# Ensure you're in the repository root
cd /path/to/ci-tools

# Download dependencies
go mod download

# Or update vendor
make update-vendor
```

### Error: "command not found: ci-operator"

**Solution:**
```bash
# Install tools
make install

# Or add $GOPATH/bin to PATH
export PATH=$PATH:$GOPATH/bin

# Or build and use directly
go build ./cmd/ci-operator
./ci-operator --help
```

### Error: "permission denied" when accessing cluster

**Solution:**
```bash
# Check kubeconfig
kubectl config current-context

# Verify cluster access
kubectl get nodes

# Check if you need to login
oc login <cluster-url>
```

### Error: "cannot load package" or import errors

**Solution:**
```bash
# Clean module cache
go clean -modcache

# Re-download dependencies
go mod download

# Verify go.mod is correct
go mod verify
```

### Error: "no such file or directory" for test data

**Solution:**
```bash
# Ensure you're running from repository root
pwd  # Should show /path/to/ci-tools

# Check test data exists
ls test/integration/
```

### Error: Build failures due to missing dependencies

**Solution:**
```bash
# Update vendor directory
make update-vendor

# Or use go mod
go mod tidy
go mod vendor
```

### Error: TypeScript compilation errors (for frontend tools)

**Solution:**
```bash
# Install Node.js dependencies
cd cmd/pod-scaler/frontend
npm install

# Or use make target
make npm-pod-scaler NPM_ARGS="ci"

# Build frontend
make cmd/pod-scaler/frontend/dist
```

### Error: "context deadline exceeded" when testing

**Solution:**
- Increase timeout: `go test -timeout=10m ./...`
- Check cluster connectivity
- Verify resource availability

## Development Workflow

### 1. Make Changes

```bash
# Create a feature branch
git checkout -b feature/my-feature

# Make your changes
# ...

# Format code
make format

# Run tests
make test
```

### 2. Verify Changes

```bash
# Run linters
make lint

# Verify code generation
make verify-gen

# Run integration tests
make integration
```

### 3. Commit Changes

```bash
# Stage changes
git add .

# Commit with descriptive message
git commit -m "Add feature X"

# Push to your fork
git push origin feature/my-feature
```

## IDE Setup

### VS Code

Recommended extensions:
- Go extension
- YAML extension
- Kubernetes extension

Settings:
```json
{
  "go.useLanguageServer": true,
  "go.formatTool": "goimports",
  "editor.formatOnSave": true
}
```

### GoLand / IntelliJ

- Install Go plugin
- Configure Go SDK
- Enable goimports on save

## Next Steps

After setting up your environment:

1. Read the [Usage Guide](USAGE.md) to learn how to use the tools
2. Explore the [Codebase Walkthrough](CODEBASE_WALKTHROUGH.md) to understand the structure
3. Check the [Contributing Guide](CONTRIBUTING_GUIDE.md) if you want to contribute
4. Review the [Onboarding Guide](ONBOARDING.md) for deep dive

## Getting Help

- Check existing documentation
- Search existing issues on GitHub
- Ask in #forum-testplatform on Slack (for OpenShift team members)
- Create a new issue with detailed error information

