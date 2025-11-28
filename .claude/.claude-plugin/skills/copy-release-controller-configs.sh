#!/bin/bash
# Skill: copy-release-controller-configs
# Description: Copy and update release-controller configurations from old version to new version
# Usage: copy-release-controller-configs.sh <old_version> <new_version> <release_repo_path>

set -euo pipefail

if [ $# -ne 3 ]; then
    echo "Usage: $0 <old_version> <new_version> <release_repo_path>"
    echo "Example: $0 4.21 4.22 /home/prucek/work/release"
    exit 1
fi

OLD_VERSION="$1"
NEW_VERSION="$2"
RELEASE_REPO="$3"
RELEASES_DIR="${RELEASE_REPO}/core-services/release-controller/_releases"

echo "Bumping release controller configs: ${OLD_VERSION} → ${NEW_VERSION}"

# Escape dots for sed patterns
OLD_ESCAPED="${OLD_VERSION//./\\.}"

# Calculate previous version for nested references
PREV_MINOR=$((${OLD_VERSION##*.} - 1))
PREV_VERSION="${OLD_VERSION%.*}.${PREV_MINOR}"
PREV_ESCAPED="${PREV_VERSION//./\\.}"
CURR_MINOR=$((${NEW_VERSION##*.} - 1))
CURR_VERSION="${NEW_VERSION%.*}.${CURR_MINOR}"

# Find all files with old_version in their name and bump them
find "$RELEASES_DIR" -type f -name "*${OLD_VERSION}*.json" 2>/dev/null | while read -r src_file; do
    # Skip if already processed/destination exists
    dst_file="${src_file//${OLD_VERSION}/${NEW_VERSION}}"
    [ -f "$dst_file" ] && continue

    echo "  $(basename "$src_file") → $(basename "$dst_file")"
    cp "$src_file" "$dst_file"

    # Bump version strings inside the file
    sed -i "s/${OLD_ESCAPED}/${NEW_VERSION}/g" "$dst_file"
    sed -i "s/${PREV_ESCAPED}/${CURR_VERSION}/g" "$dst_file"
done

echo "Done!"
