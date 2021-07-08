#!/usr/bin/env bash

set -euo pipefail

cd $(dirname $0)/..

kubeconfig="$(mktemp)"

trap 'rm -f $kubeconfig' EXIT
kubectl config view --raw>$kubeconfig
IFS=$'\n'
for additional_context in $(kubectl --kubeconfig $kubeconfig config get-contexts -o name|egrep -v 'hive'); do
  kubectl --kubeconfig $kubeconfig config delete-context "$additional_context"
done

# Make sure user env var wont overrule
unset KUBECONFIG

go run  ./cmd/dptp-pools-cm \
  --leader-election-namespace=ci \
  --leader-election-suffix="$USER" \
  --kubeconfig=$kubeconfig \
  --dry-run=true
