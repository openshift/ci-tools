#!/bin/bash

# This script runs one or many of the e2e test suites.
# To run the full test suite, use:
#
#  $ hack/test-e2e.sh
#
# To run a single test suite, use:
#
#  $ hack/test-e2e.sh <name>
#
# To run a set of suites matching some regex, use:
#
#  $ hack/test-e2e.sh <regex>
source "$(dirname "${BASH_SOURCE}")/lib/init.sh"
os::util::environment::setup_time_vars

function cleanup() {
  return_code=$?
  os::test::junit::generate_report
  os::cleanup::all
  os::util::describe_return_code "${return_code}"
  exit "${return_code}"
}
trap "cleanup" EXIT

function find_tests() {
    local test_regex="${1}"
    local full_test_list=()
    local selected_tests=()

    full_test_list=( $(find "${OS_ROOT}/test/e2e" -mindepth 1 -maxdepth 1 -name '*.sh') )
    for test in "${full_test_list[@]}"; do
        if grep -q -E "${test_regex}" <<< "${test}"; then
            selected_tests+=( "${test}" )
        fi
    done

    if [[ "${#selected_tests[@]}" -eq 0 ]]; then
        os::log::fatal "No tests were selected due to invalid regex."
    else
        echo "${selected_tests[@]}"
    fi
}
tests=( $(find_tests ${1:-.*}) )

os::cleanup::tmpdir

for test in "${tests[@]}"; do
  if ! ${test}; then
    failed="true"
    os::log::error "e2e suite ${test} failed"
  fi
done

if [[ -n "${failed:-}" ]]; then
    os::log::fatal "e2e suites failed"
fi
os::log::info "e2e suites successful"
