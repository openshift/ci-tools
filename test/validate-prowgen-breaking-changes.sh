#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT

git clone https://github.com/openshift/release.git --depth 1 "${workdir}/release"

ci-operator-prowgen --from-dir "${workdir}/release/ci-operator/config" --to-dir "${workdir}/release/ci-operator/jobs"

out="$(git -C "${workdir}/release" status --porcelain)"
if [[ -n "$out" ]]; then
  echo "ERROR: Changes in openshift/release:"
  git -C "${workdir}/release" diff
  echo "ERROR: Running Prowgen in openshift/release results in changes ^^^"
  echo "ERROR: To avoid breaking openshift/release for everyone you should regenerate"
  echo "ERROR: the jobs there and merge the changes ASAP after this change to Prowgen"
  exit 1
else
  echo "Running Prowgen in openshift/release does not result in changes, no followups needed"
fi

determinize-prow-config --prow-config-dir "${workdir}/release/core-services/prow/02_config"
out="$(git -C "${workdir}/release" status --porcelain)"
if [[ -n "$out" ]]; then
  echo "ERROR: Changes in openshift/release:"
  git -C "${workdir}/release" diff
  echo "ERROR: Running determinize-prow-config in openshift/release results in changes ^^^"
  echo "ERROR: To avoid breaking openshift/release for everyone you should make a PR there"
  echo "ERROR: to include these changes and merge it ASAP after this change to ci-tools"
  exit 1
else
  echo "Running determinize-prow-config in openshift/release does not result in changes, no followups needed"
fi
