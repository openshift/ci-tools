#!/bin/bash
# verify-image-mirroring-x-y-plus-2.sh
# Verifies that image mirroring config was updated correctly

set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <x.y+1> <x.y+2> <release-repo-path>"
    echo "Example: $0 4.22 4.23 ../release"
    exit 1
fi

XY_PLUS_1="$1"
XY_PLUS_2="$2"
RELEASE_REPO="$3"

IMAGE_MIRROR_CONFIG="$RELEASE_REPO/core-services/image-mirroring/openshift/_config.yaml"

echo "=========================================="
echo "Verifying image mirroring config"
echo "=========================================="

VERIFICATION_FAILED=0

# Verify base version section was added
if grep -q "\"${XY_PLUS_2}\":" "$IMAGE_MIRROR_CONFIG"; then
    echo "✓ Base version \"${XY_PLUS_2}\" added"
else
    echo "✗ FAILED: Base version \"${XY_PLUS_2}\" not found"
    VERIFICATION_FAILED=1
fi

# Verify variant sections were added
VARIANTS=("sriov" "metallb" "ptp" "scos")
for variant in "${VARIANTS[@]}"; do
    if grep -q "\"${variant}-${XY_PLUS_2}\":" "$IMAGE_MIRROR_CONFIG"; then
        echo "✓ Variant \"${variant}-${XY_PLUS_2}\" added"
    else
        echo "✗ FAILED: Variant \"${variant}-${XY_PLUS_2}\" not found"
        VERIFICATION_FAILED=1
    fi
done

# Verify each section has the correct version tags
for prefix in "" "sriov-" "metallb-" "ptp-" "scos-"; do
    key="${prefix}${XY_PLUS_2}"
    if grep -q "\"${key}\":" "$IMAGE_MIRROR_CONFIG"; then
        # Check if it has both version and version.0 tags
        if grep -A 3 "\"${key}\":" "$IMAGE_MIRROR_CONFIG" | grep -q "\"${XY_PLUS_2}\"" && \
           grep -A 3 "\"${key}\":" "$IMAGE_MIRROR_CONFIG" | grep -q "\"${XY_PLUS_2}.0\""; then
            echo "✓ \"${key}\" has correct version tags"
        else
            echo "⚠ Warning: \"${key}\" may have incorrect version tags"
        fi
    fi
done

echo "=========================================="

if [ $VERIFICATION_FAILED -eq 1 ]; then
    echo "Verification FAILED"
    exit 1
fi

echo "✓ Verification passed!"
exit 0
