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

if [[ ${2:-} == "remove-dummy" ]]; then
  rm -f cmd/pod-scaler/frontend/dist/dummy # we keep this file in git to keep the thing compiling without static assets
  rm -f cmd/repo-init/frontend/dist/dummy
fi

if [[ -n "${SKIPPED_IMAGES:-}" ]]; then
  echo "Skipping images: ${SKIPPED_IMAGES}"
fi

for dir in $( find ./cmd/ -mindepth 1 -maxdepth 1 -type d -not \( -name '*ipi-deprovison*' \) ); do
    command="$( basename "${dir}" )"
    if [[ -n "${SKIPPED_IMAGES:-}" ]]; then
        IFS=',' read -ra skipped <<< "${SKIPPED_IMAGES}"
        should_skip=0
        for img in "${skipped[@]}"; do
            if [[ "${img}" == "${command}" ]]; then
                should_skip=1
                break
            fi
        done
        if [[ "${should_skip}" -eq 1 ]]; then
            echo "Skipping install for ${command} (in SKIPPED_IMAGES)"
            continue
        fi
    fi
    go install -v $RACE_FLAG -ldflags "-X 'sigs.k8s.io/prow/pkg/version.Name=${command}' -X 'sigs.k8s.io/prow/pkg/version.Version=${version}'" "./cmd/${command}/..."
done
