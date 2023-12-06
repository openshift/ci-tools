#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(dirname "$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )")"

tmpdir="$(mktemp -d )"
trap 'rm -rf $tmpdir' EXIT
echo "Extracting kubeconfigs for the controller..."
oc --context app.ci --namespace ci extract secret/dptp-controller-manager --to "${tmpdir}"
if oc --kubeconfig ${tmpdir}/sa.dptp-controller-manager.app.ci.config whoami ; then
  echo "Use the existing app.ci kubeconfig"
else
  echo "Creating the app.ci kubeconfig ..."
  mkdir "${tmpdir}/config-updater"
  oc --context app.ci extract secret/config-updater -n ci --to="${tmpdir}/config-updater" --keys sa.config-updater.app.ci.config --confirm
  "${PROJECT_DIR}/images/ci-secret-generator/oc_sa_create_kubeconfig.sh" "${tmpdir}/config-updater" app.ci dptp-controller-manager ci > "${tmpdir}/sa.dptp-controller-manager.app.ci.config"
fi
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

set -x
go run ./cmd/dptp-controller-manager \
  --leader-election-namespace=dptp-controller-manager-testing \
  --leader-election-suffix="-$USER" \
  --release-repo-git-sync-path="${release}" \
  --enable-controller=promotionreconciler \
  --promotionReconcilerOptions.since=360h \
  --kubeconfig-dir="${tmpdir}" \
  --kubeconfig-suffix=config \
  --github-hourly-tokens=4000 \
  --github-allowed-burst=2000 \
  --dry-run=true
