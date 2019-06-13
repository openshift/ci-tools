#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

workdir="$( mktemp -d )"
trap 'rm -rf "${workdir}"' EXIT

git clone https://github.com/openshift/release.git --depth 1 "${workdir}/release"

ci-operator-prowgen --from-dir "${workdir}/release/ci-operator/config" --to-dir "${workdir}/release/ci-operator/jobs"

pushd "${workdir}/release"

if [ -n "$(git status --porcelain)" ]; then
  echo "[ERROR] Changes in openshift/release:"
  git diff
  echo "[ERROR] Running Prowgen in openshift/release results in changes ^^^"
  echo "[ERROR] To avoid breaking openshift/release for everyone you should regenerate"
  echo "[ERROR] the jobs there and merge the changes ASAP after this change to Prowgen"
  popd
  exit 1
else
  echo "Running Prowgen in openshift/release does not result in changes, no followups needed"
fi

popd
