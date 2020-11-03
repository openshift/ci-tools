#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

if [[ -z "${PULL_SECRET_DIR:-}" ]]; then
  os::log::fatal "\$PULL_SECRET_DIR must point to a valid registry pull secret dir. Get the data with: oc --context api.ci --as system:admin --namespace ci get secret regcred -o 'jsonpath={.data.\.dockerconfigjson}' | base64 --decode "
fi

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/e2e/multi-stage"

namespace=
if [[ -z "${CI:-}" && -n "${NAMESPACE:-}" ]]; then
    namespace="--namespace ${NAMESPACE}"
fi

os::test::junit::declare_suite_start "e2e/multi-stage"
# This test validates the ci-operator can resolve literal configs

export JOB_SPEC='{"type":"presubmit","job":"pull-ci-test-test-master-success","buildid":"0","prowjobid":"uuid","refs":{"org":"test","repo":"test","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]}}'
os::integration::configresolver::start "${suite_dir}/configs" "${suite_dir}/registry" "${OS_ROOT}/test/integration/ci-operator-configresolver/config.yaml" "true"
os::cmd::expect_success "ci-operator ${namespace} --secret-dir ${PULL_SECRET_DIR} --artifact-dir ${BASETMPDIR} --resolver-address http://127.0.0.1:8080 --target success"
os::cmd::expect_success "ci-operator ${namespace} --artifact-dir ${BASETMPDIR} --resolver-address http://127.0.0.1:8080 --target without-references --unresolved-config ${suite_dir}/config.yaml"
os::cmd::expect_success "ci-operator ${namespace} --artifact-dir ${BASETMPDIR} --resolver-address http://127.0.0.1:8080 --target with-references --unresolved-config ${suite_dir}/config.yaml"
os::cmd::expect_success "ci-operator ${namespace} --artifact-dir ${BASETMPDIR} --resolver-address http://127.0.0.1:8080 --target skip-on-success --unresolved-config ${suite_dir}/config.yaml"
os::cmd::expect_code_and_text "ci-operator ${namespace} --artifact-dir ${BASETMPDIR} --resolver-address http://127.0.0.1:8080 --target timeout --unresolved-config ${suite_dir}/config.yaml" 1 '"timeout" pod "timeout-timeout" exceeded the configured timeout activeDeadlineSeconds=120: the pod [^ ]* failed after .* \(failed containers: \): DeadlineExceeded Pod was active on the node longer than the specified deadline'
UNRESOLVED_CONFIG="$( cat "${suite_dir}/config.yaml" )"
export UNRESOLVED_CONFIG
os::cmd::expect_success "ci-operator ${namespace} --artifact-dir ${BASETMPDIR} --resolver-address http://127.0.0.1:8080 --target with-references"
unset UNRESOLVED_CONFIG
os::integration::configresolver::check_log

os::test::junit::declare_suite_end

os::test::junit::declare_suite_start "e2e/multi-stage/dependencies"
# This test validates the ci-operator can amend the graph with user input

export JOB_SPEC='{"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}'
os::cmd::expect_success "ci-operator ${namespace} --artifact-dir ${BASETMPDIR} --resolver-address http://127.0.0.1:8080 --target with-dependencies --unresolved-config ${suite_dir}/dependencies.yaml"
os::cmd::expect_success "ci-operator ${namespace} --artifact-dir ${BASETMPDIR} --resolver-address http://127.0.0.1:8080 --target with-cli --unresolved-config ${suite_dir}/dependencies.yaml"
os::integration::configresolver::check_log
os::test::junit::declare_suite_end
