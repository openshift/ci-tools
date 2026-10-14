## Read First

Read `AGENTS.md` before changing anything; it is authoritative for style, testing and documentation. Also follow `CONTRIBUTING.md` for the PR description.

## Write Tests for New Code

You MUST write tests for any new functionality you introduce. PRs that add new code without corresponding tests are incomplete.

- Write unit tests for every new exported function or method, and for non-trivial unexported functions (validation, data transformation, aggregation logic).
- Bug fixes need a regression test that fails without the fix.
- Use **table-driven tests** with descriptive case names. Search the same package for existing test patterns before writing new ones.
- Separate pure logic from calls to external systems and test the logic directly. Prefer existing fakes and `testdata/` fixtures over hand-rolled mocks, and use named fakes rather than closures.
- Place test files next to the code they test (`foo_test.go` alongside `foo.go`).
- Write only unit tests. Do **not** add or change integration or e2e tests; they have their own folders and syntax.

## Build, Test, and Verify

1. Run `make test` (or `go test` on the touched packages) to verify your changes work.
2. Run `make lint` to check for linting issues.
3. Run `make verify` when you touch generated code or `go.mod`.

## Environment

- No external services are needed; `hack/agentic_setup.sh` only downloads Go modules.
- The workspace is a clone of the upstream repository; push goes to the bot fork.

## Additional Instructions

- Keep the change small and focused on the Jira issue; do not refactor unrelated code.
- Update the relevant README in the same PR when behavior, flags or usage change.
- Do not use em dashes in docs.
