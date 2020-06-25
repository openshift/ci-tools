#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/group-auto-updater/"
workdir="${BASETMPDIR}/group-auto-updater"
mkdir -p "${workdir}"

os::test::junit::declare_suite_start "integration/group-auto-updater"
# This test validates the group-auto-updater tool

os::cmd::expect_success "group-auto-updater --group test-group --peribolos-config ${suite_dir}/peribolos-config.yaml --org test-org --dry-run > ${workdir}/group.yaml"
os::integration::compare "${workdir}/group.yaml" "${suite_dir}/expected.yaml"

os::test::junit::declare_suite_end