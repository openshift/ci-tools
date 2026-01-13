#!/bin/bash
# verify-tide-and-infra-jobs.sh
# Verifies that tide and infra jobs were updated correctly

set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <current-release> <future-release> <release-repo-path>"
    echo "Example: $0 4.21 4.22 ../release"
    exit 1
fi

CURRENT_RELEASE="$1"
FUTURE_RELEASE="$2"
RELEASE_REPO="$3"

# Extract minor versions
FUTURE_MINOR="${FUTURE_RELEASE#*.}"

INFRA_PERIODICS="$RELEASE_REPO/ci-operator/jobs/infra-periodics.yaml"

echo "=========================================="
echo "Verifying tide and infra jobs updates"
echo "=========================================="

VERIFICATION_FAILED=0

# Verify periodic-openshift-release-merge-blockers was updated
if grep -A 10 "name: periodic-openshift-release-merge-blockers" "$INFRA_PERIODICS" | \
   grep -q -- "--current-release=${FUTURE_RELEASE}"; then
    echo "✓ periodic-openshift-release-merge-blockers updated correctly"
else
    echo "✗ FAILED: periodic-openshift-release-merge-blockers not updated correctly"
    VERIFICATION_FAILED=1
fi

# Verify periodic-ocp-build-data-enforcer was updated
if grep -q "base_ref: openshift-${FUTURE_RELEASE}" "$INFRA_PERIODICS" && \
   grep -q -- "--minor=${FUTURE_MINOR}" "$INFRA_PERIODICS"; then
    echo "✓ periodic-ocp-build-data-enforcer updated correctly"
else
    echo "✗ FAILED: periodic-ocp-build-data-enforcer not updated correctly"
    VERIFICATION_FAILED=1
fi

echo "=========================================="

if [ $VERIFICATION_FAILED -eq 1 ]; then
    echo "Verification FAILED"
    exit 1
fi

echo "✓ Verification passed!"
exit 0
