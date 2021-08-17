#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"
trap os::test::junit::reconcile_output EXIT

suite_dir="${OS_ROOT}/test/integration/ci-operator-config-mirror"
workdir="${BASETMPDIR}/ci-operator-config-mirror"
mkdir -p "${workdir}"
cp -a "${suite_dir}/"* "${workdir}"

os::test::junit::declare_suite_start "integration/ci-operator-config-mirror"
# This test validates the ci-operator-config-mirror tool

actual="${workdir}/input"
os::cmd::expect_success "ci-operator-config-mirror --to-org super-priv --config-dir ${actual} --clean=false --whitelist-file ${suite_dir}/whitelist.yaml"
os::integration::compare "${actual}" "${suite_dir}/output"

actual="${workdir}/input-to-clean"
os::cmd::expect_success "ci-operator-config-mirror --to-org super-priv --config-dir ${actual} --clean=true --whitelist-file ${suite_dir}/whitelist.yaml"
os::integration::compare "${actual}" "${suite_dir}/output"

actual="${workdir}/input-to-clean"
os::cmd::expect_success "ci-operator-config-mirror --only-org super --to-org super-priv --config-dir ${actual} --clean=true --whitelist-file ${suite_dir}/whitelist.yaml"
os::integration::compare "${actual}" "${suite_dir}/output-only-super"

os::test::junit::declare_suite_end
