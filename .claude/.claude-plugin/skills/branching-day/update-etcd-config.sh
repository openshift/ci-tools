#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current> <future> <release-repo>" && exit 1

CURRENT="$1"
FUTURE="$2"
ETCD="$3/core-services/prow/02_config/openshift/etcd/_prowconfig.yaml"

[ ! -f "$ETCD" ] && echo "Error: etcd config not found" && exit 1

sed -i "/- openshift-4.20$/a\    - openshift-${CURRENT}" "$ETCD"
sed -i "s/- openshift-${CURRENT}$/- openshift-${FUTURE}/" "$ETCD"
sed -i "/excludedBranches:/,/labels:/ {
    /- openshift-${CURRENT}$/a\    - openshift-${FUTURE}
}" "$ETCD"

echo "✓ etcd config: $CURRENT → $FUTURE"
