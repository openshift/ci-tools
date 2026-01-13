#!/bin/bash
# verify-jira-validation.sh
# Verifies that Jira validation stanzas were added correctly

set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <current-release> <x.y+2> <release-repo-path>"
    echo "Example: $0 4.21 4.23 ../release"
    exit 1
fi

CURRENT_RELEASE="$1"
XY_PLUS_2="$2"
RELEASE_REPO="$3"

# Calculate X.Y+3
XY_PLUS_3=$(echo "$XY_PLUS_2" | awk -F. '{printf "%d.%d", $1, $2+1}')

JIRA_CONFIG="$RELEASE_REPO/core-services/jira-lifecycle-plugin/config.yaml"

echo "=========================================="
echo "Verifying Jira validation config"
echo "=========================================="

VERIFICATION_FAILED=0

# Verify openshift-X.Y+2 stanza exists
if grep -q "openshift-${XY_PLUS_2}:" "$JIRA_CONFIG"; then
    echo "✓ openshift-${XY_PLUS_2} stanza added"

    # Verify it has correct target_version
    if grep -A 10 "openshift-${XY_PLUS_2}:" "$JIRA_CONFIG" | grep -q "target_version: ${XY_PLUS_2}.0"; then
        echo "✓ openshift-${XY_PLUS_2} has correct target_version"
    else
        echo "✗ FAILED: openshift-${XY_PLUS_2} has incorrect target_version"
        VERIFICATION_FAILED=1
    fi

    # Verify it has correct dependent_bug_target_versions
    if grep -A 10 "openshift-${XY_PLUS_2}:" "$JIRA_CONFIG" | grep -q "${XY_PLUS_3}.0"; then
        echo "✓ openshift-${XY_PLUS_2} has correct dependent_bug_target_versions (${XY_PLUS_3}.0)"
    else
        echo "✗ FAILED: openshift-${XY_PLUS_2} missing dependent_bug_target_versions"
        VERIFICATION_FAILED=1
    fi
else
    echo "✗ FAILED: openshift-${XY_PLUS_2} stanza not found"
    VERIFICATION_FAILED=1
fi

# Verify release-X.Y+2 stanza exists
if grep -q "release-${XY_PLUS_2}:" "$JIRA_CONFIG"; then
    echo "✓ release-${XY_PLUS_2} stanza added"

    # Verify it has correct target_version
    if grep -A 10 "release-${XY_PLUS_2}:" "$JIRA_CONFIG" | grep -q "target_version: ${XY_PLUS_2}.0"; then
        echo "✓ release-${XY_PLUS_2} has correct target_version"
    else
        echo "✗ FAILED: release-${XY_PLUS_2} has incorrect target_version"
        VERIFICATION_FAILED=1
    fi
else
    echo "✗ FAILED: release-${XY_PLUS_2} stanza not found"
    VERIFICATION_FAILED=1
fi

echo "=========================================="

if [ $VERIFICATION_FAILED -eq 1 ]; then
    echo "Verification FAILED"
    exit 1
fi

echo "✓ Verification passed!"
exit 0
