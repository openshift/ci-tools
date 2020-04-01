#!/bin/bash

# This library holds utility functions for building
# and placing Golang binaries.

# os::build::setup_env will check that the `go` commands is available in
# ${PATH}. If not running on Travis, it will also check that the Go version is
# good enough for the Kubernetes build.
#
# Output Vars:
#   export GOPATH - A modified GOPATH to our created tree along with extra
#     stuff.
#   export GOBIN - This is actively unset if already set as we want binaries
#     placed in a predictable place.
function os::build::setup_env() {
  os::util::ensure::system_binary_exists 'go'

  # Travis continuous build uses a head go release that doesn't report
  # a version number, so we skip this check on Travis.  It's unnecessary
  # there anyway.
  if [[ "${TRAVIS:-}" != "true" ]]; then
    local go_version
    go_version=($(go version))
    local expected_order; expected_order="$( printf "%s\n%s\n" "${OS_REQUIRED_GO_VERSION}" "${go_version[2]}" )"
    local actual_order; actual_order="$( echo "${expected_order}" | sort --version-sort )"
    if [[ "${actual_order}" != "${expected_order}" ]]; then
      os::log::fatal "Detected Go version: ${go_version[*]}.
Builds require Go version ${OS_REQUIRED_GO_VERSION} or greater."
    fi
  fi

  # default to OS_OUTPUT_GOPATH if no GOPATH set
  if [[ -z "${GOPATH:-}" ]]; then
    export OS_OUTPUT_GOPATH=1
  fi

  # use the regular gopath for building
  if [[ -z "${OS_OUTPUT_GOPATH:-}" ]]; then
    export OS_TARGET_BIN=${GOPATH}/bin
    return
  fi

  # Append OS_EXTRA_GOPATH to the GOPATH if it is defined.
  if [[ -n ${OS_EXTRA_GOPATH:-} ]]; then
    GOPATH="${GOPATH}:${OS_EXTRA_GOPATH}"
    # TODO: needs to handle multiple directories
    OS_TARGET_BIN=${OS_EXTRA_GOPATH}/bin
  fi
  export GOPATH
  export OS_TARGET_BIN
}
readonly -f os::build::setup_env

# os::build::require_clean_tree exits if the current Git tree is not clean.
function os::build::require_clean_tree() {
  if ! git diff-index --quiet HEAD -- || test $(git ls-files --exclude-standard --others | wc -l) != 0; then
    echo "You can't have any staged or dirty files in $(pwd) for this command."
    echo "Either commit them or unstage them to continue."
    exit 1
  fi
}
readonly -f os::build::require_clean_tree