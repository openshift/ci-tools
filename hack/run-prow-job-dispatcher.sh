#!/usr/bin/env bash

set -euo pipefail

cd $(dirname $0)/..

prom_password_file="$(mktemp)"

trap 'rm -f ${prom_password_file}' EXIT

oc --context app.ci get secret -n prow-monitoring prometheus-auth-credentials -o yaml | yq -r '.data.password' | base64 -d > "${prom_password_file}"

prom_username=$(oc --context app.ci get secret -n prow-monitoring prometheus-auth-credentials -o yaml | yq -r '.data.username' | base64 -d)

oc --context app.ci get secret -n ci github-credentials-openshift-bot -o yaml | yq -r '.data.oauth' | base64 -d > /tmp/token

go build  -v -o /tmp/prow-job-dispatcher ./cmd/prow-job-dispatcher
/tmp/prow-job-dispatcher \
  --prometheus-username=${prom_username} \
  --prometheus-password-path=${prom_password_file} \
  --config-path="$(go env GOPATH)/src/github.com/openshift/release/core-services/sanitize-prow-jobs/_config.yaml" \
  --prow-jobs-dir="$(go env GOPATH)/src/github.com/openshift/release/ci-operator/jobs" \
  --target-dir="$(go env GOPATH)/src/github.com/openshift/release" \
  --github-token-path=/tmp/token \
  --github-login=openshift-bot \
  --git-name=openshift-bot \
  --git-email=openshift-bot@redhat.com \
  --create-pr=true \
