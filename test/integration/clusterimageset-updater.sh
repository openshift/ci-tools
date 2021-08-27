#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/clusterimageset-updater/"
workdir="${BASETMPDIR}/clusterimageset-updater"
mkdir -p "${workdir}"
cp -a "${suite_dir}"/* "${workdir}"
inputs="${workdir}/input"
expected="${workdir}/output"

os::test::junit::declare_suite_start "integration/clusterimageset-updater"

os::cmd::expect_success "clusterimageset-updater --pools ${inputs}/pools --imagesets ${inputs}/imagesets"
os::integration::compare "${inputs}" "${expected}"

os::test::junit::declare_suite_end
