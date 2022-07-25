#!/usr/bin/env bash

# E.g., images/ci-secret-generator/oc_sa_create_kubeconfig.sh /tmp build01 default

set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "require exactly 3 args"
  exit 1
fi

CONFIG_UPDATER_DIR=$1
readonly CONFIG_UPDATER_DIR
CLUSTER=$2
readonly CLUSTER
SERVICE_ACCOUNT=$3
readonly SERVICE_ACCOUNT

TMP_KUBE_CONFIG_FILE="$(mktemp)"
trap 'rm -rf {TMP_KUBE_CONFIG_FILE}' EXIT


URL=$(oc --kubeconfig "${CONFIG_UPDATER_DIR}/sa.config-updater.${CLUSTER}.config" config view -o jsonpath="{.clusters[0].cluster.server}")
TOKEN=$(oc --kubeconfig "${CONFIG_UPDATER_DIR}/sa.config-updater.${CLUSTER}.config" create token -n ci ${SERVICE_ACCOUNT} --duration=2419200s)
oc --kubeconfig "${TMP_KUBE_CONFIG_FILE}" login "${URL}" --token "${TOKEN}" > /dev/null
cat "${TMP_KUBE_CONFIG_FILE}" | sed "s/${SERVICE_ACCOUNT}/${CLUSTER}/g"
