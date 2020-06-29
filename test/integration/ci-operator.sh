#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/ci-operator/"
workdir="${BASETMPDIR}/ci-operator"
mkdir -p "${workdir}"
cp -a "${suite_dir}/"* "${workdir}"
artifact_dir="${workdir}/artifacts"
mkdir -p "${artifact_dir}"

function run_test() {
  local expected="$1"
  local args="${@:2}"
  actual="${workdir}/$( basename "${expected/expected/actual}" )"

  # some options for ci-operator are always required
  # but can be static in dry-run/integration tests
  local common_options=(
    "--dry-run"
    "--determinize-output"
    "--namespace=testns"
    "--lease-server=http://boskos.example.com"
    "--lease-server-username=ci"
    "--lease-server-password-file=/tmp/anything"
  )
  os::cmd::expect_success "ci-operator ${args[*]} ${common_options[*]} >${actual}"
  # we will use diff to check the output of the tool
  # but we need to ensure that we sort fields so that
  # ordering does not matter, as it is not important
  # to the function of the tool, anyway
  tempfile="$( mktemp "${workdir}/sorted.XXXXX" )"
  jq --sort-keys . <"${actual}" > "${tempfile}"
  mv "${tempfile}" "${actual}"
  os::integration::compare "${actual}" "${expected}"
}
export JOB_SPEC='{"type":"presubmit","job":"pull-ci-openshift-release-master-ci-operator-integration","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"ci-tools","base_ref":"master","base_sha":"af8a90a2faf965eeda949dc1c607c48d3ffcda3e","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]}}'
# set by Prow and will interfere with the ci-operator test executions
unset BUILD_ID

os::test::junit::declare_suite_start "integration/ci-operator/base"
# This test validates the ci-operator tool in simple executions

base_dir="${suite_dir}/base"
config_dir="${base_dir}/config"
expected_dir="${base_dir}/expected_files"
# this invocation tests running all simple targets
run_test "${expected_dir}/expected.json" "--config=${config_dir}/test-config.yaml"

# this invocation tests running with a template
export IMAGE_FORMAT="test" CLUSTER_TYPE="aws" TEST_COMMAND="test command"
run_test "${expected_dir}/expected_with_template.json" "--config=${config_dir}/test-config.yaml --template=${config_dir}/test-template.yaml --target=test-template --artifact-dir=${artifact_dir}"
unset IMAGE_FORMAT CLUSTER_TYPE TEST_COMMAND

auth_dir="${base_dir}/auth_files"
# this invocation tests running a source build with OAuth fetching
run_test "${expected_dir}/expected_src_oauth.json" "--config=${config_dir}/test-config.yaml --oauth-token-path=${auth_dir}/oauth-token --target=src --artifact-dir=${artifact_dir}"

# this invocation tests running a source build with SSH fetching
run_test "${expected_dir}/expected_src_ssh.json" "--config=${config_dir}/test-config.yaml --oauth-token-path=${auth_dir}/id_rsa --target=src --artifact-dir=${artifact_dir}"

# this invocation tests running a with a pull secret
pull_secret="${workdir}/pull_secret"
touch "${pull_secret}"
run_test "${expected_dir}/expected_pull_secret.json" "--config=${config_dir}/test-config.yaml --image-import-pull-secret=${pull_secret}"

os::test::junit::declare_suite_end

os::test::junit::declare_suite_start "integration/ci-operator/multi-stage"
# This test validates the ci-operator tool for multi-stage tests

base_dir="${suite_dir}/multi-stage"
config_dir="${base_dir}/configs"
expected_dir="${base_dir}/expected"
# with a registry, we implicitly use the org/repo/job asked for in the JOB_SPEC
export JOB_SPEC='{"type":"presubmit","job":"pull-ci-openshift-release-master-ci-operator-integration","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"installer","base_ref":"release-4.2","base_sha":"af8a90a2faf965eeda949dc1c607c48d3ffcda3e","pulls":[{"number":1234,"author":"droslean","sha":"538680dfd2f6cff3b3506c80ca182dcb0dd22a58"}]}}'

# this invocation tests running all test targets
run_test "${expected_dir}/hyperkube.json" "--config=${config_dir}/master/openshift-hyperkube-master.yaml"

# this invocation tests running with a local registry
run_test "${expected_dir}/installer.json" "--config=${config_dir}/release-4.2/openshift-installer-release-4.2.yaml --registry=${OS_ROOT}/test/multistage-registry/registry"

# this invocation tests running with a remote registry, where local config is not needed
os::integration::configresolver::start "${config_dir}" "${OS_ROOT}/test/multistage-registry/registry" "${OS_ROOT}/test/integration/ci-operator-configresolver/config.yaml"
run_test "${expected_dir}/installer.json" "--resolver-address=http://127.0.0.1:8080"
os::integration::configresolver::check_log
os::integration::configresolver::stop

os::test::junit::declare_suite_end
