#!/bin/bash

# This test runs the ci-operator-configresolver and verifies that it returns
# properly resolved configs

set -o errexit
set -o nounset
set -o pipefail

ROOTDIR=$(pwd)
WORKDIR="$( mktemp -d )"
trap "rm -rf ${WORKDIR}" EXIT

cd $WORKDIR
# copy registry to tmpdir to allow tester to modify registry
cp -a $ROOTDIR/test/ci-operator-configresolver-integration/ tests
ci-operator-configresolver -config tests/configs -log-level debug &
PID=$!
if ! timeout 10s bash -c 2>/dev/null -- "until diff tests/expected/openshift-installer-release-4.2.json <(curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2'); do sleep 1; done" &>/dev/null; then
    echo "diff: $(diff -ru tests/expected/openshift-installer-release-4.2.json <(curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2'))"
    kill $PID
    exit 1
fi
kill $PID
