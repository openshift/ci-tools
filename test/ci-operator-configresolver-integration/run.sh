#!/bin/bash

# This test runs the ci-operator-configresolver and verifies that it returns
# properly resolved configs

set -o errexit
set -o nounset
set -o pipefail

ROOTDIR=$(pwd)
WORKDIR="$( mktemp -d )"
trap 'rm -rf ${WORKDIR}' EXIT

cd "$WORKDIR"
# copy registry to tmpdir to allow tester to modify registry
cp -a "$ROOTDIR"/test/ci-operator-configresolver-integration/ tests
cp -a "$ROOTDIR"/test/multistage-registry multistage-registry
ci-operator-configresolver -config tests/configs -registry multistage-registry/registry -log-level debug -cycle 2m &
PID=$!
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8081/healthz/ready 2>/dev/null)" == "OK" ]]; then
        break
    fi
    if [[ "${i}" -eq 10 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to be ready"
        kill $PID
        wait $PID
        exit 1
    fi
    sleep 0.5
done
if ! diff -Naupr tests/expected/openshift-installer-release-4.2.json <(curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' 2>/dev/null)> "$WORKDIR/diff"; then
    echo "diff:"
    cat "$WORKDIR/diff"
    kill $PID
    wait $PID
    exit 1
fi
currGen=$(curl 'http://127.0.0.1:8080/configGeneration')
export currGen
cp tests/configs2/release-4.2/openshift-installer-release-4.2-golang111.yaml tests/configs/release-4.2/openshift-installer-release-4.2.yaml
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8080/configGeneration 2>/dev/null)" -gt $currGen+1 ]]; then
        break
    fi
    if [[ "${i}" -eq 10 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to reload configs after file change"
        kill $PID
        wait $PID
        exit 1
    fi
    sleep 0.5
done
if ! diff -Naupr tests/expected/openshift-installer-release-4.2-golang111.json <(curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' 2>/dev/null)> "$WORKDIR/diff"; then
    echo "diff:"
    cat "$WORKDIR/diff"
    kill $PID
    wait $PID
    exit 1
fi
currGen=$(curl 'http://127.0.0.1:8080/registryGeneration')
export currGen
cp -a multistage-registry/registry2 multistage-registry/registry
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8080/registryGeneration 2>/dev/null)" -gt $currGen+1 ]]; then
        break
    fi
    if [[ "${i}" -eq 10 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to reload registry after file change"
        kill $PID
        wait $PID
        exit 1
    fi
    sleep 0.5
done
if ! diff -Naupr tests/expected/openshift-installer-release-4.2-regChange.json <(curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' 2>/dev/null)> "$WORKDIR/diff"; then
    echo "diff:"
    cat "$WORKDIR/diff"
    kill $PID
    wait $PID
    exit 1
fi
kill $PID
wait $PID
