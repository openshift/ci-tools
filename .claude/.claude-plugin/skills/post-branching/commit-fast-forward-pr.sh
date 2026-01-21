#!/bin/bash
set -euo pipefail

[ "$#" -ne 2 ] && echo "Usage: $0 <x.y+2> <release-repo>" && exit 1

cd "$2"
[ "$(git branch --show-current)" != "fast-forward-$1" ] && echo "Error: Wrong branch" && exit 1

git add ci-operator/jobs/infra-periodics.yaml && \
  git diff --staged --quiet || \
  git commit -m "Update fast-forward job for $1

Configure periodic-openshift-release-fast-forward to maintain $1 branches."

echo "✓ Committed"
