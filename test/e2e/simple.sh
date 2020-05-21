#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/e2e/simple"

os::test::junit::declare_suite_start "e2e/simple"
# This test validates the ci-operator exit codes

export JOB_SPEC='{"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}'
os::cmd::expect_success "ci-operator --target success --config ${suite_dir}/config.yaml"
os::cmd::expect_failure "ci-operator --target success --target failure --config ${suite_dir}/config.yaml"
os::cmd::expect_failure "ci-operator --target failure --config ${suite_dir}/config.yaml"

os::test::junit::declare_suite_end
