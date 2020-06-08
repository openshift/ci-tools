#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"
trap os::test::junit::reconcile_output EXIT

suite_dir="${OS_ROOT}/test/integration/ci-operator-config-mirror"
cp -r "${suite_dir}/input" "${BASETMPDIR}"
cp -r "${suite_dir}/input-to-clean" "${BASETMPDIR}"

actual="${BASETMPDIR}/input"
actual_to_clean="${BASETMPDIR}/input-to-clean"

expected="${suite_dir}/output"

os::test::junit::declare_suite_start "integration/ci-operator-config-mirror"
# This test validates the ci-operator-config-mirror tool

os::cmd::expect_success "ci-operator-config-mirror --to-org super-priv --config-path ${actual} --clean=false --whitelist-file ${suite_dir}/whitelist.yaml"
os::integration::compare "${actual}" "${expected}"

os::cmd::expect_success "ci-operator-config-mirror --to-org super-priv --config-path ${actual_to_clean} --clean=true --whitelist-file ${suite_dir}/whitelist.yaml"
os::integration::compare "${actual_to_clean}" "${expected}"

os::cmd::expect_success "ci-operator-config-mirror --only-org super --to-org super-priv --config-path ${actual_to_clean} --clean=true --whitelist-file ${suite_dir}/whitelist.yaml"
os::integration::compare "${actual_to_clean}" "${suite_dir}/output-only-super"

os::test::junit::declare_suite_end
