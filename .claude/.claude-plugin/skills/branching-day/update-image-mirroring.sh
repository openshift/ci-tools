#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current> <future> <release-repo>" && exit 1

CONFIG="$3/core-services/image-mirroring/openshift/_config.yaml"
[ ! -f "$CONFIG" ] && echo "Error: Image mirroring config not found" && exit 1

echo "⚠ MANUAL EDIT REQUIRED"
echo ""
echo "Edit $CONFIG:"
echo "  Remove 'latest' from: $1 (all variants)"
echo "  Add 'latest' to: $2 (all variants)"
