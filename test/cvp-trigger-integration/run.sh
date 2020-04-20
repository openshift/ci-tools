#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

workdir="$(mktemp -d)"
#trap 'rm -rf "${workdir}"' EXIT

datadir="$(dirname "${BASH_SOURCE[0]}")"
bundle_image_ref="git@example.com/org/bundle.git"
channel="channel1"
index_image_ref="git@example.com/org/index.git"
install_namespace="namespace2"
job_config_path="${datadir}/jobs.yaml"
job_name="periodic-ipi-deprovision"
ocp_version="4.5"
operator_package_name="package1"
prow_config_path="${datadir}/config.yaml"
target_namespaces="namespace1"

output="${workdir}/${job_name}.output"
expected="${datadir}/${job_name}.expected"

update=false
if [[ ${1:-} == "--update" ]]; then
  update=true
fi

echo "[INFO] Running CVP trigger"
cvp-trigger --bundle-image-ref="${bundle_image_ref}" --index-image-ref="${index_image_ref}" --prow-config-path="${prow_config_path}" --job-config-path="${job_config_path}" --job-name="${job_name}" --ocp-version="${ocp_version}" --operator-package-name="${operator_package_name}" --channel="${channel}" --target-namespaces="${target_namespaces}" --install-namespace="${install_namespace}" --dry-run>"${workdir}/${job_name}.output"

if [[ ${update} == "true" ]]; then
  echo "[INFO] Updating ${expected}:"
  diff -u "${output}" "${expected}" || true
  cp "${output}" "${expected}"
fi

echo "[INFO] Validating triggered Prow job"
if ! diff -u "${expected}" "${output}" \
  --ignore-matching-lines 'startTime' \
  --ignore-matching-lines 'name: \w\{8\}\(-\w\{4\}\)\{3\}-\w\{12\}' \
  --ignore-matching-lines 'sha: \w\{40\}' >"${workdir}/diff" \
  ; then
  echo "ERROR: Different Prow job triggered:"
  cat "${workdir}/diff"
  exit 1
fi
