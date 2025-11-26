#!/bin/bash
# Skill: copy-handcrafted-release-gating-jobs
# Description: Populate handcrafted X.Y+1 release-gating jobs from X.Y release
# Usage: copy-handcrafted-release-gating-jobs.sh <old_version> <new_version> <release_repo_path>

set -euo pipefail

if [ $# -ne 3 ]; then
    echo "Usage: $0 <old_version> <new_version> <release_repo_path>"
    echo "Example: $0 4.21 4.22 /home/prucek/work/release"
    exit 1
fi

OLD_VERSION="$1"
NEW_VERSION="$2"
RELEASE_REPO="$3"
JOBS_DIR="${RELEASE_REPO}/ci-operator/jobs/openshift/release"
REPOS_DIR="${RELEASE_REPO}/core-services/release-controller/_repos"

echo "=========================================="
echo "Copying handcrafted release-gating jobs"
echo "Version: ${OLD_VERSION} → ${NEW_VERSION}"
echo "=========================================="
echo ""

# Check prerequisites
echo "Checking prerequisites..."

# Check if release-controller repo files exist for new version
REPO_FILES=$(find "${REPOS_DIR}" -type f -name "ocp-${NEW_VERSION}*.repo" 2>/dev/null | wc -l)

if [ "$REPO_FILES" -eq 0 ]; then
    echo ""
    echo "ERROR: No ocp-${NEW_VERSION}*.repo files found in ${REPOS_DIR}"
    echo ""
    echo "You must first execute Step 3 (Release-Controller Configurations):"
    echo "  1. Run the 'copy-release-controller-configs' skill"
    echo "  2. Ensure core-services/release-controller/_repos/ocp-${NEW_VERSION}*.repo files exist"
    echo ""
    exit 1
fi

echo "✓ Found ${REPO_FILES} ocp-${NEW_VERSION}*.repo file(s)"
echo ""

# Source and destination file paths
SRC_FILE="${JOBS_DIR}/openshift-release-release-${OLD_VERSION}-periodics.yaml"
DST_FILE="${JOBS_DIR}/openshift-release-release-${NEW_VERSION}-periodics.yaml"

# Check if source file exists
if [ ! -f "$SRC_FILE" ]; then
    echo "ERROR: Source file not found: $SRC_FILE"
    exit 1
fi

# Check if destination already exists
if [ -f "$DST_FILE" ]; then
    echo "WARNING: Destination file already exists: $DST_FILE"
    echo "Skipping copy operation."
else
    echo "Copying: $(basename "$SRC_FILE") → $(basename "$DST_FILE")"
    cp "$SRC_FILE" "$DST_FILE"
    echo "✓ File copied successfully"
    echo ""
fi

# Bump ALL version strings inside the file
echo "Bumping all version strings in $(basename "$DST_FILE")..."

# Extract the major version (e.g., "4" from "4.21")
MAJOR_VERSION="${OLD_VERSION%%.*}"

# Find all unique version numbers in the file and sort them in descending order
# This ensures we replace from highest to lowest to avoid double-replacement
VERSIONS=$(grep -oE "${MAJOR_VERSION}\.[0-9]+" "$DST_FILE" | sort -t. -k2 -nr | uniq)

# Bump each version found in the file
for VERSION in $VERSIONS; do
    # Extract minor version
    MINOR="${VERSION##*.}"
    # Calculate new minor version
    NEW_MINOR=$((MINOR + 1))
    NEW_VER="${MAJOR_VERSION}.${NEW_MINOR}"

    # Escape dots for sed
    VERSION_ESCAPED="${VERSION//./\\.}"

    echo "  Bumping ${VERSION} → ${NEW_VER}"
    sed -i "s/${VERSION_ESCAPED}/${NEW_VER}/g" "$DST_FILE"
done

echo "✓ All version strings updated"
echo ""

# Run make release-controllers
echo "Running 'make release-controllers'..."
cd "${RELEASE_REPO}"
make release-controllers
echo "✓ Release controllers generated"
echo ""

# Run make jobs
echo "Running 'make jobs'..."
make jobs
echo "✓ Prow jobs regenerated"
echo ""

echo "=========================================="
echo "✓ Handcrafted release-gating jobs ready!"
echo "=========================================="
