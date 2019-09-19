#!/bin/bash
set -euo pipefail

WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}"' EXIT

TEST_ROOT="$(dirname "${BASH_SOURCE[0]}")"
readonly TEST_ROOT
readonly TEST_CONFIG="${TEST_ROOT}/config.yaml"
readonly TEST_NAMESPACE="testns"
readonly EXPECTED="${TEST_ROOT}/expected.json"
readonly OUT="${WORKDIR}/out.json"
readonly ERR="${WORKDIR}/err.json"
readonly ARTIFACT_DIR="${WORKDIR}/artifacts"

export JOB_SPEC='{"type":"presubmit","job":"pull-ci-openshift-release-master-ci-operator-integration","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"af8a90a2faf965eeda949dc1c607c48d3ffcda3e","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]}}'
# set by Prow
unset BUILD_ID

echo "[INFO] Running ci-operator in dry-mode..."
if ! ci-operator --dry-run --determinize-output --namespace "${TEST_NAMESPACE}" --config "${TEST_CONFIG}" 2> "${ERR}" | jq --sort-keys . > "${OUT}"; then
    echo "ERROR: ci-operator failed."
    cat "${ERR}"
    exit 1
fi

if ! diff "${EXPECTED}" "${OUT}"; then
    echo "ERROR: differences have been found"
    exit 1
fi

echo "[INFO] Success"
