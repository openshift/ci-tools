#!/bin/bash
set -euo pipefail

[ "$#" -ne 2 ] && echo "Usage: $0 <x.y+2> <release-repo>" && exit 1

cd "$2"
[ "$(git branch --show-current)" != "jira-validation-$1" ] && echo "Error: Wrong branch" && exit 1

git add core-services/jira-lifecycle-plugin/config.yaml && \
  git diff --staged --quiet || \
  git commit -m "Configure Jira validation for $1 branches

Add validation criteria for openshift-$1 and release-$1 branches."

echo "✓ Committed"
