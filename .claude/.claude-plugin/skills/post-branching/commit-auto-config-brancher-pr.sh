#!/bin/bash
set -euo pipefail

[ "$#" -ne 2 ] && echo "Usage: $0 <x.y+2> <release-repo>" && exit 1

cd "$2"
[ "$(git branch --show-current)" != "auto-config-brancher-$1" ] && echo "Error: Wrong branch" && exit 1

git add ci-operator/jobs/infra-periodics.yaml && \
  git diff --staged --quiet || \
  git commit -m "Update auto-config-brancher for $1

Configure periodic-prow-auto-config-brancher to maintain $1 configs."

echo "✓ Committed"
