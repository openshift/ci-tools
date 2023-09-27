#!/bin/bash
# To execute from a local clone:
#
#     $ RELEASE=file:///path/to/release test/validate-generation-breaking-changes.sh
set -o errexit
set -o nounset
set -o pipefail

log() {
    echo >&2 "$(date --iso-8601=seconds)" "$@"
}

org=openshift
RELEASE=${RELEASE-https://github.com/$org/release.git}
workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT
clonedir="${workdir}/release"
failure=0

rm -rf "${clonedir}"
log "Cloning openshift/release"
git clone "${RELEASE}" --depth 1 "${clonedir}"

# We need to enter the git directory and run git commands from there, our git
# is too old to know the `-C` option.
pushd "${clonedir}"

log "Executing ci-operator-prowgen"
ci-operator-prowgen --from-dir "${clonedir}/ci-operator/config" --to-dir "${clonedir}/ci-operator/jobs"
out="$(git status --porcelain)"
if [[ -n "$out" ]]; then
  echo "ERROR: Changes in openshift/release:"
  git diff
  echo "ERROR: Running Prowgen in openshift/release results in changes ^^^"
  echo "ERROR: To avoid breaking openshift/release for everyone you should regenerate"
  echo "ERROR: the jobs there and merge the changes ASAP after this change to Prowgen"
  failure=1
else
  echo "Running Prowgen in openshift/release does not result in changes, no followups needed"
fi

log "Executing sanitize-prow-jobs"
sanitize-prow-jobs --prow-jobs-dir ci-operator/jobs --config-path core-services/sanitize-prow-jobs/_config.yaml
out="$(git status --porcelain)"
if [[ -n "$out" ]]; then
  echo "ERROR: Changes in openshift/release:"
  git diff
  echo "ERROR: Running sanitize-prow-jobs in openshift/release results in changes ^^^"
  echo "ERROR: To avoid breaking openshift/release for everyone you should regenerate"
  echo "ERROR: the jobs there and merge the changes ASAP after this change"
  failure=1
else
  echo "Running sanitize-prow-jobs in openshift/release does not result in changes, no followups needed"
fi

CONFIG="${clonedir}/core-services/prow/02_config"
if [[ -d "${CONFIG}" ]]; then
  log "Executing determinize-prow-config"
  determinize-prow-config --prow-config-dir "${CONFIG}" --sharded-plugin-config-base-dir "${CONFIG}"
  out="$(git status --porcelain)"
  if [[ -n "$out" ]]; then
    echo "ERROR: Changes in openshift/release:"
    git diff
    echo "ERROR: Running determinize-prow-config in openshift/release results in changes ^^^"
    echo "ERROR: To avoid breaking openshift/release for everyone you should make a PR there"
    echo "ERROR: to include these changes and merge it ASAP after this change to ci-tools"
    failure=1
  else
    echo "Running determinize-prow-config in openshift/release does not result in changes, no followups needed"
  fi
fi

log "Executing cluster-init update"
cluster-init -release-repo="${clonedir}" -update=true -create-pr=false
out="$(git status --porcelain)"
if [[ -n "$out" ]]; then
  echo "ERROR: Changes in openshift/release:"
  git diff
  echo "ERROR: Running cluster-init in update mode in openshift/release results in changes ^^^"
  echo "ERROR: To avoid breaking openshift/release for everyone you should regenerate the build clusters"
  echo "ERROR: there and merge the changes ASAP after this change to cluster-init"
  failure=1
else
  echo "Running cluster-init in update mode in openshift/release does not result in changes, no followups needed"
fi

popd

exit $failure
