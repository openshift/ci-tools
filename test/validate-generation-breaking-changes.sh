#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT
clonedir="${workdir}/release"
failure=0

# We no longer have any production config in redhat-openshift-ecosystem
# for org in openshift redhat-openshift-ecosystem; do
for org in openshift; do
  rm -rf "${clonedir}"
  echo >&2 "$(date --iso-8601=seconds) Cloning ${org}/release"
  git clone "https://github.com/${org}/release.git" --depth 1 "${clonedir}"

  # We need to enter the git directory and run git commands from there, our git
  # is too old to know the `-C` option.
  pushd "${clonedir}"

  echo >&2 "$(date --iso-8601=seconds) Executing ci-operator-prowgen"
  ci-operator-prowgen --from-dir "${clonedir}/ci-operator/config" --to-dir "${clonedir}/ci-operator/jobs"
  out="$(git status --porcelain)"
  if [[ -n "$out" ]]; then
    echo "ERROR: Changes in $org/release:"
    git diff
    echo "ERROR: Running Prowgen in $org/release results in changes ^^^"
    echo "ERROR: To avoid breaking $org/release for everyone you should regenerate"
    echo "ERROR: the jobs there and merge the changes ASAP after this change to Prowgen"
    failure=1
  else
    echo "Running Prowgen in $org/release does not result in changes, no followups needed"
  fi

  CONFIG="${clonedir}/core-services/prow/02_config"
  if [[ -d "${CONFIG}" ]]; then
    echo >&2 "$(date --iso-8601=seconds) Executing determinize-prow-config"
    determinize-prow-config --prow-config-dir "${CONFIG}" --sharded-plugin-config-base-dir "${CONFIG}"
    out="$(git status --porcelain)"
    if [[ -n "$out" ]]; then
      echo "ERROR: Changes in $org/release:"
      git diff
      echo "ERROR: Running determinize-prow-config in $org/release results in changes ^^^"
      echo "ERROR: To avoid breaking $org/release for everyone you should make a PR there"
      echo "ERROR: to include these changes and merge it ASAP after this change to ci-tools"
      failure=1
    else
      echo "Running determinize-prow-config in $org/release does not result in changes, no followups needed"
    fi
  fi

  echo >&2 "$(date --iso-8601=seconds) Executing cluster-init update"
    cluster-init -release-repo="${clonedir}" -update=true -create-pr=false
    out="$(git status --porcelain)"
    if [[ -n "$out" ]]; then
      echo "ERROR: Changes in $org/release:"
      git diff
      echo "ERROR: Running cluster-init in update mode in $org/release results in changes ^^^"
      echo "ERROR: To avoid breaking $org/release for everyone you should regenerate the build clusters"
      echo "ERROR: there and merge the changes ASAP after this change to cluster-init"
      failure=1
    else
      echo "Running cluster-init in update mode in $org/release does not result in changes, no followups needed"
    fi

  popd
done

exit $failure
