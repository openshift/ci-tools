#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/testgrid-config-generator/"
workdir="${BASETMPDIR}/testgrid-config-generator"
mkdir -p "${workdir}"
cp -a "${suite_dir}/config/testgrid/"* "${workdir}"

os::test::junit::declare_suite_start "integration/testgrid-config-generator"
# This test validates the testgrid-config-generator tool

os::cmd::expect_success "testgrid-config-generator --release-config ${suite_dir}/config/release --testgrid-config ${workdir} --prow-jobs-dir ${suite_dir}/config/jobs --allow-list ${suite_dir}/config/_allow-list.yaml --allow-list ${suite_dir}/config/_allow-list2.yaml"
os::integration::compare "${workdir}" "${suite_dir}/expected"

os::cmd::expect_failure_and_text "testgrid-config-generator --release-config ${suite_dir}/config/release --allow-list ${suite_dir}/config/_allow-list-broken.yaml --validate" "The following jobs are blocking by virtue of being in the release-controller configuration, but are also in the allow-list. Their entries in the allow-list are disallowed and should be removed: release-openshift-ocp-installer-e2e-aws-4.2"

os::test::junit::declare_suite_end