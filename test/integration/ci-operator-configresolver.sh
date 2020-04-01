#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::integration::configresolver::check_log
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/ci-operator-configresolver-integration/"
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

generation="$( os::integration::configresolver::generation::config )"
mv "${BASETMPDIR}/configs2/release-4.2/openshift-installer-release-4.2-golang111.yaml" "${BASETMPDIR}/configs/release-4.2/openshift-installer-release-4.2.yaml"
os::integration::configresolver::wait_for_config_update "${generation}"
os::cmd::expect_success "curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' >${actual}/openshift-installer-release-4.2-golang111.json"
os::integration::compare "${actual}/openshift-installer-release-4.2-golang111.json" "${expected}/openshift-installer-release-4.2-golang111.json"
os::integration::configresolver::check_log

generation="$( os::integration::configresolver::generation::registry )"
rsync -avh --quiet --delete --inplace "${BASETMPDIR}/multistage-registry/registry2/" "${BASETMPDIR}/multistage-registry/registry/"
os::integration::configresolver::wait_for_registry_update "${generation}"
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
os::integration::configresolver::wait_for_config_update "${generation}"
os::integration::configresolver::check_log

generation="$( os::integration::configresolver::generation::registry )"
rm "${BASETMPDIR}/multistage-registry/configmap/..data"
ln -s "${BASETMPDIR}/multistage-registry/configmap/..2019_11_15_19_57_20.547184898" "${BASETMPDIR}/multistage-registry/configmap/..data"
os::integration::configresolver::wait_for_registry_update "${generation}"
os::integration::configresolver::check_log

os::test::junit::declare_suite_end
