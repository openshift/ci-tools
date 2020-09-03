#!/usr/bin/env bash

set -euo pipefail

cd $(dirname $0)/..

kubeconfig="$(mktemp)"
dockercfg=$(mktemp)
trap 'rm -f $kubeconfig $dockercfg' EXIT
kubectl config view --raw>$kubeconfig
IFS=$'\n'
for additional_context in $(kubectl --kubeconfig $kubeconfig config get-contexts -o name|egrep -v 'app\.ci|api\.ci|build01|build02'); do
  kubectl --kubeconfig $kubeconfig config delete-context "$additional_context"
  kubectl --kubeconfig $kubeconfig config delete-cluster "$additional_context" || true
done
# Make sure user env var wont overrule
unset KUBECONFIG

# Steve will make this nicer at some point. We need to preserve the `auths[$registryName] = {"auth":"value" }
# structure while still filtering out the other registries
kubectl --context build01 get secret -n ci regcred  -o json  \
  |jq -r '.data[".dockerconfigjson"]' \
  |base64 -d \
  |jq '{auths : {"registry.svc.ci.openshift.org": .auths["registry.svc.ci.openshift.org"]}}' >$dockercfg


go build  -v -o /tmp/dptp-cm ./cmd/dptp-controller-manager
/tmp/dptp-cm \
  --leader-election-namespace=ci \
  --ci-operator-config-path="$(go env GOPATH)/src/github.com/openshift/release/ci-operator/config" \
  --config-path="$(go env GOPATH)/src/github.com/openshift/release/core-services/prow/02_config/_config.yaml" \
  --job-config-path="$(go env GOPATH)/src/github.com/openshift/release/ci-operator/jobs" \
  --leader-election-suffix="$USER" \
  --enable-controller=promotionreconciler \
  --step-config-path="$(go env GOPATH)/src/github.com/openshift/release/ci-operator/step-registry" \
  --testImagesDistributorOptions.imagePullSecretPath=$dockercfg \
  --kubeconfig=$kubeconfig \
  --testImagesDistributorOptions.additional-image-stream-tag=ci/clonerefs:latest \
  --secretSyncerConfigOptions.config="$(go env GOPATH)/src/github.com/openshift/release/core-services/secret-mirroring/_mapping.yaml" \
  --secretSyncerConfigOptions.secretBoostrapConfigFile="$(go env GOPATH)/src/github.com/openshift/release/core-services/ci-secret-bootstrap/_config.yaml" \
  --dry-run=true
