#!/usr/bin/env bash

set -euo pipefail

command -v deck &>/dev/null || k8s.io/test-infra/prow/cmd/deck

KUBECONFIG="$(mktemp)"
export KUBECONFIG

cleanup(){
  rm -f "$KUBECONFIG"
  rm -f "$deck_config"
  killall deck
}
trap cleanup EXIT
# Deck won't start without a cluster that has the prowjob crd and using the procution cluster
# makes everything annoyingly slow as it has a lot of data
if command -v kind &>/dev/null; then
  if kind get clusters 2>&1|grep -q 'No kind clusters found'; then
    kind create cluster
    kubectl apply -f https://raw.githubusercontent.com/kubernetes/test-infra/master/config/prow/cluster/prowjob_customresourcedefinition.yaml
  else
    kind get kubeconfig >"$KUBECONFIG"
  fi
fi

# Don't clean this up, invocating bazel needlessly is bad for the environment
if ! [[ -d /tmp/deck ]]; then
  cd "$(go env GOPATH)/src/k8s.io/test-infra"
  bazel build //prow/cmd/deck:image.tar
  mkdir -p /tmp/deck
  tar -xvf ./bazel-bin/prow/cmd/deck/asset-base-layer.tar -C /tmp/deck
  tar -xvf ./bazel-bin/prow/cmd/deck/spyglass-lenses-layer.tar -C /tmp/deck
  cd -
fi

deck_config=$(mktemp)
cat <<EOF >"$deck_config"
deck:
  spyglass:
    lenses:
    - lens:
        name: stepgraph
      required_files:
      - artifacts/ci-operator-step-graph.json
      remote_config:
        endpoint: http://127.0.0.1:1235/dynamic/steps
        priority: 60
EOF


deck -spyglass --config-path="$deck_config" \
 --template-files-location=/tmp/deck/template \
 --static-files-location=/tmp/deck/static \
 --spyglass-files-location=/tmp/deck/lenses &

echo "After the lensserver started, you can reach deck on localhost:8080"
echo "A sample url for a job is http://localhost:8080/view/gs/test-platform-results/pr-logs/pull/openshift_ci-tools/1501/pull-ci-openshift-ci-tools-master-format/1336382299314327552"
go run ./cmd/lensserver --config-path="$deck_config"
