#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current> <x.y+2> <release-repo>" && exit 1

XY_PLUS_2="$2"
XY_PLUS_1=$(echo "$1" | awk -F. '{printf "%d.%d", $1, $2+1}')
INFRA="$3/ci-operator/jobs/infra-periodics.yaml"

[ ! -f "$INFRA" ] && echo "Error: infra-periodics.yaml not found" && exit 1

sed -i "/name: periodic-openshift-release-merge-blockers$/,/name:/ {
    s/--future-release=${XY_PLUS_1}/--future-release=${XY_PLUS_2}/
}" "$INFRA"

echo "✓ merge-blockers: $XY_PLUS_1 → $XY_PLUS_2"
