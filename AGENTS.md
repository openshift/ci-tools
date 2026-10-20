# AGENTS.md

Guidance for AI agents working in this repository.

`openshift/ci-tools` holds the Go tooling behind OpenShift CI (DPTP / Test Platform): `ci-operator`, `ci-operator-prowgen`, `pod-scaler`, `ci-secret-bootstrap`, the dispatcher, `sprint-automation`, and many other controllers and generators. The CI configuration and job definitions these tools consume and generate live in [openshift/release](https://github.com/openshift/release).

## References

- [OpenShift CI documentation](https://docs.ci.openshift.org/): how ci-operator, Prow, the step registry and the build farm work
- [ci-operator reference](https://steps.ci.openshift.org/ci-operator-reference): the configuration spec implemented by `pkg/api`
- `CONTRIBUTING.md`: PR and review process (reference the Jira key, e.g. `DPTP-1234`, in the PR; keep PRs small)

## Repository layout

- `cmd/<tool>/`: one directory per binary (`main.go` plus tool-specific code)
- `pkg/`: shared libraries (`pkg/api` config types, `pkg/steps`, `pkg/prowgen`, `pkg/jobconfig`, `pkg/dispatcher`, `pkg/util`, `pkg/testhelper`, ...)
- `images/`: container image build contexts for the tools
- `test/integration/`, `test/e2e/`: integration and e2e suites (see Testing; do not touch)
- `hack/`: scripts used by the Makefile

## Commands

- `make lint`: golangci-lint (`hack/lint.sh`)
- `make test`: unit tests with the race detector (`hack/test-go.sh`; limit with `PACKAGES=./pkg/foo/...`)
- `make verify`: gofmt, `go vet` and generated-code checks; run when touching generated code or `go.mod`
- `make generate`: regenerate generated code; never hand-edit generated files
- `make production-install`: build all binaries

Use `go vet ./pkg/foo/...` and `go test ./pkg/foo/...` while iterating, then run `make lint` and `make test` before finishing.

## Go code

Write idiomatic Go ([Effective Go](https://go.dev/doc/effective_go), [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)). Do not write Python-style Go: no generic `Map`/`Filter`/`Reduce` helper chains, no wrappers that only rename a stdlib call, no deep helper hierarchies.

- **No closures.** Use named functions and methods that take their inputs as parameters instead of function literals capturing surrounding variables. A function literal is acceptable only where an API forces one (`t.Run` bodies, `sort.Slice`, `sync.Once.Do`); keep it small and do not capture mutable state.
- **Prefer a default value over `else`.** Initialise the variable with its default and override it in an `if`; use early returns and `continue` to keep the happy path unindented.

  ```go
  // good
  timeout := defaultTimeout
  if cfg.Timeout > 0 {
      timeout = cfg.Timeout
  }

  // bad
  var timeout time.Duration
  if cfg.Timeout > 0 {
      timeout = cfg.Timeout
  } else {
      timeout = defaultTimeout
  }
  ```
- **Naming.** Choose concise names that are descriptive relative to their package. Avoid generic names such as `Manager`, `Handler`, `Util`, and method names that say nothing (`Process`, `Handle`, `Do`); prefer a name that evokes a specific concept (`TestResultAggregator`, not `Manager`).
- **Documentation above functions and methods.** Start each doc comment with the function or method name. Describe **what it does**, including relevant inputs, outputs, errors, and side effects, rather than why it exists. For example: `// LoadConfig reads and validates the configuration at path.`
- **Documentation above other declarations.** Document package-level types, constants, and variables, starting with the declared name and describing what it represents.
- **Focused units.** A package is one cohesive concept, a struct is one entity in that package, a method works at one level of abstraction. Refactor structs with more than about 7 top-level fields into focused sub-types.
- **Errors.** Never discard an error with `_` without a clear justification. Wrap with context: `fmt.Errorf("doing x: %w", err)`. Compare with `errors.Is` / `errors.As` (`errorlint` is enabled). Aggregate several errors with `utilerrors.NewAggregate`. Error strings are lower case without trailing punctuation. Do not `panic` outside `init()` or fatal conditions, and check pointers for nil before dereferencing.
- **Design.** Accept interfaces, return structs; keep interfaces small and define them where they are used; make the zero value useful; no package-level mutable state; `context.Context` is the first parameter; no naked returns.
- **Sets.** Use `k8s.io/apimachinery/pkg/util/sets` (`sets.New[string]()`) to collect unique values. Do not use `map[string]bool` as a hand-rolled set.
- **Time.** Use `time.Time` and `time.Duration`, never epoch integers or pre-formatted strings, for timestamps and durations in structs and APIs.
- **Logging.** Use structured `logrus` logging; prefer `logrus.WithField("job", name)` and `logrus.WithError(err)` over formatting values into the message. Never log secrets or tokens.
- **Reuse before writing.** Search the repository for an existing helper (`pkg/util/`, `pkg/testhelper/`) before adding inline utility logic, and do not add a dependency when the stdlib or an existing dependency already does the job. Avoid calling the same function repeatedly with identical arguments in one code path.
- **Comments inside functions.** Keep them minimal and explain **why**, not what. Avoid restating logic that is already clear from the code. Favor clarity and maintainability over cleverness.
- Run `gofmt -s -w` on every file you modify; code must pass `make lint`.

## Testing

- Write **only table-driven unit tests**, in `_test.go` files next to the code (`foo_test.go` beside `foo.go`). Give each case a descriptive `name`, its inputs, and the expected output or error; run cases with `t.Run(tc.name, ...)`. Search the same package for existing test patterns first.
- **One test function per function or method under test.** Extend its existing table with other relevant cases. Do not create separate test functions for different scenarios, or hide separate tests behind renamed helpers.
- New or modified behavior needs tests: every new exported function or method, non-trivial unexported logic (validation, transformation, aggregation), and a regression test for every bug fix that fails without the fix. Exceptions: renames, formatting, comments, generated code, config-only changes, and refactors already covered by tests.
- If code is hard to test, refactor it: separate pure logic from calls to external systems and test the logic directly, rather than reaching for a mock. When one narrow call forces a connection, put it behind a small interface or a function-typed struct field wired in the constructor, and have the test supply a **named** fake (not a closure).
- Use existing fakes (for example the controller-runtime fake client) and `testdata/` fixtures instead of hand-rolled mocks of broad clients. Unit tests must not need network access or credentials, and must be deterministic: no `time.Sleep`, no reliance on map order or the current time.
- Compare with `testhelper.Diff(t, name, want, got, opts...)` or `cmp.Diff(want, got)` (`github.com/google/go-cmp`); use `testhelper.CompareWithFixture` for golden files under `testdata/` and report `t.Errorf("unexpected result (-want +got):\n%s", diff)`. Test error cases, including the expected error message.
- **Do not write integration or e2e tests.** They live in their own folders with their own syntax and tooling and are out of scope for automated changes. Do not add to or modify them unless the issue explicitly asks.

## Documentation

- When a change alters setup, usage, flags, configuration or architecture, update the relevant README (or the tool's own doc) in the same PR; never leave docs as a follow-up.
- Do not use em dashes in docs; use commas, parentheses or periods.
