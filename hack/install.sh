#!/bin/bash

# This script installs all go components into the environment's go workspace.
source "$(dirname "${BASH_SOURCE}")/lib/init.sh"

function cleanup() {
    return_code=$?
    os::util::describe_return_code "${return_code}"
    exit "${return_code}"
}
trap "cleanup" EXIT

RACE_FLAG=""
if [[ ${1:-} == "race" ]]; then
  export CGO_ENABLED=1
  RACE_FLAG="-race"
else
  export CGO_ENABLED=0
fi

# Enable transient module download error retry only for production-install
# (no-race), not for race-install where the race detector build should fail
# fast.
RETRY_MODULE_ERRORS=""
if [[ ${1:-} != "race" ]]; then
  RETRY_MODULE_ERRORS=1
fi

git_commit="$( git describe --tags --always --dirty )"
build_date="$( date -u '+%Y%m%d' )"
version="v${build_date}-${git_commit}"

if [[ ${2:-} == "remove-dummy" ]]; then
  rm -f cmd/pod-scaler/frontend/dist/dummy # we keep this file in git to keep the thing compiling without static assets
fi

declare -A skipped_images_map
if [[ -n "${SKIPPED_IMAGES:-}" ]]; then
  echo "Skipping images: ${SKIPPED_IMAGES}"
  IFS=',' read -ra skipped_array <<< "${SKIPPED_IMAGES}"
  for img in "${skipped_array[@]}"; do
    skipped_images_map["${img}"]=1
  done
fi

# go_install_with_retry wraps go install to retry up to 3 times on transient
# HTTP/2 stream errors during Go module zip downloads, with backoff (5s, 15s).
# When RETRY_MODULE_ERRORS is unset (race-install path), it delegates directly
# to go install with no retry. Compilation failures, checksum mismatches, and
# authentication errors cause immediate failure.
function go_install_with_retry() {
    if [[ -z "${RETRY_MODULE_ERRORS:-}" ]]; then
        go install "$@"
        return
    fi

    local stderr_file
    stderr_file=$(mktemp)
    trap 'rm -f "${stderr_file}"' RETURN

    local max_attempts=3
    local attempt=1

    while true; do
        : > "${stderr_file}"
        local rc=0
        # Synchronous pipeline: fd 3 preserves stdout while stderr flows
        # through tee for both live terminal output and capture to file.
        { go install "$@" 2>&1 1>&3 | tee "${stderr_file}" >&2; } 3>&1 || rc=$?

        if [[ ${rc} -eq 0 ]]; then
            return 0
        fi

        # Retry only on the specific transient error shape observed in CI:
        # HTTP/2 stream INTERNAL_ERROR during module zip reads from the proxy.
        if ! grep -qE '\.zip:.*stream error.*INTERNAL_ERROR' "${stderr_file}" 2>/dev/null; then
            return ${rc}
        fi

        if [[ ${attempt} -ge ${max_attempts} ]]; then
            echo "go install: failed after ${max_attempts} attempts due to transient module download errors" >&2
            return ${rc}
        fi

        local delay=$(( 5 + 10 * (attempt - 1) ))
        echo "go install: transient module download error on attempt ${attempt}/${max_attempts}, retrying in ${delay}s..." >&2
        sleep "${delay}"
        attempt=$((attempt + 1))
    done
}

for dir in $( find ./cmd/ -mindepth 1 -maxdepth 1 -type d -not \( -name '*ipi-deprovison*' \) ); do
    command="$( basename "${dir}" )"
    if [[ -n "${skipped_images_map[${command}]:-}" ]]; then
        echo "Skipping install for ${command} (in SKIPPED_IMAGES)"
        continue
    fi
    go_install_with_retry -v $RACE_FLAG -ldflags "-X 'sigs.k8s.io/prow/pkg/version.Name=${command}' -X 'sigs.k8s.io/prow/pkg/version.Version=${version}'" "./cmd/${command}/..."
done
