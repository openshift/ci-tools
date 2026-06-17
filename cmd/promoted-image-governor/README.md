# promoted-image-governor

## What
Garbage collector for promoted ImageStreamTags on the app.ci cluster and build farm clusters. It compares the set of tags that _should_ exist (as determined by ci-operator promotion configs) against what _actually_ exists in the cluster, and deletes orphaned tags that are no longer promoted by any configuration. Also optionally generates image mirroring mapping files for the `periodic-image-mirroring-openshift` job.

## How it works -- full flow

### Determine promoted tags
1. Walk the ci-operator config directory (`--ci-operator-config-path`)
2. For each config, call `release.PromotedTags()` to collect all ImageStreamTagReferences that the config promotes
3. If any promotion target uses `TagByCommit`, compile a regex to ignore commit-hash tags (`namespace/name:[0-9a-f]{5,40}`)

### Determine mirrored tags
1. Walk the release controller mirror config directory (`--release-controller-mirror-config-dir`)
2. Parse each JSON config to extract the `ImageStreamRef` (namespace, name, excluded tags)
3. Tags mirrored by the release controller are exempt from deletion

### Find tags to delete (on app.ci)
1. For each ImageStream that has at least one promoted tag, fetch the full ImageStream from the cluster
2. Collect all tags present in the ImageStream's `.status.tags`
3. Remove tags that are in the promoted set, in the ignored-tags regex set, or mirrored by the release controller
4. The remaining tags are orphans -- delete them

### Delete tags on build farm clusters
1. For each ImageStream with promoted tags, check if it exists on each build farm cluster
2. If the ImageStream doesn't exist on app.ci at all, delete the entire ImageStream on the build farm
3. Otherwise, compare tags: any tag present on the build farm but not on app.ci gets deleted

### Explain mode (`--explain`)
Instead of deleting, prints a table showing each queried ImageStreamTag and which ci-operator config promotes it (or "unknown" / "does not exist").

### Mapping file generation (`--openshift-mapping-dir` + `--openshift-mapping-config`)
When these flags are set, the tool generates image mirroring mapping files instead of performing deletions:
1. For each promoted tag in the configured source namespace, look up the target mappings in the mapping config
2. Write mapping files to `--openshift-mapping-dir` in the format `source destination1 destination2...`
3. These files are consumed by the `periodic-image-mirroring-openshift` job to mirror images from the internal registry to external registries (e.g., `quay.io/openshift`)

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--ci-operator-config-path` | (required) | Path to the ci-operator config directory |
| `--dry-run` | `true` | Print tags to delete without actually deleting |
| `--ignored-image-stream-tags` | (none) | Regex patterns for tags to skip (can repeat) |
| `--release-controller-mirror-config-dir` | (required) | Path to release controller mirror config JSON files |
| `--openshift-mapping-dir` | (empty) | Output directory for mirroring mapping files |
| `--openshift-mapping-config` | (empty) | Path to the openshift mapping config (must pair with `--openshift-mapping-dir`) |
| `--explain` | (none) | Print promotion source for specific ISTs (namespace/name:tag format, can repeat) |
| `--log-level` | `info` | Log output level |
| `--kubeconfig` | (various) | Kubeconfigs for app.ci and build farm clusters |

## Key files
- `cmd/promoted-image-governor/main.go` -- entry point, promoted tag collection, orphan detection, deletion, mapping generation

## Deployment
Runs as a periodic CronJob. When used for tag cleanup, it connects to app.ci (via in-cluster config or kubeconfig) and optionally to build farm clusters. When used for mapping file generation, it only reads configs and writes files (no cluster access needed beyond app.ci for the explain mode).

---

## Additional details

### What it does

`promoted-image-governor` is a tool with the following features:

- Delete the tags that are not promoted by any ci-operator config on each integration image stream on `app.ci` and every build-farm cluster.
  An image stream is an integration image stream if it has a promoted tag.
- Generate [the image mapping files](https://github.com/openshift/release/tree/main/core-services/image-mirroring/openshift) for the [quay.io/openshift](https://quay.io/organization/openshift) organization.
- Explain why an `imagestreamtag` exists.

### Why it exists

- Delete the stale images that were promoted in the past but are no more.
- Reduce the manual work on the maintenance of the mapping files and enforce their correctness.
- Save the effort of reverse engineering on which ci-operator's configuration promotes some image.

### How it works

#### Regulate the image streams

- Collect all image streams with promoted tags
- Delete the tags if it meets none of the following criteria:
  - a promoted tag defined by a ci-operator's config
  - a mirrored tag by [the release-controllers' config](https://github.com/openshift/release/tree/main/core-services/release-controller/_releases).
  - a tag matching the regular expression specified by `--ignored-image-stream-tags` flag

#### Maintain the mapping files

- Read [the config file](https://github.com/openshift/release/blob/main/core-services/image-mirroring/openshift/_config.yaml) and [the release-controllers' config](https://github.com/openshift/release/tree/main/core-services/release-controller/_releases)
- Generate the mapping files

#### Explain

Looks for the ci-operator's configuration that promotes the image stream tag.

### How is it deployed

The periodic job [periodic-promoted-image-governor](https://prow.ci.openshift.org/?job=periodic-promoted-image-governor) ([definition](https://github.com/openshift/release/blob/main/ci-operator/jobs/infra-periodics.yaml))
uses `promoted-image-governor` to regulate the image streams with promoted tags on every build-farm cluster.

The pre-submit job [pull-ci-openshift-release-openshift-image-mirror-mappings](https://prow.ci.openshift.org/?job=pull-ci-openshift-release-openshift-image-mirror-mappings) ([definition](https://github.com/openshift/release/blob/main/ci-operator/jobs/openshift/release/openshift-release-main-presubmits.yaml))
uses `promoted-image-governor` to ensure the mapping files to be aligned with the output of the tool.

`explain` is a local utility:

```console
$ istag=ocp/4.9:cli make explain
                 tag              explanation
         ocp/4.9:cli openshift/oc@release-4.9
```
