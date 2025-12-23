#!/bin/bash
# verify-infra-periodics.sh
# Verifies that infra-periodics and jira config were updated correctly

set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <current-release> <future-release> <release-repo-path>"
    echo "Example: $0 4.21 4.22 ../release"
    exit 1
fi

CURRENT_RELEASE="$1"
FUTURE_RELEASE="$2"
RELEASE_REPO="$3"

INFRA_PERIODICS="$RELEASE_REPO/ci-operator/jobs/infra-periodics.yaml"
JIRA_CONFIG="$RELEASE_REPO/core-services/jira-lifecycle-plugin/config.yaml"

echo "=========================================="
echo "Verifying infra-periodics and jira config"
echo "=========================================="

VERIFICATION_FAILED=0

# Verify periodic-prow-auto-config-brancher was updated
if grep -q "base_ref: openshift-${FUTURE_RELEASE}" "$INFRA_PERIODICS" && \
   grep -q -- "--current-release=${FUTURE_RELEASE}" "$INFRA_PERIODICS"; then
    echo "✓ periodic-prow-auto-config-brancher updated correctly"
else
    echo "✗ FAILED: periodic-prow-auto-config-brancher not updated correctly"
    VERIFICATION_FAILED=1
fi

# Verify periodic-openshift-release-fast-forward was updated
if grep -A 10 "name: periodic-openshift-release-fast-forward" "$INFRA_PERIODICS" | \
   grep -q -- "--current-release=${FUTURE_RELEASE}"; then
    echo "✓ periodic-openshift-release-fast-forward updated correctly"
else
    echo "✗ FAILED: periodic-openshift-release-fast-forward not updated correctly"
    VERIFICATION_FAILED=1
fi

# Verify Jira config was updated for main and master
if grep -A 5 "^  main:" "$JIRA_CONFIG" | grep -q "target_version: ${FUTURE_RELEASE}.0" && \
   grep -A 5 "^  master:" "$JIRA_CONFIG" | grep -q "target_version: ${FUTURE_RELEASE}.0"; then
    echo "✓ Jira config updated for main and master branches"
else
    echo "✗ FAILED: Jira config not updated correctly"
    VERIFICATION_FAILED=1
fi

echo "=========================================="

if [ $VERIFICATION_FAILED -eq 1 ]; then
    echo "Verification FAILED"
    exit 1
fi

echo "✓ Verification passed!"
exit 0
