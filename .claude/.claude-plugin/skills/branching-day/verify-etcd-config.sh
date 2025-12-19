#!/bin/bash
# verify-etcd-config.sh
# Verifies that etcd tide config was updated correctly

set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <current-release> <future-release> <release-repo-path>"
    echo "Example: $0 4.21 4.22 ../release"
    exit 1
fi

CURRENT_RELEASE="$1"
FUTURE_RELEASE="$2"
RELEASE_REPO="$3"

ETCD_CONFIG="$RELEASE_REPO/core-services/prow/02_config/openshift/etcd/_prowconfig.yaml"

echo "=========================================="
echo "Verifying etcd config updates"
echo "=========================================="

VERIFICATION_FAILED=0

# Verify current release was added to older releases query
if grep -B 5 "backport-risk-assessed" "$ETCD_CONFIG" | grep -q "openshift-${CURRENT_RELEASE}"; then
    echo "✓ openshift-${CURRENT_RELEASE} added to older releases query"
else
    echo "✗ FAILED: openshift-${CURRENT_RELEASE} not found in older releases query"
    VERIFICATION_FAILED=1
fi

# Verify future release is in the development branch query
if grep -A 5 "without backport-risk-assessed" "$ETCD_CONFIG" | grep -q "openshift-${FUTURE_RELEASE}"; then
    echo "✓ openshift-${FUTURE_RELEASE} set as development branch"
else
    echo "✗ FAILED: openshift-${FUTURE_RELEASE} not found in development query"
    VERIFICATION_FAILED=1
fi

# Verify future release was added to excluded branches
if grep -A 10 "excludedBranches:" "$ETCD_CONFIG" | grep -q "openshift-${FUTURE_RELEASE}"; then
    echo "✓ openshift-${FUTURE_RELEASE} added to excluded branches"
else
    echo "✗ FAILED: openshift-${FUTURE_RELEASE} not found in excluded branches"
    VERIFICATION_FAILED=1
fi

echo "=========================================="

if [ $VERIFICATION_FAILED -eq 1 ]; then
    echo "Verification FAILED"
    exit 1
fi

echo "✓ Verification passed!"
exit 0
