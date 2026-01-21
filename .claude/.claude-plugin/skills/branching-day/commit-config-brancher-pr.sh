#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current> <future> <release-repo>" && exit 1

cd "$3"
[ "$(git branch --show-current)" != "config-brancher-$2" ] && echo "Error: Wrong branch" && exit 1

git diff --staged --quiet || git reset HEAD

git add ci-operator/config/ && \
  git diff --staged --quiet || \
  git commit -m "config-brancher --config-dir ./ci-operator/config --current-release=$1 --future-release=$2 --bump-release=$2 --confirm"

git add -A && \
  git reset HEAD ci-operator/jobs/infra-periodics.yaml 2>/dev/null || true && \
  git reset HEAD core-services/jira-lifecycle-plugin/config.yaml 2>/dev/null || true && \
  git diff --staged --quiet || \
  git commit -m "make update"

[ -f "ci-operator/jobs/infra-periodics.yaml" ] && \
  git add ci-operator/jobs/infra-periodics.yaml && \
  git diff --staged --quiet || \
  git commit -m "bump periodic-prow-auto-config-brancher and periodic-openshift-release-fast-forward"

[ -f "core-services/jira-lifecycle-plugin/config.yaml" ] && \
  git add core-services/jira-lifecycle-plugin/config.yaml && \
  git diff --staged --quiet || \
  git commit -m "bump versions in jira config"

echo "✓ Commits: $(git log --oneline origin/master..HEAD | wc -l)"
