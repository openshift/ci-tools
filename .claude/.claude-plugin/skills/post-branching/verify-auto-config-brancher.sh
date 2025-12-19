#!/bin/bash
# verify-auto-config-brancher.sh
# Verifies that auto-config-brancher job was updated correctly

set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <x.y+1> <x.y+2> <release-repo-path>"
    echo "Example: $0 4.22 4.23 ../release"
    exit 1
fi

XY_PLUS_1="$1"
XY_PLUS_2="$2"
RELEASE_REPO="$3"

INFRA_PERIODICS="$RELEASE_REPO/ci-operator/jobs/infra-periodics.yaml"

echo "=========================================="
echo "Verifying auto-config-brancher job update"
echo "=========================================="

VERIFICATION_FAILED=0

# Verify the update was successful
if grep -A 10 "name: periodic-prow-auto-config-brancher" "$INFRA_PERIODICS" | \
   grep -q -- "--future-release=${XY_PLUS_2}"; then
    echo "✓ --future-release updated to ${XY_PLUS_2}"
else
    echo "✗ FAILED: --future-release not updated correctly"
    VERIFICATION_FAILED=1
fi

# Verify old value is gone
if grep -A 10 "name: periodic-prow-auto-config-brancher" "$INFRA_PERIODICS" | \
   grep -q -- "--future-release=${XY_PLUS_1}"; then
    echo "✗ FAILED: Old value ${XY_PLUS_1} still present"
    VERIFICATION_FAILED=1
else
    echo "✓ Old value ${XY_PLUS_1} removed"
fi

echo "=========================================="

if [ $VERIFICATION_FAILED -eq 1 ]; then
    echo "Verification FAILED"
    exit 1
fi

echo "✓ Verification passed!"
exit 0
