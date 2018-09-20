This document describes how to add new CI jobs (utilizing ci-operator) for
OpenShift components to the OpenShift CI system. In general, two steps are
needed:

1. [Prepare configuration file](#prepare-configuration-for-component-repo),
   describing how ci-operator should build and test your component.
2. [Add new jobs to Prow](#add-prow-jobs), hooking ci-operator to important
   GitHub events for your repository.

This walkthrough covers a simple use case of hooking up a job running source
code level tests (unit, performance etc.) on pull requests on a component
repository. Although ci-operator can perform more sophisticated test steps than
simply running unit tests, these use cases are beyond the scope of this doc.

## Prepare configuration for component repo

The JSON configuration file describes how to build different images in a
testing pipeline for your repository. ci-operator has different *”targets”*
that describe the goal images to build, and later targets build on successfully
built earlier targets. You will probably want to write and test your config
file locally first, and test whether it builds all targets and runs all tests
as expected (you need to be logged in to a cluster, e.g. to
[api.ci](https://api.ci.openshift.org)):

```
./ci-operator --config config.yaml --git-ref openshift/<repo>@<revision>
```

After you make sure everything works, you need to create a subdirectory
of
[ci-operator/config/openshift](https://github.com/openshift/release/tree/master/ci-operator/config/openshift)
in the [openshift/release](https://github.com/openshift/release/) repository for
your component and put the config file there.

### Source code image target

By default, ci-operator builds the `src` target image, expected by later targets
to contain the source code of the component together with its build
dependencies. Using [cloneref](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/clonerefs)
, ci-operator fetches the refs to be tested from the component repository
and injects the source code into the base image specified by the `build_root` key. 
There are two ways to specify the base image.
* From an image stream that should contain all build dependencies of the tested component, so the it will often be a `openshift/release:<tag>` image.
```yaml
build_root:
  image_stream_tag:
    cluster: https://api.ci.openshift.org
    namespace: openshift
    name: release
    tag: golang-1.10
```
* From a `Dockerfile` that is in the repository in which the PR is opened. In this case, ci-operator will build the image first and it will get the build from the latest of the target branch.
```yaml
build_root:
  project_image_build:
    dockerfile_path: Dockerfile
    context_dir: path/of/dockerfile/
```

**Note:** Both image_stream_tag and project_image_build should not be defined.

Given your component can be built in the context of the `openshift/release`
image, you can test building the `src` target:

```
$ ./ci-operator --config example.yaml --git-ref=openshift/<component>@<revision> --target=src
```

### Test targets

Test target images are built over earlier targets. The targets are specified in
a `tests` array (so it is possible to specify multiple test targets). Here is an
example of two test targets, each performing a different test by calling
different `make` target in a `src` image (of course, a `Makefile` in your
component repository would need to have these targets for this to work).

```yaml
tests:
- as: unit
  commands: make test-unit
  container:
    from: src
- as: performance
  commands: make test-performance
  container:
    from: src
```

By default, ci-operator runs all specified test targets, building all their
dependencies (and transitively, their dependencies) before. You can limit the
execution to build just one or more targets using the `--target` option.

### Intermediary binary targets

Two test targets in the previous example assume their `make` targets take care
of full build from source till the actual test. This is often the case, but it
is ineffient because each test target performs the build separately. CI
operator can create `bin` and `test-bin` targets for the test targets to share
by providing `binary_build_commands` and `test_binary_build_commands`
respectively. Note that ci-operator allows separate `bin` and `test-bin`
targets because often the compilation process is different for “normal” and
test binaries, for example in Go you might want do compile in a different way
to test for race conditions.

Here, `unit` and `integration` targets will both be built from a `test-bin`
image, which will be a result of running `make instrumented-build` over a `src`
image, while `performance` test target will be run from a `bin` image:

```yaml
binary_build_commands: make build
test_binary_builds_commands: make instrumented-build
tests:
- as: unit
  commands: make test-unit
  container:
    from: test-bin
- as: integration
  commands: make test-integration
  container:
    from: test-bin
- as: performance
  commands: make test-performance
  container:
    from: bin
```

### Using Separate Build Environment and Release Environment Images

Often, you will want to run your builds in an environment where all of your build-
time dependencies exist, but you will not want those to be present in your final
container image. For this case, `ci-operator` allows you to use a separate image
for your builds and we make use of the OpenShift `Build` image source mechanism
to deliver artifacts from one container image to another. In the following example,
we configure `ci-operator` to run such a build:

```yaml
base_images:
  release_base:
    name: release
    tag: latest
binary_build_commands: make build
images:
- context_dir: images/product
  from: release_base
  inputs:
    bin:
      paths:
      - destination_dir: /usr/bin/binary
        source_path: path/to/binary
  to: product
build_root:
  image_stream_tag:
    name: tests
    tag: latest
```

In the example, we build the binaries using `make build` in the `tests` environment
image and commit the result to the `bin` tag. Then, we build the `product` image
using the `release_base` and copying in the binary from that `bin` tag.


### Submit the configuration file to `openshift/release`

When you describe the targets for your component in the configuration file, you
will need to add the file to the
[openshift/release](https://github.com/openshift/release) repository,
specifically to its `ci-operator/config/openshift` subdirectory
[tree](https://github.com/openshift/release/tree/master/ci-operator/config/openshift).
Each OpenShift component has a separate directory there, and there is a
configuration file in it per branch of the repository under test (all files
should be in the `master` branch of the `openshift/release` repository).

### Images targets, end-to-end tests and more

Building the source code and running unit tests is basic use case for
ci-operator. In addition to that, ci-operator is able to build component images,
provision test clusters using them and run end-to-end tests on them. These use
cases would use more features in both configuration file and Prow job and would
not fit into this document. We will provide more documentation describing other
use cases soon.

## Add Prow jobs

Once the config file is prepared, you can create Prow jobs that will build
selected targets before or after a PR is merged (or even periodically). Prow
job configuration files also live in `openshift/release` repository,
specifically in `ci-operator/jobs/$org/$repo` directories. The easiest way how
to create them is to use the
[generator](https://github.com/openshift/ci-operator-prowgen). The generator can
create a good set of default Prow jobs from your ci-operator configuration
file. All you need to do is to commit the generated files.

You can find more information about how to create Prow jobs in [test-infra
documentation](https://github.com/openshift/test-infra/tree/master/prow#how-to-add-new-jobs).
