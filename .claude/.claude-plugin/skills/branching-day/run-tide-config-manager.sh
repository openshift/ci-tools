#!/bin/bash
set -euo pipefail

[ "$#" -ne 2 ] && echo "Usage: $0 <current-release> <release-repo>" && exit 1
[ ! -d "$2" ] && echo "Error: Release repo not found" && exit 1

RELEASE_REPO=$(cd "$2" && pwd)

tide-config-manager \
    --current-release="$1" \
    --lifecycle-phase=branching \
    --prow-config-dir="$RELEASE_REPO/core-services/prow/02_config/" \
    --sharded-prow-config-base-dir="$RELEASE_REPO/core-services/prow/02_config/"

echo "✓ tide-config-manager: $1"
