#!/usr/bin/env bash

set -euxo pipefail

echo "Running golangci-lint"
# CI has HOME set to '/' causing the linter to try and create a cache at /.cache for which
# it doesn't have permissions.
if [[ $HOME = '/' ]]; then
  export HOME=/tmp
fi

# We embed this so it must exist for compilation to succeed, but it's not checked in
if [[ -n ${CI:-} ]]; then touch cmd/vault-secret-collection-manager/index.js; fi

GOLANGCI_LINT_ARGS="--build-tags=e2e,e2e_framework,optional_operators"

if [[ -n ${CI:-} ]];
then
  golangci-lint run "$GOLANGCI_LINT_ARGS"
else
  DOCKER=${DOCKER:-podman}

  if ! which "$DOCKER" > /dev/null 2>&1;
  then
    echo "$DOCKER not found, please install."
    exit 1
  fi

  $DOCKER run --rm \
    --volume "${PWD}:/go/src/github.com/openshift/ci-tools:z" \
    --workdir /go/src/github.com/openshift/ci-tools \
    docker.io/golangci/golangci-lint:v1.48.0 \
    golangci-lint run "$GOLANGCI_LINT_ARGS"
fi
