#!/bin/bash
set -euo pipefail

[ "$#" -ne 2 ] && echo "Usage: $0 <x.y+2> <release-repo>" && exit 1

cd "$2"
[ "$(git branch --show-current)" != "image-mirror-$1" ] && echo "Error: Wrong branch" && exit 1

git add core-services/image-mirroring/ && \
  git diff --staged --quiet || \
  git commit -m "Update image mirroring config for $1

Add $1 version to image mirroring configuration."

echo "✓ Committed"
