#!/bin/bash

# This script installs all go components into the environment's go workspace.
source "$(dirname "${BASH_SOURCE}")/lib/init.sh"

function cleanup() {
    return_code=$?
    os::util::describe_return_code "${return_code}"
    exit "${return_code}"
}
trap "cleanup" EXIT

RACE_FLAG=""
if [[ ${1:-} == "race" ]]; then
  export CGO_ENABLED=1
  RACE_FLAG="-race"
else
  export CGO_ENABLED=0
fi


git_commit="$( git describe --tags --always --dirty )"
build_date="$( date -u '+%Y%m%d' )"
version="v${build_date}-${git_commit}"

for dir in $( find ./cmd/ -mindepth 1 -maxdepth 1 -type d -not \( -name '*ipi-deprovison*' \) ); do
    command="$( basename "${dir}" )"
    set -o xtrace
    go install -v $RACE_FLAG -ldflags "-X 'k8s.io/test-infra/prow/version.Name=${command}' -X 'k8s.io/test-infra/prow/version.Version=${version}'" "./cmd/${command}/..."
    set +o xtrace
done
