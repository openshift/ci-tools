# clusterimageset-updater

## What
Batch tool that synchronizes Hive `ClusterImageSet` resources with the latest OCP pre-release images. It reads cluster pool YAML specs from a directory, resolves the latest release image matching each pool's version bounds, writes updated `ClusterImageSet` YAML files, and patches the pool specs to reference them. Designed to run as a periodic Prow job that commits changes back to openshift/release.

## How it works -- full flow

1. **Ensure labels on pools.** Walks `--pools` directory for `*_clusterpool.yaml` files. For each pool that has an `owner` label on the resource metadata, ensures `tp.openshift.io/owner` is propagated into `spec.labels` (so claimed clusters inherit the owner). Writes the file back if modified.

2. **Collect version bounds.** Re-walks the pools directory. Each pool may have `version_lower` and `version_upper` labels (and optionally `version_stream`) that define which OCP release range the pool targets. Both lower and upper must be set or both absent. Groups pool file paths by their `VersionBounds`.

3. **Resolve pull specs.** For each unique `VersionBounds`:
   - Determines architecture: `multi` for version_lower >= 4.12, `amd64` for older versions (multi payload not available before 4.12). Returns an error if version_lower cannot be parsed as `major.minor`, so misconfigured pools fail fast.
   - Queries the OCP release controller HTTP API (`prerelease.ResolvePullSpec`) for the latest release image matching the version range.

4. **Merge colliding bounds.** If multiple pools with different `version_stream` values resolve to the same pull spec with the same lower/upper bounds, they are merged into a single canonical bounds entry (keeping the lexicographically greatest stream) to avoid duplicate ClusterImageSets.

5. **Identify outdated ClusterImageSets.** Walks `--imagesets` directory for existing `*_clusterimageset.yaml` files. Compares each against the newly resolved pull specs using its `version_lower`/`version_upper` annotations. Marks stale ones for deletion.

6. **Write new ClusterImageSets.** For each resolved bounds (in sorted order), creates a `ClusterImageSet` YAML with:
   - Name derived from the pull spec (e.g., `ocp-release-4.14.1-multi-for-4.14.0-0-to-4.15.0-0`)
   - Annotations recording `version_lower`, `version_upper`, and optionally `version_stream` for future reconciliation
   - `spec.releaseImage` set to the resolved pull spec

7. **Delete stale files.** Removes the ClusterImageSet YAML files identified as outdated in step 5.

8. **Update pool specs.** For each pool file, updates `spec.imageSetRef.name` to point at the newly written ClusterImageSet name.

### Naming convention
ClusterImageSet names follow the pattern: `ocp-release-<tag>-for-<lower>-to-<upper>`, with colons replaced by `-`, `@` replaced by `-`, and underscores replaced by `-` to produce valid Kubernetes object names.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--pools` | (required) | Directory containing cluster pool specs (`*_clusterpool.yaml`) |
| `--imagesets` | (required) | Directory containing ClusterImageSet definitions (`*_clusterimageset.yaml`) |

## Key files
- `cmd/clusterimageset-updater/main.go` -- all logic: pool parsing, release resolution, architecture selection, ClusterImageSet generation, pool patching, colliding bounds merging

## Deployment
Runs as a Prow periodic job. Reads from and writes to the openshift/release repository, then the changes are committed and PR'd automatically.

## Gotchas
- Only files ending in `_clusterpool.yaml` (with underscore) are processed -- files like `_cluster-pool.yaml` (with hyphen) are silently skipped.
- Both `version_lower` and `version_upper` must be set together or not at all; setting only one causes a fatal error.
