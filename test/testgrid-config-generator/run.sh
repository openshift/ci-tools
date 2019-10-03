#!/bin/bash

# This test runs a full generation on input data
# and ensures we match the desired output.

set -o errexit
set -o nounset
set -o pipefail

workdir="$( mktemp -d )"
trap 'rm -rf "${workdir}"' EXIT

data_dir="$( dirname "${BASH_SOURCE[0]}" )"
input_jobs_dir="${data_dir}/config/jobs"
input_release_dir="${data_dir}/config/release"
input_testgrid_dir="${data_dir}/config/testgrid"
generated_output_config_dir="${workdir}/testgrid"
expected_output_config_dir="${data_dir}/expected"

cp -r "${input_testgrid_dir}" "${workdir}"

echo "[INFO] Generating TestGrid config..."
testgrid-config-generator --release-config "${input_release_dir}" --testgrid-config "${generated_output_config_dir}" --prow-jobs-dir "${input_jobs_dir}"

if [[ -n "${UPDATE_EXPECTED-}" ]]; then
  cp "${generated_output_config_dir}"/* "${expected_output_config_dir}"
  echo "[INFO] Updated"
  exit 0
fi

echo "[INFO] Validating generated TestGrid config..."
if ! diff -Naupr "${expected_output_config_dir}" "${generated_output_config_dir}"> "${workdir}/diff"; then
  cat << EOF
ERROR: Incorrect TestGrid config was generated!
ERROR: The following errors were found:

EOF
  cat "${workdir}/diff"
  exit 1
fi

echo "[INFO] Success!"
