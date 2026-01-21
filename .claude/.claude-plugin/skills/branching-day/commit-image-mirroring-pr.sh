#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current> <future> <release-repo>" && exit 1

cd "$3"
[ "$(git branch --show-current)" != "image-mirroring-$2" ] && echo "Error: Wrong branch" && exit 1

git add core-services/image-mirroring/openshift/_config.yaml && git commit -m "bump image-mirroring config"
git add -A && git commit -m "make openshift-image-mirror-mappings"

echo "✓ Commits: $(git log --oneline origin/master..HEAD | wc -l)"
