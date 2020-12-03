#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

trap 'CHILDREN=$(jobs -p); if test -n "${CHILDREN}"; then kill ${CHILDREN} && wait; fi' TERM

function queue() {
  local LIVE="$(jobs | wc -l)"
  while [[ "${LIVE}" -ge 10 ]]; do
    sleep 1
    LIVE="$(jobs | wc -l)"
  done
  echo "${@}"
  "${@}" &
}

function deprovision() {
  WORKDIR="${1}"
  timeout --signal=SIGQUIT 30m openshift-install --dir "${WORKDIR}" --log-level error destroy cluster && touch "${WORKDIR}/success" || touch "${WORKDIR}/failure"
}

aws_cluster_age_cutoff="$(TZ=":Africa/Abidjan" date --date="${CLUSTER_TTL}" '+%Y-%m-%dT%H:%M+0000')"
echo "deprovisioning clusters with an expirationDate before ${aws_cluster_age_cutoff} in AWS ..."
# we need to pass --region for ... some reason?
for region in $( aws ec2 describe-regions --region us-east-1 --query "Regions[].{Name:RegionName}" --output text ); do
  echo "deprovisioning in AWS region ${region} ..."
  for cluster in $( aws ec2 describe-vpcs --output json --region "${region}" | jq --arg date "${aws_cluster_age_cutoff}" -r -S '.Vpcs[] | select (.Tags[]? | (.Key == "expirationDate" and .Value < $date)) | .Tags[] | select (.Value == "owned") | .Key' ); do
    workdir="${ARTIFACTS}/deprovision/${cluster:22:14}"
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
    echo "will deprovision AWS cluster ${cluster} in region ${region}"
  done
done

gce_cluster_age_cutoff="$(TZ=":America/Los_Angeles" date --date="${CLUSTER_TTL}-4 hours" '+%Y-%m-%dT%H:%M%z')"
echo "deprovisioning clusters with a creationTimestamp before ${gce_cluster_age_cutoff} in GCE ..."
export CLOUDSDK_CONFIG=/tmp/gcloudconfig
mkdir -p "${CLOUDSDK_CONFIG}"
gcloud auth activate-service-account --key-file="${GOOGLE_APPLICATION_CREDENTIALS}"
export FILTER="creationTimestamp.date('%Y-%m-%dT%H:%M%z')<${gce_cluster_age_cutoff} AND autoCreateSubnetworks=false AND name~'ci-'"
for network in $( gcloud --project=openshift-gce-devel-ci compute networks list --filter "${FILTER}" --format "value(name)" ); do
  infraID="${network%"-network"}"
  region="$( gcloud --project=openshift-gce-devel-ci compute networks describe "${network}" --format="value(subnetworks[0])" | grep -Po "(?<=regions/)[^/]+" || true )"
  if [[ -z "${region:-}" ]]; then
    region=us-east1
  fi
  workdir="${ARTIFACTS}/deprovision/${infraID}"
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
  echo "will deprovision GCE cluster ${infraID} in region ${region}"
done

for workdir in $( find "${ARTIFACTS}/deprovision" -mindepth 1 -type d | shuf ); do
  queue deprovision "${workdir}"
done

wait

gcs_bucket_age_cutoff="$(TZ="GMT" date --date="${CLUSTER_TTL}-4 hours" '+%a, %d %b %Y %H:%M:%S GMT')"
gcs_bucket_age_cutoff_seconds="$(date --date="${gcs_bucket_age_cutoff}" '+%s')"
echo "deleting GCS buckets with a creationTimestamp before ${gcs_bucket_age_cutoff} in GCE ..."
buckets=()
while read -r bucket; do
  read -r creationTime
  if [[ ${gcs_bucket_age_cutoff_seconds} -ge $( date --date="${creationTime}" '+%s' ) ]]; then
    buckets+=("${bucket}")
  fi
done <<< $( gsutil -m ls -p 'openshift-gce-devel-ci' -L -b 'gs://ci-op-*' | grep -Po "(gs:[^ ]+)|(?<=Time created:).*" )
if [[ "${#buckets[@]}" -gt 0 ]]; then
  timeout 30m gsutil -m rm -r "${buckets[@]}"
fi

FAILED="$(ls "${ARTIFACTS}"/deprovision/*/failure)"
if [[ -n "${FAILED}" ]]; then
  echo "Deprovision failed on the following clusters:"
  echo "${FAILED}"
  exit 1
else
  echo "Deprovision finished successfully"
fi
