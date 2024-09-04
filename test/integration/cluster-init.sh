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

os::test::junit::declare_suite_start "integration/cluster-init"

# test the create scenario
actual_create="${tempdir}/create/input"
expected_create="${suite_dir}/create/expected"
os::cmd::expect_success "cluster-init onboard config generate --hosted --cluster-name=newCluster --release-repo=${actual_create} --create-pr=false"
os::integration::compare "${actual_create}" "${expected_create}"
# test the update scenario
actual_update="${tempdir}/update/input"
expected_update="${suite_dir}/update/expected"
os::cmd::expect_success "cluster-init onboard config generate --cluster-name=existingCluster --release-repo=${actual_update} --update=true --create-pr=false"
os::integration::compare "${actual_update}" "${expected_update}"
# test the create ocp scenario
actual_create="${tempdir}/create-ocp/input"
expected_create="${suite_dir}/create-ocp/expected"
os::cmd::expect_success "cluster-init onboard config generate --hosted --cluster-name=newCluster --release-repo=${actual_create} --create-pr=false --osd=false"
os::integration::compare "${actual_create}" "${expected_create}"
# test the update scenario
actual_update="${tempdir}/update-ocp/input"
expected_update="${suite_dir}/update-ocp/expected"
os::cmd::expect_success "cluster-init onboard config generate --cluster-name=existingCluster --release-repo=${actual_update} --update=true --create-pr=false --osd=false"
os::integration::compare "${actual_update}" "${expected_update}"

os::test::junit::declare_suite_end