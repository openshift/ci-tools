#!/bin/bash

set -euo pipefail

WORKDIR="$( mktemp -d )"
trap 'rm -rf "${WORKDIR}"' EXIT

readonly TEST_ROOT="$( dirname "${BASH_SOURCE[0]}")"
readonly TEST_CONFIG="${TEST_ROOT}/config/test-config.yaml"
readonly TEST_TEMPLATE="${TEST_ROOT}/config/test-template.yaml"
readonly TEST_NAMESPACE="testns"

readonly EXPECTED="${TEST_ROOT}/expected_files/expected.json"
readonly EXPECTED_WITH_TEMPLATE="${TEST_ROOT}/expected_files/expected_with_template.json"

readonly DRY_RUN_JSON="${WORKDIR}/ci-op-dry.json"
readonly DRY_RUN_WITH_TEMPLATE_JSON="${WORKDIR}/ci-op-template-dry.json"

readonly ARTIFACT_DIR="${WORKDIR/artifacts}"

export JOB_SPEC='{"type":"presubmit","job":"pull-ci-openshift-release-master-ci-operator-integration","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"af8a90a2faf965eeda949dc1c607c48d3ffcda3e","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]}}'
unset BUILD_ID

echo "[INFO] Running ci-operator in dry-mode..."
if ! ci-operator --dry-run --determinize-output --namespace "${TEST_NAMESPACE}" --config "${TEST_CONFIG}" 2> "${WORKDIR}/ci-op-stderr.log" | jq -S . > "${DRY_RUN_JSON}"; then
    echo "ERROR: ci-operator failed."
    cat "${WORKDIR}/ci-op-stderr.log"
    exit 1
fi

if ! diff "${EXPECTED}" "${DRY_RUN_JSON}"; then
    echo "ERROR: differences have been found"
    exit 1
fi


echo "[INFO] Running ci-operator with a template"
export IMAGE_FORMAT="test"
export CLUSTER_TYPE="aws"
export TEST_COMMAND="test command"

if ! ci-operator --dry-run --determinize-output --namespace "${TEST_NAMESPACE}" --config "${TEST_CONFIG}" --template "${TEST_TEMPLATE}" --target test-template --artifact-dir "${ARTIFACT_DIR}" 2> "${WORKDIR}/ci-op-stderr.log" | jq -S . > "${DRY_RUN_WITH_TEMPLATE_JSON}"; then
    echo "ERROR: ci-operator failed."
    cat "${WORKDIR}/ci-op-stderr.log"
    exit 1
fi

if ! diff "${EXPECTED_WITH_TEMPLATE}" "${DRY_RUN_WITH_TEMPLATE_JSON}"; then
    echo "ERROR: differences have been found"
    exit 1
fi

echo "[INFO] Success"
