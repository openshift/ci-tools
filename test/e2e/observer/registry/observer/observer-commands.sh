#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

function cleanup() {
  echo "Running cleanup after being terminated."
  echo -n "cancelled" > "${SHARED_DIR}/cancelled"
  exit 0
}

trap cleanup EXIT
trap cleanup INT

while true; do
    if [[ -f "${KUBECONFIG}" ]]; then
      echo "\$KUBECONFIG exists"
      break
    fi
    echo "\$KUBECONFIG does not exist, waiting..."
    sleep 1
done

echo -n "waited" > "${SHARED_DIR}/output"
sleep 360 &
wait