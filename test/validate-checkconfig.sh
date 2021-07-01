#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT
clonedir="${workdir}/release"
failure=0

for org in openshift redhat-operator-ecosystem; do
  rm -rf "${clonedir}"
  git clone "https://github.com/${org}/release.git" --depth 1 "${clonedir}"

  # We need to enter the git directory and run git commands from there, our git
  # is too old to know the `-C` option.
  pushd "${clonedir}"
  if ! ci-operator-checkconfig \
      --config-dir "${clonedir}/ci-operator/config" \
      --registry "${clonedir}/ci-operator/step-registry"
  then
    echo "ERROR: Errors in $org/release"
    echo
    echo "ERROR: Running ci-operator-checkconfig in $org/release results in errors"
    echo "ERROR: To avoid breaking $org/release for everyone you should make a PR there"
    echo "ERROR: correcting these and merge it before this change to ci-tools"
    failure=1
  else
    echo "Running ci-operator-checkconfig in $org/release does not result in errors, no corrections needed"
  fi
  popd
done

exit $failure
