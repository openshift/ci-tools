# Contributing to ci-tools

Thank you for your interest in contributing to CI-Tools! This guide will help you understand how to contribute effectively.

## How to Contribute

### 1. Fork and Clone

```bash
# Fork the repository on GitHub, then clone your fork
git clone https://github.com/YOUR_USERNAME/ci-tools.git
cd ci-tools

# Add upstream remote
git remote add upstream https://github.com/openshift/ci-tools.git
```

### 2. Create a Branch

```bash
# Create a feature branch from main
git checkout -b feature/my-feature

# Or a bugfix branch
git checkout -b fix/bug-description
```

### 3. Make Your Changes

- Write clear, readable code
- Follow Go conventions and style
- Add tests for new functionality
- Update documentation as needed

### 4. Test Your Changes

```bash
# Run unit tests
make test

# Run linters
make lint

# Run integration tests (if applicable)
make integration

# Verify code generation
make verify-gen
```

### 5. Commit Your Changes

```bash
# Stage changes
git add .

# Commit with descriptive message
git commit -m "Add feature: description of changes"
```

**Commit Message Guidelines:**
- Use imperative mood ("Add feature" not "Added feature")
- Keep first line under 72 characters
- Add detailed description if needed
- Reference issues: "Fix #123: description"

### 6. Push and Create Pull Request

```bash
# Push to your fork
git push origin feature/my-feature
```

Then create a Pull Request on GitHub with:
- Clear title and description
- Reference to related issues
- Screenshots/logs if applicable
- Checklist of what was tested

## Branching Model

### Branch Naming

- `feature/description` - New features
- `fix/description` - Bug fixes
- `docs/description` - Documentation updates
- `refactor/description` - Code refactoring
- `test/description` - Test improvements

### Branch Strategy

- **main** - Production-ready code
- **Feature branches** - Created from main, merged back via PR
- **Release branches** - For release-specific fixes (if needed)

## Coding Standards

### Go Style

