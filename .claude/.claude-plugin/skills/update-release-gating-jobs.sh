#!/bin/bash
# Skill: update-release-gating-jobs
# Description: Run generated-release-gating-jobs bumper with Sippy config
# Usage: update-release-gating-jobs.sh <old_version> <release_repo_path> <ci_tools_repo_path> <sippy_config_path>

set -euo pipefail

if [ $# -ne 4 ]; then
    echo "Usage: $0 <old_version> <release_repo_path> <ci_tools_repo_path> <sippy_config_path>"
    echo "Example: $0 4.21 /home/prucek/work/release /home/prucek/work/ci-tools /tmp/sippy-openshift.yaml"
    exit 1
fi

OLD_VERSION="$1"
RELEASE_REPO="$2"
CI_TOOLS_REPO="$3"
SIPPY_CONFIG="$4"

echo "=========================================="
echo "Updating Release Gating Jobs for ${OLD_VERSION}"
echo "=========================================="
echo ""

cd "${CI_TOOLS_REPO}"

# Build the tool if not already built
echo "Building generated-release-gating-jobs..."
go build ./cmd/branchingconfigmanagers/generated-release-gating-jobs

# Run the bumper using Sippy config
echo ""
echo "Running generated-release-gating-jobs bumper..."
./generated-release-gating-jobs \
  --current-release="${OLD_VERSION}" \
  --release-repo="${RELEASE_REPO}" \
  --sippy-config="${SIPPY_CONFIG}" \
  --interval=168

echo ""
echo "=========================================="
echo "✓ Release gating jobs updated!"
echo "=========================================="
