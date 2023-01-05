#!/usr/bin/env bash

source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

readonly release_repo_relative_dir="core-services/release-controller/_repos"
readonly test_cases_dir="${OS_ROOT}/test/integration/branchcuts/rpm-deps-mirroring-services"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

function branchcuts::rpm_mirroring::run_config_manager() {
    local _test_case_data="${1}"
    local -n _cfg=$2
    local _release_repo_path="${_test_case_data}/release"
    local _current_release="${_cfg['current_ocp_release']}"

    local _cmd=$(printf "%s\n%s %s\n%s %s\n%s %s" \
        rpm-deps-mirroring-services \
        --release-repo "${_release_repo_path}" \
        --current-release "${_current_release}")

    os::log::info "${_cmd}"

    rpm-deps-mirroring-services \
        --release-repo "${_release_repo_path}" \
        --current-release "${_current_release}"
}
readonly -f branchcuts::rpm_mirroring::run_config_manager


function branchcuts::rpm_mirroring::run_tests() {
    branchcuts::run_tests_template branchcuts::rpm_mirroring::run_config_manager
}
readonly -f branchcuts::rpm_mirroring::run_tests

os::test::junit::declare_suite_start "integration/branchcuts/rpm-deps-mirroring-services"
os::cmd::expect_success "branchcuts::rpm_mirroring::run_tests"
os::test::junit::declare_suite_end
