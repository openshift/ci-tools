#!/usr/bin/env bash

set -euo pipefail

oc extract secret/config-updater -n ci --to=/tmp --confirm

if [ "$#" -ne 3 ]; then
  echo "require exactly 4 args"
  exit 1
fi

OUTPUT_DIR=$1
readonly OUTPUT_DIR
SERVICE_ACCOUNT=$2
readonly SERVICE_ACCOUNT
SA_NAMESPACE=$3
readonly=SA_NAMESPACE

declare -a StringArray=( "app.ci" "build01" "build02" "build03" "build04" "build05"  "arm01" "vsphere" )

# Iterate the string array using for loop
for cluster in ${StringArray[@]}; do
   oc_cmd="${oc_cmd:-oc}" images/ci-secret-generator/oc_sa_create_kubeconfig.sh /tmp $cluster $SERVICE_ACCOUNT $SA_NAMESPACE > ${OUTPUT_DIR}/sa.$SERVICE_ACCOUNT.$cluster.config
done
