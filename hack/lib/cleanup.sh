#!/bin/bash

# This library holds functions that are used to clean up local
# system state after other scripts have run.

# os::cleanup::all will clean up all of the processes and data that
# a script leaves around after running. All of the sub-tasks called
# from this function should gracefully handle when they do not need
# to do anything.
#
# Globals:
#  - ARTIFACT_DIR
#  - SKIP_CLEANUP
#  - SKIP_TEARDOWN
# Arguments:
#  None
# Returns:
#  None
function os::cleanup::all() {
    if [[ -n "${SKIP_CLEANUP:-}" ]]; then
        os::log::warning "[CLEANUP] Skipping cleanup routines..."
        return 0
    fi

    # All of our cleanup is best-effort, so we do not care
    # if any specific step fails.
    set +o errexit

    os::log::info "[CLEANUP] Beginning cleanup routines..."
    os::cleanup::tmpdir

    if [[ -z "${SKIP_TEARDOWN:-}" ]]; then
        os::cleanup::processes
    fi
}
readonly -f os::cleanup::all

# os::cleanup::tmpdir performs cleanup of temp directories as a precondition for running a test. It tries to
# clean up mounts in the temp directories.
#
# Globals:
#  - BASETMPDIR
#  - USE_SUDO
# Returns:
#  None
function os::cleanup::tmpdir() {
    os::log::info "[CLEANUP] Cleaning up temporary directories"
    # ensure that the directories are clean
    if os::util::find::system_binary "findmnt" &>/dev/null; then
        for target in $( ${USE_SUDO:+sudo} findmnt --output TARGET --list ); do
            if [[ "${target}" == "${BASETMPDIR}"* ]]; then
                ${USE_SUDO:+sudo} umount "${target}"
            fi
        done
    fi

    # delete any sub directory underneath BASETMPDIR
    for directory in $( find "${BASETMPDIR}" -mindepth 1 -maxdepth 1 -type d ); do
        ${USE_SUDO:+sudo} rm -rf "${directory}"
    done
}
readonly -f os::cleanup::tmpdir

# os::cleanup::truncate_large_logs truncates very large files under
# $LOG_DIR and $ARTIFACT_DIR so we do not upload them to cloud storage
# after CI runs.
#
# Globals:
#  - LOG_DIR
#  - ARTIFACT_DIR
# Arguments:
#  None
# Returns:
#  None
function os::cleanup::truncate_large_logs() {
    local max_file_size="200M"
    os::log::info "[CLEANUP] Truncating log files over ${max_file_size}"
    for file in $(find "${ARTIFACT_DIR}" "${LOG_DIR}" -type f -name '*.log' \( -size +${max_file_size} \)); do
        mv "${file}" "${file}.tmp"
        echo "LOGFILE TOO LONG ($(du -h "${file}.tmp")), PREVIOUS BYTES TRUNCATED. LAST ${max_file_size} OF LOGFILE:" > "${file}"
        tail -c ${max_file_size} "${file}.tmp" >> "${file}"
        rm "${file}.tmp"
    done
}
readonly -f os::cleanup::truncate_large_logs

# os::cleanup::processes kills all processes created by the test
# script.
#
# Globals:
#  None
# Arguments:
#  None
# Returns:
#  None
function os::cleanup::processes() {
    os::log::info "[CLEANUP] Killing child processes"
    for job in $( jobs -pr ); do
        for child in $( pgrep -P "${job}" ); do
            ${USE_SUDO:+sudo} kill "${child}" &> /dev/null
        done
        ${USE_SUDO:+sudo} kill "${job}" &> /dev/null
    done
}
readonly -f os::cleanup::processes
