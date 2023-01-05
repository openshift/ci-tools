#!/usr/bin/env bash

source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

readonly release_repo_relative_dir="core-services/release-controller/_releases"
readonly test_cases_dir="${OS_ROOT}/test/integration/branchcuts/release-controller-configs"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

function branchcuts::release_controller_cfg::run_config_manager() {
    local _test_case_data="${1}"
    local -n _cfg=$2
    local _release_repo_path="${_test_case_data}/release"

    local _cmd=$(printf "%s\n%s %s\n%s %s" \
        release-controller-config-manager \
        --release-repo "${_release_repo_path}" \
        --current-release "${_cfg['current_ocp_release']}")

    os::log::info "${_cmd}"

    release-controller-config-manager \
        --release-repo "${_release_repo_path}" \
        --current-release "${_cfg['current_ocp_release']}"
}
readonly -f branchcuts::release_controller_cfg::run_config_manager


function branchcuts::release_controller_cfg::run_tests() {
    branchcuts::run_tests_template branchcuts::release_controller_cfg::run_config_manager
}
readonly -f branchcuts::release_controller_cfg::run_tests

os::test::junit::declare_suite_start "integration/branchcuts/release-controller-configs"
os::cmd::expect_success "branchcuts::release_controller_cfg::run_tests"
os::test::junit::declare_suite_end
