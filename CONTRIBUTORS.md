# Contributing to ci-operator

## Contribution submissions

Contributions are accepted via standard GitHub pull request mechanism. Pull
requests will be automatically tested and all tests need to pass (follow the
instructions of the CI bot which will comment on your pull requests).
Additionally, the PR needs to be approved by one of the core project members
(see the [OWNERS](OWNERS) file).

## Build

To obtain sources and build the binaries provided by this repo, run the following commands:

```
$ go get github.com/openshift/ci-tools
$ cd ${GOPATH}/src/github.com/openshift/ci-tools
$ make build-bin
```

The binaries are in `./_output/` folder.

## Test

Run unit tests with `make test`:

```
$ make test
go test ./...
ok      github.com/openshift/ci-tools/cmd/applyconfig   (cached)
...
```

## Upgrade dependencies

`ci-tools` uses [`go-mod`](https://github.com/golang/go/wiki/Modules)
to manage dependencies and generates the binaries [with vendor](https://github.com/golang/go/wiki/Modules#how-do-i-use-vendoring-with-modules-is-vendoring-going-away).

Eg, upgrade the version of `k8s.io/test-infra` to the latest of it master branch.

```
$ GO111MODULE=on go get k8s.io/test-infra
$ GO111MODULE=on go mod vendor
```

TODO: Figure out the error when

```
###From `go help get`
###The -u flag instructs get to use the network to update the named packages
###and their dependencies. By default, get uses the network to check out
###missing packages but does not use it to look for updates to existing packages.
$ GO111MODULE=on go get -u k8s.io/test-infra
...
go get: error loading module requirements

```