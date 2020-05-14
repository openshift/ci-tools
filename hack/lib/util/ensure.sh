#!/bin/bash

# This script contains helper functions for ensuring that dependencies
# exist on a host system that are required to run Origin scripts.

# os::util::ensure::system_binary_exists ensures that the
# given binary exists on the system in the $PATH.
#
# Globals:
#  None
# Arguments:
#  - 1: binary to search for
# Returns:
#  None
function os::util::ensure::system_binary_exists() {
	local binary="$1"

if ! os::util::find::system_binary "${binary}" >/dev/null 2>&1; then
		os::log::fatal "Required \`${binary}\` binary was not found in \$PATH."
	fi
}
readonly -f os::util::ensure::system_binary_exists

# os::util::ensure::gopath_binary_exists ensures that the
# given binary exists on the system in $GOPATH.  If it
# doesn't, we will attempt to build it if we can determine
# the correct install path for the binary.
#
# Globals:
#  - GOPATH
# Arguments:
#  - 1: binary to search for
#  - 2: [optional] path to install from
# Returns:
#  None
function os::util::ensure::gopath_binary_exists() {
	local binary="$1"
	local install_path="${2:-}"

	if ! os::util::find::gopath_binary "${binary}" >/dev/null 2>&1; then
		if [[ -n "${install_path:-}" ]]; then
			os::log::info "No installed \`${binary}\` was found in \$GOPATH. Attempting to install using:
  $ go get ${install_path}"
  			go get "${install_path}"
		else
			os::log::fatal "Required \`${binary}\` binary was not found in \$GOPATH."
		fi
	fi
}
readonly -f os::util::ensure::gopath_binary_exists
