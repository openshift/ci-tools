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
  ./bin/hiveutil aws-tag-deprovision "${cluster_name}=owned" --region "${region}"
}



for r in "${regions[@]}"
do
  echo "doing region ${r} ..."
  json_output=$(aws ec2 describe-vpcs --output json --region "${r}")
  for cluster in $(echo ${json_output} | jq --arg date "${cluster_age_cutoff}" -r -S '.Vpcs[] | select (.Tags[]? | (.Key == "expirationDate" and .Value < $date)) | .Tags[] | select (.Value == "owned") | .Key')
  do
    expirationDateValue=$(echo ${json_output} | jq --arg cl "${cluster}" -r -S '.Vpcs[] | select (.Tags[]? | (.Key == $cl and .Value == "owned")) | .Tags[] | select (.Key == "expirationDate") | .Value')
    handle_cluster "${cluster}" "${expirationDateValue}" "${r}"
  done
done

echo "Deprovision finished successfully"
