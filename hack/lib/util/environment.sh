#!/bin/bash

# This script holds library functions for setting up the shell environment for OpenShift scripts

# os::util::environment::use_sudo updates $USE_SUDO to be 'true', so that later scripts choosing between
# execution using 'sudo' and execution without it chose to use 'sudo'
#
# Globals:
#  None
# Arguments:
#  None
# Returns:
#  - export USE_SUDO
function os::util::environment::use_sudo() {
    USE_SUDO=true
    export USE_SUDO
}
readonly -f os::util::environment::use_sudo

# os::util::environment::setup_time_vars sets up environment variables that describe durations of time
# These variables can be used to specify times for other utility functions
#
# Globals:
#  None
# Arguments:
#  None
# Returns:
#  - export TIME_MS
#  - export TIME_SEC
#  - export TIME_MIN
function os::util::environment::setup_time_vars() {
    TIME_MS=1
    export TIME_MS
    TIME_SEC="$(( 1000  * ${TIME_MS} ))"
    export TIME_SEC
    TIME_MIN="$(( 60 * ${TIME_SEC} ))"
    export TIME_MIN
}
readonly -f os::util::environment::setup_time_vars

# os::util::environment::setup_tmpdir_vars sets up temporary directory path variables
#
# Globals:
#  - TMPDIR
# Arguments:
#  - 1: the path under the root temporary directory for OpenShift where these subdirectories should be made
# Returns:
#  - export BASETMPDIR
#  - export BASEOUTDIR
#  - export LOG_DIR
#  - export ARTIFACT_DIR
#  - export OS_TMP_ENV_SET
function os::util::environment::setup_tmpdir_vars() {
    local sub_dir=$1

    tmp=${TMPDIR:-/tmp}
    [[ "${tmp}" != */ ]] && tmp="${tmp}/" # add a trailing slash if missing
    BASETMPDIR="${tmp}openshift/${sub_dir}"
    export BASETMPDIR

    BASEOUTDIR="${OS_OUTPUT_SCRIPTPATH}/${sub_dir}"
    export BASEOUTDIR
    LOG_DIR="${LOG_DIR:-${BASEOUTDIR}/logs}"
    export LOG_DIR
    ARTIFACT_DIR="${ARTIFACT_DIR:-${BASEOUTDIR}/artifacts}"
    export ARTIFACT_DIR

    mkdir -p "${BASETMPDIR}" "${LOG_DIR}" "${ARTIFACT_DIR}"

    export OS_TMP_ENV_SET="${sub_dir}"
}
readonly -f os::util::environment::setup_tmpdir_vars
