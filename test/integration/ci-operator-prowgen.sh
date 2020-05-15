#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/prowgen-integration/"
input_config_dir="${suite_dir}/data/input/config"
input_jobs_dir="${suite_dir}/data/input/jobs"
actual="${BASETMPDIR}/jobs"
mkdir -p "${actual}"
# we need to seed this with the input data as we operate "in place"
cp -a "${input_jobs_dir}/." "${actual}"
expected="${suite_dir}/data/output/jobs"

os::test::junit::declare_suite_start "integration/ci-operator-prowgen"
# This test validates the ci-operator-prowgen tool

os::cmd::expect_success "ci-operator-prowgen --from-dir ${input_config_dir} --to-dir ${actual}"
os::integration::compare "${actual}" "${expected}"

os::test::junit::declare_suite_end
