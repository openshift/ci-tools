#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"
trap os::test::junit::reconcile_output EXIT

suite_dir="${OS_ROOT}/test/ci-operator-config-mirror-integration/data/"
cp -r "${suite_dir}/input" "${BASETMPDIR}"
actual="${BASETMPDIR}/input"
expected="${suite_dir}/output"

os::test::junit::declare_suite_start "integration/ci-operator-config-mirror"
# This test validates the ci-operator-config-mirror tool

os::cmd::expect_success "ci-operator-config-mirror --to-org super-priv --config-path ${actual} --whitelist-file ${suite_dir}/whitelist.yaml"
os::integration::compare "${actual}" "${expected}"

os::test::junit::declare_suite_end
