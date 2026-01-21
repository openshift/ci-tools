#!/bin/bash
set -euo pipefail

CI_TOOLS="${1:-.}"
[ ! -f "$CI_TOOLS/Makefile" ] && echo "Error: Not a valid ci-tools repo" && exit 1

cd "$CI_TOOLS"

TOOLS=(config-brancher tide-config-manager rpm-repo-mirroring-service ci-operator-config-mirror)
FAILED=0

for tool in "${TOOLS[@]}"; do
    if make install WHAT="cmd/$tool" >/dev/null 2>&1; then
        echo "✓ $tool"
    else
        echo "✗ $tool"
        FAILED=1
    fi
done

[ $FAILED -eq 1 ] && echo "Some tools failed to build" && exit 1
echo "✓ All tools built"
