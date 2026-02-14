# promoted-image-governor

## What it does

`promoted-image-governor` is a tool with the following features:

- Delete the tags that are not promoted by any ci-operator' config on each integration image stream on `app.ci` and every build-farm cluster.
  An image stream is an integration image stream if it has a promoted tag.
- Generate [the image mapping files](https://github.com/openshift/release/tree/main/core-services/image-mirroring/openshift) for the [quay.io/openshift](https://quay.io/organization/openshift) organization.
- Explain why an `imagestreamtag` exists.


## Why it exists

- Delete the stale images that were promoted in the past but are no more.
- Reduce the manual work on the maintenance of the mapping files and enforce their correctness.
- Save the effort of reversing engineering on which ci-operator's configuration promotes some image.


## How it works

### Regulate the image streams

- Collect all image streams with promoted tags
- Delete the tags if it meets none of the following criteria:
  - a promoted tag defined by a ci-operator's config
  - a mirrored tag by [the release-controllers' config](https://github.com/openshift/release/tree/main/core-services/release-controller/_releases).
  - a tag matching the regular expression specified by `--ignored-image-stream-tags` flag

### Maintain the mapping files

- Read [the config file](https://github.com/openshift/release/blob/main/core-services/image-mirroring/openshift/_config.yaml) and [the release-controllers' config](https://github.com/openshift/release/tree/main/core-services/release-controller/_releases)
- Genenerate the mapping files

### Explain

Looks for the ci-operator's configuration that promotes the image stream tag.

## How is it deployed

The periodic job [periodic-promoted-image-governor](https://prow.ci.openshift.org/?job=periodic-promoted-image-governor) ([definition](https://github.com/openshift/release/blob/main/ci-operator/jobs/infra-periodics.yaml))
uses `promoted-image-governor` to regulate the image streams with promoted tags on every build-farm cluster.

The pre-submit job [pull-ci-openshift-release-openshift-image-mirror-mappings](https://prow.ci.openshift.org/?job=pull-ci-openshift-release-openshift-image-mirror-mappings) ([definition](https://github.com/openshift/release/blob/main/ci-operator/jobs/openshift/release/openshift-release-master-presubmits.yaml))
uses `promoted-image-governor` to ensure the mapping files to be aligned with the output of the tool.

`explain` is a local utility:

```console
$ istag=ocp/4.9:cli make explain
                 tag              explanation
         ocp/4.9:cli openshift/oc@release-4.9
```