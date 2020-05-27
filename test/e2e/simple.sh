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

os::test::junit::declare_suite_start "e2e/simple/dynamic-release"
# This test validates the ci-operator resolution of dynamic releases

export JOB_SPEC='{"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}'
if [[ -z "${PULL_SECRET_DIR:-}" ]]; then
  os::log::fatal "\$PULL_SECRET_DIR must point to a valid registry pull secret dir. Get the data with: oc --context api.ci --as system:admin --namespace ci get secret ci-pull-secret -o jsonpath={.data.\.dockerconfigjson} | base64 --decode "
fi
os::cmd::expect_success "ci-operator --secret-dir ${PULL_SECRET_DIR} --target [release:initial] --config ${suite_dir}/dynamic-releases.yaml"
os::cmd::expect_success "ci-operator --secret-dir ${PULL_SECRET_DIR} --target [release:latest] --config ${suite_dir}/dynamic-releases.yaml"
os::cmd::expect_success "ci-operator --secret-dir ${PULL_SECRET_DIR} --target [release:custom] --config ${suite_dir}/dynamic-releases.yaml"
os::cmd::expect_success "ci-operator --secret-dir ${PULL_SECRET_DIR} --target [release:pre] --config ${suite_dir}/dynamic-releases.yaml"

os::test::junit::declare_suite_end
