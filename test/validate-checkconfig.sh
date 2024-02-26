#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT
clonedir="${workdir}/release"
failure=0

# The `openshift/release` registry is used for all repositories.
registry="${clonedir}/openshift/ci-operator/step-registry"

# We no longer have any production config in redhat-openshift-ecosystem
# for org in openshift redhat-openshift-ecosystem; do
for org in openshift; do
  git clone "https://github.com/${org}/release.git" --depth 1 "${clonedir}/${org}"

  # We need to enter the git directory and run git commands from there, our git
  # is too old to know the `-C` option.
  pushd "${clonedir}/${org}"

  # First we'll run registry-replacer to prune unused base images.
  registry-replacer \
    --config-dir ci-operator/config \
    --registry "${registry}" \
    --prune-unused-base-images=true \
    --apply-replacements=false

  if ! ci-operator-checkconfig \
    --config-dir ci-operator/config \
    --cluster-profiles-config core-services/cluster-profiles/_config.yaml \
    --cluster-claim-owners-config core-services/cluster-pools/_config.yaml \
    --registry "${registry}"
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
