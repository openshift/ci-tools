#!/usr/bin/env bash

set -euo pipefail

cd $(dirname $0)/..

TMP_DIR="$(mktemp -d)"
prom_token_file=${TMP_DIR}/prom_token_file
gh_token_file=${TMP_DIR}/gh_token_file

trap 'rm -rf ${TMP_DIR}' EXIT

oc --context app.ci -n ci extract secret/app-ci-openshift-user-workload-monitoring-credentials --to=- --keys=sa.prometheus-user-workload.app.ci.token.txt > "${prom_token_file}"

oc --context app.ci -n ci extract secret/github-credentials-openshift-bot --to=- --keys=oauth > "${gh_token_file}"

RELEASE="${RELEASE:-$(go env GOPATH)/src/github.com/openshift/release}"

go build  -v -o /tmp/prow-job-dispatcher ./cmd/prow-job-dispatcher
/tmp/prow-job-dispatcher \
  --prometheus-bearer-token-path=${prom_token_file} \
  --config-path="${RELEASE}/core-services/sanitize-prow-jobs/_config.yaml" \
  --prow-jobs-dir="${RELEASE}/ci-operator/jobs" \
  --target-dir="${RELEASE}" \
  --github-token-path=${gh_token_file} \
  --github-login=openshift-bot \
  --git-name=openshift-bot \
  --git-email=openshift-bot@redhat.com \
  --create-pr=false \
