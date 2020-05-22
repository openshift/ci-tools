#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/e2e/credentials"
cp "${suite_dir}/config.yaml" "${BASETMPDIR}"

os::test::junit::declare_suite_start "e2e/credentials"
# This test validates the ci-operator can mount credentials

export JOB_SPEC='{"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}'
# we need to create a credential to mount, and we know we can
# access secrets in our current namespace, so we'll place it
# in here for now
current_namespace="$( oc project --short )"
sed -i "s/CHANGE_ME/${current_namespace}/" "${BASETMPDIR}/config.yaml"
os::cmd::expect_success "oc --namespace ${current_namespace} create secret generic credential --from-literal key=value"
os::cmd::expect_success "ci-operator --target multi-stage-with-credentials --config ${BASETMPDIR}/config.yaml"

os::test::junit::declare_suite_end
