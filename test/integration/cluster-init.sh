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
actual="${tempdir}/input"
expected="${suite_dir}/expected"

os::test::junit::declare_suite_start "integration/cluster-init"

os::cmd::expect_success "cluster-init -cluster-name=newCluster -release-repo=${actual} -create-pr=false"
os::integration::compare "${actual}" "${expected}"

os::test::junit::declare_suite_end