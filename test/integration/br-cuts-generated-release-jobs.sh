#!/usr/bin/env bash

source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

readonly release_repo_relative_dir="ci-operator/config/openshift/release"
readonly test_cases_dir="${OS_ROOT}/test/integration/branchcuts/generated-release-gating-jobs"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

function branchcuts::generated_release_jobs::run_config_manager() {
    local _test_case_data="${1}"
    local -n _cfg=$2
    local _release_repo_path="${_test_case_data}/release"

    local _cmd=$(printf "%s\n%s %s\n%s %s\n%s %s" \
        generated-release-gating-jobs \
        --release-repo "${_release_repo_path}" \
        --current-release "${_cfg['current_ocp_release']}" \
        --interval "${_cfg['interval']}")

    os::log::info "${_cmd}"

    generated-release-gating-jobs \
        --release-repo "${_release_repo_path}" \
        --current-release "${_cfg['current_ocp_release']}" \
        --interval "${_cfg['interval']}"
}
readonly -f branchcuts::generated_release_jobs::run_config_manager

function branchcuts::generated_release_jobs::run_tests() {
    branchcuts::run_tests_template branchcuts::generated_release_jobs::run_config_manager
}
readonly -f branchcuts::generated_release_jobs::run_tests

os::test::junit::declare_suite_start "integration/branchcuts/generated-release-gating-jobs"
os::cmd::expect_success "branchcuts::generated_release_jobs::run_tests"
os::test::junit::declare_suite_end
