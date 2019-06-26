#!/bin/bash

# This test runs a full generation on input data
# and ensures we match the desired output.

set -o errexit
set -o nounset
set -o pipefail

workdir="$( mktemp -d )"
trap 'rm -rf "${workdir}"' EXIT

data_dir="$( dirname "${BASH_SOURCE[0]}" )/data"
input_config_dir="${data_dir}/input/config"
input_jobs_dir="${data_dir}/input/jobs"
generated_output_jobs_dir="${workdir}/jobs"
expected_output_jobs_dir="${data_dir}/output/jobs"

mkdir -p "${generated_output_jobs_dir}"
cp -r "${input_jobs_dir}" "${workdir}"

echo "[INFO] Generating Prow jobs..."
ci-operator-prowgen --from-dir "${input_config_dir}" --to-dir "${generated_output_jobs_dir}"

echo "[INFO] Validating generated Prow jobs..."
if ! diff -Naupr "${expected_output_jobs_dir}" "${generated_output_jobs_dir}"> "${workdir}/diff"; then
  cat << EOF
ERROR: Incorrect Prow jobs were generated!
ERROR: The following errors were found:

EOF
  cat "${workdir}/diff"
  exit 1
fi

determinized_output_jobs_dir="${workdir}/determinized"
mkdir -p "${determinized_output_jobs_dir}"
cp -r "${generated_output_jobs_dir}"/* "${determinized_output_jobs_dir}"

echo "[INFO] Determinizing Prow jobs..."
determinize-prow-jobs --prow-jobs-dir "${determinized_output_jobs_dir}"

echo "[INFO] Validating determinized Prow jobs..."
if ! diff -Naupr "${determinized_output_jobs_dir}" "${generated_output_jobs_dir}"> "${workdir}/diff"; then
  cat << EOF
ERROR: Prow job generator did not output determinized jobs!
ERROR: The following errors were found:

EOF
  cat "${workdir}/diff"
  exit 1
fi

echo "[INFO] Success!"