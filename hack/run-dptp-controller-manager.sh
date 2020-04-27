#!/usr/bin/env bash

set -euo pipefail

cd $(dirname $0)/..
CGO_ENABLED=0 go build -v -o /tmp/dptp-cm ./cmd/dptp-controller-manager
/tmp/dptp-cm \
  --leader-election-namespace=ci \
  --imagestreamtagreconciler.ignored-github-organization=openshift-priv \
  --ci-operator-config-path="$(go env GOPATH)/src/github.com/openshift/release/ci-operator/config" \
  --prow-job-namespace=ci \
  --dry-run=true
