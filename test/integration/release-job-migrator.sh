#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/release-job-migrator/"
workdir="${BASETMPDIR}/release-job-migrator"
mkdir -p "${workdir}"
cp -a "${suite_dir}"/* "${workdir}"
inputs="${workdir}/input"
output="${workdir}/output.yaml"

os::test::junit::declare_suite_start "integration/release-job-migrator"
os::cmd::expect_success "release-job-migrator --config ${inputs}/config.yaml --jobs ${inputs}/jobs --ci-op-configs ${inputs}/ci-operator/openshift/release --rc-configs ${inputs}/releases -testgrid-allowlist ${inputs}/_allow-list.yaml -ignore-release '4.8' > ${output}"
os::integration::compare "${inputs}" "${suite_dir}/expected"
os::integration::compare "${output}" "${suite_dir}/expected.txt"

# reset workdir
rm -r "${workdir}"/*
cp -a "${suite_dir}"/* "${workdir}"
os::cmd::expect_success "release-job-migrator --config ${inputs}/config.yaml --jobs ${inputs}/jobs --ci-op-configs ${inputs}/ci-operator/openshift/release --rc-configs ${inputs}/releases -testgrid-allowlist ${inputs}/_allow-list.yaml -ignore-release '4.8' -ignore-unresolved > ${output}"
os::integration::compare "${inputs}" "${suite_dir}/expected2"
os::integration::compare "${output}" "${suite_dir}/expected2.txt"
os::test::junit::declare_suite_end
