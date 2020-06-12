#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/config-brancher/"
workdir="${BASETMPDIR}/config-brancher"
mkdir -p "${workdir}"
cp -a "${suite_dir}/"* "${workdir}"
actual="${workdir}/input"

os::test::junit::declare_suite_start "integration/config-brancher"
# This test validates the config-brancher tool

# this invocation will branch a config from a source dev config
os::cmd::expect_success "config-brancher --org org --repo new --config-dir ${actual} --current-release=4.5 --future-release=4.6 --confirm"
# this invocation will branch, and bump the dev config
os::cmd::expect_success "config-brancher --org org --repo bump --config-dir ${actual} --current-release=4.5 --future-release=4.6 --bump-release=4.6 --confirm"
# this invocation will edit config in place while bumping
os::cmd::expect_success "config-brancher --org org --repo existing --config-dir ${actual} --current-release=4.5 --future-release=4.6 --bump-release=4.6 --confirm"
os::integration::compare "${actual}" "${suite_dir}/expected"

os::test::junit::declare_suite_end
