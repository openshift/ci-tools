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
$ make install
```

The binaries are in `${GOPATH}/bin` folder.

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
****