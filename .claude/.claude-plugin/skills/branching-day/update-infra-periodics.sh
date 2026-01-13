#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current> <future> <release-repo>" && exit 1

CURRENT="$1"
FUTURE="$2"
INFRA="$3/ci-operator/jobs/infra-periodics.yaml"
JIRA="$3/core-services/jira-lifecycle-plugin/config.yaml"

[ ! -f "$INFRA" ] && echo "Error: infra-periodics.yaml not found" && exit 1
[ ! -f "$JIRA" ] && echo "Error: Jira config not found" && exit 1

sed -i "/name: periodic-prow-auto-config-brancher/,/name:/ {
    s/base_ref: openshift-${CURRENT}/base_ref: openshift-${FUTURE}/
    s/--current-release=${CURRENT}/--current-release=${FUTURE}/
    s/--future-release=${CURRENT}/--future-release=${FUTURE}/
}" "$INFRA"

sed -i "/name: periodic-openshift-release-fast-forward$/,/secretName:/ {
    s/--current-release=${CURRENT}/--current-release=${FUTURE}/
    s/--future-release=${CURRENT}/--future-release=${FUTURE}/
}" "$INFRA"

sed -i "/^  main:/,/^  [a-z]/ {
    s/target_version: ${CURRENT}\.0/target_version: ${FUTURE}.0/
}" "$JIRA"
sed -i "/^  master:/,/^  [a-z]/ {
    s/target_version: ${CURRENT}\.0/target_version: ${FUTURE}.0/
}" "$JIRA"

echo "✓ infra-periodics + jira: $CURRENT → $FUTURE"
