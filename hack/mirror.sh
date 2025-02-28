#!/usr/bin/env bash

set -eu

if [[ -z "${1:-}" ]] || [[ -z "${2:-}" ]]; then
	echo 'Usage: mirror.sh <src_registry> <dst_registry>'
	exit 1
fi

CLUSTER_SRC=`echo ${1} | sed -nE 's,registry\.([a-zA-Z0-9]+)\..*,\1,p'`
CLUSTER_DST=`echo ${2} | sed -nE 's,registry\.([a-zA-Z0-9]+)\..*,\1,p'`
OCP_USER=`oc config get-users | cut -d/ -f1 | sort | uniq -d | head -n1`

trap "oc adm policy remove-cluster-role-from-user cluster-admin ${OCP_USER} --as system:admin --context ${CLUSTER_DST}" EXIT

[[ "${CLUSTER_SRC}" == "ci"  ]] && CLUSTER_SRC=app.ci
[[ "${CLUSTER_DST}" == "ci"  ]] && CLUSTER_DST=app.ci

AUTH_FILE="/tmp/auth-${RANDOM}.json"
oc registry login --context ${CLUSTER_SRC} --to ${AUTH_FILE}
oc registry login --context ${CLUSTER_DST} --to ${AUTH_FILE}
AUTH_CONTENT=`cat ${AUTH_FILE}`

SCRIPT=`cat <<EOF
trap "rm -rf ${AUTH_FILE}" EXIT
echo '${AUTH_CONTENT}' > ${AUTH_FILE}
oc image mirror ${1} ${2} --continue-on-error --registry-config=${AUTH_FILE}
EOF
`

NODE=`oc get nodes --context ${CLUSTER_DST} --no-headers | grep -E '\sReady\s*worker' | head -n1 | cut -d' ' -f1`
oc adm policy add-cluster-role-to-user cluster-admin ${OCP_USER} --as system:admin --context ${CLUSTER_DST}
oc --context ${CLUSTER_DST} debug node/${NODE} -- chroot /host bash -c "${SCRIPT}"
