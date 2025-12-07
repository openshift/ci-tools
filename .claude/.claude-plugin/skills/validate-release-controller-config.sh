#!/bin/bash
# Skill: validate-release-controller-config
# Description: Validate release-controller configurations
# Usage: validate-release-controller-config.sh <release_repo_path>

set -euo pipefail

if [ $# -ne 1 ]; then
    echo "Usage: $0 <release_repo_path>"
    echo "Example: $0 /home/prucek/work/release"
    exit 1
fi

RELEASE_REPO="$1"

echo "=========================================="
echo "Validating Release Controller Configuration"
echo "=========================================="
echo ""

cd "${RELEASE_REPO}"

# Run validation script
echo "Running hack/validate-release-controller-config.sh..."
hack/validate-release-controller-config.sh .

EXIT_CODE=$?

echo ""
if [ $EXIT_CODE -eq 0 ]; then
    echo "=========================================="
    echo "✓ Validation passed!"
    echo "=========================================="
else
    echo "=========================================="
    echo "⚠ Validation failed - manual job copying may be required"
    echo "=========================================="
fi

exit $EXIT_CODE
