#!/usr/bin/env bash

# oc --context app.ci extract secret/config-updater -n ci --to=/tmp --confirm
# E.g., images/ci-secret-generator/oc_create_kubeconfig.sh /tmp build01 default ci

set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "require exactly 4 args"
  exit 1
fi

TMP_FILE="$(mktemp)"
trap 'rm -rf ${TMP_FILE}' EXIT

oc_cmd="${oc_cmd:-oc}"

CONFIG_UPDATER_DIR=$1
readonly CONFIG_UPDATER_DIR
CLUSTER=$2
readonly CLUSTER
SERVICE_ACCOUNT=$3
readonly SERVICE_ACCOUNT
SA_NAMESPACE=$4
readonly=SA_NAMESPACE

if [ ! -f "${CONFIG_UPDATER_DIR}/sa.config-updater.${CLUSTER}.config" ]
then
    >&2 echo "error: file ${CONFIG_UPDATER_DIR}/sa.config-updater.${CLUSTER}.config does not exist!"
    exit 1
fi

API_SERVER_URL=$(${oc_cmd} --kubeconfig "${CONFIG_UPDATER_DIR}/sa.config-updater.${CLUSTER}.config" config view -o jsonpath="{.clusters[0].cluster.server}")

if [ -z "{API_SERVER_URL}" ]
then
      >&2 echo "\${API_SERVER_URL} is empty"
      exit 1
fi

cat >"${TMP_FILE}" <<'EOL'
apiVersion: v1
clusters:
- cluster:
    server: {{API_SERVER_URL}}
  name: {{CLUSTER}}
contexts:
- context:
    cluster: {{CLUSTER}}
    namespace: {{SA_NAMESPACE}}
    user: {{SERVICE_ACCOUNT}}
  name: {{CLUSTER}}
current-context: {{CLUSTER}}
kind: Config
preferences: {}
users:
- name: {{SERVICE_ACCOUNT}}
  user:
    tokenFile: sa.{{SERVICE_ACCOUNT}}.{{CLUSTER}}.token.txt
EOL

sed "s/{{CLUSTER}}/${CLUSTER}/g;s/{{SERVICE_ACCOUNT}}/${SERVICE_ACCOUNT}/g;s/{{SA_NAMESPACE}}/${SA_NAMESPACE}/g;s/{{API_SERVER_URL}}/${API_SERVER_URL//\//\\/}/g" ${TMP_FILE}
