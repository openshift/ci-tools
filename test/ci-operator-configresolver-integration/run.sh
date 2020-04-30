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
ci-operator-configresolver -config tests/configs -registry multistage-registry/registry -prow-config tests/config.yaml -log-level debug -cycle 2m &> output.log &
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
# This used to be just a cp but that makes the test flaky because the configresolver would sometimes see an empty file, presumably because cp does create, read, write
# and we already get triggered by the create. Since its a race in an app that has a hardcoded listenAddress its annoying to reproduce, last time I used a go app
# that starts many containers that have the binary and script mounted and stops as soon as an error occurs.
# There are more races in here, but this is the only one we saw in CI so I am not going to bother for now.
tmpfile=$(mktemp)
cp tests/configs2/release-4.2/openshift-installer-release-4.2-golang111.yaml $tmpfile
mv $tmpfile tests/configs/release-4.2/openshift-installer-release-4.2.yaml
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

rm output.log

echo "[INFO] Testing configmap style reloading"
# Test configmap reloading
ci-operator-configresolver -config tests/ci-op-configmaps -registry multistage-registry/configmap -prow-config tests/config.yaml -log-level debug -cycle 2m -flat-registry &> output.log &
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8081/healthz/ready 2>/dev/null)" == "OK" ]]; then
        break
    fi
    if [[ "${i}" -eq 9 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to be ready in cm-mount mode"
        kill $(jobs -p)
        wait $(jobs -p)
        echo "configresolver output:"
        cat output.log
        exit 1
    fi
    sleep 0.5
done

# test with configs
pushd tests/ci-op-configmaps/master
currGen=$(curl 'http://127.0.0.1:8080/configGeneration' 2>/dev/null)
export currGen
# simulate a configmap update
rm ..data && ln -s ..2019_11_15_19_57_20.547184898 ..data
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8080/configGeneration 2>/dev/null)" -gt $currGen ]]; then
        break
    fi
    if [[ "${i}" -eq 9 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to reload configs after file change in cm-mount mode"
        kill $(jobs -p)
        wait $(jobs -p)
        echo "configresolver output:"
        cat output.log
        exit 1
    fi
    sleep 0.5
done
popd
echo "[INFO] Successfully reloaded master configs"

# test against another branch to make sure all branches are being watched
pushd tests/ci-op-configmaps/release-4.2
currGen=$(curl 'http://127.0.0.1:8080/configGeneration' 2>/dev/null)
export currGen
# simulate a configmap update
rm ..data && ln -s ..2019_11_15_19_57_20.547184898 ..data
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8080/configGeneration 2>/dev/null)" -gt $currGen ]]; then
        break
    fi
    if [[ "${i}" -eq 9 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to reload configs after file change in cm-mount mode"
        kill $(jobs -p)
        wait $(jobs -p)
        echo "configresolver output:"
        cat output.log
        exit 1
    fi
    sleep 0.5
done
popd
echo "[INFO] Successfully reloaded release-4.2 configs"

# test with registry
pushd multistage-registry/configmap
currGen=$(curl 'http://127.0.0.1:8080/registryGeneration' 2>/dev/null)
export currGen
# simulate a configmap update
rm ..data && ln -s ..2019_11_15_19_57_20.547184898 ..data
for (( i = 0; i < 10; i++ )); do
    if [[ "$(curl http://127.0.0.1:8080/registryGeneration 2>/dev/null)" -gt $currGen ]]; then
        break
        exit 1
    fi
    if [[ $i -eq 9 ]]; then
        echo "[ERROR] Timed out waiting for ci-operator-configresolver to reload registry after file change in cm-mount mode"
        kill $(jobs -p)
        wait $(jobs -p)
        echo "configresolver output:"
        cat output.log
        exit 1
    fi
    sleep 0.5
done
popd
echo "[INFO] Successfully reloaded registry"

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
