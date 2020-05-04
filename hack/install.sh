#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# if we're not building an official output binary, we don't care to tag it
if [[ -z "${OPENSHIFT_CI:-}" ]]; then
	go install ./cmd/...
	exit
fi

git_commit="$( git describe --tags --always --dirty )"
build_date="$( date -u '+%Y%m%d' )"
version="v${build_date}-${git_commit}"

for dir in $( find ./cmd/ -mindepth 1 -maxdepth 1 -type d ); do
	command="$( basename "${dir}" )"
	go install -ldflags "-X 'k8s.io/test-infra/prow/version.Name=${command}' -X 'k8s.io/test-infra/prow/version.Version=${version}'" "./cmd/${command}/..."
done
