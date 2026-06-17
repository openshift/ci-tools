# docgen

## What
Generates a Go source file containing an annotated YAML reference for the `ReleaseBuildConfiguration` struct (the ci-operator config schema). The output is a string constant used by the step registry web UI to display interactive, commented documentation of every ci-operator config field.

## How it works -- full flow

1. Glob all `.go` files in `./pkg/api/` to find the Go source files that define the ci-operator configuration types.
2. Build a `CommentMap` using Prow's `genyaml.NewCommentMap()`:
   - Parses the Go source files' AST to extract doc comments from every struct field
   - Maps each struct field's JSON/YAML tag to its corresponding doc comment
   - Uses a trivial import path resolver (identity function that returns the directory unchanged)
3. Generate an annotated reference YAML by calling `commentMap.GenYaml()` with a fully-populated instance of `ReleaseBuildConfiguration`:
   - `genyaml.PopulateStruct()` uses reflection to create an instance of the struct with all fields set to non-zero values (including nested structs, slices, and maps), so every possible field appears in the output
   - `GenYaml()` serializes this populated struct to YAML and injects the doc comments as `#`-prefixed comment lines above each field
4. Post-process the generated YAML string into a Go string constant:
   - Escape all double quotes (`"` -> `\"`)
   - Split the YAML into individual lines
   - Wrap each line into a Go string concatenation expression (`"line1\n" + \n"line2\n" + ...`)
   - Prepend with `package webreg` declaration and `const ciOperatorReferenceYaml = "..."`
5. Write the result to `./pkg/webreg/zz_generated.ci_operator_reference.go` with `0644` permissions.

### Output file
The generated file `zz_generated.ci_operator_reference.go` contains a single large string constant `ciOperatorReferenceYaml` in the `webreg` package. This constant is used by the configresolver web UI to render the ci-operator configuration reference documentation page.

The `zz_generated.` prefix follows the Go convention for generated files that should not be manually edited and are typically excluded from linting.

### Important: must run from repo root
The tool uses relative paths (`./pkg/api/*.go` and `./pkg/webreg/...`), so it **must** be executed from the ci-tools repository root directory.

## Flags
None. This tool takes no command-line flags.

## Key files
- `cmd/docgen/main.go` -- entire tool: source parsing, YAML generation, Go source output
- `pkg/api/types.go` -- the `ReleaseBuildConfiguration` struct and all nested types whose doc comments become the reference documentation
- `pkg/webreg/zz_generated.ci_operator_reference.go` -- the generated output file consumed by the web UI
- `sigs.k8s.io/prow/pkg/genyaml` module -- `NewCommentMap()` and `GenYaml()` that parse Go comments and produce annotated YAML; `PopulateStruct()` that creates a fully-populated struct instance via reflection

## Deployment
CLI tool. Run as part of the code generation pipeline (typically `make generate` or equivalent). Must be re-run whenever the `pkg/api/` type definitions or their doc comments change. Not deployed as a service.
