#!/bin/bash
# Skill: regenerate-prow-jobs
# Description: Regenerate Prow job configurations from CI operator configs
# Usage: regenerate-prow-jobs.sh <release_repo_path>

set -euo pipefail

if [ $# -ne 1 ]; then
    echo "Usage: $0 <release_repo_path>"
    echo "Example: $0 /home/prucek/work/release"
    exit 1
fi

RELEASE_REPO="$1"

echo "=========================================="
echo "Regenerating Prow Jobs"
echo "=========================================="
echo ""

cd "${RELEASE_REPO}"

# Run make jobs
echo "Running 'make jobs'..."
make jobs

echo ""
echo "=========================================="
echo "✓ Prow jobs regenerated successfully!"
echo "=========================================="
