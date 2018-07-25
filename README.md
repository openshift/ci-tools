# ci-operator

ci-operator automates and simplifies the process of building and testing
OpenShift component images (i.e. any `openshift/origin-{component}` images).

Given a Git repository reference and a component-specific JSON configuration
file, describing base images and which images should be built and tested and
how, ci-operator builds the component images within an OpenShift cluster and
runs the tests. All artifacts are built in a new namespace named using a hash of
all inputs, so the artifacts can be reused when the inputs are identical.

ci-operator is mainly intended to be run inside a pod in a cluster, triggered by
the Prow CI infrastructure, but it is also possible to run it as a CLI tool on a
developer laptop.

Note: ci-operator orchestrates builds and tests, but should not be confused
with [Kubernetes operator](https://coreos.com/operators/) which make managing
software on top of Kubernetes easier.

## Obtaining ci-operator

Currently, users must download the source and build it themselves:

```
$ git clone https://github.com/openshift/ci-operator.git
$ cd ci-operator
$ go build ./cmd/ci-operator
```

You can execute ci-operator locally afterwards:

```
./ci-operator --config master.json --namespace 'test-namespace' --git-ref=openshift/{repo}@master
```

See [ONBOARDING.md](ONBOARDING.md#config.json) on how to write repository
configuration.

## Onboarding a component to ci-operator and Prow

See [ONBOARD.md](ONBOARD.md)

## OpenShift components using ci-operator

A number of [OpenShift
components](https://github.com/openshift/release/tree/master/ci-operator/config)
are already using ci-operator.
