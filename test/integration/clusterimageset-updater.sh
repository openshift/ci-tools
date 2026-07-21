#!/bin/bash
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

mock_pid=""
function cleanup() {
    if [[ -n "${mock_pid}" ]]; then
        kill "${mock_pid}" 2>/dev/null || true
    fi
    os::test::junit::reconcile_output
    os::cleanup::processes
}
trap "cleanup" EXIT

suite_dir="${OS_ROOT}/test/integration/clusterimageset-updater/"
workdir="${BASETMPDIR}/clusterimageset-updater"
mkdir -p "${workdir}"
cp -a "${suite_dir}/input" "${suite_dir}/output" "${workdir}/"
inputs="${workdir}/input"
expected="${workdir}/output"
mock_pullspec="quay.io/openshift-release-dev/ocp-release:4.21.25-multi"

mock_binary="${workdir}/mock-release-server-bin"
os::cmd::expect_success "go build -o ${mock_binary} ${suite_dir}/mock-release-server"
release_service_url_file="${workdir}/release-service-url"
"${mock_binary}" --pullspec "${mock_pullspec}" > "${release_service_url_file}" &
mock_pid=$!
for _ in $(seq 1 50); do
    if [[ -s "${release_service_url_file}" ]]; then
        break
    fi
    sleep 0.1
done
release_service_url=$(< "${release_service_url_file}")
if [[ -z "${release_service_url}" ]]; then
    os::log::fatal "mock release server did not publish its service URL"
fi

os::test::junit::declare_suite_start "integration/clusterimageset-updater"

os::cmd::expect_success "clusterimageset-updater --pools ${inputs}/pools --imagesets ${inputs}/imagesets --release-service-url ${release_service_url}"
os::integration::compare "${inputs}" "${expected}"

os::test::junit::declare_suite_end
