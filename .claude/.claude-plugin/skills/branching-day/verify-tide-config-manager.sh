#!/bin/bash
# verify-tide-config-manager.sh
# Verifies that tide-config-manager modified prow config files

set -euo pipefail

if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <release-repo-path>"
    echo "Example: $0 ../release"
    exit 1
fi

RELEASE_REPO="$1"

RELEASE_REPO_ABSOLUTE=$(cd "$RELEASE_REPO" && pwd)

echo "=========================================="
echo "Verifying tide-config-manager output"
echo "=========================================="

VERIFICATION_FAILED=0

cd "$RELEASE_REPO_ABSOLUTE"

# Check if prow config files were modified
if git diff --quiet core-services/prow/02_config/; then
    echo "✗ FAILED: No changes detected in core-services/prow/02_config/"
    VERIFICATION_FAILED=1
else
    echo "✓ Changes detected in prow config files"

    MODIFIED_FILES=$(git diff --name-only core-services/prow/02_config/ | wc -l)
    echo "✓ Modified $MODIFIED_FILES prow config file(s)"
fi

echo "=========================================="

if [ $VERIFICATION_FAILED -eq 1 ]; then
    echo "Verification FAILED"
    exit 1
fi

echo "✓ Verification passed!"
exit 0
