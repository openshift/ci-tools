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
  rm -f cmd/repo-init/frontend/dist/dummy
fi

# BUILD_IMAGES is an allow-list: if set, only these binaries are built.
# SKIPPED_IMAGES is a deny-list: if set, these binaries are skipped.
# BUILD_IMAGES takes precedence over SKIPPED_IMAGES.
declare -A build_images_map
if [[ -n "${BUILD_IMAGES:-}" ]]; then
  echo "Building only: ${BUILD_IMAGES}"
  IFS=',' read -ra build_array <<< "${BUILD_IMAGES}"
  for img in "${build_array[@]}"; do
    build_images_map["${img}"]=1
  done
fi

declare -A skipped_images_map
if [[ -n "${SKIPPED_IMAGES:-}" ]]; then
  echo "Skipping images: ${SKIPPED_IMAGES}"
  IFS=',' read -ra skipped_array <<< "${SKIPPED_IMAGES}"
  for img in "${skipped_array[@]}"; do
    skipped_images_map["${img}"]=1
  done
fi

MAX_PARALLEL="${MAX_PARALLEL:-4}"
pids=()
failures=()

for dir in $( find ./cmd/ -mindepth 1 -maxdepth 1 -type d -not \( -name '*ipi-deprovison*' \) | sort ); do
    command="$( basename "${dir}" )"

    # If BUILD_IMAGES is set, only build listed binaries
    if [[ -n "${BUILD_IMAGES:-}" ]]; then
      if [[ -z "${build_images_map[${command}]:-}" ]]; then
        continue
      fi
    elif [[ -n "${skipped_images_map[${command}]:-}" ]]; then
      echo "Skipping install for ${command} (in SKIPPED_IMAGES)"
      continue
    fi

    echo "Building ${command}..."
    go install -v $RACE_FLAG -ldflags "-X 'sigs.k8s.io/prow/pkg/version.Name=${command}' -X 'sigs.k8s.io/prow/pkg/version.Version=${version}'" "./cmd/${command}/..." &
    pids+=($!)

    # Throttle: wait for a slot when we hit the limit
    if [[ ${#pids[@]} -ge $MAX_PARALLEL ]]; then
      if ! wait "${pids[0]}"; then
        failures+=("${pids[0]}")
      fi
      pids=("${pids[@]:1}")
    fi
done

# Wait for remaining builds
for pid in "${pids[@]}"; do
    if ! wait "$pid"; then
        failures+=("$pid")
    fi
done

if [[ ${#failures[@]} -gt 0 ]]; then
    echo "ERROR: ${#failures[@]} build(s) failed"
    exit 1
fi
