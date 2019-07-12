# Prow job generator for ci-operator

The purpose of this tool is to reduce an amount of boilerplate that component
owners need to write when they use
[ci-operator](https://github.com/openshift/ci-operator) to set up CI for their
component. The generator is able to entirely generate the necessary Prow job
configuration from the ci-operator configuration file.

## TL;DR

**Question:** How do I create Prow jobs running ci-operator for a newly onboarded OpenShift
component?

**Answer:**
1. Get a working copy of [openshift/release](https://github.com/openshift/release) (we’ll shorten path to it to `$RELEASE`)
2. Create a [ci-operator configuration file](https://github.com/openshift/ci-operator/blob/master/ONBOARD.md#prepare-configuration-for-component-repo) under `$RELEASE/ci-operator/config`, following the `organization/component/branch.yaml` convention.
3. Run `ci-operator-prowgen --from-dir $RELEASE/ci-operator/config --to-dir $RELEASE/ci-operator/jobs <org>/<component>`
4. Review Prow job configuration files created in `$RELEASE/ci-operator/jobs/<org>/<component>` 
5. Commit both ci-operator configuration file and Prow job configuration files and issue a PR to upstream.
6. Profit after merge.

## Use

To use the generator, you need to build it:

```
$ make build
```

Alternatively, you may obtain a containerized version from the registry on
`api.ci.openshift.org`:

```
$ docker pull registry.svc.ci.openshift.org/ci/ci-operator-prowgen:latest
```

### Generate Prow jobs for new ci-operator config

The generator uses the naming conventions and directory structure of the
[openshift/release](https://github.com/openshift/release) repository. Provided
you placed your `ci-operator` configuration file to the correct place in
[ci-operator/config](https://github.com/openshift/release/tree/master/ci-operator/config),
you may run the following (`$REPO is a path to `openshift/release` working
copy):

```
$ ./ci-operator-prowgen --from-dir $REPO/ci-operator/config/ \
 --to-dir $REPO/ci-operator/jobs org/component/
```

This extracts the `org` and `component` from the configuration file path, reads
the configuration files and generates new Prow job configuration files in the
`(...)/ci-operator/jobs/` directory, creating the necessary directory structure
and files if needed. If the target files already exist and contain Prow job
configuration, newly generated jobs will be merged with the old ones (jobs are
matched by name).

### Generate Prow jobs for multiple ci-operator config files

The generator may take one or more directories as input. In this case, the
generator walks the directory structure under each given directory, finds all
YAML files there and generates jobs for all of them.

You can generate jobs for a certain component, organization, or everything:

```
$ ./ci-operator-prowgen --from-dir $REPO/ci-operator/config --to-dir $REPO/ci-operator/jobs org/component
$ ./ci-operator-prowgen --from-dir $REPO/ci-operator/config --to-dir $REPO/ci-operator/jobs org
$ ./ci-operator-prowgen --from-dir $REPO/ci-operator/config --to-dir $REPO/ci-operator/jobs
```

If you have cloned `openshift/release` with `go get` and you have `$GOPATH` set
correctly, the generator can derive the paths for the input/output directories.
These invocations are equivalent:

```
$ ./ci-operator-prowgen --from-release-repo --to-release-repo
$ ./ci-operator-prowgen --from-dir $GOPATH/src/github.com/openshift/release/ci-operator/config \
 --to-dir $GOPATH/src/github.com/openshift/release/ci-operator/jobs
```

## What does the generator create?

See [GENERATOR.md](GENERATOR.md).


## Develop

To build the generator, run:

```
$ make build
```

To run unit-tests, run:

```
$ make test
```
