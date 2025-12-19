#!/bin/bash
# verify-config-brancher.sh
# Verifies that config-brancher successfully created config files

set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <future-release> <release-repo-path>"
    echo "Example: $0 4.22 ../release"
    exit 1
fi

FUTURE_RELEASE="$1"
RELEASE_REPO="$2"

echo "=========================================="
echo "Verifying config-brancher output"
echo "=========================================="

VERIFICATION_FAILED=0

# Check if any config files were modified
cd "$RELEASE_REPO"
if git diff --quiet ci-operator/config/; then
    echo "✗ FAILED: No changes detected in ci-operator/config/"
    VERIFICATION_FAILED=1
else
    echo "✓ Changes detected in ci-operator/config/"

    # Count new config files for the future release
    NEW_CONFIGS=$(git diff --name-only ci-operator/config/ | grep -c "release-${FUTURE_RELEASE}" || true)
    if [ "$NEW_CONFIGS" -gt 0 ]; then
        echo "✓ Found $NEW_CONFIGS new config files for release-${FUTURE_RELEASE}"
    else
        echo "⚠ Warning: No new config files found for release-${FUTURE_RELEASE}"
    fi
fi

echo "=========================================="

if [ $VERIFICATION_FAILED -eq 1 ]; then
    echo "Verification FAILED"
    exit 1
fi

echo "✓ Verification passed!"
exit 0
