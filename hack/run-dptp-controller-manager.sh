#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "$0")/.."

tmpdir="$(mktemp -d )"
trap 'rm -rf $tmpdir' EXIT
echo "Extracting kubeconfigs for the controller..."
oc --context app.ci --namespace ci extract secret/dptp-controller-manager --to "${tmpdir}"
oc --context app.ci --namespace ci serviceaccounts create-kubeconfig dptp-controller-manager | sed 's/dptp-controller-manager/app.ci/g' > "${tmpdir}/app-ci-kubeconfig"
rm -rf "${tmpdir}/api-ci-kubeconfig"
unset KUBECONFIG

# TODO: we could also just make the SA access all leases and CMs in ci
cat <<EOF | oc --as system:admin --context app.ci apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: dptp-controller-manager-testing
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: dptp-controller-manager-testing
rules:
- apiGroups:
  - prow.k8s.io
  resources:
  - prowjobs
  verbs:
  - '*'
- apiGroups:
  - coordination.k8s.io
  resources:
  - leases
  verbs:
  - get
  - update
- apiGroups:
  - coordination.k8s.io
  resources:
  - leases
  verbs:
  - create
- apiGroups:
  - ""
  resources:
  - configmaps
  verbs:
  - get
  - update
- apiGroups:
  - ""
  resources:
  - configmaps
  - events
  verbs:
  - create
- apiGroups:
  - ""
  resources:
  - namespaces
  verbs:
  - create
  - get
  - watch
  - list
---
kind: RoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: dptp-controller-manager-testing
  namespace: dptp-controller-manager-testing
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: dptp-controller-manager-testing
subjects:
- kind: ServiceAccount
  name: dptp-controller-manager
  namespace: ci
EOF

release="${RELEASE:-"$(go env GOPATH)/src/github.com/openshift/release"}"

go build  -v -o /tmp/dptp-cm ./cmd/dptp-controller-manager
set -x
KUBECONFIG="$(kubeconfigs=("${tmpdir}/"*); IFS=":"; echo "${kubeconfigs[*]}")" /tmp/dptp-cm \
  --leader-election-namespace=dptp-controller-manager-testing \
  --leader-election-suffix="-$USER" \
  --config-path="${release}/core-services/prow/02_config/_config.yaml" \
  --job-config-path="${release}/ci-operator/jobs" \
  --ci-operator-config-path="${release}/ci-operator/config" \
  --step-config-path="${release}/ci-operator/step-registry" \
  --enable-controller=promotion_namespace_reconciler \
  --dry-run=true
