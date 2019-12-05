#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

WORKDIR="$( mktemp -d )"
trap 'rm -rf "${WORKDIR}"' EXIT

TEST_ROOT="$( dirname "${BASH_SOURCE[0]}")"

readonly OUTPUT="${WORKDIR}/output.yaml"
readonly ERROR_OUTPUT="${WORKDIR}/group-auto-updater-stderr.log"


GROUP="test-group"
PERIBOLOS_CONFIG="${TEST_ROOT}/data/peribolos-config.yaml"
ORG="test-org"


compare_to_expected() {
  local expected="${TEST_ROOT}/data/expected.yaml"
  local output="$1"
  diff -Naupr "$expected" "$output"
}


if ! group-auto-updater --group "${GROUP}" --peribolos-config "${PERIBOLOS_CONFIG}" --org "${ORG}" --dry-run > "${OUTPUT}" 2> "${ERROR_OUTPUT}";then
  echo "ERROR: group-auto-updater failed:"
  cat "${ERROR_OUTPUT}"
  exit 1
fi


echo "[INFO] Validating updated group"
if ! output="$(compare_to_expected "${OUTPUT}")"; then
  cat "${ERROR_OUTPUT}"
  output="$( printf -- "${output}" | sed 's/^/ERROR: /' )"
  printf "ERROR: group-auto-updater output differs from expected:\n\n$output\n"
  exit 1
fi

echo "[INFO] Success!"
