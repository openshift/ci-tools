#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::integration::configresolver::check_log
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/ci-operator-configresolver/"
cp -a "${suite_dir}"/* "${BASETMPDIR}"
cp -a "${OS_ROOT}/test/multistage-registry" "${BASETMPDIR}"
actual="${BASETMPDIR}/actual"
mkdir -p "${actual}"
expected="${suite_dir}/expected"

os::test::junit::declare_suite_start "integration/ci-operator-configresolver"
# This test validates the ci-operator-configresolver tool

os::integration::configresolver::start "${BASETMPDIR}/configs" "${BASETMPDIR}/multistage-registry/registry" "${BASETMPDIR}/config.yaml"
os::cmd::expect_success "curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' >${actual}/openshift-installer-release-4.2.json"
os::integration::compare "${actual}/openshift-installer-release-4.2.json" "${expected}/openshift-installer-release-4.2.json"
os::integration::configresolver::check_log
os::cmd::expect_success "curl -X POST -H 'Content-Type: application/json' --data @${BASETMPDIR}/unresolved-config.json 'http://127.0.0.1:8080/resolve' >${actual}/resolved-config.json"
os::integration::compare "${actual}/resolved-config.json" "${expected}/resolved-config.json"
os::integration::configresolver::check_log
os::cmd::expect_success "curl 'http://127.0.0.1:8080/configWithInjectedTest?org=openshift&repo=installer&branch=release-4.2&injectTestFromOrg=openshift&injectTestFromRepo=release&injectTestFromBranch=master&injectTestFromVariant=ci-4.9&injectTest=e2e' >${actual}/openshift-installer-release-4.2-injected.json"
os::integration::compare "${actual}/openshift-installer-release-4.2-injected.json" "${expected}/openshift-installer-release-4.2-injected.json"
os::integration::configresolver::check_log
os::cmd::expect_success "curl 'http://127.0.0.1:8080/mergeConfigsWithInjectedTest?org=openshift,openshift&repo=installer,console&branch=release-4.2,master&injectTestFromOrg=openshift&injectTestFromRepo=release&injectTestFromBranch=master&injectTestFromVariant=ci-4.9&injectTest=e2e' >${actual}/openshift-installer-console-merged-release-4.2-injected.json"
os::integration::compare "${actual}/openshift-installer-console-merged-release-4.2-injected.json" "${expected}/openshift-installer-console-merged-release-4.2-injected.json"
os::integration::configresolver::check_log

generation="$( os::integration::configresolver::generation::config )"
mv "${BASETMPDIR}/configs2/release-4.2/openshift-installer-release-4.2-golang111.yaml" "${BASETMPDIR}/configs/release-4.2/openshift-installer-release-4.2.yaml"
os::integration::configresolver::wait_for_config_update "$(($generation+1))"
os::cmd::expect_success "curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' >${actual}/openshift-installer-release-4.2-golang111.json"
os::integration::compare "${actual}/openshift-installer-release-4.2-golang111.json" "${expected}/openshift-installer-release-4.2-golang111.json"
os::integration::configresolver::check_log

generation="$( os::integration::configresolver::generation::registry )"
mv "${BASETMPDIR}/multistage-registry/registry2/ipi/install/install/ipi-install-install-commands.sh"  "${BASETMPDIR}/multistage-registry/registry/ipi/install/install/ipi-install-install-commands.sh"
mv "${BASETMPDIR}/multistage-registry/registry2/ipi/install/install/ipi-install-install-ref.yaml"  "${BASETMPDIR}/multistage-registry/registry/ipi/install/install/ipi-install-install-ref.yaml"
mv "${BASETMPDIR}/multistage-registry/registry2/ipi/install/rbac/ipi-install-rbac-commands.sh"  "${BASETMPDIR}/multistage-registry/registry/ipi/install/rbac/ipi-install-rbac-commands.sh"
os::integration::configresolver::wait_for_registry_update "$(($generation+3))"
os::cmd::expect_success "curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' >${actual}/openshift-installer-release-4.2-regChange.json"
os::integration::compare "${actual}/openshift-installer-release-4.2-regChange.json" "${expected}/openshift-installer-release-4.2-regChange.json"
os::integration::configresolver::check_log
os::integration::configresolver::stop

os::integration::configresolver::start "${BASETMPDIR}/ci-op-configmaps" "${BASETMPDIR}/multistage-registry/configmap" "${BASETMPDIR}/config.yaml" "true"
os::integration::configresolver::generation::config
generation="$( os::integration::configresolver::generation::config )"
rm "${BASETMPDIR}/ci-op-configmaps/master/..data"
os::integration::configresolver::generation::config
ln -s "${BASETMPDIR}/ci-op-configmaps/master/..2019_11_15_19_57_20.547184898" "${BASETMPDIR}/ci-op-configmaps/master/..data"
os::integration::configresolver::generation::config
os::integration::configresolver::wait_for_config_update "$(($generation+1))"
os::integration::configresolver::check_log

generation="$( os::integration::configresolver::generation::registry )"
rm "${BASETMPDIR}/multistage-registry/configmap/..data"
ln -s "${BASETMPDIR}/multistage-registry/configmap/..2019_11_15_19_57_20.547184898" "${BASETMPDIR}/multistage-registry/configmap/..data"
os::integration::configresolver::wait_for_registry_update "$((generation+1))"
os::integration::configresolver::check_log

os::test::junit::declare_suite_end
