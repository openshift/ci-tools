#!/usr/bin/env bash

# E.g., images/ci-secret-generator/oc_sa_create_kubeconfig.sh /tmp build01 default

set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "require exactly 4 args"
  exit 1
fi

oc_cmd="${oc_cmd:-oc}"

CONFIG_UPDATER_DIR=$1
readonly CONFIG_UPDATER_DIR
CLUSTER=$2
readonly CLUSTER
SERVICE_ACCOUNT=$3
readonly SERVICE_ACCOUNT
SA_NAMESPACE=$4
readonly=SA_NAMESPACE

TMP_KUBE_CONFIG_FILE="$(mktemp)"
trap 'rm -rf ${TMP_KUBE_CONFIG_FILE}' EXIT

URL=$(${oc_cmd} --kubeconfig "${CONFIG_UPDATER_DIR}/sa.config-updater.${CLUSTER}.config" config view -o jsonpath="{.clusters[0].cluster.server}")
TOKEN=$(${oc_cmd} --kubeconfig "${CONFIG_UPDATER_DIR}/sa.config-updater.${CLUSTER}.config" create token -n ${SA_NAMESPACE} ${SERVICE_ACCOUNT} --duration=2419200s)

${oc_cmd} --kubeconfig "${TMP_KUBE_CONFIG_FILE}" login "${URL}" --token "${TOKEN}" > /dev/null

CONTEXT_NAME=$(${oc_cmd} --kubeconfig "${TMP_KUBE_CONFIG_FILE}" config current-context)
#It is required by Prow that the current context name is the cluster name
${oc_cmd} --kubeconfig "${TMP_KUBE_CONFIG_FILE}" config rename-context ${CONTEXT_NAME} ${CLUSTER} > /dev/null
#oc410 sa create-kubeconfig -h
#Generate a kubeconfig file that will utilize this service account.
#
#The kubeconfig file will reference the service account token and use the current server, namespace,
#and cluster contact info.
${oc_cmd} --kubeconfig "${TMP_KUBE_CONFIG_FILE}" config set-context --current --namespace=${SA_NAMESPACE} > /dev/null

#Validate before output
WHO_AM_I=$(${oc_cmd} --kubeconfig "${TMP_KUBE_CONFIG_FILE}" whoami)
readonly WHO_AM_I
if [[ "${WHO_AM_I}" != "system:serviceaccount:${SA_NAMESPACE}:${SERVICE_ACCOUNT}" ]]; then
  >&2 echo "error: whoami is ${WHO_AM_I} while expecting "system:serviceaccount:${SA_NAMESPACE}:${SERVICE_ACCOUNT}""
  exit 1
fi


CURRENT_CONTEXT_NAME=$(${oc_cmd} --kubeconfig "${TMP_KUBE_CONFIG_FILE}" config current-context)
readonly CURRENT_CONTEXT_NAME
if [[ "${CURRENT_CONTEXT_NAME}" != "${CLUSTER}" ]]; then
  >&2 echo "error: current context name is ${CURRENT_CONTEXT_NAME} while expecting "${CLUSTER}""
  exit 1
fi

CURRENT_PROJECT=$(${oc_cmd} --kubeconfig "${TMP_KUBE_CONFIG_FILE}" config view --minify -o jsonpath='{..namespace}')
readonly CURRENT_PROJECT
if [[ "${CURRENT_PROJECT}" != "${SA_NAMESPACE}" ]]; then
  >&2 echo "error: current project is ${CURRENT_CONTEXT_NAME} while expecting "${SA_NAMESPACE}""
  exit 1
fi

cat "${TMP_KUBE_CONFIG_FILE}"

