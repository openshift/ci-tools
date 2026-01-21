#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current> <future> <release-repo>" && exit 1

CURRENT="$1"
FUTURE="$2"
CURRENT_MINOR="${CURRENT#*.}"
FUTURE_MINOR="${FUTURE#*.}"
INFRA="$3/ci-operator/jobs/infra-periodics.yaml"

[ ! -f "$INFRA" ] && echo "Error: infra-periodics.yaml not found" && exit 1

sed -i "/name: periodic-openshift-release-merge-blockers/,/secretName:/ {
    s/--current-release=${CURRENT}/--current-release=${FUTURE}/
    s/--future-release=${CURRENT}/--future-release=${FUTURE}/
}" "$INFRA"

sed -i "/name: periodic-ocp-build-data-enforcer/,/secretName:/ {
    s/base_ref: openshift-${CURRENT}/base_ref: openshift-${FUTURE}/
    s/--minor=${CURRENT_MINOR}/--minor=${FUTURE_MINOR}/
}" "$INFRA"

echo "✓ tide + infra jobs: $CURRENT → $FUTURE"
