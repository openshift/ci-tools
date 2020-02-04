#!/bin/bash

set -o errexit
set -o nounset

WORKDIR="$( mktemp -d )"
trap 'rm -rf "${WORKDIR}"' EXIT

TEST_ROOT="$( dirname "${BASH_SOURCE[0]}")"
INPUT_FILES="${TEST_ROOT}/data/input"
EXPECTED="${TEST_ROOT}/expected_files/expected.yaml"
EXPECTED_SKIPPED="${TEST_ROOT}/expected_files/expected_skipped.yaml"


echo "[INFO] Running ci-op-configs-mirror in dry-mode..."
if ! ci-op-configs-mirror --to-org super-priv --config-path "${INPUT_FILES}" --dry-run 2> "${WORKDIR}/ci-op-configs-mirror-stderr.log" > "${WORKDIR}/ci-op-configs-mirror-stdout.log"; then
  echo "ERROR: ci-op-configs-mirror failed:"
  cat "${WORKDIR}/ci-op-configs-mirror-stderr.log"
  exit 1
fi

echo "[INFO] Validating generated ci-operator configuration files"
if ! diff -u "${EXPECTED}" "${WORKDIR}/ci-op-configs-mirror-stdout.log"; then
  printf "ERROR: ci-op-configs-mirror output differs from expected\n"
  exit 1
fi


echo "[INFO] Running ci-op-configs-mirror in dry-mode from specifig org..."
if ! ci-op-configs-mirror --from-org super --to-org super-priv --config-path "${INPUT_FILES}" --dry-run 2> "${WORKDIR}/ci-op-configs-mirror-stderr_skipped.log" > "${WORKDIR}/ci-op-configs-mirror-stdout_skipped.log"; then
  echo "ERROR: ci-op-configs-mirror failed:"
  cat "${WORKDIR}/ci-op-configs-mirror-stderr.log"
  exit 1
fi

echo "[INFO] Validating generated ci-operator configuration files"
if ! diff -u "${EXPECTED_SKIPPED}" "${WORKDIR}/ci-op-configs-mirror-stdout_skipped.log"; then
  printf "ERROR: ci-op-configs-mirror output differs from expected\n"
  exit 1
fi



echo "[INFO] Success!"

