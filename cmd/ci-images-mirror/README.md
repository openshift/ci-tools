# ci-images-mirror

## What
Mirrors CI images from the app.ci OpenShift image registry to `quay.io/openshift/ci`. This ensures CI images are available in Quay for consumers that cannot directly pull from the internal cluster registry. It consists of two cooperating systems: a controller-runtime controller that watches ImageStreams for changes and queues mirror tasks, and a consumer loop that processes the queue by executing `oc image mirror` commands.

## How it works -- full flow

### Architecture overview
The tool runs three concurrent subsystems:

1. **ImageStream controller** (`quay_io_ci_images_distributor`): watches ImageStream events on the app.ci cluster and determines which tags need mirroring based on ci-operator configs and the step registry
2. **Mirror consumer**: a background goroutine that continuously takes tasks from the mirror store and executes `oc image mirror` commands
3. **Supplemental/ART image service**: periodic tickers that mirror images defined in the config file (supplemental CI images and ART images)

### ImageStream controller
- Watches ImageStream resources and maps events to individual ImageStreamTag reconcile requests
- Filters tags through `testInputImageStreamTagFilterFactory`: only mirrors tags that are referenced by ci-operator test configs (as base images, test inputs, or release inputs) or are in additional namespaces/imagestreams/tags specified via flags
- Tags in the `--ignore-image-stream-tag` list and supplemental CI image targets are excluded from controller mirroring (they are handled separately)
- On reconcile, compares the source image digest with the existing Quay target digest. If they differ, queues a `MirrorTask` in the mirror store
- Only mirrors images with valid manifest v2 by default (`--only-valid-manifest-v2-images`)

### Mirror store and consumer
- The `MirrorStore` is an in-memory queue (keyed by destination) of `MirrorTask` objects
- The `MirrorConsumerController` runs in a loop: takes batches of 10 tasks, executes `oc image mirror` with the configured registry credentials
- Includes metrics tracking for queue depth and mirror operations

### Supplemental CI images
Defined in the config file under `supplementalCIImages`. These are images not discovered by the controller but explicitly configured for mirroring (e.g., third-party images needed by CI).
- Runs on an hourly ticker
- Compares source and target digests before mirroring (skip if already in sync)
- Creates a backup/prune task before each mirror to preserve the old image

### ART images
Defined in the config file under `artImages`. These are ART (Automated Release Team) images resolved from ImageStreams on the cluster.
- Also runs on an hourly ticker
- Uses the same mirror-store mechanism

### Image naming convention
Target images in Quay follow the pattern: `quay.io/openshift/ci:{namespace}_{name}_{tag}` (slashes and colons in the ImageStreamTag name are replaced with underscores).

