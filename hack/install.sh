#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

export CGO_ENABLED=0

git_commit="$( git describe --tags --always --dirty )"
build_date="$( date -u '+%Y%m%d' )"
version="v${build_date}-${git_commit}"

for dir in $( find ./cmd/ -mindepth 1 -maxdepth 1 -type d ); do
	command="$( basename "${dir}" )"
	go install -ldflags "-X 'k8s.io/test-infra/prow/version.Name=${command}' -X 'k8s.io/test-infra/prow/version.Version=${version}'" "./cmd/${command}/..."
done
