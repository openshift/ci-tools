#!/usr/bin/env bash

set -euo pipefail

cd $(dirname $0)/..

kubeconfig="$(mktemp)"

trap 'rm -f $kubeconfig' EXIT
oc --context app.ci --as system:admin --namespace ci serviceaccounts create-kubeconfig promoted-image-governor >$kubeconfig


release="${RELEASE:-"$(go env GOPATH)/src/github.com/openshift/release"}"

go run  ./cmd/promoted-image-governor \
  --kubeconfig=$kubeconfig \
  --ci-operator-config-path=${release}/ci-operator/config \
  --release-controller-mirror-config-dir=${release}/core-services/release-controller/_releases \
  --dry-run=true