### Config validation mode
With `--validate-config-only`, the tool validates the config file against the release repo (checks that source images are accessible, targets don't overwrite promoted tags, etc.) and exits.

### HTTP API
Exposes an API server for monitoring:
- `GET /api/health` -- health check
- `GET /api/v1/mirrors?action=summarize` -- summary of the mirror queue
- `GET /api/v1/mirrors?action=show&limit=N` -- show N pending mirror tasks

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--leader-election-namespace` | `ci` | Namespace for leader election |
| `--leader-election-suffix` | (empty) | Suffix for leader election lock (for local testing, requires `--dry-run`) |
| `--enable-controller` | (none) | Enable specific controllers. Available: `quay_io_ci_images_distributor` |
| `--dry-run` | `false` | Dry-run mode |
| `--release-repo-git-sync-path` | (required) | Path to the release repository (for ci-operator configs and step registry) |
| `--config` | (empty) | Path to the CIImagesMirrorConfig file (supplemental and ART images) |
| `--registry-config` | (required) | Path to Docker registry credentials file |
| `--only-valid-manifest-v2-images` | `true` | Skip images with invalid manifest v2 |
| `--port` | `8090` | HTTP API server port |
| `--gracePeriod` | `10s` | Server shutdown grace period |
| `--validate-config-only` | `false` | Validate config and exit |
| `--quayIOCIImagesDistributorOptions.additional-image-stream-tag` | (none) | Extra ISTs to mirror (can repeat) |
| `--quayIOCIImagesDistributorOptions.additional-image-stream` | (none) | Extra ISs to mirror (can repeat) |
| `--quayIOCIImagesDistributorOptions.additional-image-stream-namespace` | (none) | Extra namespaces to mirror (can repeat) |
| `--quayIOCIImagesDistributorOptions.ignore-image-stream-tag` | (none) | ISTs to skip mirroring (can repeat) |

## Key files
- `cmd/ci-images-mirror/main.go` -- entry point, manager setup, supplemental image service, HTTP API, config validation
- `pkg/controller/quay_io_ci_images_distributor/quay_io_ci_images_distributor.go` -- ImageStream controller, tag filtering, reconciler
- `pkg/controller/quay_io_ci_images_distributor/mirror.go` -- MirrorStore, MirrorConsumerController, `oc image mirror` execution
- `pkg/controller/quay_io_ci_images_distributor/oc_quay_io_image_helper.go` -- `oc image info` wrapper
- `pkg/controller/quay_io_ci_images_distributor/supplemental_images.go` -- config file loading and types
- `pkg/controller/quay_io_ci_images_distributor/metrics.go` -- Prometheus metrics

## Deployment
Long-lived controller-runtime Deployment on app.ci with leader election. Requires in-cluster access to the OpenShift image registry and registry credentials for Quay push access.

Uses a git-sync sidecar to keep the release repo up-to-date for ci-operator config and step registry resolution.

---

## Background

This tool mirrors images used by tests in OpenShift CI from `app.ci`'s integrated registry to `quay.io/openshift/ci`. It is a temporary automation while migrating all users of the CI images
from `app.ci` to `quay.io`. When the migration process is complete, i.e., all clients uses images from `quay.io`,
this tool will be decommissioned and CI images will stop promoting images to `app.ci`.

The tool watches all image stream tags on `app.ci`, and compare its digest with the one from the targeting image on `quay.io`.
If they have different digests or the target image does not exist, a mirroring task will be created in a queue which will
be picked up and executed afterwords.

Similar to the `ci-operator's` promotion, there are always 2 tags to push to keep all the images from quay.io's image pruning:
For example, `registry.ci.openshift.org/namespace/name:tag` is mirrored to
- `quay.io/openshift/ci:name_tag`, and
- `quay.io/openshift/ci:<Date>_sha256_<DIGEST>` where `<Date>` is today's date, e.g., `20231029` and `<DIGEST>` is the
  SHA256 hash of the docker image without the prefix `sha256:`.

This tool is extended with the following features (and thus is no longer temporary):
- mirror images from external registries to QCI: See `.supplementalCIImages` of [the configuration file](https://github.com/openshift/release/blob/main/core-services/image-mirroring/_config.yaml).
- mirror ART images from app.ci to QCI: See `.artImages` of [the configuration file](https://github.com/openshift/release/blob/main/core-services/image-mirroring/_config.yaml).

## Run the tool locally

```console
# E.g., RELEASE="/Users/hongkliu/repo/openshift/release"
$ RELEASE=<PATH_TO_THE_RELEASE_REPO> hack/run-ci-images-mirror.sh

# Check the tasks in the queue
$ curl -s http://localhost:8090/api/v1/mirrors\?action\=show\&limit\=1 | jq
{
  "mirrors": [
    {
      "source_tag_ref": {
        "namespace": "ocp-private",
        "name": "4.9-priv",
        "tag": "telemeter"
      },
      "source": "registry.ci.openshift.org/ocp-private/4.9-priv@sha256:censored_digest",
      "destination": "quay.io/openshift/ci:ocp-private_4.9-priv_telemeter",
      "current_quay_digest": "",
      "created_at": "2023-10-11T10:15:13.179246-04:00",
      "stale": true
    }
  ],
  "total": 14
}

# grep mirror cmd in the log
$ RELEASE="/Users/hongkliu/repo/openshift/release" hack/run-ci-images-mirror.sh 2>&1 | grep 'image mirror'
```

## Debug the deployment in production

```console
$ oc port-forward -n ci $(oc get pods -n ci -l app=ci-images-mirror --no-headers -o custom-columns=":metadata.name") 28090:8090

$ curl -s http://localhost:28090/api/v1/mirrors\?action\=show\&limit\=1  | jq
{
  "mirrors": [
    {
      "source_tag_ref": {
        "namespace": "ocp-private",
        "name": "4.9-priv",
        "tag": "node-problem-detector"
      },
      "source": "registry.ci.openshift.org/ocp-private/4.9-priv@sha256:censored_digest",
      "destination": "quay.io/openshift/ci:20231011_sha256_censored_digest",
      "current_quay_digest": "",
      "created_at": "2023-10-11T14:46:23.426063734Z",
      "stale": true
    }
  ],
  "total": 12
}

$ oc logs -n ci -l app=ci-images-mirror -c ci-images-mirror -f | grep -i keep-manifest-list
```
