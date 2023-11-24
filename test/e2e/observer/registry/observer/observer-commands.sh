#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

function cleanup() {
  echo "Running cleanup after being terminated."
}

trap cleanup EXIT
trap cleanup INT

echo "do-not-upload-me" >"${SHARED_DIR}/intruder"

for (( i=1; i<=300; i++ )); do 
  echo "${i}: checking ${KUBECONFIG}"
  if test -s "$KUBECONFIG"; then
    echo 'kubeconfig received!'
    exit 0
  fi
  sleep 1
done

echo 'kubeconfig was not received'
exit 1