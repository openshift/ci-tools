#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/pj-rehearse/"
workdir="${BASETMPDIR}/pj-rehearse"
repo="${workdir}/release"
mkdir -p "${workdir}" "${repo}"
cp -a "${suite_dir}/"* "${workdir}"
actual="${workdir}/input"

os::test::junit::declare_suite_start "integration/pj-rehearse"
# This test sets up a temporary testing repository
# resembling `openshift/release` and tries to execute
# pj-rehearse as if run over a PR for that repo

echo "[INFO] Preparing fake input repository..."
pushd "${repo}" >/dev/null
git init --quiet
git config --local user.name test
git config --local user.email test
cp -R "${suite_dir}/master"/* .
git add ci-operator core-services cluster
git commit -m "Master version of openshift/release" --quiet
base_sha="$(git rev-parse HEAD)"
rm -rf ./ci-operator/step-registry
cp -R "${suite_dir}/candidate"/* .
git add ci-operator core-services cluster
git commit -m "Candidate version of openshift/release" --quiet
candidate_sha="$(git rev-parse HEAD)"
popd >/dev/null

os::cmd::expect_success "ci-operator-checkconfig --config-dir ${suite_dir}/master/ci-operator/config --registry ${suite_dir}/master/ci-operator/step-registry --cluster-profiles-config ${suite_dir}/master/ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml" --cluster-claim-owners-config ${suite_dir}/master/core-services/cluster-pools/_config.yaml"
os::cmd::expect_success "ci-operator-checkconfig --config-dir ${suite_dir}/candidate/ci-operator/config --registry ${suite_dir}/candidate/ci-operator/step-registry --cluster-profiles-config ${suite_dir}/candidate/ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml" --cluster-claim-owners-config ${suite_dir}/candidate/core-services/cluster-pools/_config.yaml"
os::cmd::expect_success "ci-operator-prowgen --from-dir ${suite_dir}/master/ci-operator/config --to-dir ${suite_dir}/master/ci-operator/jobs --registry ${suite_dir}/master/ci-operator/step-registry"
os::cmd::expect_success '[[ -z "$(git -C '"${suite_dir}"'/master status --short -- .)" ]]'
os::cmd::expect_success "ci-operator-prowgen --from-dir ${suite_dir}/candidate/ci-operator/config --to-dir ${suite_dir}/candidate/ci-operator/jobs --registry ${suite_dir}/candidate/ci-operator/step-registry"
os::cmd::expect_success '[[ -z "$(git -C '"${suite_dir}"'/candidate status --short -- .)" ]]'

export PR='{"number": 1234, "user": {"login": "username"},  "base": {"sha": "'${base_sha}'", "ref": "master", "repo": {"name": "release", "owner": {"login": "openshift"}}}, "head": {"sha": "'${candidate_sha}'"}}'

actual="${workdir}/rehearsals.yaml"
os::cmd::expect_success "pj-rehearse --dry-run=true --dry-run-path ${repo} --pull-request-var PR > ${actual}"
os::integration::sanitize_prowjob_yaml ${actual}
# Substitute the SHA in the job names to a known SHA for comparison
sed -i -E -e "s/-${candidate_sha}-/-4de8ab7c20656998264a2593116288f5eb070b32-/g" ${actual}

os::integration::compare "${actual}" "${suite_dir}/expected.yaml"
os::test::junit::declare_suite_end
