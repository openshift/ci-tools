# CI Operator Configuration Reference

The CI Operator consumes a configuration file that describes how release artifacts
are built from a repository's branch. An example file is shown below with all
optional fields present:

```yaml
base_images:
  <name>:
    cluster: ''
    name: ''
    namespace: ''
    tag: ''
binary_build_commands: ''
canonical_go_repository: ''
images:
- context_dir: ''
  dockerfile_path: ''
  from: ''
  inputs:
    <name>:
      as: ''
      paths:
      - destination_dir: ''
        source_path: ''
  optional: false
  to: ''
promotion:
  additional_images:
    <name>: ''
  excluded_images:
  - <name>
  name: ''
  name_prefix: ''
  namespace: ''
  tag: ''
raw_steps: []
resources:
  <name>:
    limits:
      cpu: ''
      memory: ''
    requests:
      cpu: ''
      memory: ''
rpm_build_commands: ''
rpm_build_location: ''
tag_specification:
  cluster: ''
  name: ''
  namespace: ''
  tag: ''
  tag_overrides: {}
build_root:
  image_stream_tag:
    cluster: ''
    name: ''
    namespace: ''
    tag: ''
  project_image:
    dockerfile_path: ''
    context_dir: ''
test_binary_build_commands: ''
tests:
- artifact_dir: ''
  as: ''
  commands: ''
  container:
    from: ''
  secret:
    name: ''
    mount_path: ''
  openshift_ansible:
    cluster_profile: ''
  openshift_ansible_src:
    cluster_profile: ''
  openshift_ansible_custom:
    cluster_profile: ''
  openshift_ansible_upgrade:
    cluster_profile: ''
    previous_version: ''
    previous_rpm_deps: ''
  openshift_installer:
    cluster_profile: ''
  openshift_installer_src:
    cluster_profile: ''
  openshift_installer_console:
    cluster_profile: ''
```

# `tag_specification`
The `tag_specification` describes how images from _other_ components should be
tagged into tests for the repository. The Origin CI assembles latest releases
for all components using one `ImageStream` and many tags. To use these releases,
use the following specification:

```yaml
tag_specification:
  cluster: https://api.ci.openshift.org
  name: origin-v3.11
  namespace: openshift
  tag_overrides: {}
```

The release tag specification points to an image stream containing multiple tags,
each of which references a single component by a well known name, e.g.
`openshift/origin-v3.9:control-plane`.

## `tag_specification.cluster`
`cluster` is an optional cluster string (`host`, `host:port`, or `scheme://host:port`)
to connect to for the `ImageStream`. The referenced OpenShift cluster must support
anonymous access to retrieve `ImageStream`s, `ImageStreamTag`s, and
`ImageStreamImage`s in the provided namespace unless `--kubeconfig` is filled as an arg:
`ci-operator -h` for details.

## `tag_specification.namespace`
`namespace` determines the `Namespace` on the target cluster where release
`ImageStreams` are located.

## `tag_specification.name`
`name` is the `ImageStream` name where a single `ImageStream` and multiple
tags are used to assemble a release.

## `tag_specification.tag_overrides`
`tag_overrides` is a mapping from `ImageStream` name to tag which can be used to
override specific components for specific tests at non-standard levels when many
streams and one tag are used to assemble a release.

# `base_images`
`base_images` are parents for images built from the repository, but are not built
from the repository. The field is a mapping from pipeline image name to remote
`ImageStream` specification. A common base image might be an operating system:

```yaml
base_images:
  os:
    cluster: https://api.ci.openshift.org
    name: centos
    namespace: openshift
    tag: '7'
```

The key in this mapping is the name that can be used in `"from"` fields elsewhere
in the configuration to refer to this image. If `tag_specification` is set, you
may omit `cluster`, `namespace`, and `name` and they will be defaulted from the
`tag_specification`.

# `base_rpm_images`
`base_rpm_images` are images that will have the RPMs that are built from the
repository injected into them before the image is used in the builds. The field
has an identical structure to `base_images`.

