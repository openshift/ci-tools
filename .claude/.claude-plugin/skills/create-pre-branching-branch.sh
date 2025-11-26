#!/bin/bash
# Skill: create-pre-branching-branch
# Description: Create and checkout pre-branching branch in release repository
# Usage: create-pre-branching-branch.sh <old_version> <new_version> <release_repo_path>

set -euo pipefail

if [ $# -ne 3 ]; then
    echo "Usage: $0 <old_version> <new_version> <release_repo_path>"
    echo "Example: $0 4.21 4.22 /home/prucek/work/release"
    exit 1
fi

OLD_VERSION="$1"
NEW_VERSION="$2"
RELEASE_REPO="$3"

echo "=========================================="
echo "Creating Pre-Branching Branch"
echo "=========================================="
echo ""

cd "${RELEASE_REPO}"

# Fetch latest changes
echo "Fetching latest changes from origin..."
git fetch origin
git pull origin master

# Create branch name
BRANCH_NAME="pre-branching-${OLD_VERSION}-to-${NEW_VERSION}"

# Check if branch already exists
if git show-ref --verify --quiet "refs/heads/${BRANCH_NAME}"; then
    echo ""
    echo "Branch '${BRANCH_NAME}' already exists locally."
    echo "Switching to existing branch..."
    git checkout "${BRANCH_NAME}"
else
    echo ""
    echo "Creating new branch: ${BRANCH_NAME}"
    git checkout -b "${BRANCH_NAME}" origin/master
fi

echo ""
echo "=========================================="
echo "✓ On branch: ${BRANCH_NAME}"
echo "=========================================="
