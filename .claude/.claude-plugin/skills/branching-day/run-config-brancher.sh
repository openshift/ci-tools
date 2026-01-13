#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current-release> <future-release> <release-repo>" && exit 1
[ ! -d "$2" ] && echo "Error: Release repo not found" && exit 1

config-brancher \
    --config-dir "$3/ci-operator/config" \
    --current-release="$1" \
    --future-release="$2" \
    --bump-release="$2" \
    --confirm

echo "✓ config-brancher: $1 → $2"
