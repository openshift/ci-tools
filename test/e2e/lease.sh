#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

if [[ -z "${PULL_SECRET_DIR:-}" ]]; then
  os::log::fatal "\$PULL_SECRET_DIR must point to a valid registry pull secret dir. Get the data with: oc --context api.ci --as system:admin --namespace ci get secret ci-pull-credentials -o jsonpath={.data.\.dockerconfigjson} | base64 --decode "
fi
if [[ -z "${IMPORT_SECRET_DIR:-}" ]]; then
  os::log::fatal "\$IMPORT_SECRET_DIR must point to a valid registry pull secret dir. Get the data with: oc --context api.ci --as system:admin --namespace ci get secret ci-pull-secret -o jsonpath={.data.\.dockerconfigjson} | base64 --decode "
fi

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/e2e/lease"
workdir="${BASETMPDIR}/e2e/lease"
mkdir -p "${workdir}"
cluster_profiles="${workdir}/cluster-profiles"
mkdir -p "${cluster_profiles}" "${cluster_profiles}"/success-cluster-profile "${cluster_profiles}"/invalid-lease-cluster-profile
touch "${cluster_profiles}/success-cluster-profile/data" "${cluster_profiles}/invalid-lease-cluster-profile/data"

namespace=
if [[ "${CI:-}" ]]; then
    # Set by the parent ci-operator
    unset NAMESPACE
elif [[ -n "${NAMESPACE:-}" ]]; then
    namespace="--namespace ${NAMESPACE}"
fi

os::test::junit::declare_suite_start "e2e/lease"

export JOB_SPEC='{"type":"presubmit","job":"pull-ci-test-test-master-success","buildid":"0","prowjobid":"uuid","refs":{"org":"test","repo":"test","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]}}'
os::integration::boskos::start "${suite_dir}/boskos.yaml"
os::cmd::expect_failure "ci-operator ${namespace} --image-import-pull-secret ${IMPORT_SECRET_DIR}/.dockerconfigjson --secret-dir ${PULL_SECRET_DIR} --artifact-dir ${BASETMPDIR} --config ${suite_dir}/config.yaml --target success"
os::cmd::expect_success "ci-operator ${namespace} --image-import-pull-secret ${IMPORT_SECRET_DIR}/.dockerconfigjson --secret-dir ${PULL_SECRET_DIR} --artifact-dir ${BASETMPDIR} --config ${suite_dir}/config.yaml --lease-server http://localhost:8080 --lease-server-password-file /dev/null --lease-acquire-timeout 2s --target success --secret-dir ${cluster_profiles}/success-cluster-profile"
os::cmd::expect_failure "ci-operator ${namespace} --image-import-pull-secret ${IMPORT_SECRET_DIR}/.dockerconfigjson --secret-dir ${PULL_SECRET_DIR} --artifact-dir ${BASETMPDIR} --config ${suite_dir}/config.yaml --lease-server http://localhost:8080 --lease-server-password-file /dev/null --lease-acquire-timeout 2s --target invalid-lease --secret-dir ${cluster_profiles}/invalid-lease-cluster-profile"
os::cmd::expect_success "CLUSTER_TYPE=aws ci-operator ${namespace} --config ${suite_dir}/config.yaml --lease-server http://localhost:8080 --lease-server-password-file /dev/null --lease-acquire-timeout 2s --target template --template ${suite_dir}/template.yaml"

os::integration::boskos::stop

os::test::junit::declare_suite_end