# `build_root`

`build_root` provides clone-time and build-time dependencies to the builds
but not to the published images. The field describes the `ImageStreamTag`is
created in the namespace for the job to be used as the build environment for the
source code cloning and any downstream builds like compilation or unit tests.

Commonly, the `openshift/release` image is used:

```yaml
build_root:
  image_stream_tag:
    cluster: https://api.ci.openshift.org
    name: release
    namespace: openshift
    tag: golang-1.10
```

## `build_root.image_stream_tag`
`image_stream_tag` configures a remote `ImageStreamTag` to use for the build root.

## `build_root.project_image`
`project_image` configures a Docker build from the repository under test for use
as the build root. The project image will be built from the current `HEAD` for
the branch targeted by the pull request under test.

## `build_root.project_image.context_dir`
`context_dir` is the relative directory in the repository from which the container
build will be run. This field is used to populate the `build.spec.source.contextDir`.
See the [upstream documentation](https://docs.okd.io/latest/rest_api/apis-build.openshift.io/v1.Build.html#object-schema)
for more detail.

## `build_root.project_image.dockerfile_path`
`dockerfile_path` is the `Dockerfile` location in the repository which
the container build will use to run. This field is used to populate the
`build.spec.strategy.dockerStrategy.dockerfilePath`. See the
[upstream documentation](https://docs.okd.io/latest/rest_api/apis-build.openshift.io/v1.Build.html#object-schema)
for more detail.

# `canonical_go_repository`
`canonical_go_repository` is the path that is used to import the code in a Go
project. Disregard this field if the project is not a Go project. This field
becomes necessary when the `ci-operator` is testing a downstream fork or a
repository with a vanity import, or both. For instance, for the
`openshift/kubernetes-autoscaler` repository, which is a fork of
`kubernetes/autoscaler`, the canonical Go repository would be `k8s.io/autoscaler`.

# `binary_build_commands`
`binary_build_commands` are the commands that will be run in the source directory
to build the `bin` pipeline image from the `src` pipeline image. These commands
are passed as a single argument to a shell, so it is possible to create complex
execution flows here, but it is suggested that a script or `make` target is run
in the repository instead.

# `test_binary_build_commands`
`test_binary_build_commands` are the commands that will be run in the source
directory to build the `test-bin` pipeline image from the `src` pipeline image.
This is optional and a separate field from `binary_build_commands` as often
projects will want to compile their binaries with race detection on for testing
but not for their productized builds.

# `rpm_build_commands`
`rpm_build_commands` are the commands that will be run in the source directory
to build the `rpm` pipeline image from the `src` pipeline image. The `rpm` image
will be used to serve RPMs for downstream consumers.

# `rpm_build_location`
`rpm_build_location` is the relative path to the output RPMs from the repository,
this will default under the repository root to `_output/local/releases/rpms/`.
This field allows `ci-operator` to publish the RPMs from the project and serve
them in a repo.

# `images`
`images` is an array of images to be built from the repository. `ci-operator`
expects to find `Dockerfile`s in the source repository that can be used to run
OpenShift `Build`s with.

## `images.$name.from`
`from` is the parent image name, either another image built from the
repository, or any of the images tagged in using `base_image` or `base_rpm_images`.

## `images.$name.to`
`to` is the image name to be built with this configuration stanza.

## `images.$name.context_dir`
`context_dir` is the relative directory in the repository from which the container
build will be run. This field is used to populate the `build.spec.source.contextDir`.
See the [upstream documentation](https://docs.okd.io/latest/rest_api/apis-build.openshift.io/v1.Build.html#object-schema)
for more detail.

## `images.$name.dockerfile_path`
`dockerfile_path` is the `Dockerfile` location in the repository which
the container build will use to run. This field is used to populate the
`build.spec.strategy.dockerStrategy.dockerfilePath`. See the
[upstream documentation](https://docs.okd.io/latest/rest_api/apis-build.openshift.io/v1.Build.html#object-schema)
for more detail.

## `images.$name.inputs`
`inputs` maps pipeline image tags to image input specifications for
use in the build. The fields here will populate the `build.spec.source.images`.
See the [upstream documentation](https://docs.okd.io/latest/dev_guide/builds/build_inputs.html#image-source)
for more detail.

## `images.$name.inputs.$tag.as`
`as` is a name alias used in multi-stage builds using the `AS` keyword in a
Dockerfile. Use this field if input sources come from an aliased image and the
correct image will be specified in the `Dockerfile`'s `FROM` directive.
This field will be used to populate `build.spec.source.images.as`.

## `images.$name.inputs.$tag.paths`
`paths` defines the input that will be added to the build by mapping data in the
source image to destinations in the build.

## `images.$name.inputs.$tag.paths.source_path`
`source_path` is a path in the input image source that will be copied into your
build. This field will be used to populate `build.spec.source.images.paths.sourcePath`.

## `images.$name.inputs.$tag.paths.destination_dir`
`destination_dir` is a path in your build image source where the image source
path will be copied. This field will be used to populate `build.spec.source.images.paths.destinationDir`.

## `images.$name.optional`
`optional` means the build step is not built, published, or promoted unless
explicitly targeted using `--target` on the `ci-operator` invocation or as a
dependency of an explicit `--target`. Use for builds which are invoked only when
testing isolated parts of the repo.

# `tests`
`tests` is an array of configuration which the `ci-operator` will use to run
tests on the repository. These tests are run in containers on OpenShift and
can use the images built by the pipeline (`src`, `bin`, `test-bin` and `rpm`) or
any of the input tagged images or any output images (from `images`).

See the individual fields below for a description of the different test types
supported. A test type is selected by populating one (and only one) of the
respective fields in each test. All types except `container` are currently
ignored by `ci-operator`, but are used by the configuration generator to create
Prow jobs to execute them.

## `tests.as`
`as` is the test name and can be used to run the test with the `--target`
flag on `ci-operator`. Test names should be unique in a `ci-operator` configuration.

## `tests.commands`
`commands` are the commands that will run in this test. These commands are executed
in the top-level directory for the repository.

## `tests.artifact_dir`
`artifact_dir` is the absolute directory in the test `Pod` where artifacts will
be expected. Your test should deposit artifacts here so `ci-operator` can expose
them after the job has finished.

## `tests.container`
`container` is a test that runs the test commands inside a container using one
of the images in the pipeline.

## `tests.container.from`
`from` is the pipeline image tag that this test will be run on.

## `tests.container.memory_backed_volume`
`memory_backed_volume` if specified mounts a tmpfs (filesystem in RAM) at
`/tmp/volume` with the specified size (required).

## `tests.container.memory_backed_volume.size`
`size` is the required quantity of the volume to create in bytes. Use Kubernetes
resource quantity semantics (e.g. `1Gi` or `500M`).

# `tests.secret` 

`Secret` field enables users to mount a secret inside test container.
It is users responsibility to make sure secret is available in the temporary namespace.
This can be done by providing flag `--secret-dir` to ci-operator in prow configuration.

## `tests.secret.name`
`secret.name` is the name of the secret to be mounted inside a test container.

## `tests.secret.path`
`secret.path` is the path at which to mount the secret. Optional, defaults to `/usr/test-secret`

## `tests.openshift_ansible`
`openshift_ansible` is a test that provisions a cluster using openshift-ansible
and runs conformance tests.

## `tests.openshift_ansible_src`
`openshift_ansible_src` is a test that provisions a cluster using
`openshift-ansible` and executes a command in the `src` image.

## `tests.openshift_ansible_custom`
`openshift_ansible_upgrade` is a test that provisions a cluster using
`openshift-ansible`'s custom provisioner, and runs conformance tests.

## `tests.openshift_ansible_upgrade`
`openshift_ansible_upgrade` is a test that provisions a cluster using
`openshift-ansible`, upgrades it to the next version and runs conformance tests.

## `tests.openshift_ansible_40`
`openshift_ansible_40` is a test that provisions 4.0 cluster using new installer and
openshift-ansible

## `tests.openshift_installer`
`openshift_installer` is a test that provisions a cluster using
`openshift-installer` and runs conformance tests.

## `tests.openshift_installer_src`
`openshift_installer_src` is a test that provisions a cluster using
`openshift-installer` and executes a test in the `src` image.

## `tests.openshift_installer_console`
`openshift_installer_console` is a test that provisions a cluster using
`openshift-installer` and executes a test in the `console_test` image. This allows a
component to run a portion of the latest UI tests on changes.

## `tests.openshift_installer_upi`
`openshift_installer_upi` is a test that provisions machines using `installer-upi` image
and installs the cluster using UPI flow.

## `tests.*.cluster_profile`
`cluster_profile` chooses the profile used as input to the installer. This
field is only valid in tests that provision a cluster (`openshift_ansible`,
`openshift_ansible_src`, and `openshift_installer`). Valid values are:

- `aws`
- `aws-atomic`
- `aws-centos`
- `azure4`
- `gcp`
- `gcp-ha`
- `gcp-crio`
- `vsphere`

These are a subset of the profiles found in the
[`release` repository](https://github.com/openshift/release/tree/master/cluster/test-deploy).

# `raw_steps`
`raw_steps` is intended for advanced use of `ci-operator` to build custom execution
graphs. Contact a CI administrator if a workflow is complex enough to warrant use
of this field.

# `promotion`
`promotion` configures how images that are built from the repository under test
are promoted for use in tests for other repositories. Images built from the repo
under test are promoted by creating `ImageStreamTag`s in external namespaces.
Promotion is run when the `--promote` flag is passed to `ci-operator` and will
be run only if all the `--target`s are successful in that run.

If no promotion options are provided, the `tag_specification` fields will be used
to provide default values.

## `promotion.namespace`
`namespace` is the namespace in which the promoted images will be exposed.

## `promotion.name_prefix`
`name_prefix` is prepended to the output `ImageStream` name, if provided.

## `promotion.name`
`name` is the `ImageStream` name to be used when a release is assembled
using one stream and multiple tags.

## `promotion.tag`
`tag` is the tag to use in the release namespace when a release is assembled
using one tag but many `ImageStream`s.

## `promotion.additional_images`
`additional_images` is a mapping of pipeline image tags to promotion target names.
Pipeline images are not considered for promotion by default, but you can use this
field to promote those intermediary images like compiled tests that are not part
of the normal release for the component but may be useful for other repositories.

## `promotion.excluded_images`
`excluded_images` is an array of image names that should never be promoted. This
exclusion is performed on images that were built, but does not prevent images
specified in `additional_images` from being promoted.

# `resources`
`resources` configures the resource requests and limits set on build and test
`Pod`s by `ci-operator`. This is a mapping between test or build name and the
resource configuration. Use the `"*"` name to apply a default resource spec to
all steps. See the [upstream documentation](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/)
for more information.

## `resources.$name.requests`
`requests` holds the CPU and memory requests for the step. You should make sure
to request enough resources so that the test will run cleanly, if the test is
starved of resources and scheduled to an overcommitted node, the test may flake.

### `resources.$name.requests.cpu`
`cpu` is the requested CPU, with the unit, often in millicores (`100m`).

### `resources.$name.requests.memory`
`memory` is the requested RAM, with the unit, often in MiB (`200Mi`).

## `resources.$name.limits`
`limits` are the hard limits for resources for the test. Be careful to set these
above the expected maximum use, but be wary of being a resource hog. The closer
you can set these to their actual peak usage, the more efficient the CI build
cluster can run. If the test hits the limit, though, it will likely crash when
the test fails to `malloc()`.

### `resources.$name.limits.cpu`
`cpu` is the maximum CPU, with the unit, often in millicores (`100m`).

### `resources.$name.limits.memory`
`memory` is the maximum RAM, with the unit, often in MiB (`200Mi`).
