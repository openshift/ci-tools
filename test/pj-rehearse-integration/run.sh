#!/bin/bash

# This test sets up a temporary testing repository
# resembling `openshift/release` and tries to execute
# pj-rehearse as if run over a PR for that repo

set -o errexit
set -o nounset
set -o pipefail

workdir="$( mktemp -d )"
trap 'rm -rf "${workdir}"' EXIT

test_root="$( dirname "${BASH_SOURCE[0]}")"
master_data="${test_root}/master"
candidate_data="${test_root}/candidate"
fake_openshift_release="${workdir}/repo"
expected_rehearsed_jobs="${test_root}/expected_jobs"

echo "[INFO] Preparing fake input repository..."
mkdir "${fake_openshift_release}"
git -C "${fake_openshift_release}" init --quiet
cp -R "${master_data}"/* "${fake_openshift_release}"
git -C "${fake_openshift_release}" add ci-operator cluster
git -C "${fake_openshift_release}" commit -m "Master version of openshift/release" --quiet
base_sha="$(git -C "${fake_openshift_release}" rev-parse HEAD)"
cp -R "${candidate_data}"/* "${fake_openshift_release}"
git -C "${fake_openshift_release}" add ci-operator cluster
git -C "${fake_openshift_release}" commit -m "Candidate version of openshift/release" --quiet
candidate_sha="$(git -C "${fake_openshift_release}" rev-parse HEAD)"

echo "[INFO] Running pj-rehearse in dry-mode..."
submitted_yamls="${workdir}/rehearsals.yaml"
error_log="${workdir}/errors.log"
export JOB_SPEC='{"type":"presubmit","job":"pull-ci-openshift-release-master-rehearse","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"release","base_ref":"master","base_sha":"'${base_sha}'","pulls":[{"number":1234,"author":"petr-muller","sha":"'${candidate_sha}'"}]}}'

if ! pj-rehearse --dry-run=true --no-fail=false --candidate-path "${fake_openshift_release}" > "${submitted_yamls}" 2> "${error_log}"; then
  echo "[ERROR] pj-rehearse failed:"
  cat "${error_log}"
  exit 1
fi

echo "[INFO] Validating created rehearsals"
rehearsed_jobs="${workdir}/rehearsed_jobs"
grep "^  job: rehearse-1234-pull-ci" "${submitted_yamls}" | cut -c '8-' | sort  > "${rehearsed_jobs}"
if ! diff -u "${expected_rehearsed_jobs}" "${rehearsed_jobs}" > "${workdir}/diff"; then
  echo "[ERROR] pj-rehearse would attempt to rehearse unexpected jobs:"
  cat "${workdir}/diff"
  exit 1
fi

echo "[INFO] Success!"
