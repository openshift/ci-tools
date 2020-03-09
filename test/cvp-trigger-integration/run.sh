#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT

datadir="$(dirname "${BASH_SOURCE[0]}")"
prow_config="$datadir/config.yaml"
job_config="$datadir/jobs.yaml"
job="periodic-ipi-deprovision"

output="$workdir/$job.output"
expected="$datadir/$job.expected"

update=false
if [[ ${1:-} == "--update" ]]; then
  update=true
fi

echo "[INFO] Running CVP trigger"
cvp-trigger --prow-config-path="$prow_config" --job-config-path="$job_config" --periodic="$job" >"$workdir/$job.output"

if [[ $update == "true" ]]; then
  echo "[INFO] Updating $expected:"
  diff -u "$output" "$expected" || true
  cp "$output" "$expected"
fi

echo "[INFO] Validating triggered Prow job"
if ! diff -u "$expected" "$output" \
  --ignore-matching-lines 'startTime' \
  --ignore-matching-lines 'name: \w\{8\}\(-\w\{4\}\)\{3\}-\w\{12\}' \
  --ignore-matching-lines 'sha: \w\{40\}' >"$workdir/diff" \
  ; then
  echo "ERROR: Different Prow job triggered:"
  cat "$workdir/diff"
  exit 1
fi
