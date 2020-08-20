#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/e2e/simple"
workdir="${BASETMPDIR}/e2e/simple"
mkdir -p "${workdir}"

os::test::junit::declare_suite_start "e2e/simple"
# This test validates the ci-operator exit codes

export JOB_SPEC='{"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}'
os::cmd::expect_success "ci-operator --target success --config ${suite_dir}/config.yaml"
os::cmd::expect_failure "ci-operator --target success --target failure --config ${suite_dir}/config.yaml"
os::cmd::expect_failure "ci-operator --target failure --config ${suite_dir}/config.yaml"

cluster_profile="${workdir}/cluster-profile"
mkdir -p "${cluster_profile}"
touch "${cluster_profile}/data"
artifact_dir="${workdir}/artifacts"
mkdir -p "${artifact_dir}"
unset NAMESPACE JOB_NAME_SAFE # set by the job running us, override
os::cmd::expect_success "CLUSTER_TYPE=something TEST_COMMAND=executable ci-operator --template ${suite_dir}/template.yaml --target template --config ${suite_dir}/template-config.yaml --secret-dir ${cluster_profile} --artifact-dir=${artifact_dir}"
os::integration::compare "${artifact_dir}/template" "${suite_dir}/artifacts/template"
sed -i 's/time=".*"/time="whatever"/g' "${artifact_dir}/junit_operator.xml"
os::integration::compare "${artifact_dir}/junit_operator.xml" "${suite_dir}/artifacts/junit_operator.xml"

os::test::junit::declare_suite_end

os::test::junit::declare_suite_start "e2e/simple/dynamic-release"
# This test validates the ci-operator resolution of dynamic releases

export JOB_SPEC='{"type":"postsubmit","job":"branch-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"6d231cc37652e85e0f0e25c21088b73d644d89ad","pulls":[]}}'
if [[ -z "${PULL_SECRET_DIR:-}" ]]; then
  os::log::fatal "\$PULL_SECRET_DIR must point to a valid registry pull secret dir. Get the data with: oc --context api.ci --as system:admin --namespace ci get secret regcred -o jsonpath={.data.\.dockerconfigjson} | base64 --decode "
fi
if [[ -z "${IMPORT_SECRET_DIR:-}" ]]; then
  os::log::fatal "\$IMPORT_SECRET_DIR must point to a valid registry pull secret dir. Get the data with: oc --context api.ci --as system:admin --namespace ci get secret ci-pull-secret -o jsonpath={.data.\.dockerconfigjson} | base64 --decode "
fi
os::cmd::expect_success "ci-operator --image-import-pull-secret ${IMPORT_SECRET_DIR}/.dockerconfigjson --secret-dir ${PULL_SECRET_DIR} --target [release:initial] --config ${suite_dir}/dynamic-releases.yaml"
os::cmd::expect_success "ci-operator --image-import-pull-secret ${IMPORT_SECRET_DIR}/.dockerconfigjson --secret-dir ${PULL_SECRET_DIR} --target [release:latest] --config ${suite_dir}/dynamic-releases.yaml"
os::cmd::expect_success "ci-operator --image-import-pull-secret ${IMPORT_SECRET_DIR}/.dockerconfigjson --secret-dir ${PULL_SECRET_DIR} --target [release:custom] --config ${suite_dir}/dynamic-releases.yaml"
os::cmd::expect_success "ci-operator --image-import-pull-secret ${IMPORT_SECRET_DIR}/.dockerconfigjson --secret-dir ${PULL_SECRET_DIR} --target [release:pre] --config ${suite_dir}/dynamic-releases.yaml"
RELEASE_IMAGE_LATEST="$( curl -s -H "Accept: application/json"  "https://api.openshift.com/api/upgrades_info/v1/graph?channel=stable-4.4&arch=amd64" | jq --raw-output ".nodes[0].payload" )"
export RELEASE_IMAGE_LATEST
os::cmd::expect_success "ci-operator --secret-dir ${PULL_SECRET_DIR} --target [release:latest] --config ${suite_dir}/dynamic-releases.yaml"
unset RELEASE_IMAGE_LATEST
os::test::junit::declare_suite_end

os::test::junit::declare_suite_start "e2e/simple/optional-operator"
if [[ -z "${PULL_BASE_SHA:-}" ]]; then
  os::log::fatal "\$PULL_BASE_SHA must be set for this test"
fi
if [[ -z "${PULL_NUMBER:-}" ]]; then
  os::log::fatal "\$PULL_NUMBER must be set for this test"
fi
if [[ -z "${PULL_PULL_SHA:-}" ]]; then
  os::log::fatal "\$PULL_PULL_SHA must be set for this test"
fi
export JOB_SPEC='{"type":"presubmit","job":"pull-ci-openshift-ci-tools-master-ci-operator-e2e","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"'${PULL_BASE_SHA}'","pulls":[{"number":'${PULL_NUMBER}',"author":"AlexNPavel","sha":"'${PULL_PULL_SHA}'"}]}}'
os::cmd::expect_success "ci-operator --image-import-pull-secret ${PULL_SECRET_DIR}/.dockerconfigjson --target [images] --target ci-index --config ${suite_dir}/optional-operators.yaml"
os::test::junit::declare_suite_end
