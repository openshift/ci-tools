#!/bin/bash

# This test runs the ci-operator-configresolver and verifies that it returns
# properly resolved configs

#===================================================================
# FUNCTION trap_add ()
#
# Purpose:  prepends a command to a trap
#
# - 1st arg:  code to add
# - remaining args:  names of traps to modify
#
# Example:  trap_add 'echo "in trap DEBUG"' DEBUG
#
# See: http://stackoverflow.com/questions/3338030/multiple-bash-traps-for-the-same-signal
#===================================================================
function trap_add() {
    trap_add_cmd=$1; shift || fatal "${FUNCNAME} usage error"
    new_cmd=
    for trap_add_name in "$@"; do
        # Grab the currently defined trap commands for this trap
        existing_cmd=`trap -p "${trap_add_name}" |  awk -F"'" '{print $2}'`

        # Define default command
        [ -z "${existing_cmd}" ] && existing_cmd="echo exiting @ `date`"

        # Generate the new command
        new_cmd="${trap_add_cmd};${existing_cmd}"

        # Assign the test
         trap   "${new_cmd}" "${trap_add_name}" || \
                fatal "unable to add to trap ${trap_add_name}"
    done
}

set -o errexit
set -o nounset
set -o pipefail

ROOTDIR=$(pwd)
WORKDIR="$( mktemp -d )"
trap_add "rm -rf ${WORKDIR}" EXIT

pushd $WORKDIR
go build -o resolver $ROOTDIR/cmd/ci-operator-configresolver
go build -o tester $ROOTDIR/test/ci-op-configresolver-integration/main.go
# copy registry to tmpdir to allow tester to modify registry
cp -a $ROOTDIR/test/ci-op-configresolver-integration/ tests
./resolver -config tests/configs -registry tests/registry -cycle 2m -log-level debug &
PID=$!
disown
trap_add "kill -9 ${PID} || true" EXIT
# wait for registry to be resolved
sleep 1
./tester -serverAddress "http://127.0.0.1:8080"
popd
