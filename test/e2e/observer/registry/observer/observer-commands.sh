#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

kubeconfig_received=0

function cleanup() {
  echo "Running cleanup after being terminated."
  if test $kubeconfig_received -eq 0
  then
    echo 'kubeconfig was not received'
    exit 1
  fi
  exit 0
}

trap cleanup EXIT
trap cleanup INT

echo "do-not-upload-me" >"${SHARED_DIR}/intruder"

for (( i=1; i<=300; i++ ))
do 
  echo "${i}: checking ${KUBECONFIG}"
  if test -s "$KUBECONFIG"
  then
    echo 'kubeconfig received!'
    kubeconfig_received=1
    break
  fi
  sleep 1
done
