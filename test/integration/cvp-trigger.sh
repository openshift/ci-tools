#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/cvp-trigger/"
workdir="${BASETMPDIR}/cvp-trigger"
mkdir -p "${workdir}"

os::test::junit::declare_suite_start "integration/cvp-trigger"
# This test validates the cvp-trigger tool

actual="${workdir}/output.yaml"
os::cmd::expect_success "cvp-trigger --bundle-image-ref=git@example.com/org/bundle.git --index-image-ref=git@example.com/org/index.git --prow-config-path=${suite_dir}/config.yaml --job-config-path=${suite_dir}/jobs.yaml --job-name=periodic-ipi-deprovision --ocp-version=4.5 --operator-package-name=package1 --channel=channel1 --target-namespaces=namespace1 --install-namespace=namespace2 --dry-run>${actual}"
os::integration::sanitize_prowjob_yaml ${actual}
os::integration::compare "${actual}" "${suite_dir}/periodic-ipi-deprovision.expected"

os::test::junit::declare_suite_end
