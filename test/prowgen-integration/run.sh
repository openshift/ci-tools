#!/bin/bash

# This test runs a full generation on input data
# and ensures we match the desired output.

set -o errexit
set -o nounset
set -o pipefail

UPDATE="${UPDATE:-false}"

workdir="$( mktemp -d )"
trap 'rm -rf "${workdir}"' EXIT

subdir=${1:-}
data_dir="$( dirname "${BASH_SOURCE[0]}" )/data"
input_config_dir="${data_dir}/input/config"
input_jobs_dir="${data_dir}/input/jobs"
input_registry_dir="${data_dir}/input/step-registry"
generated_output_jobs_dir="${workdir}/jobs"
expected_output_jobs_dir="${data_dir}/output/jobs"

mkdir -p "${generated_output_jobs_dir}"
cp -r "${input_jobs_dir}" "${workdir}"

echo "[INFO] Generating Prow jobs..."
ci-operator-prowgen \
    --from-dir "${input_config_dir}" \
    --to-dir "${generated_output_jobs_dir}" \
    --registry "${input_registry_dir}" \
    $subdir

echo "[INFO] Validating generated Prow jobs..."
if [[ "$UPDATE" = true ]]; then
  rm -rf "${expected_output_jobs_dir}/${subdir}"
  cp -r "${generated_output_jobs_dir}/${subdir}" "${expected_output_jobs_dir}/${subdir}"
fi
if ! diff -Naupr "${expected_output_jobs_dir}/${subdir}" "${generated_output_jobs_dir}/${subdir}"> "${workdir}/diff"; then
  cat << EOF
ERROR: Incorrect Prow jobs were generated!
ERROR: The following errors were found:

EOF
  cat "${workdir}/diff"
  echo "ERROR: If this is expected, run \`make update-integration\`"
  exit 1
fi

if [[ "${subdir}" ]]; then
  echo "[INFO] Verifying other sub-directories were not modified..."
  rm -rf "${generated_output_jobs_dir}/${subdir}"
  if [[ -d "${input_jobs_dir}/${subdir}" ]]; then
    cp -r "${input_jobs_dir}/${subdir}" "${generated_output_jobs_dir}/${subdir}"
  fi
  if ! diff -Naupr "${input_jobs_dir}" "${generated_output_jobs_dir}"> "${workdir}/subdir_diff"; then
    cat << EOF
ERROR: Incorrect Prow jobs were generated!
ERROR: The following errors were found:

EOF
    cat "${workdir}/subdir_diff"
    echo "ERROR: If this is expected, run \`make update-integration\`"
    exit 1
  fi
fi

echo "[INFO] Success!"
