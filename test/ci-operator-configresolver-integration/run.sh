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
ci-operator-configresolver -config tests/configs -registry multistage-registry/registry -log-level debug -cycle 2m &> output.log &
echo "[INFO] Started configresolver"
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8081/healthz/ready 2>/dev/null)" == "OK" ]]; then
        break
    fi
    if [[ "${i}" -eq 9 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to be ready"
        kill $(jobs -p)
        wait $(jobs -p)
        echo "configresolver output:"
        cat output.log
        exit 1
    fi
    sleep 0.5
done
echo "[INFO] configresolver ready"
if ! diff -Naupr tests/expected/openshift-installer-release-4.2.json <(curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' 2>/dev/null)> "$WORKDIR/diff"; then
    echo "[ERROR] diff:"
    cat "$WORKDIR/diff"
    kill $(jobs -p)
    wait $(jobs -p)
    echo "configresolver output:"
    cat output.log
    exit 1
fi
echo "[INFO] Got correct resolved config from resolver"
currGen=$(curl 'http://127.0.0.1:8080/configGeneration' 2>/dev/null)
export currGen
cp tests/configs2/release-4.2/openshift-installer-release-4.2-golang111.yaml tests/configs/release-4.2/openshift-installer-release-4.2.yaml
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8080/configGeneration 2>/dev/null)" -gt $currGen ]]; then
        break
    fi
    if [[ "${i}" -eq 9 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to reload configs after file change"
        kill $(jobs -p)
        wait $(jobs -p)
        echo "configresolver output:"
        cat output.log
        exit 1
    fi
    sleep 0.5
done
if ! diff -Naupr tests/expected/openshift-installer-release-4.2-golang111.json <(curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' 2>/dev/null)> "$WORKDIR/diff"; then
    echo "[ERROR] diff:"
    cat "$WORKDIR/diff"
    kill $(jobs -p)
    wait $(jobs -p)
    echo "configresolver output:"
    cat output.log
    exit 1
fi
echo "[INFO] Got correct resolved config from resolver after config change"
currGen=$(curl 'http://127.0.0.1:8080/registryGeneration' 2>/dev/null)
export currGen
rsync -avh --quiet --delete --inplace multistage-registry/registry2/ multistage-registry/registry/
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8080/registryGeneration 2>/dev/null)" -gt $currGen ]]; then
        break
    fi
    if [[ "${i}" -eq 9 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to reload registry after file change"
        kill $(jobs -p)
        wait $(jobs -p)
        echo "configresolver output:"
        cat output.log
        exit 1
    fi
    sleep 0.5
done
if ! diff -Naupr tests/expected/openshift-installer-release-4.2-regChange.json <(curl 'http://127.0.0.1:8080/config?org=openshift&repo=installer&branch=release-4.2' 2>/dev/null)> "$WORKDIR/diff"; then
    echo "[ERROR] diff:"
    cat "$WORKDIR/diff"
    kill $(jobs -p)
    wait $(jobs -p)
    echo "configresolver output:"
    cat output.log
    exit 1
fi
echo "[INFO] Got correct resolved config from resolver after registry change"

kill $(jobs -p)
wait $(jobs -p)

# check for logrus style errors
if grep -q "level=error" output.log; then
    echo "configresolver output:"
    cat output.log
    echo "[ERROR] Detected errors in output:"
    grep "level=error" output.log
    exit 1
fi

echo "[INFO] Success"