Follow [Effective Go](https://golang.org/doc/effective_go) and [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments).

**Formatting:**
```bash
# Use gofmt
make gofmt

# Or goimports (handles imports)
go run ./vendor/github.com/openshift-eng/openshift-goimports/ -m github.com/openshift/ci-tools
```

**Key Guidelines:**
- Use `gofmt` for formatting
- Run `goimports` to organize imports
- Follow naming conventions (exported = Capital, unexported = lowercase)
- Keep functions focused and small
- Add comments for exported functions/types

### Code Organization

- **Packages**: Group related functionality
- **Files**: Keep files focused (one main type per file when possible)
- **Tests**: `*_test.go` files alongside source
- **Test Data**: Use `testdata/` directories

### Error Handling

```go
// Good: Wrap errors with context
if err != nil {
    return fmt.Errorf("failed to load config: %w", err)
}

// Good: Use errors.Is and errors.As for error checking
if errors.Is(err, os.ErrNotExist) {
    // handle
}
```

### Logging

```go
// Use logrus for structured logging
import "github.com/sirupsen/logrus"

logrus.WithFields(logrus.Fields{
    "config": configPath,
    "error": err,
}).Error("Failed to load config")
```

### Testing

**Unit Tests:**
```go
func TestFunction(t *testing.T) {
    // Arrange
    input := "test"
    
    // Act
    result := Function(input)
    
    // Assert
    if result != expected {
        t.Errorf("Expected %v, got %v", expected, result)
    }
}
```

**Table-Driven Tests:**
```go
func TestFunction(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {
            name:     "normal case",
            input:    "test",
            expected: "result",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := Function(tt.input)
            if result != tt.expected {
                t.Errorf("got %v, want %v", result, tt.expected)
            }
        })
    }
}
```

## The Code Review Process
### Submit a PR

The author submits a PR. The PR should contain the following to help the reviewers and approvers understand the PR and make the code review process more efficient:
- a short summary of what is being done.
- an informative body of what problem the PR solves and how it solves the problem. If the PR also brings any shortcomings or limitations, they should be mentioned too.
- the identifier of the Jira card (or an upstream issue) if applicable. Then the Jira plugin will link the PR to the Jira card.
- log or other types of output to show the problem that it attempts to solve, or the result of the solved problem
- a preview or a screenshot based on the PR if the PR is about a UI change.

The author should check and ensure the presubmits of the PR run successfully.
In addition, the author should keep the change in a PR [small, simple](https://google.github.io/eng-practices/review/developer/small-cls.html) and independent, and [respond to the comments](https://google.github.io/eng-practices/review/developer/handling-comments.html) from the reviewers.

### Assign Reviewers and Approvers
The [blunderbuss](https://github.com/kubernetes/test-infra/tree/master/prow/plugins/blunderbuss) plugin chooses the reviewers and the approvers defined in the [OWNERS](https://www.kubernetes.dev/docs/guide/owners/) files. All TP members are both reviewers and approvers of the test platform’s github repositories.

#### Reviewers
Reviewers look for: general code quality, correctness, sane software engineering, style, etc.
Anyone in the organization, besides the chosen reviewers except the author of the PR, can act as a reviewer.
If the changes made by the PR look good to them, a reviewer types `/lgtm` in a PR comment; if they change their mind, `/lgtm cancel`.

#### Approvers
The PR author `/assign`s the suggested approvers to approve the PR.
Only the approvers listed in the OWNERS file can approve the PR.
Approvers look for holistic acceptance criteria, including dependencies with other features, forwards/backwards compatibility, API and flag definitions, etc
If the changes made by the PR look good to them, a reviewer types `/approve` in a PR comment; if they change their mind, `/approve cancel`.

### PR Merge Automation
Once all the conditions are satisfied, [Tide](https://github.com/kubernetes/test-infra/blob/master/prow/cmd/tide/README.md) merges the PR.

### Sanity Check After Merging
The PR author is responsible for ensuring the change lands in the production system and works as expected. If the users get impacted by an error caused by the change, revert the PR and roll back the system to give space to think of the fix. If no one is around to approve the reverted PR, impersonate the merge robot whose credentials are stored in BitWarden to do the green button merge with a comment on the PR indicating who is behind the scenes. This is only for the cases where our production system does not work properly and the revert is going to fix it.

## The Code Review Guidelines
In general, reviewers should favor approving a PR once it is in a state where it definitely improves the overall code health of the system being worked on, even if it isn’t perfect.

### Design and Functionality
Reviewers should take the following into account:
- Does the PR bring a useful feature to the system? Does the PR implement the feature requested in a Jira card from the DP team’s current sprint?
- Should the functionality be refactored into an existing tool? For example, if someone added a new tool rather than enhancing an existing one.
- Is there any existing code that can be refactored and/or reused in the PR?
- How to verify if the new feature or the fix from the PR works after it lands in production?

### PR size

If the size of a PR is too big, reviewers can think about breaking it into smaller ones.

### Tests
[The Pull Request Workflow section](https://docs.google.com/document/d/1Qd4qcRHUxk5-eiFIjQm2TTH1TaGQ-zhbphLNXxyvr00/edit?usp=sharing) as described in “Definition of Done”

### Naming
A good name is long enough to fully communicate what the item is or does, without being so long that it becomes hard to read.

### Comments
- Is the comment useful? E.g., explaining why some code exists.
- Is there a TODO comment that can be removed since  the PR does the TODO?
- GoLang documentation on Classes/Functions/Fields are also comments. Are they written properly?

### Documentation
Should the change from the PR be documented in the README file? Should the README file be created for the PR if it is for a new tool? Should the ci-docs site be updated accordingly in case the change will impact our CI users?

### Every line
Check every line of human written code. There should be automation checking the generated code. The reviewers should ask questions until they understand what the code is doing.

For the critical or complex changes, it is acceptable to review the code partially, comment LGTM on the part and ask other reviewers to cover the rest.

### Errors Handling
- Are the errors handled correctly in the PR? Should it be ignored, logged, wrapped and raised up?
- Is the error message informative enough for the developer to understand the error?

### Logging
- Is the correct logger used? File, Standard error?
- Is the level correct?

### Parallel Programming
- Is there a potential deadlock?
- Is there a racing condition?

### Security
- Does it expose any sensitive information to the Internet?
- Could an API be abused?

### Impact After Merging
- Do we need to announce the change to avoid users’ surprises?
- Could the PR cause orphaned objects in the production? Should we do the cleanup manually?

### Good Things
_If you see something nice in the CL, tell the developer, especially when they addressed one of your comments in a great way. Code reviews often just focus on mistakes, but they should offer encouragement and appreciation for good practices, as well. It’s sometimes even more valuable, in terms of mentoring, to tell a developer what they did right than to tell them what they did wrong._ [1]

## FAQ

### General Questions

**What is CI-Tools?**
CI-Tools is a collection of command-line utilities and services that power the OpenShift Continuous Integration system. It provides tools for managing CI configurations, orchestrating builds, running tests, and managing the CI infrastructure.

**Who maintains CI-Tools?**
The OpenShift Test Platform (TP) team maintains CI-Tools. See the [OWNERS](OWNERS) file for the list of maintainers.

**How do I get started?**
1. Read the [docs/README.md](docs/README.md) for overview
2. Set up your environment ([docs/SETUP.md](docs/SETUP.md))
3. Explore the codebase ([docs/ARCHITECTURE.md](docs/ARCHITECTURE.md))

### Development Questions

**How do I build the project?**
```bash
# Build all tools
make build

# Install to $GOPATH/bin
make install

# Build specific tool
go build ./cmd/ci-operator
```

**How do I run tests?**
```bash
# Run all unit tests
make test

# Run specific package tests
go test ./pkg/api/...

# Run integration tests
make integration
```

**What's the difference between unit tests, integration tests, and e2e tests?**
- **Unit Tests**: Test individual functions/units in isolation
- **Integration Tests**: Test components working together
- **E2E Tests**: Test complete workflows end-to-end (require cluster access)

### CI Operator Questions

**What is CI Operator?**
CI Operator is the core orchestration engine that reads YAML configurations and executes multi-stage image builds and tests on OpenShift clusters.

**How do I test a CI Operator config locally?**
```bash
ci-operator \
  --config=path/to/config.yaml \
  --git-ref=org/repo@branch \
  --target=test-name \
  --namespace=my-namespace
```

**What's the difference between `--target` and running all steps?**
`--target` runs only the specified target and its dependencies. Without `--target`, all steps are run.

### Troubleshooting

**My build is failing, how do I debug it?**
1. Check the config: `ci-operator-checkconfig --config=config.yaml`
2. Run locally: `ci-operator --config=config.yaml --git-ref=...`
3. Check logs: `oc logs -n namespace pod-name`
4. Check artifacts: `oc rsync -n namespace pod:/artifacts ./local`

**I'm getting "permission denied" errors**
1. Check kubeconfig: `kubectl config current-context`
2. Verify cluster access: `kubectl get nodes`
3. Check RBAC permissions
4. Try logging in: `oc login <cluster-url>`

**Config changes aren't taking effect**
1. Verify config is in correct location
2. Check if Prow jobs were regenerated
3. Verify PR was merged
4. Check if Prow picked up changes (may take a few minutes)

## Onboarding for New Contributors

### What to Learn Before Contributing

**Essential Knowledge:**
1. **Go Programming Language** - Go basics, concurrency, testing, modules
2. **Kubernetes/OpenShift** - Kubernetes API, Pods, Services, Controllers
3. **YAML** - YAML syntax and working with YAML in Go
4. **Git and GitHub** - Git workflow, GitHub Pull Request process

**Recommended Knowledge:**
1. **CI/CD Concepts** - Continuous Integration principles, build pipelines
2. **Prow** - Prow architecture, job types, plugins
3. **Container Technology** - Docker basics, container images, ImageStreams
4. **Distributed Systems** - Event-driven architecture, controller pattern

### Important Concepts

**CI Operator Configuration**: Declarative YAML configurations that define base images, images to build, tests to run, and image promotion rules. See `pkg/api/config.go` and `pkg/config/load.go`.

**Dependency Graph**: CI Operator builds a dependency graph to determine execution order. Steps have inputs (Requires) and outputs (Creates). See `pkg/api/graph.go`.

**Step Execution Framework**: Steps are composable building blocks that implement a common interface. See `pkg/steps/step.go`.

**Controller Pattern**: Many tools use the Kubernetes controller pattern - watch resources, reconcile desired state, handle errors gracefully. See `pkg/controller/util/reconciler.go`.

### Beginner Roadmap

**Phase 1: Understanding the Basics (Week 1-2)**
- Read [docs/README.md](docs/README.md) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- Set up development environment ([docs/SETUP.md](docs/SETUP.md))
- Build and run a simple tool locally
- Read through `cmd/ci-operator/main.go`

**Phase 2: Understanding CI Operator (Week 3-4)**
- Study `pkg/api/config.go` to understand configuration structure
- Study `pkg/api/graph.go` to understand dependency graphs
- Study `pkg/steps/` to understand step execution
- Create a simple test config and run it locally

**Phase 3: Making Your First Contribution (Week 5+)**
- Find a good first issue (labeled "good first issue" or "help wanted")
- Understand the problem and proposed solution
- Implement the fix or feature
- Write tests
- Create a Pull Request

### Getting Help

- **GitHub Issues**: Search existing issues or create new ones
- **Pull Requests**: Ask questions in PR comments
- **Slack**: #forum-testplatform (for OpenShift team members)
- **Documentation**: Read the docs in `docs/` directory

## References

1. [Google Engineering Practices Documentation](https://google.github.io/eng-practices/): [How to do a code review](https://google.github.io/eng-practices/review/reviewer/) and [The CL author's guide to getting through code review](https://google.github.io/eng-practices/review/developer/)
1. [The Code Review Process in Kubernetes community](https://github.com/kubernetes/community/blob/master/contributors/guide/owners.md#the-code-review-process)
1. [Submitting patches: the essential guide to getting your code into the kernel](https://www.kernel.org/doc/html/latest/process/submitting-patches.html)
