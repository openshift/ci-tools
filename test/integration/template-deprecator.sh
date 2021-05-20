#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/template-deprecator/"
workdir="${BASETMPDIR}/template-deprecator"
mkdir -p "${workdir}"
cp -a "${suite_dir}/"* "${workdir}"
inputs="${workdir}/input"
allowlist="${workdir}/allowlist.yaml"

os::test::junit::declare_suite_start "integration/template-deprecator"

# this invocation will generate a new allowlist
os::cmd::expect_success "template-deprecator --prow-jobs-dir ${inputs}/jobs --prow-config-path ${inputs}/config.yaml --plugin-config ${inputs}/plugins.yaml --allowlist-path ${allowlist}"
os::integration::compare "${allowlist}" "${suite_dir}/expected/allowlist.yaml"

# this invocation will grow an existing allowlist
cp "${inputs}/partial-allowlist.yaml" "${allowlist}"
os::cmd::expect_success "template-deprecator --prow-jobs-dir ${inputs}/jobs --prow-config-path ${inputs}/config.yaml --plugin-config ${inputs}/plugins.yaml --allowlist-path ${allowlist}"
os::integration::compare "${allowlist}" "${suite_dir}/expected/allowlist.yaml"

# this invocation will respect the blockers already present in allowlist
cp "${inputs}/blockered-allowlist.yaml" "${allowlist}"
os::cmd::expect_success "template-deprecator --prow-jobs-dir ${inputs}/jobs --prow-config-path ${inputs}/config.yaml --plugin-config ${inputs}/plugins.yaml --allowlist-path ${allowlist}"
os::integration::compare "${allowlist}" "${suite_dir}/expected/blockered-allowlist.yaml"

# this invocation should prune old jobs from the allowlist
cp "${inputs}/to-be-pruned-allowlist.yaml" "${allowlist}"
os::cmd::expect_success "template-deprecator --prune=true --prow-jobs-dir ${inputs}/jobs --prow-config-path ${inputs}/config.yaml --plugin-config ${inputs}/plugins.yaml --allowlist-path ${allowlist}"
os::integration::compare "${allowlist}" "${suite_dir}/expected/blockered-allowlist.yaml"

os::test::junit::declare_suite_end
