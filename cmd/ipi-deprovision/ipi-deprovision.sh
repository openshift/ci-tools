#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

aws_cluster_age_cutoff="$(TZ=":Africa/Abidjan" date --date="${CLUSTER_TTL}" '+%Y-%m-%dT%H:%M+0000')"
echo "deprovisioning clusters with an expirationDate before ${aws_cluster_age_cutoff} in AWS ..."
# we need to pass --region for ... some reason?
for region in $( aws ec2 describe-regions --region us-east-1 --query "Regions[].{Name:RegionName}" --output text ); do
  echo "deprovisioning in AWS region ${region} ..."
  for cluster in $( aws ec2 describe-vpcs --output json --region "${region}" | jq --arg date "${aws_cluster_age_cutoff}" -r -S '.Vpcs[] | select (.Tags[]? | (.Key == "expirationDate" and .Value < $date)) | .Tags[] | select (.Value == "owned") | .Key' ); do
    workdir="/tmp/deprovision/${cluster:22:14}"
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
export FILTER="creationTimestamp.date('%Y-%m-%dT%H:%M%z')<${gce_cluster_age_cutoff} AND name~'ci-*'"
for network in $( gcloud --project=openshift-gce-devel-ci compute networks list --filter "${FILTER}" --format "value(name)" ); do
  infraID="${network%"-network"}"
  region="$( gcloud --project=openshift-gce-devel-ci compute networks describe "${network}" --format="value(subnetworks[0])" | grep -Po "(?<=regions/)[^/]+" || true )"
  if [[ -z "${region:-}" ]]; then
    echo "could not determine region for cluster ${infraID}, ignoring ..."
    continue
  fi
  workdir="/tmp/deprovision/${infraID}"
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

libvirt_p_cluster_age_cutoff="4 hours ago"
echo "deprovisioning clusters with creation time before ${libvirt_p_cluster_age_cutoff} in libvirt-p"
for network in $( ssh "${bastion_p}" virsh net-list --all --name | grep -v default ) ; do
	if $( ssh "${bastion_p}" journalctl -u libvirtd.service --until "${libvirt_p_cluster_age_cutoff}" 2>/dev/null | grep "${network}" ) ; then
    infraID="${network}"
    workdir="/tmp/deprovision/${infraID}"
    mkdir -p "${workdir}"
    cat <<EOF >"${workdir}/metadata.json"
{
  "infraID":"${infraID}",
  "libvirt":{
    "uri":"qemu+tcp://${bastion_p}/system"
  }
}
EOF
  echo "will deprovision libvirt cluster ${infraID} on p host ${bastion_p}"
  fi
done


for workdir in $( find /tmp/deprovision -mindepth 1 -type d | shuf ); do
  timeout 30m openshift-install --dir "${workdir}" --log-level debug destroy cluster
done

echo "Deprovision finished successfully"
