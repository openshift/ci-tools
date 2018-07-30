This document describes how to add new CI jobs (utilizing ci-operator) for
OpenShift components to the OpenShift CI system. In general, two steps are
needed:

1. [Prepare configuration file](#prepare-configuration-for-component-repo),
   describing how ci-operator should build and test your component.
2. [Add new jobs to Prow](#add-prow-jobs), hooking ci-operator to important
   GitHub events for your repository.

## Prepare configuration for component repo

The JSON configuration file describes how to build different images in a
testing pipeline for your repository. ci-operator has different *”targets”*
that describe the goal images to build, and later targets build on successfully
built earlier targets.

### Source code image target

By default, ci-operator builds the `src` target image, expected by later targets
to contain the source code of the component together with its build
dependencies. Using [cloneref](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/clonerefs)
, ci-operator fetches the refs to be tested from the component repository
and injects the source code into the base image specified by the
`test_base_image` key.  The base image should contain all build dependencies of
the tested component, so the it will often be a `openshift/release:<tag>` image.

```json
{
  "test_base_image": {
    "cluster": "https://api.ci.openshift.org",
    "namespace": "openshift",
    "name": "release",
    "tag": "golang-1.10"
  }
}
```

Given your component can be built in the context of the `openshift/release`
image, you can test building the `src` target:

```
$ ./ci-operator --config example.json --git-ref=openshift/<component>@<revision> --target=src
```

### Test targets

Test target images are built over earlier targets. The targets are specified in
a `tests` array (so it is possible to specify multiple test targets). Here is an
example of two test targets, each performing a different test by calling
different `make` target in a `src` image (of course, a `Makefile` in your
component repository would need to have these targets for this to work).

```json
{
  (...)
  "tests": [
    {
      "as": "unit",
      "from": "src",
      "commands": "make test-unit"
    },
    {
      "as": "performance",
      "from": "src",
      "commands": "make test-performance"
    }
  ]
}
```

### Intermediary binary targets

Two test targets in the previous example assume their `make` targets take care
of full build from source till the actual test. This is often the case, but it
is ineffient because each test target performs the build separately. CI
operator can create `bin` and `test-bin` targets for the test targets to share
by providing `binary_build_commands` and `test_binary_build_commands`
respectively (we have two test lists here because often it's a different
compilation process for test binaries than for normal ones -- in Go that is the
difference between "normal" compilation and compilation to test race
conditions):

```json
{
  (...)
  “binary_build_commands”: “make build”,
  “test_binary_builds_commands”: “make instrumented-build”,
  “tests”: [
    {
      “as”: “unit”,
      “from”: “bin”,
      “commands”: “make test-unit”
    },
    {
      “as”: “integration”,
      “from”: “bin”,
      “commands”: “make test-integration”,
    },
    {
      “as”: “performance”,
      “from”: “test-bin”,
      “commands”: “make test-performance”
    }
  ]
}
```

Here, `unit` and `integration` targets will both be built from a `bin` image,
which will be a result of running `make build` over a `src` image.

## Add Prow jobs

TODO
