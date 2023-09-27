#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/ci-operator-prowgen"
actual="${BASETMPDIR}/jobs"
mkdir -p "${actual}"
# we need to seed this with the input data as we operate "in place"
cp -a "${suite_dir}/input/jobs/." "${actual}"

os::test::junit::declare_suite_start "integration/ci-operator-prowgen"
# This test validates the ci-operator-prowgen tool

os::cmd::expect_success "ci-operator-prowgen --registry ${suite_dir}/input/registry --known-infra-file infra-periodics.yaml --from-dir ${suite_dir}/input/config --to-dir ${actual}"
os::integration::compare "${actual}" "${suite_dir}/output/jobs"

os::test::junit::declare_suite_end
