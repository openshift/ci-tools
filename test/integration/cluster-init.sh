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

# the :q is to exit the vim console that opens in order to add info to the README file
os::cmd::expect_success "echo ':q' |  cluster-init -cluster-name=newCluster -release-repo=${actual}"
os::integration::compare "${actual}" "${expected}"

os::test::junit::declare_suite_end