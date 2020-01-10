#!/bin/bash

# This test sets up a temporary testing repository
# resembling `openshift/release` and tries to execute
# pj-rehearse as if run over a PR for that repo

set -o errexit
set -o nounset
set -o pipefail

WORKDIR="$( mktemp -d )"
trap 'rm -rf "${WORKDIR}"' EXIT

TEST_ROOT="$( dirname "${BASH_SOURCE[0]}")"
readonly FAKE_OPENSHIFT_RELEASE="${WORKDIR}/repo"

make_testing_repository() {
  local -r master_data="${PWD}/${TEST_ROOT}/master"
  local -r candidate_data="${PWD}/${TEST_ROOT}/candidate"
  local -r registry="${PWD}/${TEST_ROOT}/../multistage-registry/registry"
  local -r registry2="${PWD}/${TEST_ROOT}/../multistage-registry/registry2"
  local base_sha
  local candidate_sha

  echo "[INFO] Preparing fake input repository..."
  mkdir "${FAKE_OPENSHIFT_RELEASE}"
  pushd "${FAKE_OPENSHIFT_RELEASE}" >/dev/null
  git init --quiet
  git config --local user.name test
  git config --local user.email test
  cp -R "${master_data}"/* .
  cp -R  "${registry}"/ ./ci-operator/step-registry
  git add ci-operator core-services cluster
  git commit -m "Master version of openshift/release" --quiet
  base_sha="$(git rev-parse HEAD)"
  cp -R "${candidate_data}"/* .
  rm -rf ./ci-operator/step-registry
  cp -R  "${registry2}"/ ./ci-operator/step-registry
  git add ci-operator core-services cluster
  git commit -m "Candidate version of openshift/release" --quiet
  candidate_sha="$(git rev-parse HEAD)"
  popd >/dev/null

  export JOB_SPEC='{"type":"presubmit","job":"pull-ci-openshift-release-master-rehearse","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"release","base_ref":"master","base_sha":"'${base_sha}'","pulls":[{"number":1234,"author":"petr-muller","sha":"'${candidate_sha}'"}]}}'
}

compare_to_expected() {
  local expected="${TEST_ROOT}/expected.yaml"
  local rehearsed="$1"
  diff -u "$expected" "$rehearsed"  \
    --ignore-matching-lines 'startTime' \
    --ignore-matching-lines 'name: \w\{8\}\(-\w\{4\}\)\{3\}-\w\{12\}' \
    --ignore-matching-lines 'sha: \w\{40\}'
}

make_testing_repository

readonly REHEARSED_JOBS="${WORKDIR}/rehearsals.yaml"
echo "[INFO] Running pj-rehearse in dry-mode..."
if ! pj-rehearse --dry-run=true --no-fail=false --candidate-path "${FAKE_OPENSHIFT_RELEASE}" --rehearsal-limit 20 > "${REHEARSED_JOBS}" 2> "${WORKDIR}/pj-rehearse-stderr.log"; then
  echo "ERROR: pj-rehearse failed:"
  cat "${WORKDIR}/pj-rehearse-stderr.log"
  exit 1
fi

echo "[INFO] Validating created rehearsals"
if ! output="$(compare_to_expected "${REHEARSED_JOBS}")"; then
  cat "${WORKDIR}/pj-rehearse-stderr.log"
  output="$( printf -- "${output}" | sed 's/^/ERROR: /' )"
  printf "ERROR: pj-rehearse output differs from expected:\n\n$output\n"
  exit 1
fi

echo "[INFO] Success!"
