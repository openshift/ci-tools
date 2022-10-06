#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

function cleanup() {
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

registry="${OS_ROOT}/test/multistage-registry/"
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
cp -R "${registry}/registry"/ ./ci-operator/step-registry
git add ci-operator core-services cluster
git commit -m "Master version of openshift/release" --quiet
base_sha="$(git rev-parse HEAD)"
cp -R "${suite_dir}/candidate"/* .
rm -rf ./ci-operator/step-registry
cp -R "${registry}/registry2"/ ./ci-operator/step-registry
git add ci-operator core-services cluster
git commit -m "Candidate version of openshift/release" --quiet
candidate_sha="$(git rev-parse HEAD)"
popd >/dev/null

export JOB_SPEC='{"type":"presubmit","job":"pull-ci-openshift-release-master-rehearse","buildid":"0","prowjobid":"uuid","refs":{"org":"openshift","repo":"release","base_ref":"master","base_sha":"'${base_sha}'","pulls":[{"number":1234,"author":"petr-muller","sha":"'${candidate_sha}'"}]}}'

actual="${workdir}/rehearsals.yaml"
os::cmd::expect_success "pj-rehearse --dry-run=true --candidate-path ${repo} --rehearsal-limit 20 > ${actual}"
os::integration::sanitize_prowjob_yaml ${actual}
# Substitute the SHA in the job names to a known SHA for comparison
sed -i -E -e "s/-${candidate_sha}-/-4de8ab7c20656998264a2593116288f5eb070b32-/g" ${actual}

os::integration::compare "${actual}" "${suite_dir}/expected.yaml"

os::test::junit::declare_suite_end
