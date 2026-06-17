# generate-registry-metadata

## What
Generates `.metadata.json` files for each step registry component (references, chains, workflows, observers). These metadata files contain the relative path and OWNERS information for each component and are consumed by the ci-operator-configresolver's web UI to display ownership and navigation information.

## How it works -- full flow

### Metadata generation
1. Parse the `--registry` flag (required), pointing to the step registry directory.
2. Walk the entire registry directory tree recursively using `filepath.WalkDir()`.
3. For each `.yaml` file found (which represents a registry component -- a step reference, chain, workflow, or observer definition):
   a. Compute the file's path relative to the registry root directory.
   b. Look for an `OWNERS` file in the **same directory** as the component. Every registry component directory is **required** to have an OWNERS file.
   c. If the OWNERS file is missing, record an error (but continue processing other files).
   d. If the OWNERS file exists, read and unmarshal it as a Prow `repoowners.Config` struct (supports `approvers`, `reviewers`, `labels`, etc.).
   e. Store the metadata: `{filename.yaml -> {path: relative_path, owners: owners_config}}`.
4. If any errors occurred (missing OWNERS files, read failures, unmarshal failures), aggregate them and exit with a fatal error.

### Metadata writing
5. For each collected metadata entry, write a `.metadata.json` file:
   - The output file is placed in the same directory as the source `.yaml` file
   - The filename is derived from the component filename: `{component-name}.metadata.json` (the `.yaml` extension is replaced with `.metadata.json`)
   - Example: `ci-operator/step-registry/ipi/install/ipi-install-ref.yaml` produces `ipi-install-ref.metadata.json` in the same directory
   - The JSON is pretty-printed with tab indentation via `json.MarshalIndent()`
6. Written with `0644` permissions.

### Output format
Each `.metadata.json` file contains:
```json
{
    "path": "ipi/install/ipi-install-ref.yaml",
    "owners": {
        "approvers": ["user1", "user2"],
        "reviewers": ["user3"]
    }
}
```

The `path` field is the component's path relative to the registry root, and `owners` is the full parsed OWNERS config from the component's directory.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--registry` | (required) | Path to the step registry directory |

## Key files
- `cmd/generate-registry-metadata/main.go` -- entire tool: directory walking, OWNERS parsing, JSON output
- `pkg/api/types.go` -- `RegistryMetadata` (map of filename to `RegistryInfo`) and `RegistryInfo` (path + owners) type definitions
- `pkg/load/load.go` -- `MetadataSuffix` constant (`.metadata.json`), used for consistency with the registry loader that reads these files back

## Gotchas
- Every registry component directory **must** have an OWNERS file. If any are missing, the tool reports errors for all of them but still processes the rest.
- The tool reads OWNERS files using `gzip.ReadFileMaybeGZIP()`, so gzip-compressed OWNERS files are supported (though uncommon).
- Only `.yaml` files are considered as registry components. Other files (`.md` docs, `.metadata.json` from previous runs, etc.) are ignored.

## Deployment
CLI tool. Run as part of the registry metadata generation pipeline. The output `.metadata.json` files are checked into the repository alongside the registry components and consumed by the configresolver web UI at runtime.
