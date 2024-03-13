#!/usr/bin/env bash

set -euo pipefail

TMP_DIR="$(mktemp -d)"

trap 'rm -rf ${TMP_DIR}' EXIT
oc --context app.ci  -n ci extract secret/ci-images-mirror --to="${TMP_DIR}"
oc --context app.ci -n ci extract secret/registry-push-credentials-ci-images-mirror --to=- --keys .dockerconfigjson | jq > "${TMP_DIR}/a.c"

release="${RELEASE:-"$(go env GOPATH)/src/github.com/openshift/release"}"

set -x
KUBECONFIG="${TMP_DIR}/sa.ci-images-mirror.app.ci.config" go run  ./cmd/ci-images-mirror \
  --registry-config="${TMP_DIR}/a.c" \
  --leader-election-namespace=ci \
  --leader-election-suffix="-${USER}" \
  --release-repo-git-sync-path="${release}"  \
  --config="${release}/core-services/image-mirroring/supplemental-ci-images/_config.yaml" \
  --quayIOCIImagesDistributorOptions.additional-image-stream-namespace=ci \
  --dry-run=true
