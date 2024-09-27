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
kubeconfigs="$tempdir/kubeconfigs"
mkdir -p "$kubeconfigs"

function generate_kubeconfig() {
    clustername="$1"
    path="$2"
    cat >"$path" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://api.${clustername}.fake:6443
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
    hosted="$3"
    osd="$4"
    cat >"${tempdir}/cluster-install.yaml" <<EOF
clusterName: $clustername
installBase: $install_base
onboard:
    releaseRepo: $releaserepo
    hosted: $hosted
    osd: $osd
    kubeconfigDir: $kubeconfigs
    kubeconfigSuffix: config
    dex:
      redirectURI:
        newCluster: https://oauth-openshift.apps.newCluster.ky4t.p1.openshiftapps.com/oauth2callback/RedHat_Internal_SSO
        existingCluster: https://oauth-openshift.apps.existingCluster.ky4t.p1.openshiftapps.com/oauth2callback/RedHat_Internal_SSO
    quayioPullThroughCache:
      mirrorURI:
        newCluster: quayio-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com
        existingCluster: quayio-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com
    certificate:
      baseDomains:
        newCluster: ci.devcluster.openshift.com
        existingCluster: ci.devcluster.openshift.com
      imageRegistryPublicHosts:
        newCluster: registry.newCluster.ci.openshift.org
        existingCluster: registry.existingCluster.ci.openshift.org
EOF
}

generate_kubeconfig "newCluster" "$kubeconfigs/newCluster.config"
generate_kubeconfig "existingCluster" "$kubeconfigs/existingCluster.config"

os::test::junit::declare_suite_start "integration/cluster-init"

# test the create scenario
actual_create="${tempdir}/create/input"
expected_create="${suite_dir}/create/expected"
generate_kubeconfig "newCluster" "$install_base/ocp-install-base/auth/kubeconfig"
generate_cluster_install "newCluster" "$actual_create" true true
os::cmd::expect_success "cluster-init --cluster-install=${tempdir}/cluster-install.yaml onboard config generate --create-pr=false"
os::integration::compare "${actual_create}" "${expected_create}"

# test the update scenario
actual_update="${tempdir}/update/input"
expected_update="${suite_dir}/update/expected"
generate_cluster_install "existingCluster" "$actual_update" false true
os::cmd::expect_success "cluster-init onboard --cluster-install=${tempdir}/cluster-install.yaml config generate --update=true --create-pr=false"
os::integration::compare "${actual_update}" "${expected_update}"

# test the create ocp scenario
actual_create="${tempdir}/create-ocp/input"
expected_create="${suite_dir}/create-ocp/expected"
generate_kubeconfig "newCluster" "$install_base/ocp-install-base/auth/kubeconfig"
generate_cluster_install "newCluster" "$actual_create" false false
os::cmd::expect_success "cluster-init onboard --cluster-install=${tempdir}/cluster-install.yaml config generate --create-pr=false"
os::integration::compare "${actual_create}" "${expected_create}"

# test the update scenario
actual_update="${tempdir}/update-ocp/input"
expected_update="${suite_dir}/update-ocp/expected"
generate_cluster_install "existingCluster" "$actual_update" false false
os::cmd::expect_success "cluster-init onboard --cluster-install=${tempdir}/cluster-install.yaml config generate --update=true --create-pr=false"
os::integration::compare "${actual_update}" "${expected_update}"

os::test::junit::declare_suite_end