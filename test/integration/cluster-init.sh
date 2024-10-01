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
clusterinstall_dir="$tempdir/clusterinstall"
mkdir -p "$clusterinstall_dir"

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

generate_kubeconfig "newCluster" "$kubeconfigs/newCluster.config"
generate_kubeconfig "existingCluster" "$kubeconfigs/existingCluster.config"

os::test::junit::declare_suite_start "integration/cluster-init"

# test the create scenario
actual_create="${tempdir}/create/input"
expected_create="${suite_dir}/create/expected"
cat >"${clusterinstall_dir}/newCluster.yaml" <<EOF
clusterName: newCluster
onboard:
    hosted: true
    osd: true
    dex:
      redirectURI: https://oauth-openshift.apps.newCluster.ky4t.p1.openshiftapps.com/oauth2callback/RedHat_Internal_SSO
    quayioPullThroughCache:
      mirrorURI: quayio-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com
    certificate:
      baseDomains: ci.devcluster.openshift.com
      imageRegistryPublicHost: registry.newCluster.ci.openshift.org
EOF
generate_kubeconfig "newCluster" "$install_base/ocp-install-base/auth/kubeconfig"
os::cmd::expect_success "cluster-init --cluster-install="${clusterinstall_dir}/newCluster.yaml" onboard config generate --install-base=$install_base --release-repo=$actual_create"
os::integration::compare "${actual_create}" "${expected_create}"

# test the update scenario
actual_update="${tempdir}/update/input"
expected_update="${suite_dir}/update/expected"
rm "${clusterinstall_dir}/"*
cat >"${clusterinstall_dir}/existingCluster.yaml" <<EOF
clusterName: existingCluster
onboard:
    hosted: false
    osd: true
    dex:
      redirectURI: https://oauth-openshift.apps.existingCluster.ky4t.p1.openshiftapps.com/oauth2callback/RedHat_Internal_SSO
    quayioPullThroughCache:
      mirrorURI: quayio-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com
    certificate:
      baseDomains: ci.devcluster.openshift.com
      imageRegistryPublicHost: registry.existingCluster.ci.openshift.org
EOF
os::cmd::expect_success "cluster-init onboard config update --release-repo=$actual_update --cluster-install-dir=$clusterinstall_dir --kubeconfig-dir=$kubeconfigs --kubeconfig-suffix=config"
os::integration::compare "${actual_update}" "${expected_update}"

# test the create ocp scenario
actual_create="${tempdir}/create-ocp/input"
expected_create="${suite_dir}/create-ocp/expected"
generate_kubeconfig "newCluster" "$install_base/ocp-install-base/auth/kubeconfig"
rm "${clusterinstall_dir}/"*
cat >"${clusterinstall_dir}/newCluster.yaml" <<EOF
clusterName: newCluster
onboard:
    hosted: false
    osd: false
    dex:
      redirectURI: https://oauth-openshift.apps.newCluster.ky4t.p1.openshiftapps.com/oauth2callback/RedHat_Internal_SSO
    quayioPullThroughCache:
      mirrorURI: quayio-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com
    certificate:
      baseDomains: ci.devcluster.openshift.com
      imageRegistryPublicHost: registry.newCluster.ci.openshift.org
EOF
os::cmd::expect_success "cluster-init --cluster-install="${clusterinstall_dir}/newCluster.yaml" onboard config generate --install-base=$install_base --release-repo=$actual_create"
os::integration::compare "${actual_create}" "${expected_create}"

# test the update ocp scenario
actual_update="${tempdir}/update-ocp/input"
expected_update="${suite_dir}/update-ocp/expected"
rm "${clusterinstall_dir}/"*
cat >"${clusterinstall_dir}/existingCluster.yaml" <<EOF
clusterName: existingCluster
onboard:
    hosted: false
    osd: false
    dex:
      redirectURI: https://oauth-openshift.apps.existingCluster.ky4t.p1.openshiftapps.com/oauth2callback/RedHat_Internal_SSO
    quayioPullThroughCache:
      mirrorURI: quayio-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com
    certificate:
      baseDomains: ci.devcluster.openshift.com
      imageRegistryPublicHost: registry.existingCluster.ci.openshift.org
EOF
os::cmd::expect_success "cluster-init onboard config update --release-repo=$actual_update --cluster-install-dir=$clusterinstall_dir --kubeconfig-dir=$kubeconfigs --kubeconfig-suffix=config"
os::integration::compare "${actual_update}" "${expected_update}"

os::test::junit::declare_suite_end