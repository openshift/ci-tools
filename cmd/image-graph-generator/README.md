# image-graph-generator

## What
CLI tool that builds and maintains an image dependency graph in a Dgraph database by reading ci-operator configurations, image mirroring mappings, and OpenShift manifests from the openshift/release repository. The resulting graph enables querying parent/child relationships between container images across the entire CI system.

## How it works -- full flow

### 1. Initialize Dgraph client
Connects to the Dgraph GraphQL endpoint specified by `--graphql-endpoint-address` using the `shurcooL/graphql` client.

### 2. Load existing graph state
The `Operator.Load()` method populates in-memory caches by querying Dgraph for all existing data:
- **Images**: queries all `Image` nodes, caching `name -> id` mappings
- **Organizations**: queries all `Organization` nodes
- **Repositories**: queries all `Repository` nodes
- **Branches**: queries all `Branch` nodes
- **Manifests**: walks `clusters/app.ci/` in the release repo, parsing YAML files for `ImageStream` and `BuildConfig` objects

### 3. Process mirror mappings
`UpdateMirrorMappings()` walks `core-services/image-mirroring/` in the release repo, reading files prefixed with `mapping_`. Each line maps a source image to a destination in the app.ci registry (`registry.ci.openshift.org`). For each destination image:
- Parses the namespace, imagestream name, and tag
- Creates or updates the image node in Dgraph with the source URL

### 4. Process OpenShift manifests
`AddManifestImages()` processes the ImageStream and BuildConfig objects loaded in step 2:
- **ImageStreams**: for each tag, creates/updates an image node with the tag's `from` reference as the source
- **BuildConfigs**: for each BuildConfig, creates/updates the output image node. If the build uses a DockerStrategy with a `from` reference, that reference becomes a parent image in the graph

### 5. Process ci-operator configurations
`OperateOnCIOperatorConfigs()` walks `ci-operator/config/` in the release repo. For each config file (skipping `openshift-priv`):
- Creates/updates branch references for the org/repo/branch
- For each promotion target, processes every image in `images`:
  - Determines the full image name: `{namespace}/{name}:{tag}` or `{namespace}/{tag}:{imageName}`
  - Identifies parent images from `from` references and `inputs.as` entries
  - Creates or updates the image node in Dgraph with parent/child relationships
  - Records whether the image is multi-arch (has `additional_architectures`)
  - Skips images listed in `excluded_images`
  - Internal base images (`root`, `src`, `bin`) are marked with `fromRoot: true`

### Graph data model
The Dgraph schema includes these node types:
- **Organization**: org name and ID
- **Repository**: repo name, linked to organization
- **Branch**: branch name, linked to repository
- **Image**: name, namespace, imageStreamRef, source URL, fromRoot flag, multiArch flag, linked to branches and parent images

All mutations use GraphQL via the `Client` interface (supports both real Dgraph and a fake in-memory client for testing).

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--release-repo` | (required) | Path to the local checkout of the openshift/release repository |
| `--graphql-endpoint-address` | (required) | URL of the Dgraph GraphQL endpoint |

## Key files
- `cmd/image-graph-generator/main.go` -- entry point, Dgraph client initialization, orchestration of Load/UpdateMirrorMappings/AddManifestImages/OperateOnCIOperatorConfigs
- `pkg/image-graph-generator/operator.go` -- `Operator` struct, `Load()`, `OperateOnCIOperatorConfigs()`, ci-operator config callback
- `pkg/image-graph-generator/images.go` -- `UpdateImage()`, `addImageRef()`, `updateImageRef()`, `loadImages()`, image URL parsing
- `pkg/image-graph-generator/mirror_mappings.go` -- `UpdateMirrorMappings()`, mirror mapping file parsing
- `pkg/image-graph-generator/manifests.go` -- `loadManifests()`, `AddManifestImages()`, ImageStream and BuildConfig processing
- `pkg/image-graph-generator/organizations.go` -- organization CRUD operations
- `pkg/image-graph-generator/repositories.go` -- repository CRUD operations
- `pkg/image-graph-generator/branches.go` -- branch CRUD operations
- `pkg/image-graph-generator/client.go` -- `Client` interface, `fakeClient` for testing
- `pkg/image-graph-generator/graphql.go` -- GraphQL mutation/query type definitions

## Deployment
CLI tool. Requires a running Dgraph instance with the appropriate GraphQL schema deployed. Typically run as a periodic Prow job with a local checkout of openshift/release.
The `image-graph-generator` is expected to operate against a [Dgraph](https://dgraph.io/) database.

The schema is defined and maintained in the `types.graphql` file.

Update or change or update the schema in the database:
```console
curl -X POST http://localhost:8080/admin/schema --data-binary '@types.graphql'
```


## Usage

```
Usage of image-graph-generator:
  --release-repo string
      Path to the openshift/release repository.
  --graphql-endpoint-address string
      Address of the Dgraph's graphql endpoint.
```
