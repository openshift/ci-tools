#!/usr/bin/env bash

set -euo pipefail

cd $(dirname $0)/..

kubeconfig="$(mktemp)"
dockercfg=$(mktemp)
trap 'rm -f $kubeconfig $dockercfg' EXIT
kubectl config view >$kubeconfig
IFS=$'\n'
for additional_context in $(kubectl --kubeconfig $kubeconfig config get-contexts -o name|egrep -v 'app\.ci|api\.ci|build01'); do
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
  --enable-controller=test_images_distributor \
  --step-config-path="$(go env GOPATH)/src/github.com/openshift/release/ci-operator/step-registry" \
  --testImagesDistributorOptions.imagePullSecretPath=$dockercfg \
  --kubeconfig=$kubeconfig \
  --dry-run=true
