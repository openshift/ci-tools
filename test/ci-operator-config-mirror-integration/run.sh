#!/bin/bash

set -o errexit
set -o nounset

WORKDIR="$( mktemp -d )"
trap 'rm -rf "${WORKDIR}"' EXIT

TEST_ROOT="$( dirname "${BASH_SOURCE[0]}")"
INPUT_FILES="${TEST_ROOT}/data/input"
OUTPUT_FILES="${TEST_ROOT}/data/output"
WHITELIST="${TEST_ROOT}/data/whitelist.yaml"

cp -r ${INPUT_FILES} ${WORKDIR}

echo "[INFO] Running ci-operator-config-mirror..."
if ! ci-operator-config-mirror --to-org "super-priv" --config-path "${WORKDIR}/input" --whitelist-file "${WHITELIST}" 2> "${WORKDIR}/ci-operator-config-mirror-stderr.log"; then
  echo "ERROR: ci-operator-config-mirror failed:"
  cat "${WORKDIR}/ci-operator-config-mirror-stderr.log"
  exit 1
fi

echo "[INFO] Validating generated ci-operator configuration files"
if ! diff -Naupr "${WORKDIR}/input" "${OUTPUT_FILES}"; then
  printf "ERROR: ci-operator-config-mirror output differs from expected\n"
  exit 1
fi

echo "[INFO] Success!"
