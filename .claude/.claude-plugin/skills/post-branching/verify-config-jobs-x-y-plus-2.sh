#!/bin/bash
# verify-config-jobs-x-y-plus-2.sh
# Verifies that CI operator configs and jobs were created correctly

set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <x.y+1> <x.y+2> <release-repo-path>"
    echo "Example: $0 4.22 4.23 ../release"
    exit 1
fi

XY_PLUS_1="$1"
NEXT="$2"
RELEASE_REPO="$3"

cd "$RELEASE_REPO"

echo "=========================================="
echo "Verifying CI configs and jobs creation"
echo "=========================================="

VERIFICATION_FAILED=0

# Verify commits were created
COMMIT_COUNT=$(git log --oneline origin/master..HEAD | wc -l)
if [ "$COMMIT_COUNT" -ge 4 ]; then
    echo "✓ Created $COMMIT_COUNT commits (expected at least 4)"
else
    echo "✗ FAILED: Only $COMMIT_COUNT commits created (expected at least 4)"
    VERIFICATION_FAILED=1
fi

# Verify ci-operator config files for X.Y+2 were created
CONFIG_COUNT=$(find ci-operator/config -name "*release-${NEXT}*.yaml" | wc -l)
if [ "$CONFIG_COUNT" -gt 0 ]; then
    echo "✓ Created $CONFIG_COUNT ci-operator config files for release-${NEXT}"
else
    echo "✗ FAILED: No ci-operator config files found for release-${NEXT}"
    VERIFICATION_FAILED=1
fi

# Verify jobs were created for X.Y+2
JOB_COUNT=$(find ci-operator/jobs -name "*${NEXT}*.yaml" | wc -l)
if [ "$JOB_COUNT" -gt 0 ]; then
    echo "✓ Created $JOB_COUNT job files for ${NEXT}"
else
    echo "✗ FAILED: No job files found for ${NEXT}"
    VERIFICATION_FAILED=1
fi

# Verify openshift-priv configs were created
PRIV_CONFIG_COUNT=$(find ci-operator/config/openshift-priv -name "*release-${NEXT}*.yaml" 2>/dev/null | wc -l)
if [ "$PRIV_CONFIG_COUNT" -gt 0 ]; then
    echo "✓ Created $PRIV_CONFIG_COUNT openshift-priv config files"
else
    echo "⚠ Warning: No openshift-priv config files found"
fi

# Verify template deprecation allowlist was updated
if git diff origin/master..HEAD core-services/template-deprecation/_allowlist.yaml | grep -q "+"; then
    echo "✓ Template deprecation allowlist updated"
else
    echo "⚠ Warning: Template deprecation allowlist may not have been updated"
fi

echo "=========================================="

if [ $VERIFICATION_FAILED -eq 1 ]; then
    echo "Verification FAILED"
    exit 1
fi

echo "✓ Verification passed!"
exit 0
