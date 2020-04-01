#!/bin/bash

# This script contains helper functions for finding components
# in the Origin repository or on the host machine running scripts.

# os::util::find::system_binary determines the absolute path to a
# system binary, if it exists.
#
# Globals:
#  None
# Arguments:
#  - 1: binary name
# Returns:
#  - location of the binary
function os::util::find::system_binary() {
    local binary_name="$1"

    command -v "${binary_name}"
}
readonly -f os::util::find::system_binary

# os::util::find::gopath_binary determines the absolute path to a
# binary installed through the go toolchain, if it exists.
#
# Globals:
#  - GOPATH
# Arguments:
#  - 1: binary name
# Returns:
#  - location of the binary
function os::util::find::gopath_binary() {
    local binary_name="$1"

    local old_ifs="${IFS}"
    IFS=":"
    for part in ${GOPATH}; do
        local binary_path="${part}/bin/${binary_name}"
        # we need to check that the path leads to a file
        # as directories also have the executable bit set
        if [[ -f "${binary_path}" && -x "${binary_path}" ]]; then
            echo "${binary_path}"
            IFS="${old_ifs}"
            return 0
        fi
    done
    IFS="${old_ifs}"
    return 1
}
readonly -f os::util::find::gopath_binary