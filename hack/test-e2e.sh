#!/bin/bash

# This script runs all of the e2e test suites.
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

os::cleanup::tmpdir

for test in $(find "${OS_ROOT}/test/e2e" -name '*.sh'); do
  if ! ${test}; then
    failed="true"
    os::log::error "e2e suite ${test} failed"
  fi
done

if [[ -n "${failed:-}" ]]; then
    os::log::fatal "e2e suites failed"
fi
os::log::info "e2e suites successful"
