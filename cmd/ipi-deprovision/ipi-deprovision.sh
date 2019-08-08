#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

### cluster_age_cutoff is going to be used as argument in the aws command below
### to filter out the expired clusters with the tags of the key expirationDate
### which is always with timezone +00:00, eg, "2019-07-23T22:35+0000".
### CLUSTER_TTL controlls the relative time to the running time of this script.
### eg, export CLUSTER_TTL="22 hours ago"
echo "Searching for clusters with a TTL of ${CLUSTER_TTL}"
cluster_age_cutoff="$(TZ=":Africa/Abidjan" date --date="${CLUSTER_TTL}" '+%Y-%m-%dT%H:%M+0000')"

echo "cluster_age_cutoff: ${cluster_age_cutoff}"

### eg, export AWS_REGIONS="us-east-1;us-west-1"
echo "take regions from env. var.: AWS_REGIONS: ${AWS_REGIONS}"
IFS=';' read -r -a regions <<< "$AWS_REGIONS"

handle_cluster () {
  local cluster_name
  cluster_name=$1
  local expirationDateValue
  expirationDateValue=$2
  local region
  region=$3
  echo "handling cluster: ${cluster_name} in region ${region} with expirationDate: ${expirationDateValue}"
  timeout 10m ./bin/hiveutil aws-tag-deprovision --loglevel debug "${cluster_name}=owned" --region "${region}"
}

collect_metadata () {
  local cluster_name
  cluster_name=$1
  ###eg cluster_name=kubernetes.io/cluster/ci-op-724qy8fn-55c01-s5c8w
  local ns
  ns=${cluster_name:22:14}
  echo "cluster is used by namespace ${ns}"
  if ! oc get ns "${ns}" >/dev/null 2>&1; then
    echo "namespace ${ns} does not exist any more"
    return
  fi
  local cluster_name_in_pod
  cluster_name_in_pod=${cluster_name:22:20}
  local cache_json
  cache_json=$(oc get pods -n "${ns}" -o json)
  ###eg, the value of env var CLUSTER_NAME: ci-op-724qy8fn-55c01
  for pod_name in $(echo "${cache_json}" | jq --arg cn "${cluster_name_in_pod}" -r '.items[] | . as $pod | $pod.spec.containers[] | select(.name == "setup") | .env[]? | select(.name == "CLUSTER_NAME" and (.value == $cn )) | $pod.metadata.name')
  do
    echo "collecting metadata from pod/${pod_name} in namespace ${ns}"
    oc describe pod -n "${ns}" "${pod_name}"
  done
}

for r in "${regions[@]}"
do
  echo "doing region ${r} ..."
  json_output=$(aws ec2 describe-vpcs --output json --region "${r}")
  for cluster in $(echo ${json_output} | jq --arg date "${cluster_age_cutoff}" -r -S '.Vpcs[] | select (.Tags[]? | (.Key == "expirationDate" and .Value < $date)) | .Tags[] | select (.Value == "owned") | .Key')
  do
    expirationDateValue=$(echo ${json_output} | jq --arg cl "${cluster}" -r -S '.Vpcs[] | select (.Tags[]? | (.Key == $cl and .Value == "owned")) | .Tags[] | select (.Key == "expirationDate") | .Value')
    handle_cluster "${cluster}" "${expirationDateValue}" "${r}"
    collect_metadata "${cluster}"
  done
done

echo "Deprovision finished successfully"
