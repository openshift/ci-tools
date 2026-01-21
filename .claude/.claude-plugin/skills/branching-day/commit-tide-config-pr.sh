#!/bin/bash
set -euo pipefail

[ "$#" -ne 3 ] && echo "Usage: $0 <current> <future> <release-repo>" && exit 1

cd "$3"
[ "$(git branch --show-current)" != "tide-config-$2" ] && echo "Error: Wrong branch" && exit 1

git add core-services/prow/ && git commit -m "tide-config-manager make prow-config"
git add ci-operator/jobs/infra-periodics.yaml && git commit -m "bump periodic-openshift-release-merge-blockers"
git add ci-operator/jobs/infra-periodics.yaml && git commit -m "bump periodic-ocp-build-data-enforcer"

git diff --quiet core-services/prow/02_config/openshift/etcd/_prowconfig.yaml || \
  (git add core-services/prow/02_config/openshift/etcd/_prowconfig.yaml && git commit -m "etcd manual change")

echo "✓ Commits: $(git log --oneline origin/master..HEAD | wc -l)"
