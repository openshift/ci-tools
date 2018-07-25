## Prepare configuration for component repo

The JSON configuration file describes how to build different images in a
testing pipeline for your repository. ci-operator has different *”targets”*
that describe the goal images to build, and later targets build on successfully
built earlier targets.

### Source code image target

By default, ci-operator builds the `src` target image. The `src` image is
expected to contain the component source code together with all build
dependencies. You can specify the base image to which the source code will be
injected with the `test_base_image` key (the base image will almost always be
some `openshift/release:<tag>` image.

```
$ cat example.json
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
$ ./ci-operator --config example.json --namespace 'ciop-test' --git-ref=openshift/<component>@master --target=src
```

### Test targets

Test target images are built over earlier targets. The targets are specified in
a `tests` array (so it is possible to specify multiple test targets). Here is an
example of two test targets, each performing a different test by calling
different `make` target in a `src` image (of course, a `Makefile` in your
component repository would need to have these targets for this to work).

```
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
is ineffient because each test target will then perform the build separately. CI
operator can create `bin` and `test-bin` targets for the test targets to share
by providing `binary_build_commands` and `test_binary_build_commands`
respectively:

```
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

### Images targets

TODO

## Create a Prow job for the component

TODO
