#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
    rm -rf ${tempdir}
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/cluster-init/"
tempdir="${BASETMPDIR}/cluster-init"
mkdir -p "${tempdir}"
cp -a "${suite_dir}"/* "${tempdir}"

install_base="$tempdir/install-base"
mkdir -p "$install_base/ocp-install-base/auth"
function generate_kubeconfig() {
    clustername="$1"
    cat >"$install_base/ocp-install-base/auth/kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://api.${clustername}.ci.devcluster.openshift.com:6443
  name: api-${clustername}-ci-devcluster-openshift-com
contexts:
- context:
    cluster: api-${clustername}-ci-devcluster-openshift-com
    namespace: ci
    user: user/api-${clustername}-ci-devcluster-openshift-com
  name: ${clustername}
name: ${clustername}
current-context: ${clustername}
preferences: {}
users:
- name: user/api-${clustername}-ci-devcluster-openshift-com
  user:
    token: xxx
EOF
}

function generate_cluster_install() {
    clustername="$1"
    releaserepo="$2"
    out="$3"
    hosted="$4"
    osd="$5"
    cat >"${tempdir}/${out}" <<EOF
clusterName: $clustername
installBase: $install_base
onboard:
    releaseRepo: $releaserepo
    hosted: $hosted
    osd: $osd
    dex:
      redirectURI:
        $clustername: https://oauth-openshift.apps.${clustername}.ky4t.p1.openshiftapps.com/oauth2callback/RedHat_Internal_SSO
EOF
}

os::test::junit::declare_suite_start "integration/cluster-init"

# test the create scenario
actual_create="${tempdir}/create/input"
expected_create="${suite_dir}/create/expected"
generate_cluster_install "newCluster" "$actual_create" "cluster-install-newCluster.yaml" true true
generate_kubeconfig "newCluster"
os::cmd::expect_success "cluster-init --cluster-install=${tempdir}/cluster-install-newCluster.yaml onboard config generate --create-pr=false"
os::integration::compare "${actual_create}" "${expected_create}"
# test the update scenario
actual_update="${tempdir}/update/input"
expected_update="${suite_dir}/update/expected"
generate_cluster_install "existingCluster" "$actual_update" "cluster-install-existingCluster.yaml" false true
generate_kubeconfig "existingCluster"
os::cmd::expect_success "cluster-init onboard --cluster-install=${tempdir}/cluster-install-existingCluster.yaml config generate --update=true --create-pr=false"
os::integration::compare "${actual_update}" "${expected_update}"
# test the create ocp scenario
actual_create="${tempdir}/create-ocp/input"
expected_create="${suite_dir}/create-ocp/expected"
generate_cluster_install "newCluster" "$actual_create" "cluster-install-newCluster-ocp.yaml" false false
generate_kubeconfig "newCluster"
os::cmd::expect_success "cluster-init onboard --cluster-install=${tempdir}/cluster-install-newCluster-ocp.yaml config generate --create-pr=false"
os::integration::compare "${actual_create}" "${expected_create}"
# test the update scenario
actual_update="${tempdir}/update-ocp/input"
expected_update="${suite_dir}/update-ocp/expected"
generate_cluster_install "existingCluster" "$actual_update" "cluster-install-existingCluster-ocp.yaml" false false
generate_kubeconfig "existingCluster"
os::cmd::expect_success "cluster-init onboard --cluster-install=${tempdir}/cluster-install-existingCluster-ocp.yaml config generate --update=true --create-pr=false"
os::integration::compare "${actual_update}" "${expected_update}"

os::test::junit::declare_suite_end