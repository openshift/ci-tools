#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <x.y+1> <x.y+2> <release-repo>" && exit 1

XY_PLUS_1="$1"
XY_PLUS_2="$2"
INFRA="$3/ci-operator/jobs/infra-periodics.yaml"

[ ! -f "$INFRA" ] && echo "Error: infra-periodics.yaml not found" && exit 1

sed -i "/name: periodic-prow-auto-config-brancher/,/name:/ {
    s/--future-release=${XY_PLUS_1}/--future-release=${XY_PLUS_2}/
}" "$INFRA"

echo "✓ auto-config-brancher: $XY_PLUS_1 → $XY_PLUS_2"
