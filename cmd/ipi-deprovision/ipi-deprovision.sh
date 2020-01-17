#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# CLUSTER_TTL controls the relative time to the running time of this script.
# eg, export CLUSTER_TTL="22 hours ago"
echo "Searching for clusters with a TTL of ${CLUSTER_TTL}"
cluster_age_cutoff="$(TZ=":Africa/Abidjan" date --date="${CLUSTER_TTL}" '+%Y-%m-%dT%H:%M+0000')"

echo "cluster_age_cutoff: ${cluster_age_cutoff}"

echo "deprovisioning in AWS ..."
# we need to pass --region for ... some reason?
for region in $( aws ec2 describe-regions --region us-east-1 --query "Regions[].{Name:RegionName}" --output text ); do
  echo "deprovisioning in AWS region ${region} ..."
  for cluster in $( aws ec2 describe-vpcs --output json --region "${region}" | jq --arg date "${cluster_age_cutoff}" -r -S '.Vpcs[] | select (.Tags[]? | (.Key == "expirationDate" and .Value < $date)) | .Tags[] | select (.Value == "owned") | .Key' | shuf ); do
    workdir="/tmp/deprovision/aws/${infraID}"
    mkdir -p "${workdir}"
    cat <<EOF >"${workdir}/metadata.json"
{
  "aws":{
    "region":"${region}",
    "identifier":[{
      "${cluster}": "owned"
    }]
  }
}
EOF
    echo "will deprovision AWS cluster ${cluster} in region ${region}:"
    cat "${workdir}/metadata.json"
  done
done

echo "deprovisioning in GCE ..."
export CLOUDSDK_CONFIG=/tmp/gcloudconfig
mkdir -p "${CLOUDSDK_CONFIG}"
gcloud auth activate-service-account --key-file="${GOOGLE_APPLICATION_CREDENTIALS}"
export FILTER="creationTimestamp.date('%Y-%m-%dT%H:%M+0000')<${cluster_age_cutoff} AND name~'ci-*'"
for network in $( gcloud --project=openshift-gce-devel-ci compute networks list --filter "${FILTER}" --format "value(name)" | shuf ); do
  infraID="${network%"-network"}"
  region="$( gcloud --project=openshift-gce-devel-ci compute networks describe "${network}" --format="value(subnetworks[0])" | grep -Po "(?<=regions/)[^/]+" )"
  if [[ -z "${region:-}" ]]; then
    echo "could not determine region for cluster ${infraID}, ignoring ..."
    continue
  fi
  workdir="/tmp/deprovision/gce/${infraID}"
  mkdir -p "${workdir}"
  cat <<EOF >"${workdir}/metadata.json"
{
  "infraID":"${infraID}",
  "gcp":{
    "region":"${region}",
    "projectID":"openshift-gce-devel-ci"
  }
}
EOF
  echo "will deprovision GCE cluster ${infraID} in region ${region}:"
  cat "${workdir}/metadata.json"
done

for workdir in $( find /tmp/deprovision -mindepth 1 -type d ); do
  echo openshift-install --dir "${workdir}" --log-level debug destroy cluster
done

echo "Deprovision finished successfully"
