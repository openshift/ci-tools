#!/bin/bash

set -euo pipefail

WORKDIR="$( mktemp -d )"
trap 'rm -rf "${WORKDIR}"' EXIT

TEST_ROOT="$(dirname "${BASH_SOURCE[0]}")"
readonly TEST_ROOT
readonly TEST_CONFIG="${TEST_ROOT}/config/test-config.yaml"
readonly TEST_TEMPLATE="${TEST_ROOT}/config/test-template.yaml"
readonly TEST_NAMESPACE="testns"

readonly EXPECTED="${TEST_ROOT}/expected_files/expected.json"
readonly EXPECTED_WITH_TEMPLATE="${TEST_ROOT}/expected_files/expected_with_template.json"
readonly EXPECTED_WITH_OAUTH="${TEST_ROOT}/expected_files/expected_src_oauth.json"
readonly EXPECTED_WITH_SSH="${TEST_ROOT}/expected_files/expected_src_ssh.json"
EXPECTED_WITH_PULL_SECRET="${TEST_ROOT}/expected_files/expected_pull_secret.json"
readonly EXPECTED_WITH_PULL_SECRET

readonly DRY_RUN_JSON="${WORKDIR}/ci-op-dry.json"
readonly DRY_RUN_WITH_TEMPLATE_JSON="${WORKDIR}/ci-op-template-dry.json"
readonly DRY_RUN_WITH_OAUTH="${WORKDIR}/ci-op-oauth-dry.json"
readonly DRY_RUN_WITH_SSH="${WORKDIR}/ci-op-ssh-dry.json"
DRY_RUN_WITH_PULL_SECRET="${WORKDIR}/ci-op-pull-secret-dry.json"
readonly DRY_RUN_WITH_PULL_SECRET

readonly OAUTH_FILE="${TEST_ROOT}/auth_files/oauth-token"
readonly SSH_FILE="${TEST_ROOT}/auth_files/id_rsa"
readonly ARTIFACT_DIR="${WORKDIR}/artifacts"

export JOB_SPEC='{"type":"presubmit","job":"pull-ci-openshift-release-master-ci-operator-integration","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"af8a90a2faf965eeda949dc1c607c48d3ffcda3e","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]}}'
# set by Prow
unset BUILD_ID

run_test() {
    if ! ci-operator \
        --dry-run \
        --determinize-output \
        --namespace "${TEST_NAMESPACE}" \
        --config "${TEST_CONFIG}" \
        "$@" \
        2> "${WORKDIR}/ci-op-stderr.log" | jq --sort-keys .
    then
        echo >&2 "ERROR: ci-operator failed."
        cat >&2 "${WORKDIR}/ci-op-stderr.log"
        return 1
    fi
}

check() {
    if ! diff "$@"; then
        echo >"ERROR: differences have been found against $1"
        return 1
    fi
}

echo "[INFO] Running ci-operator in dry-mode..."
run_test --lease-server http://boskos.example.com > "${DRY_RUN_JSON}"
if [[ ${UPDATE:-false} = true ]]; then cat $DRY_RUN_JSON > $EXPECTED; fi
check "${EXPECTED}" "${DRY_RUN_JSON}"

echo "[INFO] Running ci-operator with a template"
IMAGE_FORMAT=test CLUSTER_TYPE=aws TEST_COMMAND='test command' \
    run_test > "${DRY_RUN_WITH_TEMPLATE_JSON}" \
    --template "${TEST_TEMPLATE}" \
    --target test-template \
    --artifact-dir "${ARTIFACT_DIR}"
if [[ ${UPDATE:-false} = true ]]; then cat $DRY_RUN_WITH_TEMPLATE_JSON > $EXPECTED_WITH_TEMPLATE; fi
check "${EXPECTED_WITH_TEMPLATE}" "${DRY_RUN_WITH_TEMPLATE_JSON}"

echo "[INFO] Running ci-operator with OAuth"
run_test > "${DRY_RUN_WITH_OAUTH}" \
    --oauth-token-path "${OAUTH_FILE}" \
    --artifact-dir "${ARTIFACT_DIR}" \
    --lease-server http://boskos.example.com
check \
    "${EXPECTED_WITH_OAUTH}" \
    <(jq '.[] | select(.metadata.name=="src")' "${DRY_RUN_WITH_OAUTH}")

echo "[INFO] Running ci-operator with SSH"
run_test > "${DRY_RUN_WITH_SSH}" \
    --ssh-key-path "${SSH_FILE}" \
    --artifact-dir "${ARTIFACT_DIR}" \
    --lease-server http://boskos.example.com
check \
    "${EXPECTED_WITH_SSH}" \
    <(jq '.[] | select(.metadata.name=="src")' "${DRY_RUN_WITH_SSH}")

PULL_SECRET_PATH="${WORKDIR}/pull_secret"
readonly PULL_SECRET_PATH
touch "${PULL_SECRET_PATH}"

echo "[INFO] Running ci-operator with a pull secret"
run_test --lease-server http://boskos.example.com --image-import-pull-secret "${PULL_SECRET_PATH}" > "${DRY_RUN_WITH_PULL_SECRET}"
if [[ ${UPDATE:-false} = true ]]; then cat $DRY_RUN_WITH_PULL_SECRET > $EXPECTED_WITH_PULL_SECRET; fi
check "${EXPECTED_WITH_PULL_SECRET}" "${DRY_RUN_WITH_PULL_SECRET}"

echo "[INFO] Success"
