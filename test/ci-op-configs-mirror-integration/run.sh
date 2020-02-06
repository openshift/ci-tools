#!/bin/bash

set -o errexit
set -o nounset

WORKDIR="$( mktemp -d )"
trap 'rm -rf "${WORKDIR}"' EXIT

TEST_ROOT="$( dirname "${BASH_SOURCE[0]}")"
INPUT_FILES="${TEST_ROOT}/data/input"
OUTPUT_FILES="${TEST_ROOT}/data/output"

cp -r ${INPUT_FILES} ${WORKDIR}

echo "[INFO] Running ci-op-configs-mirror..."
if ! ci-op-configs-mirror --to-org "super-priv" --config-path "${WORKDIR}/input" 2> "${WORKDIR}/ci-op-configs-mirror-stderr.log"; then
  echo "ERROR: ci-op-configs-mirror failed:"
  cat "${WORKDIR}/ci-op-configs-mirror-stderr.log"
  exit 1
fi

echo "[INFO] Validating generated ci-operator configuration files"
if ! diff -Naupr "${WORKDIR}/input" "${OUTPUT_FILES}"; then
  printf "ERROR: ci-op-configs-mirror output differs from expected\n"
  exit 1
fi

echo "[INFO] Success!"
