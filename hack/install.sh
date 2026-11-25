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

# go_install_with_retry runs go install with the given arguments and retries up
# to 3 times on transient module download errors with backoff (5s, 15s).
# Compilation failures, checksum mismatches, and authentication errors cause
# immediate failure without retry.
function go_install_with_retry() {
    local max_attempts=3
    local attempt=1

    while true; do
        local stderr_file
        stderr_file=$(mktemp)
        local rc=0
        # Tee stderr for live build output while capturing it for error analysis.
        go install "$@" 2> >(tee "${stderr_file}" >&2) || rc=$?

        if [[ ${rc} -eq 0 ]]; then
            rm -f "${stderr_file}"
            return 0
        fi

        # Allow the tee process substitution to flush before reading.
        sleep 0.1

        # Only retry on transient module download errors from the Go module proxy:
        # HTTP/2 stream errors, connection resets, and network timeouts.
        if ! grep -qE 'INTERNAL_ERROR|connection reset by peer|i/o timeout|TLS handshake timeout' "${stderr_file}" 2>/dev/null; then
            rm -f "${stderr_file}"
            return ${rc}
        fi

        if [[ ${attempt} -ge ${max_attempts} ]]; then
            echo "go install: failed after ${max_attempts} attempts due to transient module download errors" >&2
            rm -f "${stderr_file}"
            return ${rc}
        fi

        local delay=$(( 5 + 10 * (attempt - 1) ))
        echo "go install: transient module download error on attempt ${attempt}/${max_attempts}, retrying in ${delay}s..." >&2
        rm -f "${stderr_file}"
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
